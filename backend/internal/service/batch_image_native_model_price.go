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
	card := nativeWalletImagePriceCard(group, req.Model)
	if card == nil {
		return 0, false, nil
	}
	price := nativeWalletImageTierPrice(card, req.ImageSize)
	if !positiveNativeImagePrice(price) {
		return 0, true, ErrBatchImageSettlementPricingMissing
	}
	return *price, true, nil
}

func nativeWalletImagePriceCard(group *Group, model string) *ChannelModelPricing {
	card := matchGroupModelPricing(group, model)
	if card == nil || card.Currency != PricingCurrencyCNY || (card.BillingMode != BillingModeImage && card.BillingMode != BillingModePerRequest) {
		return nil
	}
	return card
}

func nativeWalletImageTierPrice(card *ChannelModelPricing, size string) *float64 {
	if card == nil {
		return nil
	}
	for _, tier := range card.Intervals {
		if strings.EqualFold(tier.TierLabel, size) && tier.PerRequestPrice != nil {
			return tier.PerRequestPrice
		}
	}
	return card.PerRequestPrice
}

func positiveNativeImagePrice(price *float64) bool {
	return price != nil && *price > 0 && !math.IsInf(*price, 0)
}
