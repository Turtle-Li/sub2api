package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func currencyTestBilling(currency string) *BillingService {
	s := NewBillingService(&config.Config{}, nil)
	s.currencyPolicy.Store(PricingCurrencySettings{SettlementCurrency: currency, USDToCNYRate: 6.75})
	return s
}

func TestBillingCurrencyPreservesDiscountAndPurchasingPower(t *testing.T) {
	usd, cny := currencyTestBilling("USD"), currencyTestBilling("CNY")
	tokens := UsageTokens{InputTokens: 1000000, OutputTokens: 200000, CacheReadTokens: 500000}
	before, err := usd.CalculateCost("claude-sonnet-4", tokens, 0.8)
	require.NoError(t, err)
	after, err := cny.CalculateCost("claude-sonnet-4", tokens, 0.8)
	require.NoError(t, err)
	require.InDelta(t, before.TotalCost*6.75, after.TotalCost, 1e-10)
	require.InDelta(t, after.TotalCost*0.8, after.ActualCost, 1e-10)
	require.InDelta(t, 100/before.ActualCost, 675/after.ActualCost, 1e-10)
}

func TestBillingCurrencyPartialCNYCardKeepsFallbackInSameUnit(t *testing.T) {
	s := currencyTestBilling("CNY")
	input := 6.75e-6
	group := &Group{ModelPricing: []ChannelModelPricing{{Models: []string{"claude-sonnet-4"}, Currency: "CNY", InputPrice: &input}}, LongContextPricingEnabled: true}
	r := NewModelPricingResolver(nil, s)
	cost, err := s.CalculateCostUnified(CostInput{Model: "claude-sonnet-4", Group: group, Resolver: r, RateMultiplier: 0.8, Tokens: UsageTokens{InputTokens: 1000000, OutputTokens: 1000000}})
	require.NoError(t, err)
	require.InDelta(t, 6.75, cost.InputCost, 1e-10)
	require.InDelta(t, 15*6.75, cost.OutputCost, 1e-10)
	require.InDelta(t, (6.75+15*6.75)*0.8, cost.ActualCost, 1e-10)
	require.Equal(t, input, *group.ModelPricing[0].InputPrice, "normalization must not mutate authored card")
}

func TestBillingCurrencyCNYCardUSDWalletAndRequestTier(t *testing.T) {
	s := currencyTestBilling("USD")
	price := 13.5
	group := &Group{ModelPricing: []ChannelModelPricing{{Models: []string{"custom-image"}, Currency: "CNY", BillingMode: BillingModeImage, PerRequestPrice: &price}}}
	cost, err := s.CalculateCostUnified(CostInput{Model: "custom-image", Group: group, Resolver: NewModelPricingResolver(nil, s), RateMultiplier: 0.6, RequestCount: 2})
	require.NoError(t, err)
	require.InDelta(t, 4, cost.TotalCost, 1e-12)
	require.InDelta(t, 2.4, cost.ActualCost, 1e-12)
}

func TestBillingCurrencyMediaConvertedOnce(t *testing.T) {
	usd, cny := currencyTestBilling("USD"), currencyTestBilling("CNY")
	for name, fn := range map[string]func(*BillingService) *CostBreakdown{
		"web-search": func(s *BillingService) *CostBreakdown { return s.CalculateWebSearchCost(2, nil, .8) },
		"search":     func(s *BillingService) *CostBreakdown { return s.CalculateSearchCost(2, nil, .8) },
		"audio":      func(s *BillingService) *CostBreakdown { return s.CalculateAudioCost("tts", 2, nil, .8) },
		"image":      func(s *BillingService) *CostBreakdown { return s.CalculateImageCost("gemini-image", "1K", 2, nil, .8) },
		"video": func(s *BillingService) *CostBreakdown {
			return s.CalculateVideoCost("grok-imagine-video", "720p", 2, 6, nil, .8)
		},
	} {
		t.Run(name, func(t *testing.T) {
			before, after := fn(usd), fn(cny)
			require.Positive(t, before.ActualCost)
			require.InDelta(t, before.ActualCost*6.75, after.ActualCost, 1e-10)
			require.InDelta(t, after.TotalCost*.8, after.ActualCost, 1e-10)
		})
	}
}

func TestBillingCurrencySubscriptionAndAccountQuotaKeepUSD(t *testing.T) {
	s := currencyTestBilling("CNY")
	cost, err := s.CalculateCost("claude-sonnet-4", UsageTokens{InputTokens: 1000000}, .8)
	require.NoError(t, err)
	require.InDelta(t, 3, accountQuotaBasis(cost), 1e-12)
	// Changing the live setting must not change an already calculated amount.
	s.currencyPolicy.Store(PricingCurrencySettings{SettlementCurrency: "CNY", USDToCNYRate: 7})
	subscriptionCost(cost)
	require.InDelta(t, 2.4, cost.ActualCost, 1e-12)
	require.Equal(t, "USD", costCurrency(cost))
	subscriptionCost(cost)
	require.InDelta(t, 2.4, cost.ActualCost, 1e-12)
}

func TestBillingCurrencyFingerprintSurvivesWalletCutover(t *testing.T) {
	tokens := UsageTokens{InputTokens: 1000000}
	before, err := currencyTestBilling("USD").CalculateCost("claude-sonnet-4", tokens, .8)
	require.NoError(t, err)
	after, err := currencyTestBilling("CNY").CalculateCost("claude-sonnet-4", tokens, .8)
	require.NoError(t, err)
	p := &postUsageBillingParams{Cost: before, User: &User{ID: 1}, APIKey: &APIKey{ID: 2}, Account: &Account{ID: 3, Type: AccountTypeOAuth}}
	log := &UsageLog{Model: "claude-sonnet-4", InputTokens: 1000000}
	oldCommand := buildUsageBillingCommand("same-request", log, p)
	p.Cost = after
	newCommand := buildUsageBillingCommand("same-request", log, p)
	require.Equal(t, oldCommand.RequestFingerprint, newCommand.RequestFingerprint)
	require.InDelta(t, oldCommand.BalanceCost*6.75, newCommand.BalanceCost, 1e-12)
	after.ActualCost *= 2
	require.NotEqual(t, oldCommand.RequestFingerprint, buildUsageBillingCommand("same-request", log, p).RequestFingerprint, "real price conflicts must still be rejected")
}

func TestBillingCurrencyNormalizesAllCardMoneyWithoutChangingRatios(t *testing.T) {
	s := currencyTestBilling("CNY")
	price, ratio, max := 13.5, .8, 200000
	card := &ChannelModelPricing{Currency: "CNY", InputPrice: &price, OutputPrice: &price, CacheWritePrice: &price, CacheWrite1hPrice: &price, CacheReadPrice: &price, ImageInputPrice: &price, ImageOutputPrice: &price, PerRequestPrice: &price,
		Intervals: []PricingInterval{{MinTokens: 100000, MaxTokens: &max, InputPrice: &price, OutputPrice: &price, CacheWritePrice: &price, CacheWrite1hPrice: &price, CacheReadPrice: &price, PerRequestPrice: &price, InputMultiplier: &ratio}}}
	got := s.pricingCardInUSD(card)
	for _, v := range []*float64{got.InputPrice, got.OutputPrice, got.CacheWritePrice, got.CacheWrite1hPrice, got.CacheReadPrice, got.ImageInputPrice, got.ImageOutputPrice, got.PerRequestPrice, got.Intervals[0].InputPrice, got.Intervals[0].OutputPrice, got.Intervals[0].CacheWritePrice, got.Intervals[0].CacheWrite1hPrice, got.Intervals[0].CacheReadPrice, got.Intervals[0].PerRequestPrice} {
		require.InDelta(t, 2, *v, 1e-12)
	}
	require.Equal(t, ratio, *got.Intervals[0].InputMultiplier)
	require.Equal(t, max, *got.Intervals[0].MaxTokens)
	require.Equal(t, price, *card.Intervals[0].CacheWrite1hPrice)
}

func TestBillingCurrencyPriorityPricingConvertedOnce(t *testing.T) {
	for _, tier := range []string{"", "priority", "flex"} {
		t.Run(tier, func(t *testing.T) {
			usage := UsageTokens{InputTokens: 300000, OutputTokens: 100000, CacheReadTokens: 50000}
			before, err := currencyTestBilling("USD").CalculateCostWithServiceTier("gpt-5", usage, .6, tier)
			require.NoError(t, err)
			after, err := currencyTestBilling("CNY").CalculateCostWithServiceTier("gpt-5", usage, .6, tier)
			require.NoError(t, err)
			require.InDelta(t, before.ActualCost*6.75, after.ActualCost, 1e-10)
			subscriptionCost(after)
			require.InDelta(t, before.ActualCost, after.ActualCost, 1e-10)
		})
	}
}

func TestBillingCurrencyDisplayScheduleStaysUSD(t *testing.T) {
	input, image := 6.75e-6, 13.5e-6
	group := &Group{ID: 1, ModelPricing: []ChannelModelPricing{{Models: []string{"claude-sonnet-4"}, Currency: "CNY", InputPrice: &input, ImageInputPrice: &image}}}
	bs := currencyTestBilling("CNY")
	resolver := NewModelPricingResolver(nil, bs)
	schedule, err := bs.ResolveContextPricingSchedule(context.Background(), resolver, ContextPricingScheduleInput{Model: "claude-sonnet-4", Group: group})
	require.NoError(t, err)
	require.NotEmpty(t, schedule.Tiers)
	require.InDelta(t, 1e-6, *schedule.Tiers[0].Input, 1e-12)
	model := &PlazaModel{Name: "claude-sonnet-4", Pricing: &group.ModelPricing[0]}
	plaza := &ModelPlazaService{billingService: bs, resolver: resolver}
	plaza.fillDisplayPricing(context.Background(), model, group)
	require.InDelta(t, 1e-6, *model.Pricing.InputPrice, 1e-12)
	require.InDelta(t, 2e-6, *model.Pricing.ImageInputPrice, 1e-12)
	require.Equal(t, image, *group.ModelPricing[0].ImageInputPrice)
}
