<p align="center">
<picture>
<img width="160" height="160"  alt="XPanel" src="https://github.com/iPmartNetwork/iPmart-SSH/blob/main/images/logo.png">
</picture>
  </p> 
<p align="center">
<h1 align="center"/>XMPlus</h1>
<h6 align="center">XManagerPlus<h6>
</p>


## Features

|Function             | vmess | vless | trojan | ss   | ss-plugin|
| ----------------| ----- | ----- | -------| -----|----------|
| Get Server Info     | √     |  √    |   √    |  √   |    √     |
| Get User Info     | √     |  √    |   √    |  √   |    √     |
| Report User Stats     | √     |  √    |   √    |  √   |    √     |
| Report Server Stats   | √     |  √    |   √    |  √   |    √     |
| Auto Apply tls Cert  | √     |  √    |   √    |  √   |    √     |
| Auto-Renew tls Cert  | √     |  √    |   √    |  √   |    √     |
| Online Statistics     | √     |  √    |   √    |  √   |    √     |
| Online User IPlinit   | √     |  √    |   √    |  √   |    √     |
| Detection Rules        | √     |  √    |   √    |  √   |    √     |
| Server Speedlimit     | √     |  √    |   √    |  √   |    √     |
| User Speedlimit     | √     |  √    |   √    |  √   |    √     |
| Customize DNS       | √     |  √    |   √    |  √   |    √     |
| Server Relay       | √     |  √    |   √    |  √   |    X     |


#### Config directory
```
cd /etc/XMPlus
```

### Onclick Install
```
bash <(curl -Ls https://raw.githubusercontent.com/ipmartnetwork/XMPlus/install/install.sh)
```

## FRONTEND SERVER CONFIG

### Network Settings


#### TCP
```
{
  "transport" : "tcp",
  "acceptProxyProtocol": false,
  "flow": "xtls-rprx-vision",
  "header": {
    "type": "none"
  }
}
```
#### TCP + HTTP
```
{
  "transport" : "tcp",
  "acceptProxyProtocol": false,
  "header": {
    "type": "http",
    "request": {
      "path": "/xmplus",
      "headers": {
        "Host": "x.tld.com"
      }
    }
  }
}
```
####  WS
```
{
  "transport" : "ws",
  "acceptProxyProtocol": false,
  "path": "/xmplus",
  "headers": {
    "Host": "x.tld.com"
  }
}
```

####  H2
```
{
  "transport" : "h2",
  "acceptProxyProtocol": false,
  "host": "x.tld.com",
  "path": "/xmplus"
}
```

####  GRPC
```
{
  "transport" : "grpc",
  "acceptProxyProtocol": false,
  "serviceName": "xmplus"
}
```
####  QUIC
```
{
  "transport" : "quic",
  "acceptProxyProtocol": false,
  "security": "none",
  "key": "",
  "header": {
    "type": "none"
  }
}
```
####  KCP
```
{
  "transport" : "kcp",
  "acceptProxyProtocol": false,
  "congestion": false,
  "header": {
    "type": "none"
  },
  "seed": "password"
}
```

### Security Settings


#### TLS
```
{
  "serverName": "xmplus.dev",
  "rejectUnknownSni": true,
  "allowInsecure": false,
  "fingerprint": "chrome"
}
```
#### REALITY
[Generate Private and Public Keys Here](https://go.dev/play/p/N5kQhIjtye7)
```
{
  "show" : false,
  "dest": "www.lovelive-anime.jp:443",
  "privatekey" : "yBaw532IIUNuQWDTncozoBaLJmcd1JZzvsHUgVPxMk8",
  "minclientver":"",
  "maxclientver":"",
  "maxtimediff":0,
  "proxyprotocol":0,
  "shortids" : [
    "6ba85179e30d4fc2"
  ],
  "serverNames": [
    "www.lovelive-anime.jp",
    "www.cloudflare.com"
  ],
  "fingerprint": "chrome",
  "spiderx": "",
  "publickey": "7xhH4b_VkliBxGulljcyPOH-bYUA2dl-XAdZAsfhk04"
}
```

## BACKEND SERVER CONFIG

```
Log:
  Level: warning # Log level: none, error, warning, info, debug 
  AccessPath: # /etc/XMPlus/access.Log
  ErrorPath: # /etc/XMPlus/error.log
DnsConfigPath:  #/etc/XMPlus/dns.json
RouteConfigPath: # /etc/XMPlus/route.json
InboundConfigPath: # /etc/XMPlus/inbound.json
OutboundConfigPath: # /etc/XMPlus/outbound.json
ConnectionConfig:
  Handshake: 8 
  ConnIdle: 300 
  UplinkOnly: 0 
  DownlinkOnly: 0 
  BufferSize: 64
Nodes:
  -
    ApiConfig:
      ApiHost: "https://www.xyz.com"
      ApiKey: "123"
      NodeID: 1
      Timeout: 30 
    ControllerConfig:
      CertConfig:
        Email: author@xmplus.dev                    # Required when Cert Mode is not none
        CertFile: /etc/XMPlus/node1.xmplus.dev.crt  # Required when Cert Mode is file
        KeyFile: /etc/XMPlus/node1.xmplus.dev.key   # Required when Cert Mode is file
        Provider: cloudflare                        # Required when Cert Mode is dns
        CertEnv:                                    # Required when Cert Mode is dns
          CLOUDFLARE_EMAIL:                         # Required when Cert Mode is dns
          CLOUDFLARE_API_KEY:                       # Required when Cert Mode is dns
      EnableDNS: false # Use custom DNS config, Please ensure that you set the dns.json well
      DNSStrategy: AsIs # AsIs, UseIP, UseIPv4, UseIPv6
      EnableFallback: false # Only support for Trojan and Vless
      FallBackConfigs:  # Support multiple fallbacks
        - SNI: # TLS SNI(Server Name Indication), Empty for any
          Alpn: # Alpn, Empty for any
          Path: # HTTP PATH, Empty for any
          Dest: 80 # Required, Destination of fallback, check https://xtls.github.io/config/features/fallback.html for details.
          ProxyProtocolVer: 0 # Send PROXY protocol version, 0 for disable
      EnableFragment: false 
      FragmentConfigs:
        Packets: "tlshello" # TLS Hello Fragmentation (into multiple handshake messages)
        Length: "100-200"   # minLength to maxLength
        Interval: "10-20"   # minInterval to maxInterval   
```


# تلگرام

[@ipmart_network](https://t.me/ipmart_network)

[@iPmart Group](https://t.me/ipmartnetwork_gp)




 # حمایت از ما :hearts:
حمایت های شما برای ما دلگرمی بزرگی است<br> 
<p align="left">
<a href="https://plisio.net/donate/kB7QU7f7" target="_blank"><img src="https://plisio.net/img/donate/donate_light_icons_mono.png" alt="Donate Crypto on Plisio" width="240" height="80" /></a><br>
	
|                    TRX                   |                       BNB                         |                    Litecoin                       |
| ---------------------------------------- |:-------------------------------------------------:| -------------------------------------------------:|
| ```TJbTYV1fFo2485sYMyajxGPLFzxmNmPrNA``` |  ```0x4af3de9b303a8d43105e284823d95b4c600961a3``` | ```MPrkzFiNtw4Rg67bbZB6gCxa9LV87orABM``` |	

</p>	




<p align="center">
<picture>
<img width="160" height="160"  alt="XPanel" src="https://github.com/iPmartNetwork/iPmart-SSH/blob/main/images/logo.png">
</picture>
  </p> 
