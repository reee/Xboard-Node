package model

import (
	"fmt"
	"strings"
	"sync"

	"github.com/cedar2025/xboard-node/internal/config"
	"github.com/cedar2025/xboard-node/internal/nlog"
)

// legacyKernelTypeOnce guards the one-time process-wide deprecation warning
// for legacy kernel_type values (sing-box support was removed; xray is the
// only kernel).
var legacyKernelTypeOnce sync.Once

func ValidateNodeSpec(n *NodeSpec, kcfg config.KernelConfig) error {
	if n == nil {
		return nil
	}

	if legacy := strings.TrimSpace(n.KernelType); legacy != "" && !strings.EqualFold(legacy, "xray") {
		legacyKernelTypeOnce.Do(func() {
			nlog.Core().Warn("sing-box kernel support was removed; legacy kernel_type is ignored, using xray",
				"kernel_type", legacy)
		})
	}

	additionalOutboundSources, err := collectAdditionalOutboundTagSources(kcfg.CustomConfig, kcfg.CustomOutbound)
	if err != nil {
		return fmt.Errorf("collect additional outbound tags: %w", err)
	}
	if err := validateOutboundTagCollisions(n.CustomOutbounds, additionalOutboundSources); err != nil {
		return fmt.Errorf("validate outbound tags: %w", err)
	}
	additionalTags := additionalTagNames(additionalOutboundSources)
	availableTags := buildAvailableOutboundTags(n.CustomOutbounds, additionalTags)
	if err := ValidateCustomOutbounds(n.CustomOutbounds, additionalTags); err != nil {
		return fmt.Errorf("validate custom outbounds: %w", err)
	}
	if err := ValidateCustomRouteRules(n.CustomRouteRules, availableTags); err != nil {
		return fmt.Errorf("validate custom route rules: %w", err)
	}
	return nil
}

func buildAvailableOutboundTags(structured []OutboundConfig, rawTags []string) map[string]struct{} {
	available := map[string]struct{}{
		"direct": {},
		"block":  {},
	}
	for _, outbound := range structured {
		tag := strings.ToLower(strings.TrimSpace(outbound.Tag))
		if tag != "" {
			available[tag] = struct{}{}
		}
	}
	for _, tag := range rawTags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag != "" {
			available[tag] = struct{}{}
		}
	}
	return available
}
