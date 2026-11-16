package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type openAIModelBillingSettingsRepoStub struct {
	values map[string]string
}

func (s *openAIModelBillingSettingsRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, ErrSettingNotFound
}

func (s *openAIModelBillingSettingsRepoStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (s *openAIModelBillingSettingsRepoStub) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func (s *openAIModelBillingSettingsRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, errors.New("unexpected GetMultiple call")
}

func (s *openAIModelBillingSettingsRepoStub) SetMultiple(context.Context, map[string]string) error {
	return errors.New("unexpected SetMultiple call")
}

func (s *openAIModelBillingSettingsRepoStub) GetAll(context.Context) (map[string]string, error) {
	return nil, errors.New("unexpected GetAll call")
}

func (s *openAIModelBillingSettingsRepoStub) Delete(context.Context, string) error {
	return errors.New("unexpected Delete call")
}

func TestOpenAIModelBillingSettingsDefaultsAndRuntimeOverride(t *testing.T) {
	repo := &openAIModelBillingSettingsRepoStub{values: map[string]string{}}
	settings := NewSettingService(repo, &config.Config{})

	effective, err := settings.GetOpenAIModelBillingSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2.0, effective.Multipliers["gpt-6.1-sol"])
	require.Equal(t, 1.8, effective.Multipliers["gpt-6-sol"])

	require.NoError(t, settings.UpdateOpenAIModelBillingSettings(context.Background(), OpenAIModelBillingSettings{
		Multipliers: map[string]float64{
			"gpt-6.1-sol":      3,
			"new-openai-model": 1.25,
		},
	}))

	effective, err = settings.GetOpenAIModelBillingSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, map[string]float64{"gpt-6.1-sol": 3, "new-openai-model": 1.25}, effective.Multipliers)
}

func TestOpenAIModelBillingSettingsRemovalPersistsAcrossInstances(t *testing.T) {
	repo := &openAIModelBillingSettingsRepoStub{values: map[string]string{
		SettingKeyOpenAIModelBillingSettings: `{"multipliers":{"gpt-6-luna":2.25,"custom-openai-model":4}}`,
	}}
	settings := NewSettingService(repo, &config.Config{})
	before, err := settings.GetOpenAIModelBillingSettings(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2.0, before.Multipliers["gpt-6.1-sol"])
	require.Equal(t, 4.0, before.Multipliers["custom-openai-model"])

	retained := OpenAIModelBillingSettings{Multipliers: map[string]float64{
		"gpt-5.6-luna": before.Multipliers["gpt-5.6-luna"],
		"gpt-6-luna":   before.Multipliers["gpt-6-luna"],
	}}
	require.NoError(t, settings.UpdateOpenAIModelBillingSettings(t.Context(), retained))
	reloaded := NewSettingService(repo, &config.Config{})
	for _, instance := range []*SettingService{settings, reloaded} {
		effective, err := instance.GetOpenAIModelBillingSettings(t.Context())
		require.NoError(t, err)
		require.Equal(t, retained, effective)
		require.Equal(t, 1.0, instance.OpenAIConsumptionMultiplier("gpt-6.1-sol"))
	}
	// Older binaries merge with defaults. Their stored overrides must still cancel them.
	require.JSONEq(t, `{"multipliers":{"gpt-5.6-luna":1.5,"gpt-6-luna":2.25,"gpt-6-astra":1,"gpt-6-sol":1,"gpt-6.1-sol":1}}`, repo.values[SettingKeyOpenAIModelBillingSettings])
}

func TestOpenAIModelBillingSettingsNeutralOverridesUseGroupDebit(t *testing.T) {
	repo := &openAIModelBillingSettingsRepoStub{values: map[string]string{
		SettingKeyOpenAIModelBillingSettings: `{"multipliers":{"gpt-5.6-luna":1.5,"gpt-6-luna":1.5,"gpt-6-astra":1,"gpt-6-sol":1,"gpt-6.1-sol":1}}`,
	}}
	settings := NewSettingService(repo, &config.Config{})
	effective, err := settings.GetOpenAIModelBillingSettings(t.Context())
	require.NoError(t, err)
	require.Equal(t, map[string]float64{"gpt-5.6-luna": 1.5, "gpt-6-luna": 1.5}, effective.Multipliers)

	billing := NewBillingService(&config.Config{}, nil)
	billing.SetSettingService(settings)
	const groupRate = 1.6
	for _, tc := range []struct {
		model    string
		internal float64
	}{
		{"gpt-6-astra", 1},
		{"gpt-6-sol", 1},
		{"gpt-6.1-sol", 1},
		{"gpt-5.6-luna", 1.5},
		{"gpt-6-luna", 1.5},
	} {
		t.Run(tc.model, func(t *testing.T) {
			cost, err := billing.CalculateCostUnified(CostInput{
				Model:          tc.model,
				Tokens:         UsageTokens{InputTokens: 100, OutputTokens: 50},
				RateMultiplier: groupRate,
			})
			require.NoError(t, err)
			base := cost.InputCost + cost.OutputCost
			require.Positive(t, base)
			require.InDelta(t, base*tc.internal, cost.TotalCost, 1e-12)
			require.InDelta(t, base*tc.internal*groupRate, cost.ActualCost, 1e-12)
		})
	}
}

func TestOpenAIModelBillingSettingsClearsAllOnlyWithExplicitMap(t *testing.T) {
	repo := &openAIModelBillingSettingsRepoStub{values: map[string]string{}}
	settings := NewSettingService(repo, &config.Config{})
	require.Error(t, settings.UpdateOpenAIModelBillingSettings(t.Context(), OpenAIModelBillingSettings{}))
	require.Empty(t, repo.values)
	require.NoError(t, settings.UpdateOpenAIModelBillingSettings(t.Context(), OpenAIModelBillingSettings{
		Multipliers: map[string]float64{},
	}))

	reloaded := NewSettingService(repo, &config.Config{})
	effective, err := reloaded.GetOpenAIModelBillingSettings(t.Context())
	require.NoError(t, err)
	require.NotNil(t, effective.Multipliers)
	require.Empty(t, effective.Multipliers)
	require.Equal(t, 1.0, reloaded.OpenAIConsumptionMultiplier("gpt-6-luna"))
}

func TestBillingServiceUsesRuntimeOpenAIModelBillingMultiplier(t *testing.T) {
	repo := &openAIModelBillingSettingsRepoStub{values: map[string]string{
		SettingKeyOpenAIModelBillingSettings: `{"multipliers":{"gpt-6.1-sol":3}}`,
	}}
	settings := NewSettingService(repo, &config.Config{})
	billing := NewBillingService(&config.Config{}, nil)
	billing.SetSettingService(settings)

	base, err := billing.CalculateCost("gpt-6.1-sol", UsageTokens{InputTokens: 100, OutputTokens: 50}, 1)
	require.NoError(t, err)
	withoutRuntime := NewBillingService(&config.Config{}, nil)
	defaultCost, err := withoutRuntime.CalculateCost("gpt-6.1-sol", UsageTokens{InputTokens: 100, OutputTokens: 50}, 1)
	require.NoError(t, err)

	require.InDelta(t, defaultCost.TotalCost*1.5, base.TotalCost, defaultCost.TotalCost*0.001)
	require.InDelta(t, defaultCost.ActualCost*1.5, base.ActualCost, defaultCost.ActualCost*0.001)
}

func TestOpenAIModelBillingSettingsRejectInvalidMultiplier(t *testing.T) {
	repo := &openAIModelBillingSettingsRepoStub{values: map[string]string{}}
	settings := NewSettingService(repo, &config.Config{})
	err := settings.UpdateOpenAIModelBillingSettings(context.Background(), OpenAIModelBillingSettings{
		Multipliers: map[string]float64{"gpt-6.1-sol": 0},
	})
	require.Error(t, err)
}
