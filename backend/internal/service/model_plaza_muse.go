package service

import (
	"context"
	"math"
	"slices"
)

func scopeMusePlazaGroup(group *Group, pg *PlazaGroup) {
	dedicated := group.IsMuseMediaOnly()
	if !dedicated && !isMusePlazaGroup(pg) {
		return
	}
	pg.CatalogProvider = "muse"
	pg.Models = slices.DeleteFunc(pg.Models, func(model PlazaModel) bool {
		return !IsMuseMediaModel(model.Name) || (dedicated && !group.ModelAllowlist.Allows(model.Name))
	})
}

func isMusePlazaGroup(g *PlazaGroup) bool {
	if g.Platform != PlatformOpenAI || len(g.Models) == 0 {
		return false
	}
	for _, m := range g.Models {
		switch m.Name {
		case legacyMuseTextModel, MuseImageModel, MuseVideoModel:
		default:
			return false
		}
	}
	return true
}

func (s *ModelPlazaService) musePlazaUnitPricing(ctx context.Context, g *Group, pg *PlazaGroup, model string) *PlazaUnitPricing {
	switch model {
	case MuseImageModel:
		pricing := &PlazaUnitPricing{Unit: "image", Quotes: make([]PlazaUnitQuote, 0, 3)}
		for _, size := range []string{ImageBillingSize1K, ImageBillingSize2K, ImageBillingSize4K} {
			if _, price := museImageWalletPriceForGroup(g, size); price > 0 {
				pricing.Quotes = append(pricing.Quotes, PlazaUnitQuote{Label: size, Price: price})
			}
		}
		return pricing
	case MuseVideoModel:
		pricing := &PlazaUnitPricing{Unit: "second", Quotes: make([]PlazaUnitQuote, 0, 1)}
		if price, ok := s.museVideoPlazaUnitPrice(ctx, g); ok {
			if pg.DisplayCurrency == "CNY" {
				price *= pg.DisplayUSDRate
			}
			pricing.Quotes = append(pricing.Quotes, PlazaUnitQuote{Label: VideoBillingResolution720P, Price: price})
		}
		return pricing
	default:
		return nil
	}
}

func (s *ModelPlazaService) museVideoPlazaUnitPrice(ctx context.Context, g *Group) (float64, bool) {
	// Native Muse admission requires a configured 720p price, not a catalog or channel fallback.
	configured := g.GetVideoPriceForModel(MuseVideoModel, VideoBillingResolution720P)
	if configured == nil || *configured <= 0 || math.IsNaN(*configured) || math.IsInf(*configured, 0) {
		return 0, false
	}
	if s.resolver != nil && s.billingService != nil {
		resolved := s.resolver.Resolve(ctx, PricingInput{Model: MuseVideoModel, GroupID: &g.ID, Group: g})
		if resolved.Source == PricingSourceGroup || resolved.Source == PricingSourceChannel {
			// A token override bypasses per-second billing; don't advertise a seconds quote for it.
			if resolved.Mode == BillingModeToken {
				return 0, false
			}
			// As in calculateOpenAIVideoCost, a model-specific group video card wins.
			if resolved.Source == PricingSourceGroup && resolved.Mode == BillingModeVideo {
				cost, err := s.billingService.calculatePerRequestCost(resolved, CostInput{
					Resolver: s.resolver, UsageUnits: 1, SizeTier: VideoBillingResolution720P, RateMultiplier: 1,
				})
				if err == nil {
					return cost.TotalCost, true
				}
			}
		}
	}
	return *configured, true
}
