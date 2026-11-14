package model

// xrayRouteSupport lists the route matchers and actions supported by the xray
// kernel, the only kernel.
var xrayRouteSupport = KernelRouteSupport{
	Matchers: []string{
		"domains",
		"domain_suffixes",
		"ip_cidrs",
		"ports",
		"networks",
		"source_cidrs",
		"source_ports",
	},
	Actions: []string{"block", "direct", "route"},
}

type KernelRouteSupport struct {
	Matchers []string
	Actions  []string
}
