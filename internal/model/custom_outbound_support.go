package model

// xrayOutboundSupport lists the outbound protocols and features supported by
// the xray kernel, the only kernel.
var xrayOutboundSupport = KernelOutboundSupport{
	Protocols: []string{
		"vmess",
		"vless",
		"trojan",
		"shadowsocks",
		"socks",
		"http",
		"wireguard",
	},
	Features: []string{
		"tag",
		"protocol",
		"settings",
		"proxy_tag",
	},
}

type KernelOutboundSupport struct {
	Protocols []string
	Features  []string
}
