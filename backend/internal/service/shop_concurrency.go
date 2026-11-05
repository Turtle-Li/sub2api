package service

import (
	"encoding/json"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

type ShopConcurrencyOption struct {
	Concurrency int     `json:"concurrency"`
	Price       float64 `json:"price"`
}

type shopDaypassParams struct {
	Duration    int `json:"duration_seconds"`
	Concurrency int `json:"default_concurrency"`
}

func shopConcurrencyFen(concurrency int) int64 {
	switch concurrency {
	case 2:
		return 600
	case 3:
		return 800
	default:
		return 0
	}
}

func shopNativeConcurrency(params json.RawMessage) int {
	var p shopDaypassParams
	if json.Unmarshal(params, &p) != nil || p.Duration != 86400 || shopConcurrencyFen(p.Concurrency) == 0 {
		return 0
	}
	return p.Concurrency
}

func shopProductConcurrency(p ShopProduct) int {
	if len(p.PaymentActions) != 1 || p.PaymentActions[0].Adapter != shopaction.Native {
		return 0
	}
	return shopNativeConcurrency(p.PaymentActions[0].Params)
}

func (s *ShopService) resolveShopConcurrency(p ShopProduct, selected int) (ShopProduct, error) {
	if s.daypassCfg.MaxConcurrency == 2 && selected > 2 {
		return p, infraerrors.BadRequest("SHOP_CONCURRENCY_INVALID", "3并发档位暂未开放")
	}
	return selectShopConcurrency(p, selected)
}

func selectShopConcurrency(p ShopProduct, selected int) (ShopProduct, error) {
	native := len(p.PaymentActions) == 1 && p.PaymentActions[0].Adapter == shopaction.Native
	if !native {
		if selected != 0 {
			return p, infraerrors.BadRequest("SHOP_CONCURRENCY_INVALID", "此商品不支持选择并发")
		}
		return p, nil
	}
	// Only the approved base catalog may create new purchases.
	if p.Price != 6 || shopProductConcurrency(p) != 2 || !validShopDaypassProduct(p) {
		return p, shopConflict()
	}
	if selected == 0 {
		selected = 2
	}
	if shopConcurrencyFen(selected) == 0 {
		return p, infraerrors.BadRequest("SHOP_CONCURRENCY_INVALID", "请选择可购买的2或3并发档位")
	}
	// Copy before changing params: callers may still hold the base catalog slice.
	p.PaymentActions = append([]shopaction.Binding(nil), p.PaymentActions...)
	p.PaymentActions[0].Params, _ = json.Marshal(shopDaypassParams{Duration: 86400, Concurrency: selected})
	p.Price = float64(shopConcurrencyFen(selected)) / 100
	p.Concurrency = selected
	return p, nil
}

func (s *ShopService) publicCatalogProduct(p ShopProduct) ShopProduct {
	p = publicShopProduct(p)
	if s.daypassCfg.MaxConcurrency == 2 && len(p.ConcurrencyOptions) == 2 {
		p.ConcurrencyOptions = p.ConcurrencyOptions[:1]
	}
	return p
}
