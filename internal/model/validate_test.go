package model

import (
	"strings"
	"testing"

	"github.com/cedar2025/xboard-node/internal/config"
)

// xray is the only kernel: transport restrictions per kernel are gone.
func TestValidateNodeSpec_AcceptsXhttpTransport(t *testing.T) {
	spec := &NodeSpec{Protocol: "vless", ServerPort: 443, Network: "xhttp"}
	if err := ValidateNodeSpec(spec, config.KernelConfig{Type: "xray"}); err != nil {
		t.Fatalf("expected xhttp transport to be accepted, got %v", err)
	}
}

// Legacy kernel_type values are accepted but ignored: validation succeeds and
// only a one-time deprecation warning is emitted.
func TestValidateNodeSpec_LegacyKernelTypeIgnored(t *testing.T) {
	spec := &NodeSpec{Protocol: "vless", ServerPort: 443, Network: "xhttp", KernelType: "singbox"}
	if err := ValidateNodeSpec(spec, config.KernelConfig{}); err != nil {
		t.Fatalf("legacy kernel_type should validate successfully (warning only), got %v", err)
	}
}

// Outbound protocols that only sing-box supported are rejected against the
// xray support set.
func TestValidateCustomOutbounds_RejectsSingboxOnlyProtocols(t *testing.T) {
	for _, protocol := range []string{"tuic", "hysteria2", "anytls"} {
		outbounds := []OutboundConfig{{
			Tag:      "custom",
			Protocol: protocol,
			Settings: map[string]any{"server": "1.2.3.4", "server_port": 443},
		}}
		err := ValidateCustomOutbounds(outbounds, nil)
		if err == nil {
			t.Fatalf("expected %q outbound to be rejected, got nil", protocol)
		}
		if !strings.Contains(err.Error(), `is not supported by the xray kernel`) {
			t.Fatalf("unexpected error for %q: %v", protocol, err)
		}
	}
}
