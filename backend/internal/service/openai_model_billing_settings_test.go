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
	require.Equal(t, 3.0, effective.Multipliers["gpt-6.1-sol"])
	require.Equal(t, 1.25, effective.Multipliers["new-openai-model"])
	// Code defaults remain effective for models that were not overridden.
	require.Equal(t, 1.8, effective.Multipliers["gpt-6-sol"])
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
