package service

import "strings"

func isKimi31PricingModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "kimi-k3.1-pro", "k3.1-pro", "cline-pass/kimi-k3.1-pro":
		return true
	default:
		return false
	}
}

// Native Kimi CNY prices, per token, verified 2026-10-25:
// https://platform.kimi.com/docs/pricing/chat
func kimiOfficialCNYPricing(model string) *ModelPricing {
	if isKimi31PricingModel(model) {
		return &ModelPricing{
			InputPricePerToken: 13e-6, OutputPricePerToken: 68e-6,
			CacheReadPricePerToken: 1.3e-6, CacheCreationPricePerToken: 13e-6,
			CacheCreation5mPrice: 13e-6, CacheCreation1hPrice: 26e-6, SupportsCacheBreakdown: true,
		}
	}
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "kimi-k3", "k3", "k3-256k", "cline-pass/kimi-k3":
		return &ModelPricing{
			InputPricePerToken: 20e-6, OutputPricePerToken: 100e-6,
			CacheReadPricePerToken: 2e-6, CacheCreationPricePerToken: 20e-6,
			CacheCreation5mPrice: 20e-6, CacheCreation1hPrice: 40e-6, SupportsCacheBreakdown: true,
		}
	default:
		return nil
	}
}

// GetModelPricingForAuthoring keeps native CNY quotes exact. Other catalog
// models are converted with the same active FX policy used by channel billing.
func (s *BillingService) GetModelPricingForAuthoring(model, currency string) (*ModelPricing, error) {
	if currency == PricingCurrencyCNY {
		if native := kimiOfficialCNYPricing(model); native != nil {
			return native, nil
		}
	}
	if !s.HasIdentifiedTokenPricing(model) {
		return nil, ErrModelPricingUnavailable
	}
	pricing, err := s.GetModelPricing(model)
	if err != nil {
		return nil, err
	}
	copy := *pricing
	if currency == PricingCurrencyCNY {
		scaleAuthoringTokenPrices(&copy, s.currentCurrencyPolicy().USDToCNYRate)
	}
	return &copy, nil
}

func scaleAuthoringTokenPrices(pricing *ModelPricing, factor float64) {
	for _, price := range []*float64{
		&pricing.InputPricePerToken, &pricing.OutputPricePerToken,
		&pricing.CacheCreationPricePerToken, &pricing.CacheCreation5mPrice,
		&pricing.CacheCreation1hPrice, &pricing.CacheReadPricePerToken,
		&pricing.ImageInputPricePerToken, &pricing.ImageOutputPricePerToken,
	} {
		*price *= factor
	}
}
