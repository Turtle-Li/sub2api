package service

import (
	"strings"
	"unicode"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func defaultModelTraceAllowedDetections() map[string][]string {
	return map[string][]string{"gpt-6.1-sol": {"gpt-6-astra"}}
}

func normalizeModelTracePolicy(cfg *ModelTraceGlobalConfig) error {
	if cfg.NotificationCooldownMinutes == 0 {
		cfg.NotificationCooldownMinutes = 60
	}
	if cfg.NotificationCooldownMinutes < 1 || cfg.NotificationCooldownMinutes > 1440 {
		return infraerrors.BadRequest("MODELTRACE_CONFIG_INVALID", "notification cooldown must be between 1 and 1440 minutes")
	}
	if cfg.AllowedDetections == nil {
		cfg.AllowedDetections = defaultModelTraceAllowedDetections()
	}
	if len(cfg.AllowedDetections) > 50 {
		return infraerrors.BadRequest("MODELTRACE_CONFIG_INVALID", "configure at most 50 allowed model rules")
	}
	normalized := make(map[string][]string, len(cfg.AllowedDetections))
	for expected, candidates := range cfg.AllowedDetections {
		expected = strings.ToLower(strings.TrimSpace(expected))
		if !modelTracePolicyModelID(expected) || len(candidates) == 0 || len(candidates) > 10 {
			return infraerrors.BadRequest("MODELTRACE_CONFIG_INVALID", "each allowed model rule requires a model ID and 1 to 10 fingerprints")
		}
		if _, exists := normalized[expected]; exists {
			return infraerrors.BadRequest("MODELTRACE_CONFIG_INVALID", "duplicate allowed model rule")
		}
		models := make([]string, 0, len(candidates))
		seen := make(map[string]bool, len(candidates))
		for _, candidate := range candidates {
			candidate = strings.ToLower(strings.TrimSpace(candidate))
			if !modelTracePolicyModelID(candidate) || !modelTraceBankHasModel(candidate) {
				return infraerrors.BadRequest("MODELTRACE_CONFIG_INVALID", "invalid allowed fingerprint ID")
			}
			if !seen[candidate] {
				models = append(models, candidate)
				seen[candidate] = true
			}
		}
		normalized[expected] = models
	}
	cfg.AllowedDetections = normalized
	return nil
}

func modelTracePolicyModelID(model string) bool {
	return model != "" && len(model) <= 200 && !strings.ContainsAny(model, "*?=,") &&
		strings.IndexFunc(model, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) < 0
}
