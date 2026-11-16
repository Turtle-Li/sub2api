package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const openAIModelBillingSettingsCacheTTL = 5 * time.Second

// OpenAIModelBillingSettings contains per-model multipliers applied only to
// internal consumption/debit totals. It deliberately has no model-price
// fields, so changing this setting cannot change public model pricing.
type OpenAIModelBillingSettings struct {
	Multipliers map[string]float64 `json:"multipliers"`
}

type cachedOpenAIModelBillingSettings struct {
	settings  OpenAIModelBillingSettings
	expiresAt int64
}

func defaultOpenAIModelBillingSettings() OpenAIModelBillingSettings {
	multipliers := make(map[string]float64)
	for model, policy := range openAIModelBillingPolicies {
		if policy.hiddenConsumptionMultiplier > 0 && policy.hiddenConsumptionMultiplier != 1 {
			multipliers[model] = policy.hiddenConsumptionMultiplier
		}
	}
	return OpenAIModelBillingSettings{Multipliers: multipliers}
}

func normalizeOpenAIModelBillingKey(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return ""
	}
	if canonical := normalizeKnownOpenAICodexModel(model); canonical != "" {
		return canonical
	}
	return model
}

func normalizeOpenAIModelBillingSettings(settings OpenAIModelBillingSettings) (OpenAIModelBillingSettings, error) {
	normalized := OpenAIModelBillingSettings{Multipliers: make(map[string]float64, len(settings.Multipliers))}
	for model, multiplier := range settings.Multipliers {
		key := normalizeOpenAIModelBillingKey(model)
		if key == "" {
			return OpenAIModelBillingSettings{}, fmt.Errorf("model name cannot be empty")
		}
		if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier < 0.01 || multiplier > 100 {
			return OpenAIModelBillingSettings{}, fmt.Errorf("multiplier for %s must be between 0.01 and 100", key)
		}
		normalized.Multipliers[key] = multiplier
	}
	return normalized, nil
}

func effectiveOpenAIModelBillingSettings(stored OpenAIModelBillingSettings) OpenAIModelBillingSettings {
	effective := defaultOpenAIModelBillingSettings()
	for model, multiplier := range stored.Multipliers {
		if multiplier == 1 {
			delete(effective.Multipliers, model)
			continue
		}
		effective.Multipliers[model] = multiplier
	}
	return effective
}

func cloneOpenAIModelBillingSettings(settings OpenAIModelBillingSettings) OpenAIModelBillingSettings {
	cloned := OpenAIModelBillingSettings{Multipliers: make(map[string]float64, len(settings.Multipliers))}
	for model, multiplier := range settings.Multipliers {
		cloned.Multipliers[model] = multiplier
	}
	return cloned
}

// GetOpenAIModelBillingSettings returns the effective model-level internal
// billing multipliers. Legacy settings inherit defaults for unspecified models;
// a stored multiplier of 1 opts out and is omitted from the effective map.
func (s *SettingService) GetOpenAIModelBillingSettings(ctx context.Context) (OpenAIModelBillingSettings, error) {
	defaults := defaultOpenAIModelBillingSettings()
	if s == nil || s.settingRepo == nil {
		return defaults, fmt.Errorf("openai model billing settings service is unavailable")
	}
	if cached, ok := s.openAIModelBillingSettingsCache.Load().(*cachedOpenAIModelBillingSettings); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
		return cloneOpenAIModelBillingSettings(cached.settings), nil
	}

	result, loadErr, _ := s.openAIModelBillingSettingsSF.Do("openai-model-billing-settings", func() (any, error) {
		if cached, ok := s.openAIModelBillingSettingsCache.Load().(*cachedOpenAIModelBillingSettings); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
			return cached, nil
		}
		raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAIModelBillingSettings)
		if err != nil {
			// The setting is optional: absence means use the built-in defaults.
			if errors.Is(err, ErrSettingNotFound) {
				entry := &cachedOpenAIModelBillingSettings{settings: defaults, expiresAt: time.Now().Add(openAIModelBillingSettingsCacheTTL).UnixNano()}
				s.openAIModelBillingSettingsCache.Store(entry)
				return entry, nil
			}
			if cached, ok := s.openAIModelBillingSettingsCache.Load().(*cachedOpenAIModelBillingSettings); ok && cached != nil {
				return cached, err
			}
			return &cachedOpenAIModelBillingSettings{settings: defaults, expiresAt: time.Now().Add(openAIModelBillingSettingsCacheTTL).UnixNano()}, err
		}

		var stored OpenAIModelBillingSettings
		if err := json.Unmarshal([]byte(raw), &stored); err != nil {
			return &cachedOpenAIModelBillingSettings{settings: defaults, expiresAt: time.Now().Add(openAIModelBillingSettingsCacheTTL).UnixNano()}, fmt.Errorf("decode openai model billing settings: %w", err)
		}
		normalized, err := normalizeOpenAIModelBillingSettings(stored)
		if err != nil {
			return &cachedOpenAIModelBillingSettings{settings: defaults, expiresAt: time.Now().Add(openAIModelBillingSettingsCacheTTL).UnixNano()}, fmt.Errorf("normalize openai model billing settings: %w", err)
		}
		entry := &cachedOpenAIModelBillingSettings{settings: effectiveOpenAIModelBillingSettings(normalized), expiresAt: time.Now().Add(openAIModelBillingSettingsCacheTTL).UnixNano()}
		s.openAIModelBillingSettingsCache.Store(entry)
		return entry, nil
	})
	if entry, ok := result.(*cachedOpenAIModelBillingSettings); ok && entry != nil {
		return cloneOpenAIModelBillingSettings(entry.settings), loadErr
	}
	return defaults, fmt.Errorf("load openai model billing settings: unavailable result")
}

// UpdateOpenAIModelBillingSettings replaces all effective rules. An empty map
// disables them all. The effective cache is refreshed synchronously.
func (s *SettingService) UpdateOpenAIModelBillingSettings(ctx context.Context, settings OpenAIModelBillingSettings) error {
	if s == nil || s.settingRepo == nil {
		return fmt.Errorf("openai model billing settings service is unavailable")
	}
	if settings.Multipliers == nil {
		return errors.New("multipliers must be provided as an object")
	}
	normalized, err := normalizeOpenAIModelBillingSettings(settings)
	if err != nil {
		return err
	}
	// Persist omitted defaults as neutral overrides so older binaries do not
	// restore their debits during a rolling reload or rollback.
	for model := range defaultOpenAIModelBillingSettings().Multipliers {
		if _, configured := normalized.Multipliers[model]; !configured {
			normalized.Multipliers[model] = 1
		}
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal openai model billing settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyOpenAIModelBillingSettings, string(data)); err != nil {
		return fmt.Errorf("set openai model billing settings: %w", err)
	}
	effective := effectiveOpenAIModelBillingSettings(normalized)
	s.openAIModelBillingSettingsSF.Forget("openai-model-billing-settings")
	s.openAIModelBillingSettingsCache.Store(&cachedOpenAIModelBillingSettings{
		settings:  effective,
		expiresAt: time.Now().Add(openAIModelBillingSettingsCacheTTL).UnixNano(),
	})
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return nil
}

func (s *SettingService) OpenAIConsumptionMultiplier(model string) float64 {
	if s == nil {
		return openAIConsumptionMultiplier(model)
	}
	key := normalizeOpenAIModelBillingKey(model)
	if cached, ok := s.openAIModelBillingSettingsCache.Load().(*cachedOpenAIModelBillingSettings); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
		if multiplier, ok := cached.settings.Multipliers[key]; ok {
			return multiplier
		}
		return 1
	}
	settings, _ := s.GetOpenAIModelBillingSettings(context.Background())
	if multiplier, ok := settings.Multipliers[key]; ok {
		return multiplier
	}
	return 1
}
