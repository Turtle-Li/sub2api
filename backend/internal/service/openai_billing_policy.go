package service

// openAIModelBillingPolicy is the single code-level configuration point for
// OpenAI model price adjustments. Official prices are USD per token.
//
// The customer-facing model price is official price * modelPriceMultiplier.
// The billed total then applies hiddenConsumptionMultiplier before the
// existing group/user multiplier. The hidden multiplier never changes the
// component prices exposed by pricing APIs or the model plaza.
type openAIModelBillingPolicy struct {
	officialInputPrice      float64
	officialOutputPrice     float64
	officialCacheWritePrice float64
	officialCacheReadPrice  float64

	modelPriceMultiplier        float64
	hiddenConsumptionMultiplier float64
}

// Update model price and hidden settlement adjustments here. Models without
// an official price override may leave the official price fields at zero.
var openAIModelBillingPolicies = map[string]openAIModelBillingPolicy{
	"gpt-6-astra": {
		modelPriceMultiplier:        1,
		hiddenConsumptionMultiplier: 1.5,
	},
	"gpt-6-sol": {
		modelPriceMultiplier:        1,
		hiddenConsumptionMultiplier: 1.8,
	},
	"gpt-5.6-luna": {
		officialInputPrice:          0.2e-6,
		officialOutputPrice:         1.2e-6,
		officialCacheWritePrice:     0.25e-6,
		officialCacheReadPrice:      0.02e-6,
		modelPriceMultiplier:        2.5,
		hiddenConsumptionMultiplier: 1.5,
	},
	"gpt-6-luna": {
		officialInputPrice:          0.1e-6,
		officialOutputPrice:         0.5e-6,
		officialCacheWritePrice:     0.125e-6,
		officialCacheReadPrice:      0.01e-6,
		modelPriceMultiplier:        2.5,
		hiddenConsumptionMultiplier: 1.5,
	},
}

func openAIModelBillingPolicyFor(model string) (openAIModelBillingPolicy, bool) {
	policy, ok := openAIModelBillingPolicies[normalizeKnownOpenAICodexModel(model)]
	return policy, ok
}

func openAIConsumptionMultiplier(model string) float64 {
	policy, ok := openAIModelBillingPolicyFor(model)
	if !ok || policy.hiddenConsumptionMultiplier <= 0 {
		return 1
	}
	return policy.hiddenConsumptionMultiplier
}

func openAIModelPriceMultiplier(model string) float64 {
	policy, ok := openAIModelBillingPolicyFor(model)
	if !ok || policy.modelPriceMultiplier <= 0 {
		return 1
	}
	return policy.modelPriceMultiplier
}

func applyOpenAIConfiguredModelPrice(model string, pricing *ModelPricing, customerFacing bool) *ModelPricing {
	if pricing == nil {
		return nil
	}
	policy, ok := openAIModelBillingPolicyFor(model)
	if !ok || policy.officialInputPrice <= 0 || policy.officialOutputPrice <= 0 {
		return pricing
	}

	multiplier := 1.0
	if customerFacing {
		multiplier = openAIModelPriceMultiplier(model)
	}

	cloned := *pricing
	cloned.InputPricePerToken = policy.officialInputPrice * multiplier
	cloned.OutputPricePerToken = policy.officialOutputPrice * multiplier
	cloned.CacheCreationPricePerToken = policy.officialCacheWritePrice * multiplier
	cloned.CacheReadPricePerToken = policy.officialCacheReadPrice * multiplier
	cloned.CacheCreationPriceExplicit = true
	cloned.CacheCreation5mPrice = cloned.CacheCreationPricePerToken
	cloned.CacheCreation1hPrice = 0
	cloned.SupportsCacheBreakdown = false

	if fastRatio := openAIModelFastPricingRatio(normalizeKnownOpenAICodexModel(model)); fastRatio > 0 {
		enforceOpenAIFastPricingRatio(&cloned, fastRatio)
	}
	return &cloned
}
