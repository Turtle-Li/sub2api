package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
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

func mergeOpenAIModelBillingSettings(overrides OpenAIModelBillingSettings) OpenAIModelBillingSettings {
	merged := defaultOpenAIModelBillingSettings()
	for model, multiplier := range overrides.Multipliers {
		merged.Multipliers[model] = multiplier
	}
	return merged
}

func cloneOpenAIModelBillingSettings(settings OpenAIModelBillingSettings) OpenAIModelBillingSettings {
	cloned := OpenAIModelBillingSettings{Multipliers: make(map[string]float64, len(settings.Multipliers))}
	for model, multiplier := range settings.Multipliers {
		cloned.Multipliers[model] = multiplier
	}
	return cloned
}

func sortedOpenAIModelBillingMultipliers(settings OpenAIModelBillingSettings) map[string]float64 {
	// Return a fresh map so callers cannot mutate the immutable cache entry.
	keys := make([]string, 0, len(settings.Multipliers))
	for model := range settings.Multipliers {
		keys = append(keys, model)
	}
	sort.Strings(keys)
	result := make(map[string]float64, len(keys))
	for _, model := range keys {
		result[model] = settings.Multipliers[model]
	}
	return result
}

// GetOpenAIModelBillingSettings returns the effective model-level internal
// billing multipliers. Missing settings use the code defaults, while stored
// entries override or extend those defaults for new models.
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
		entry := &cachedOpenAIModelBillingSettings{settings: mergeOpenAIModelBillingSettings(normalized), expiresAt: time.Now().Add(openAIModelBillingSettingsCacheTTL).UnixNano()}
		s.openAIModelBillingSettingsCache.Store(entry)
		return entry, nil
	})
	if entry, ok := result.(*cachedOpenAIModelBillingSettings); ok && entry != nil {
		return cloneOpenAIModelBillingSettings(entry.settings), loadErr
	}
	return defaults, fmt.Errorf("load openai model billing settings: unavailable result")
}

// UpdateOpenAIModelBillingSettings validates and persists the full override
// map. The effective cache is refreshed synchronously, so new debits on this
// instance observe the change immediately.
func (s *SettingService) UpdateOpenAIModelBillingSettings(ctx context.Context, settings OpenAIModelBillingSettings) error {
	if s == nil || s.settingRepo == nil {
		return fmt.Errorf("openai model billing settings service is unavailable")
	}
	normalized, err := normalizeOpenAIModelBillingSettings(settings)
	if err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		Multipliers map[string]float64 `json:"multipliers"`
	}{Multipliers: sortedOpenAIModelBillingMultipliers(normalized)})
	if err != nil {
		return fmt.Errorf("marshal openai model billing settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyOpenAIModelBillingSettings, string(data)); err != nil {
		return fmt.Errorf("set openai model billing settings: %w", err)
	}
	effective := mergeOpenAIModelBillingSettings(normalized)
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
		return openAIConsumptionMultiplier(model)
	}
	settings, _ := s.GetOpenAIModelBillingSettings(context.Background())
	if multiplier, ok := settings.Multipliers[key]; ok {
		return multiplier
	}
	return openAIConsumptionMultiplier(model)
}
