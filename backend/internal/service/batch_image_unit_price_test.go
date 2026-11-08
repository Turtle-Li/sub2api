package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBatchImageUnitPriceConvertsGeminiImageTokens(t *testing.T) {
	for _, test := range []struct {
		model      string
		size       string
		rate, want float64
	}{
		{"gemini-2.5-flash-image", "1K", 30e-6, 0.0387},
		{"gemini-3-pro-image", "1K", 120e-6, 0.1344},
		{"gemini-3-pro-image", "2K", 120e-6, 0.1344},
		{"gemini-3-pro-image-preview", "4K", 120e-6, 0.24},
		{"gemini-3.1-flash-image", "1K", 60e-6, 0.0672},
		{"gemini-3.1-flash-image-preview", "2K", 60e-6, 0.1008},
		{"gemini-3.1-flash-image", "4K", 60e-6, 0.1512},
		{"gemini-3.1-flash-lite-image", "1K", 30e-6, 0.0336},
	} {
		t.Run(test.model+"/"+test.size, func(t *testing.T) {
			// Given an image-output price denominated per token, not per image.
			billing := &BillingService{pricingService: &PricingService{pricingData: map[string]*LiteLLMModelPricing{
				test.model: {OutputCostPerImageToken: test.rate},
			}}}
			resolver := &BatchImageModelPricingResolver{Resolver: NewModelPricingResolver(nil, billing)}
			// When quoting one output at the requested resolution.
			price, err := resolver.BatchImageUnitPrice(context.Background(), &BatchImageJob{Model: test.model, ImageSize: test.size})
			// Then the charge is the price of the whole image.
			require.NoError(t, err)
			require.InDelta(t, test.want, price, 1e-12)
		})
	}
}

func TestBatchImageUnitPriceRejectsUnknownTokenConversion(t *testing.T) {
	// Given a token-priced model without a verified whole-image conversion.
	billing := &BillingService{pricingService: &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"unknown-image": {OutputCostPerImageToken: 30e-6},
	}}}
	resolver := &BatchImageModelPricingResolver{Resolver: NewModelPricingResolver(nil, billing)}
	// When requesting a flat image quote.
	_, err := resolver.BatchImageUnitPrice(context.Background(), &BatchImageJob{Model: "unknown-image", ImageSize: "1K"})
	// Then a token price cannot silently become an image price.
	require.ErrorIs(t, err, ErrBatchImageSettlementPricingMissing)
}
