package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// ProvideCurrencyAwareBillingService validates the policy before serving traffic.
// Legacy constructors used by tests retain USD behavior. Settings outages retain
// the last successfully read policy; they must never turn CNY debits into USD.
func ProvideCurrencyAwareBillingService(cfg *config.Config, pricing *PricingService, settings *SettingService) (*BillingService, error) {
	s := NewBillingService(cfg, pricing)
	policy, err := settings.GetPricingCurrencySettings(context.Background())
	if err != nil {
		return nil, fmt.Errorf("load pricing currency: %w", err)
	}
	s.currencyPolicy.Store(policy)
	s.currencyPolicyProvider = settings.GetPricingCurrencySettings
	return s, nil
}

func (s *BillingService) currentCurrencyPolicy() PricingCurrencySettings {
	if s.currencyPolicyProvider != nil {
		if policy, err := s.currencyPolicyProvider(context.Background()); err == nil {
			s.currencyPolicy.Store(policy)
		}
	}
	if policy, ok := s.currencyPolicy.Load().(PricingCurrencySettings); ok {
		return policy
	}
	return PricingCurrencySettings{SettlementCurrency: "USD", USDToCNYRate: 6.75}
}

func (s *BillingService) SettlementCurrency() string {
	return s.currentCurrencyPolicy().SettlementCurrency
}

func (s *BillingService) settleCost(cost *CostBreakdown) {
	if cost == nil || cost.settlementCurrency != "" {
		return
	}
	policy := s.currentCurrencyPolicy()
	if policy.SettlementCurrency == "CNY" {
		applyCostBreakdownMultiplier(cost, policy.USDToCNYRate)
		cost.settlementCurrency = "CNY"
		cost.settlementRate = policy.USDToCNYRate
	}
}

// Subscription entitlements keep their existing USD quota unit. Use the rate
// captured with this breakdown, not a later settings read, to preserve all
// existing subscription/key windows while wallets change denomination.
func subscriptionCost(cost *CostBreakdown) {
	if cost != nil && cost.settlementCurrency == "CNY" && cost.settlementRate > 0 {
		applyCostBreakdownMultiplier(cost, 1/cost.settlementRate)
		cost.settlementCurrency = "USD"
		cost.settlementRate = 1
	}
}

// Standard OpenAI groups quote USD reference prices and use their existing
// multiplier as CNY per reference dollar. Finalize the same breakdown before
// logging and debiting, so wallet/key/platform usage all omit the extra FX.
// Subscription groups (including wallet fallback) retain their existing units.
func finalizeUsageCurrency(cost *CostBreakdown, key *APIKey, subscription bool) {
	if subscription {
		subscriptionCost(cost)
		return
	}
	if cost == nil || key == nil || key.Group == nil ||
		key.Group.Platform != PlatformOpenAI || key.Group.SubscriptionType != SubscriptionTypeStandard ||
		cost.settlementCurrency != "CNY" || cost.settlementRate <= 0 {
		return
	}
	applyCostBreakdownMultiplier(cost, 1/cost.settlementRate)
	cost.settlementRate = 1
}

func costCurrency(cost *CostBreakdown) string {
	if cost != nil && cost.settlementCurrency != "" {
		return cost.settlementCurrency
	}
	return "USD"
}

// Account quota is an upstream admission limit, not a customer wallet. Keep
// its existing unit so changing wallet denomination cannot exhaust accounts.
func accountQuotaBasis(cost *CostBreakdown) float64 {
	if cost == nil {
		return 0
	}
	if cost.settlementCurrency == "CNY" && cost.settlementRate > 0 {
		return cost.TotalCost / cost.settlementRate
	}
	return cost.TotalCost
}

// pricingCardInUSD normalizes only money, before merging partial overrides with
// the USD catalog. Clone keeps cached/authored prices and all ratios untouched.
func (s *BillingService) pricingCardInUSD(card *ChannelModelPricing) *ChannelModelPricing {
	if card == nil || !strings.EqualFold(strings.TrimSpace(card.Currency), "CNY") {
		return card
	}
	normalized := card.Clone()
	rate := s.currentCurrencyPolicy().USDToCNYRate
	scale := func(value **float64) {
		if *value != nil {
			converted := **value / rate
			*value = &converted
		}
	}
	for _, field := range []**float64{&normalized.InputPrice, &normalized.OutputPrice, &normalized.CacheWritePrice, &normalized.CacheWrite1hPrice, &normalized.CacheReadPrice, &normalized.ImageInputPrice, &normalized.ImageOutputPrice, &normalized.PerRequestPrice} {
		scale(field)
	}
	for i := range normalized.Intervals {
		p := &normalized.Intervals[i]
		for _, field := range []**float64{&p.InputPrice, &p.OutputPrice, &p.CacheWritePrice, &p.CacheWrite1hPrice, &p.CacheReadPrice, &p.PerRequestPrice} {
			scale(field)
		}
	}
	normalized.Currency = "USD"
	return &normalized
}

// Key quota denomination follows the bound group, even if a subscription-group
// request falls back to wallet billing because no entitlement is active.
func keyQuotaCost(cost *CostBreakdown, key *APIKey) float64 {
	if cost == nil {
		return 0
	}
	if key != nil && key.Group != nil && key.Group.IsSubscriptionType() && cost.settlementCurrency == "CNY" && cost.settlementRate > 0 {
		return cost.ActualCost / cost.settlementRate
	}
	return cost.ActualCost
}
