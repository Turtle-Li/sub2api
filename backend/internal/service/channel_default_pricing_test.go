//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKimi31Pricing_PreservesNativeCNYAndNormalizesBillingBasis(t *testing.T) {
	billing := NewBillingService(nil, nil)
	billing.currencyPolicy.Store(PricingCurrencySettings{SettlementCurrency: "CNY", USDToCNYRate: 7.4})
	for _, model := range []string{"kimi-k3.1-pro", "k3.1-pro", "cline-pass/kimi-k3.1-pro"} {
		t.Run(model, func(t *testing.T) {
			pricing, err := billing.GetModelPricingForAuthoring(model, "CNY")
			require.NoError(t, err)
			require.InDelta(t, 13e-6, pricing.InputPricePerToken, 1e-12)
			usd, err := billing.GetModelPricing(model)
			require.NoError(t, err)
			require.InDelta(t, 13e-6/7.4, usd.InputPricePerToken, 1e-12)
			require.InDelta(t, 68e-6/7.4, usd.OutputPricePerToken, 1e-12)
			require.InDelta(t, 26e-6/7.4, usd.CacheCreation1hPrice, 1e-12)
			require.True(t, usd.SupportsCacheBreakdown)
		})
	}
}

func TestKimi31Pricing_RejectsUnknownVariants(t *testing.T) {
	billing := NewBillingService(nil, nil)
	for _, model := range []string{"kimi-k3.1-pro-highspeed", "kimi-k3.1-pro-new", "foo-kimi-k3.1-pro", "k3.1-pro[1m]", "foo/kimi-k3.1-pro", "cline-pass/k3.1-pro"} {
		_, err := billing.GetModelPricingForAuthoring(model, "CNY")
		require.ErrorIs(t, err, ErrModelPricingUnavailable, model)
		require.False(t, billing.HasIdentifiedTokenPricing(model), model)
	}
}

func TestModelAuthoringCurrency_RejectsFamilyGuesses(t *testing.T) {
	billing := NewBillingService(nil, nil)
	for _, model := range []string{"claude-unknown-model", "deepseek-foo"} {
		for _, currency := range []string{"CNY", "USD"} {
			_, err := billing.GetModelPricingForAuthoring(model, currency)
			require.ErrorIs(t, err, ErrModelPricingUnavailable, model)
		}
	}
}

func TestKimi31Pricing_IdentifiesOnlySupportedNames(t *testing.T) {
	billing := NewBillingService(nil, nil)
	for _, model := range []string{"kimi-k3.1-pro", "k3.1-pro", "cline-pass/kimi-k3.1-pro", " KIMI-K3.1-PRO "} {
		require.True(t, billing.HasIdentifiedTokenPricing(model), model)
	}
}

func TestKimi31Pricing_DynamicCatalogRejectsUnknownPrefixes(t *testing.T) {
	pricing := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"kimi-k3.1-pro": {InputCostPerToken: 2e-6, OutputCostPerToken: 10e-6},
	}}
	billing := NewBillingService(nil, pricing)
	for _, model := range []string{
		"foo/kimi-k3.1-pro", "models/foo/kimi-k3.1-pro", "cline-pass/k3.1-pro",
		"kimi-k3.1-pro-20261025", "k3.1-pro-20261025", "kimi-k3.1-pro-v1:0",
	} {
		require.False(t, billing.HasIdentifiedTokenPricing(model), model)
		for _, currency := range []string{"CNY", "USD"} {
			_, err := billing.GetModelPricingForAuthoring(model, currency)
			require.ErrorIs(t, err, ErrModelPricingUnavailable, model)
		}
	}
}

func TestModelAuthoringCurrency_NativeCNYRemainsIndependentOfUSDCatalog(t *testing.T) {
	pricing := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"kimi-k3.1-pro": {InputCostPerToken: 2e-6, OutputCostPerToken: 10e-6},
	}}
	billing := NewBillingService(nil, pricing)
	cny, err := billing.GetModelPricingForAuthoring("kimi-k3.1-pro", "CNY")
	require.NoError(t, err)
	require.InDelta(t, 13e-6, cny.InputPricePerToken, 1e-12)
	usd, err := billing.GetModelPricingForAuthoring("kimi-k3.1-pro", "USD")
	require.NoError(t, err)
	require.InDelta(t, 2e-6, usd.InputPricePerToken, 1e-12)
}

func TestModelAuthoringCurrency_DoesNotMutateExistingDefault(t *testing.T) {
	billing := NewBillingService(nil, nil)
	cny, err := billing.GetModelPricingForAuthoring("claude-sonnet-4", "CNY")
	require.NoError(t, err)
	require.InDelta(t, 3e-6*6.75, cny.InputPricePerToken, 1e-12)
	usd, err := billing.GetModelPricing("claude-sonnet-4")
	require.NoError(t, err)
	require.InDelta(t, 3e-6, usd.InputPricePerToken, 1e-12)
}
