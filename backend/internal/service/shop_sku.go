package service

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

// ShopSKU is catalog data. The selected record is frozen in the order snapshot.
type ShopSKU struct {
	ID            string            `json:"id"`
	Attributes    map[string]string `json:"attributes"`
	Price         float64           `json:"price"`
	DurationValue int               `json:"duration_value"`
	DurationUnit  string            `json:"duration_unit"`
	Concurrency   int               `json:"concurrency,omitempty"`
	Enabled       bool              `json:"enabled"`
}

type shopSKUConfig struct {
	SKUs         []ShopSKU `json:"skus"`
	DefaultSKUID string    `json:"default_sku_id"`
}

var shopSKUIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$`)

func shopPriceValid(price float64, enabled bool) bool {
	return !math.IsNaN(price) && !math.IsInf(price, 0) && price >= 0 && price <= 1000000 &&
		math.Abs(price*100-math.Round(price*100)) < 0.000001 && (!enabled || price > 0)
}

func nativeShopProduct(p ShopProduct) bool {
	return len(p.PaymentActions) == 1 && p.PaymentActions[0].Adapter == shopaction.Native
}

func normalizeShopSKUs(p *ShopProduct) error {
	if len(p.SKUs) == 0 {
		if p.DefaultSKUID != "" {
			return infraerrors.BadRequest("SHOP_SKU_INVALID", "默认 SKU 不存在，请重新选择")
		}
		return nil // Older API clients and historical snapshots retain their contract.
	}
	if len(p.SKUs) > 100 {
		return infraerrors.BadRequest("SHOP_SKU_INVALID", "每件商品最多配置 100 个 SKU")
	}
	ids, combinations := map[string]bool{}, map[string]bool{}
	var attributeKeys map[string]bool
	var defaultSKU *ShopSKU
	for i := range p.SKUs {
		sku := &p.SKUs[i]
		if sku.Attributes == nil {
			sku.Attributes = map[string]string{}
		}
		if !shopSKUIDPattern.MatchString(sku.ID) || ids[sku.ID] || len(sku.Attributes) > 3 {
			return infraerrors.BadRequest("SHOP_SKU_INVALID", "SKU 编码须唯一，最多包含三个规格属性")
		}
		ids[sku.ID] = true
		if !shopPriceValid(sku.Price, p.Enabled && sku.Enabled) || sku.DurationValue < 1 || sku.DurationValue > 3650 ||
			(sku.DurationUnit != "day" && sku.DurationUnit != "month" && sku.DurationUnit != "year") {
			return infraerrors.BadRequest("SHOP_SKU_INVALID", fmt.Sprintf("SKU %s 的价格或有效期无效；上架价格须大于 0 且最多两位小数", sku.ID))
		}
		keys := make(map[string]bool, len(sku.Attributes))
		for name, value := range sku.Attributes {
			if strings.TrimSpace(name) != name || name == "" || strings.TrimSpace(value) != value || value == "" ||
				!utf8.ValidString(name+value) || utf8.RuneCountInString(name) > 40 || utf8.RuneCountInString(value) > 80 || strings.ContainsRune(name+value, 0) {
				return infraerrors.BadRequest("SHOP_SKU_INVALID", "规格名称和值不能为空，且不能包含无效字符")
			}
			keys[name] = true
		}
		if attributeKeys == nil {
			attributeKeys = keys
		}
		if len(keys) != len(attributeKeys) {
			return infraerrors.BadRequest("SHOP_SKU_INVALID", "每个 SKU 必须使用相同的规格属性")
		}
		for name := range keys {
			if !attributeKeys[name] {
				return infraerrors.BadRequest("SHOP_SKU_INVALID", "每个 SKU 必须使用相同的规格属性")
			}
		}
		encoded, err := json.Marshal(sku.Attributes)
		if err != nil {
			return err
		}
		if combinations[string(encoded)] {
			return infraerrors.BadRequest("SHOP_SKU_INVALID", "规格组合重复，请合并重复的 SKU")
		}
		combinations[string(encoded)] = true
		if nativeShopProduct(*p) {
			if sku.DurationUnit != "day" || (sku.DurationValue != 1 && sku.DurationValue != 7) || (sku.Concurrency != 2 && sku.Concurrency != 3) {
				return infraerrors.BadRequest("SHOP_SKU_INVALID", "DeepSeek 服务当前支持 1/7 天和 2/3 并发，请使用对应规格")
			}
		} else if sku.Concurrency != 0 {
			return infraerrors.BadRequest("SHOP_SKU_INVALID", "并发配置仅用于 DeepSeek 自动交付")
		}
		if sku.ID == p.DefaultSKUID {
			defaultSKU = sku
		}
	}
	if defaultSKU == nil || p.Enabled && !defaultSKU.Enabled {
		return infraerrors.BadRequest("SHOP_SKU_INVALID", "请选择一个可销售的默认 SKU")
	}
	p.Price, p.DurationValue, p.DurationUnit = defaultSKU.Price, defaultSKU.DurationValue, defaultSKU.DurationUnit
	return nil
}

func selectConfiguredShopSKU(p ShopProduct, selected ShopSKUSelection) (ShopProduct, error) {
	id := selected.ID
	if id == "" {
		return p, infraerrors.BadRequest("SHOP_SKU_INVALID", "请选择商品规格后重试")
	}
	for _, sku := range p.SKUs {
		if sku.ID != id {
			continue
		}
		if !sku.Enabled || !shopPriceValid(sku.Price, true) {
			return p, infraerrors.BadRequest("SHOP_SKU_UNAVAILABLE", "所选规格已下架，请选择其他规格")
		}
		if selected.Concurrency != 0 && selected.Concurrency != sku.Concurrency || selected.DurationDays != 0 && (sku.DurationUnit != "day" || selected.DurationDays != sku.DurationValue) {
			return p, infraerrors.BadRequest("SHOP_SKU_INVALID", "所选 SKU 与规格参数不一致")
		}
		p.SKUID, p.Concurrency = sku.ID, sku.Concurrency
		p.Price, p.DurationValue, p.DurationUnit = sku.Price, sku.DurationValue, sku.DurationUnit
		if nativeShopProduct(p) {
			params := shopDaypassParams{Version: 2, PriceFen: int64(math.Round(sku.Price * 100)), Duration: sku.DurationValue * shopDaypassDaySeconds, Concurrency: sku.Concurrency}
			raw, err := json.Marshal(params)
			if err != nil {
				return p, err
			}
			p.PaymentActions = append([]shopaction.Binding(nil), p.PaymentActions...)
			p.PaymentActions[0].Params = raw
		}
		// Orders contain the purchased SKU only, never future catalog alternatives.
		p.SKUs = []ShopSKU{sku}
		p.DefaultSKUID = sku.ID
		return p, nil
	}
	return p, infraerrors.BadRequest("SHOP_SKU_UNAVAILABLE", "所选规格不存在，请刷新商品后重试")
}

func purchasedShopSelection(p ShopProduct) ShopSKUSelection {
	if p.SKUID != "" {
		return ShopSKUSelection{ID: p.SKUID}
	}
	return shopProductSKU(p).selection()
}
