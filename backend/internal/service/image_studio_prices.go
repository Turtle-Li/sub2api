package service

import (
	"context"
	"math"
)

// Quotes reuse submission pricing, including user/group/account rates and native
// batch discounts. A range is honest when eligible accounts have different rates.
type BatchImageModelPriceRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

func (s *BatchImagePublicService) cataloguePrices(ctx context.Context, owner BatchImageOwner, provider, model string, account *Account) map[string]BatchImageModelPriceRange {
	sizes := []string{"1K", "2K", "4K"}
	if caps := s.modelCapabilities(model); caps != nil {
		sizes = caps.ImageSizes
	}
	prices := make(map[string]BatchImageModelPriceRange)
	for _, size := range sizes {
		quote, err := s.resolvePricingSnapshot(ctx, owner, BatchImageSubmitRequest{Model: model, ImageSize: size}, provider, account)
		if err != nil || math.IsNaN(quote.BillableUnitPrice) || math.IsInf(quote.BillableUnitPrice, 0) || quote.BillableUnitPrice < 0 {
			continue
		}
		prices[size] = BatchImageModelPriceRange{Min: quote.BillableUnitPrice, Max: quote.BillableUnitPrice}
	}
	return prices
}

func mergeBatchImagePrices(target, source map[string]BatchImageModelPriceRange) {
	for size, value := range source {
		if current, ok := target[size]; ok {
			value.Min = math.Min(current.Min, value.Min)
			value.Max = math.Max(current.Max, value.Max)
		}
		target[size] = value
	}
}
