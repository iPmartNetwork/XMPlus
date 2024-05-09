package mydispatcher

//go:generate go run github.com/xcode75/xcore/common/errors/errorgen

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/xcode75/xcore/common"
	"github.com/xcode75/xcore/common/buf"
	"github.com/xcode75/xcore/common/log"
	"github.com/xcode75/xcore/common/net"
	"github.com/xcode75/xcore/common/protocol"
	"github.com/xcode75/xcore/common/session"
	"github.com/xcode75/xcore/core"
	"github.com/xcode75/xcore/features/dns"
	"github.com/xcode75/xcore/features/outbound"
	"github.com/xcode75/xcore/features/policy"
	"github.com/xcode75/xcore/features/routing"
	routingSession "github.com/xcode75/xcore/features/routing/session"
	"github.com/xcode75/xcore/features/stats"
	"github.com/xcode75/xcore/transport"
	"github.com/xcode75/xcore/transport/pipe"

	"github.com/xcode75/XMPlus/common/limiter"
	"github.com/xcode75/XMPlus/common/rule"
	router_ru "github.com/xcode75/xcore/app/router"
)

var errSniffingTimeout = newError("timeout on sniffing")

type cachedReader struct {
	sync.Mutex
	reader *pipe.Reader
	cache  buf.MultiBuffer
}

func (r *cachedReader) Cache(b *buf.Buffer) {
	mb, _ := r.reader.ReadMultiBufferTimeout(time.Millisecond * 100)
	r.Lock()
	if !mb.IsEmpty() {
		r.cache, _ = buf.MergeMulti(r.cache, mb)
	}
	b.Clear()
	rawBytes := b.Extend(buf.Size)
	n := r.cache.Copy(rawBytes)
	b.Resize(0, int32(n))
	r.Unlock()
}

func (r *cachedReader) readInternal() buf.MultiBuffer {
	r.Lock()
	defer r.Unlock()

	if r.cache != nil && !r.cache.IsEmpty() {
		mb := r.cache
		r.cache = nil
		return mb
	}

	return nil
}

func (r *cachedReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb := r.readInternal()
	if mb != nil {
		return mb, nil
	}

	return r.reader.ReadMultiBuffer()
}

func (r *cachedReader) ReadMultiBufferTimeout(timeout time.Duration) (buf.MultiBuffer, error) {
	mb := r.readInternal()
	if mb != nil {
		return mb, nil
	}

	return r.reader.ReadMultiBufferTimeout(timeout)
}

func (r *cachedReader) Interrupt() {
	r.Lock()
	if r.cache != nil {
		r.cache = buf.ReleaseMulti(r.cache)
	}
	r.Unlock()
	r.reader.Interrupt()
}

// DefaultDispatcher is a default implementation of Dispatcher.
type DefaultDispatcher struct {
	ohm         outbound.Manager
	router      routing.Router
	policy      policy.Manager
	stats       stats.Manager
	dns         dns.Client
	fdns        dns.FakeDNSEngine
	Limiter     *limiter.Limiter
	RuleManager *rule.Manager
	RouterRule  *router_ru.Router
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		d := new(DefaultDispatcher)
		if err := core.RequireFeatures(ctx, func(om outbound.Manager, router routing.Router, pm policy.Manager, sm stats.Manager, dc dns.Client) error {
			core.RequireFeatures(ctx, func(fdns dns.FakeDNSEngine) {
				d.fdns = fdns
			})
			return d.Init(config.(*Config), om, router, pm, sm, dc)
		}); err != nil {
			return nil, err
		}
		return d, nil
	}))
}

// Init initializes DefaultDispatcher.
func (d *DefaultDispatcher) Init(config *Config, om outbound.Manager, router routing.Router, pm policy.Manager, sm stats.Manager, dns dns.Client) error {
	d.ohm = om
	d.router = router
	d.policy = pm
	d.stats = sm
	d.Limiter = limiter.New()
	d.RuleManager = rule.New()
	d.RouterRule = router_ru.NewRouter()
	d.dns = dns
	return nil
}

// Type implements common.HasType.
func (*DefaultDispatcher) Type() interface{} {
	return routing.DispatcherType()
}

// Start implements common.Runnable.
func (*DefaultDispatcher) Start() error {
	return nil
}

// Close implements common.Closable.
func (*DefaultDispatcher) Close() error {
	return nil
}

func (d *DefaultDispatcher) getLink(ctx context.Context, network net.Network, sniffing session.SniffingRequest) (*transport.Link, *transport.Link, error) {
	downOpt := pipe.OptionsFromContext(ctx)
	upOpt := downOpt

	if network == net.Network_UDP {
		var ip2domain *sync.Map // net.IP.String() => domain, this map is used by server side when client turn on fakedns
		// Client will send domain address in the buffer.UDP.Address, server record all possible target IP addrs.
		// When target replies, server will restore the domain and send back to client.
		// Note: this map is not global but per connection context
		upOpt = append(upOpt, pipe.OnTransmission(func(mb buf.MultiBuffer) buf.MultiBuffer {
			for i, buffer := range mb {
				if buffer.UDP == nil {
					continue
				}
				addr := buffer.UDP.Address
				if addr.Family().IsIP() {
					if fkr0, ok := d.fdns.(dns.FakeDNSEngineRev0); ok && fkr0.IsIPInIPPool(addr) && sniffing.Enabled {
						domain := fkr0.GetDomainFromFakeDNS(addr)
						if len(domain) > 0 {
							buffer.UDP.Address = net.DomainAddress(domain)
							newError("[fakedns client] override with domain: ", domain, " for xUDP buffer at ", i).WriteToLog(session.ExportIDToError(ctx))
						} else {
							newError("[fakedns client] failed to find domain! :", addr.String(), " for xUDP buffer at ", i).AtWarning().WriteToLog(session.ExportIDToError(ctx))
						}
					}
				} else {
					if ip2domain == nil {
						ip2domain = new(sync.Map)
						newError("[fakedns client] create a new map").WriteToLog(session.ExportIDToError(ctx))
					}
					domain := addr.Domain()
					ips, err := d.dns.LookupIP(domain, dns.IPOption{IPv4Enable: true, IPv6Enable: true})
					if err == nil {
						for _, ip := range ips {
							ip2domain.Store(ip.String(), domain)
						}
						newError("[fakedns client] candidate ip: "+fmt.Sprintf("%v", ips), " for xUDP buffer at ", i).WriteToLog(session.ExportIDToError(ctx))
					} else {
						newError("[fakedns client] failed to look up IP for ", domain, " for xUDP buffer at ", i).Base(err).WriteToLog(session.ExportIDToError(ctx))
					}
				}
			}
			return mb
		}))
		downOpt = append(downOpt, pipe.OnTransmission(func(mb buf.MultiBuffer) buf.MultiBuffer {
			for i, buffer := range mb {
				if buffer.UDP == nil {
					continue
				}
				addr := buffer.UDP.Address
				if addr.Family().IsIP() {
					if ip2domain == nil {
						continue
					}
					if domain, found := ip2domain.Load(addr.IP().String()); found {
						buffer.UDP.Address = net.DomainAddress(domain.(string))
						newError("[fakedns client] restore domain: ", domain.(string), " for xUDP buffer at ", i).WriteToLog(session.ExportIDToError(ctx))
					}
				} else {
					if fkr0, ok := d.fdns.(dns.FakeDNSEngineRev0); ok {
						fakeIp := fkr0.GetFakeIPForDomain(addr.Domain())
						buffer.UDP.Address = fakeIp[0]
						newError("[fakedns client] restore FakeIP: ", buffer.UDP, fmt.Sprintf("%v", fakeIp), " for xUDP buffer at ", i).WriteToLog(session.ExportIDToError(ctx))
					}
				}
			}
			return mb
		}))
	}
	uplinkReader, uplinkWriter := pipe.New(upOpt...)
	downlinkReader, downlinkWriter := pipe.New(downOpt...)

	inboundLink := &transport.Link{
		Reader: downlinkReader,
		Writer: uplinkWriter,
	}

	outboundLink := &transport.Link{
		Reader: uplinkReader,
		Writer: downlinkWriter,
	}

	sessionInbound := session.InboundFromContext(ctx)
	var user *protocol.MemoryUser
	if sessionInbound != nil {
		user = sessionInbound.User
	}

	if user != nil && len(user.Email) > 0 {
		// Speed Limit and Device Limit
		bucket, ok, reject := d.Limiter.GetUserBucket(sessionInbound.Tag, user.Email, sessionInbound.Source.Address.IP().String())
		if reject {
			newError("Devices reach the limit: ", user.Email).AtWarning().WriteToLog()
			common.Close(outboundLink.Writer)
			common.Close(inboundLink.Writer)
			common.Interrupt(outboundLink.Reader)
			common.Interrupt(inboundLink.Reader)
			return nil, nil, newError("Devices reach the limit: ", user.Email)
		}
		if ok {
			inboundLink.Writer = d.Limiter.RateWriter(inboundLink.Writer, bucket)
			outboundLink.Writer = d.Limiter.RateWriter(outboundLink.Writer, bucket)
		}
		p := d.policy.ForLevel(user.Level)
		if p.Stats.UserUplink {
			name := "user>>>" + user.Email + ">>>traffic>>>uplink"
			if c, _ := stats.GetOrRegisterCounter(d.stats, name); c != nil {
				inboundLink.Writer = &SizeStatWriter{
					Counter: c,
					Writer:  inboundLink.Writer,
				}
			}
		}
		if p.Stats.UserDownlink {
			name := "user>>>" + user.Email + ">>>traffic>>>downlink"
			if c, _ := stats.GetOrRegisterCounter(d.stats, name); c != nil {
				outboundLink.Writer = &SizeStatWriter{
					Counter: c,
					Writer:  outboundLink.Writer,
				}
			}
		}
	}

	return inboundLink, outboundLink, nil
}

func (d *DefaultDispatcher) shouldOverride(ctx context.Context, result SniffResult, request session.SniffingRequest, destination net.Destination) bool {
	domain := result.Domain()
	for _, d := range request.ExcludeForDomain {
		if strings.ToLower(domain) == d {
			return false
		}
	}
	protocolString := result.Protocol()
	if resComp, ok := result.(SnifferResultComposite); ok {
		protocolString = resComp.ProtocolForDomainResult()
	}
	for _, p := range request.OverrideDestinationForProtocol {
		if strings.HasPrefix(protocolString, p) {
			return true
		}
		if fkr0, ok := d.fdns.(dns.FakeDNSEngineRev0); ok && protocolString != "bittorrent" && p == "fakedns" &&
			destination.Address.Family().IsIP() && fkr0.IsIPInIPPool(destination.Address) {
			newError("Using sniffer ", protocolString, " since the fake DNS missed").WriteToLog(session.ExportIDToError(ctx))
			return true
		}
		if resultSubset, ok := result.(SnifferIsProtoSubsetOf); ok {
			if resultSubset.IsProtoSubsetOf(p) {
				return true
			}
		}
	}

	return false
}

// Dispatch implements routing.Dispatcher.
func (d *DefaultDispatcher) Dispatch(ctx context.Context, destination net.Destination) (*transport.Link, error) {
	if !destination.IsValid() {
		panic("Dispatcher: Invalid destination.")
	}
	ob := &session.Outbound{
		Target: destination,
	}
	ctx = session.ContextWithOutbound(ctx, ob)
	content := session.ContentFromContext(ctx)
	if content == nil {
		content = new(session.Content)
		ctx = session.ContextWithContent(ctx, content)
	}

	sniffingRequest := content.SniffingRequest
	inbound, outbound, err := d.getLink(ctx, destination.Network, sniffingRequest)
	if err != nil {
		return nil, err
	}
	if !sniffingRequest.Enabled {
		go d.routedDispatch(ctx, outbound, destination)
	} else {
		go func() {
			cReader := &cachedReader{
				reader: outbound.Reader.(*pipe.Reader),
			}
			outbound.Reader = cReader
			result, err := sniffer(ctx, cReader, sniffingRequest.MetadataOnly, destination.Network)
			if err == nil {
				content.Protocol = result.Protocol()
			}
			if err == nil && d.shouldOverride(ctx, result, sniffingRequest, destination) {
				domain := result.Domain()
				newError("sniffed domain: ", domain).WriteToLog(session.ExportIDToError(ctx))
				destination.Address = net.ParseAddress(domain)
				if sniffingRequest.RouteOnly && result.Protocol() != "fakedns" {
					ob.RouteTarget = destination
				} else {
					ob.Target = destination
				}
			}
			d.routedDispatch(ctx, outbound, destination)
		}()
	}
	return inbound, nil
}

// DispatchLink implements routing.Dispatcher.
func (d *DefaultDispatcher) DispatchLink(ctx context.Context, destination net.Destination, outbound *transport.Link) error {
	if !destination.IsValid() {
		return newError("Dispatcher: Invalid destination.")
	}
	ob := &session.Outbound{
		Target: destination,
	}
	ctx = session.ContextWithOutbound(ctx, ob)
	content := session.ContentFromContext(ctx)
	if content == nil {
		content = new(session.Content)
		ctx = session.ContextWithContent(ctx, content)
	}
	sniffingRequest := content.SniffingRequest
	if !sniffingRequest.Enabled {
		go d.routedDispatch(ctx, outbound, destination)
	} else {
		go func() {
			cReader := &cachedReader{
				reader: outbound.Reader.(*pipe.Reader),
			}
			outbound.Reader = cReader
			result, err := sniffer(ctx, cReader, sniffingRequest.MetadataOnly, destination.Network)
			if err == nil {
				content.Protocol = result.Protocol()
			}
			if err == nil && d.shouldOverride(ctx, result, sniffingRequest, destination) {
				domain := result.Domain()
				newError("sniffed domain: ", domain).WriteToLog(session.ExportIDToError(ctx))
				destination.Address = net.ParseAddress(domain)
				if sniffingRequest.RouteOnly && result.Protocol() != "fakedns" {
					ob.RouteTarget = destination
				} else {
					ob.Target = destination
				}
			}
			d.routedDispatch(ctx, outbound, destination)
		}()
	}
	return nil
}

func sniffer(ctx context.Context, cReader *cachedReader, metadataOnly bool, network net.Network) (SniffResult, error) {
	payload := buf.New()
	defer payload.Release()

	sniffer := NewSniffer(ctx)

	metaresult, metadataErr := sniffer.SniffMetadata(ctx)

	if metadataOnly {
		return metaresult, metadataErr
	}

	contentResult, contentErr := func() (SniffResult, error) {
		totalAttempt := 0
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
				totalAttempt++
				if totalAttempt > 2 {
					return nil, errSniffingTimeout
				}

				cReader.Cache(payload)
				if !payload.IsEmpty() {
					result, err := sniffer.Sniff(ctx, payload.Bytes(), network)
					if err != common.ErrNoClue {
						return result, err
					}
				}
				if payload.IsFull() {
					return nil, errUnknownContent
				}
			}
		}
	}()
	if contentErr != nil && metadataErr == nil {
		return metaresult, nil
	}
	if contentErr == nil && metadataErr == nil {
		return CompositeResult(metaresult, contentResult), nil
	}
	return contentResult, contentErr
}

func (d *DefaultDispatcher) routedDispatch(ctx context.Context, link *transport.Link, destination net.Destination) {
	ob := session.OutboundFromContext(ctx)
	if hosts, ok := d.dns.(dns.HostsLookup); ok && destination.Address.Family().IsDomain() {
		proxied := hosts.LookupHosts(ob.Target.String())
		if proxied != nil {
			ro := ob.RouteTarget == destination
			destination.Address = *proxied
			if ro {
				ob.RouteTarget = destination
			} else {
				ob.Target = destination
			}
		}
	}

	var handler outbound.Handler

	// Check if domain and protocol hit the rule
	sessionInbound := session.InboundFromContext(ctx)
	// Whether the inbound connection contains a user
	if sessionInbound.User != nil {
		if d.RuleManager.Detect(sessionInbound.Tag, destination.String(), sessionInbound.User.Email) {
			newError(fmt.Sprintf("User %s access %s reject by rule", sessionInbound.User.Email, destination.String())).AtError().WriteToLog()
			newError("destination is reject by rule")
			common.Close(link.Writer)
			common.Interrupt(link.Reader)
			return
		}
	}

	routingLink := routingSession.AsRoutingContext(ctx)
	inTag := routingLink.GetInboundTag()
	isPickRoute := 0
	if forcedOutboundTag := session.GetForcedOutboundTagFromContext(ctx); forcedOutboundTag != "" {
		ctx = session.SetForcedOutboundTagToContext(ctx, "")
		if h := d.ohm.GetHandler(forcedOutboundTag); h != nil {
			isPickRoute = 1
			newError("Taking a detour [", forcedOutboundTag, "] for [", destination, "]").WriteToLog(session.ExportIDToError(ctx))
			handler = h
		} else {
			newError("Non existing tag for a detour: ", forcedOutboundTag).AtError().WriteToLog(session.ExportIDToError(ctx))
			common.Close(link.Writer)
			common.Interrupt(link.Reader)
			return
		}
	} else if d.router != nil {
		if route, err := d.router.PickRoute(routingLink); err == nil {
			outTag := route.GetOutboundTag()
			if h := d.ohm.GetHandler(outTag); h != nil {
				isPickRoute = 2
				newError("Taking A Detour [", outTag, "] for [", destination, "]").WriteToLog(session.ExportIDToError(ctx))
				handler = h
			} else {
				newError("Non Existing OutboundTag: ", outTag).AtWarning().WriteToLog(session.ExportIDToError(ctx))
			}
		} else {
			newError("Default Route For ", destination).WriteToLog(session.ExportIDToError(ctx))
		}
	}

	if handler == nil {
		handler = d.ohm.GetHandler(inTag) // Default outbound handler tag should be as same as the inbound tag
	}

	// If there is no outbound with tag as same as the inbound tag
	if handler == nil {
		handler = d.ohm.GetDefaultHandler()
	}

	if handler == nil {
		newError("Default outbound handler not exist").WriteToLog(session.ExportIDToError(ctx))
		common.Close(link.Writer)
		common.Interrupt(link.Reader)
		return
	}

	if accessMessage := log.AccessMessageFromContext(ctx); accessMessage != nil {
		if tag := handler.Tag(); tag != "" {
			if inTag == "" {
				accessMessage.Detour = tag
			} else if isPickRoute == 1 {
				accessMessage.Detour = inTag + " ==> " + tag
			} else if isPickRoute == 2 {
				accessMessage.Detour = inTag + " -> " + tag
			} else {
				accessMessage.Detour = inTag + " >> " + tag
			}
		}
		log.Record(accessMessage)
	}

	handler.Dispatch(ctx, link)
}