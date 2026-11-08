package service

import (
	"math"
	"strings"
)

// Native CNY image cards reuse the existing per-model group pricing editor.
// Their authored number is already a wallet-unit price on standard OpenAI
// groups, like ImagePrice1K. This permits several native models on one key
// without applying Muse's group default price to Wan/Seedream.
func batchImageNativeModelPrice(group *Group, req BatchImageSubmitRequest, provider string) (float64, bool, error) {
	if !batchImageUsesOpenAIWalletPricing(provider, group) ||
		(provider != BatchImageProviderMuse && provider != BatchImageProviderWorkBuddy && !isImageBridgeProvider(provider)) {
		return 0, false, nil
	}
	card := matchGroupModelPricing(group, req.Model)
	if card == nil || card.Currency != PricingCurrencyCNY || (card.BillingMode != BillingModeImage && card.BillingMode != BillingModePerRequest) {
		return 0, false, nil
	}
	price := card.PerRequestPrice
	for _, tier := range card.Intervals {
		if strings.EqualFold(tier.TierLabel, req.ImageSize) && tier.PerRequestPrice != nil {
			price = tier.PerRequestPrice
			break
		}
	}
	if price == nil || *price <= 0 || math.IsNaN(*price) || math.IsInf(*price, 0) {
		return 0, true, ErrBatchImageSettlementPricingMissing
	}
	return *price, true, nil
}
