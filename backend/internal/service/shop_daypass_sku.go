package service

import (
	"encoding/json"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

type ShopConcurrencyOption struct {
	Concurrency int     `json:"concurrency"`
	Price       float64 `json:"price"`
	WeeklyPrice float64 `json:"weekly_price,omitempty"`
}

func legacyShopSKUOptions(weekly bool) []ShopConcurrencyOption {
	options := make([]ShopConcurrencyOption, 0, 2)
	for _, concurrency := range []int{2, 3} {
		day := shopDaypassParams{Duration: shopDaypassDaySeconds, Concurrency: concurrency}
		option := ShopConcurrencyOption{Concurrency: concurrency, Price: float64(day.priceFen()) / 100}
		if weekly {
			week := shopDaypassParams{Duration: shopDaypassWeekDays * shopDaypassDaySeconds, Concurrency: concurrency}
			option.WeeklyPrice = float64(week.priceFen()) / 100
		}
		options = append(options, option)
	}
	return options
}

const (
	shopDaypassDaySeconds    = 24 * 60 * 60
	shopDaypassWeekDays      = 7
	shopDaypassWeekPriceDays = 5
)

// Zero fields are omitted selections. Native checkout supplies the legacy
// defaults; unrelated products accept neither option.
type ShopSKUSelection struct {
	ID           string
	Concurrency  int
	DurationDays int
}

func (s ShopSKUSelection) withNativeDefaults() ShopSKUSelection {
	if s.Concurrency == 0 {
		s.Concurrency = 2
	}
	if s.DurationDays == 0 {
		s.DurationDays = 1
	}
	return s
}

type shopDaypassParams struct {
	Version     int   `json:"sku_version,omitempty"`
	PriceFen    int64 `json:"price_fen,omitempty"`
	Duration    int   `json:"duration_seconds"`
	Concurrency int   `json:"default_concurrency"`
}

func (p shopDaypassParams) priceFen() int64 {
	if p.Version == 2 {
		if p.PriceFen > 0 && p.PriceFen <= 100000000 && (p.Concurrency == 2 || p.Concurrency == 3) && (p.Duration == shopDaypassDaySeconds || p.Duration == 7*shopDaypassDaySeconds) {
			return p.PriceFen
		}
		return 0
	}
	if p.Version != 0 || p.PriceFen != 0 {
		return 0
	}
	var dailyPrice int64
	switch p.Concurrency {
	case 2:
		dailyPrice = 600
	case 3:
		dailyPrice = 800
	default:
		return 0
	}
	switch p.Duration {
	case shopDaypassDaySeconds:
		return dailyPrice
	case shopDaypassWeekDays * shopDaypassDaySeconds:
		return shopDaypassWeekPriceDays * dailyPrice
	default:
		return 0
	}
}

func (p shopDaypassParams) selection() ShopSKUSelection {
	return ShopSKUSelection{Concurrency: p.Concurrency, DurationDays: p.Duration / shopDaypassDaySeconds}
}

// Historical version-zero orders retain their original price-based mapping.
func shopDaypassDurationForPrice(priceFen int64) int64 {
	for _, days := range []int{1, shopDaypassWeekDays} {
		for _, concurrency := range []int{2, 3} {
			sku := shopDaypassParams{Duration: days * shopDaypassDaySeconds, Concurrency: concurrency}
			if sku.priceFen() == priceFen {
				return int64(sku.Duration)
			}
		}
	}
	return 0
}

func shopNativeSKU(params json.RawMessage) shopDaypassParams {
	var p shopDaypassParams
	if json.Unmarshal(params, &p) != nil || p.priceFen() == 0 {
		return shopDaypassParams{}
	}
	return p
}

func shopProductSKU(p ShopProduct) shopDaypassParams {
	if len(p.PaymentActions) == 1 && p.PaymentActions[0].Adapter == shopaction.Native {
		return shopNativeSKU(p.PaymentActions[0].Params)
	}
	return shopDaypassParams{}
}

func shopProductConcurrency(p ShopProduct) int {
	return shopProductSKU(p).Concurrency
}

func (s *ShopService) resolveShopSKU(p ShopProduct, selected ShopSKUSelection) (ShopProduct, error) {
	resolved, err := selectShopSKU(p, selected)
	if err != nil {
		return p, err
	}
	if nativeShopProduct(p) && s.daypassCfg.MaxConcurrency == 2 && resolved.Concurrency == 3 {
		return p, infraerrors.BadRequest("SHOP_CONCURRENCY_INVALID", "3并发档位暂未开放")
	}
	if nativeShopProduct(p) && resolved.DurationValue == shopDaypassWeekDays && !s.daypassCfg.WeeklyEnabled {
		return p, infraerrors.BadRequest("SHOP_DURATION_INVALID", "周卡暂未开放")
	}
	return resolved, err
}

func selectShopSKU(p ShopProduct, selected ShopSKUSelection) (ShopProduct, error) {
	if len(p.SKUs) > 0 {
		return selectConfiguredShopSKU(p, selected)
	}
	if selected.ID != "" {
		return p, infraerrors.BadRequest("SHOP_SKU_UNAVAILABLE", "此商品的规格已更新，请刷新后重试")
	}
	native := len(p.PaymentActions) == 1 && p.PaymentActions[0].Adapter == shopaction.Native
	if !native {
		if selected.DurationDays != 0 {
			return p, infraerrors.BadRequest("SHOP_DURATION_INVALID", "此商品不支持选择有效期")
		}
		if selected.Concurrency != 0 {
			return p, infraerrors.BadRequest("SHOP_CONCURRENCY_INVALID", "此商品不支持选择并发")
		}
		return p, nil
	}
	// Only the approved base catalog may create new purchases.
	if p.DurationValue != 1 || shopProductConcurrency(p) != 2 || !validShopDaypassProduct(p) {
		return p, shopConflict()
	}
	selected = selected.withNativeDefaults()
	if selected.DurationDays != 1 && selected.DurationDays != shopDaypassWeekDays {
		return p, infraerrors.BadRequest("SHOP_DURATION_INVALID", "请选择日卡或周卡")
	}
	sku := shopDaypassParams{Duration: selected.DurationDays * shopDaypassDaySeconds, Concurrency: selected.Concurrency}
	price := sku.priceFen()
	if price == 0 {
		return p, infraerrors.BadRequest("SHOP_CONCURRENCY_INVALID", "请选择可购买的2或3并发档位")
	}
	raw, err := json.Marshal(sku)
	if err != nil {
		return p, err
	}
	// Copy before changing params: callers may still hold the base catalog slice.
	p.PaymentActions = append([]shopaction.Binding(nil), p.PaymentActions...)
	p.PaymentActions[0].Params = raw
	p.Price = float64(price) / 100
	p.DurationValue = selected.DurationDays
	p.Concurrency = selected.Concurrency
	return p, nil
}

func (s *ShopService) publicCatalogProduct(p ShopProduct) ShopProduct {
	native := nativeShopProduct(p)
	p = publicShopProduct(p)
	if len(p.SKUs) > 0 {
		visible := make([]ShopSKU, 0, len(p.SKUs))
		for _, sku := range p.SKUs {
			if !sku.Enabled || native && (s.daypassCfg.MaxConcurrency == 2 && sku.Concurrency == 3 || !s.daypassCfg.WeeklyEnabled && sku.DurationValue == 7) {
				continue
			}
			visible = append(visible, sku)
		}
		p.SKUs = visible
		p.ConcurrencyOptions = nil
		if len(visible) == 0 {
			p.Enabled = false
		} else {
			chosen := visible[0]
			for _, sku := range visible {
				if sku.ID == p.DefaultSKUID {
					chosen = sku
				}
			}
			p.DefaultSKUID = chosen.ID
			p.Price, p.DurationValue, p.DurationUnit = chosen.Price, chosen.DurationValue, chosen.DurationUnit
		}
		return p
	}
	if s.daypassCfg.MaxConcurrency == 2 && len(p.ConcurrencyOptions) == 2 {
		p.ConcurrencyOptions = p.ConcurrencyOptions[:1]
	}
	if s.daypassCfg.WeeklyEnabled && p.ID == s.daypassCfg.ProductID {
		for i := range p.ConcurrencyOptions {
			option := &p.ConcurrencyOptions[i]
			weekly := shopDaypassParams{Duration: shopDaypassWeekDays * shopDaypassDaySeconds, Concurrency: option.Concurrency}
			option.WeeklyPrice = float64(weekly.priceFen()) / 100
		}
	}
	return p
}
