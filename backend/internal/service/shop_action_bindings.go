package service

import (
	"context"
	"encoding/json"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

func shopAutomaticFulfillment(p ShopProduct) bool {
	return p.AutomaticFulfillment || len(p.PaymentActions) != 0
}

func publicShopProduct(p ShopProduct) ShopProduct {
	p.AutomaticFulfillment = shopAutomaticFulfillment(p)
	p.Concurrency = shopProductConcurrency(p)
	p.ConcurrencyOptions = nil
	if validShopDaypassProduct(p) {
		for _, concurrency := range []int{2, 3} {
			sku := shopDaypassParams{Duration: shopDaypassDaySeconds, Concurrency: concurrency}
			p.ConcurrencyOptions = append(p.ConcurrencyOptions, ShopConcurrencyOption{Concurrency: concurrency, Price: float64(sku.priceFen()) / 100})
		}
	}
	p.PaymentActions = nil
	return p
}

func (s *ShopService) normalizePaymentActions(p *ShopProduct) error {
	if len(p.PaymentActions) == 0 {
		p.AutomaticFulfillment = false
		return nil
	}
	if len(p.PaymentActions) != 1 || p.FulfillmentMode != ShopAccountDelivery {
		return infraerrors.BadRequest("SHOP_ACTION_BINDING_INVALID", "付款后动作仅支持无需买家补充资料的交付商品，每件商品限一个动作")
	}
	b := &p.PaymentActions[0]
	if b.BindingKey == "" {
		b.BindingKey = "primary-delivery"
	}
	if b.BindingKey != "primary-delivery" {
		return infraerrors.BadRequest("SHOP_ACTION_BINDING_INVALID", "交付动作标识无效")
	}
	i, ok := s.findActionIntegration(*b)
	if !ok || shopaction.ValidateIntegration(i) != nil {
		return infraerrors.BadRequest("SHOP_INTEGRATION_INVALID", "请选择已登记的集成及版本")
	}
	b.Integration = &i
	if p.Enabled && (!s.actionsCfg.Enabled || !i.Enabled) {
		return infraerrors.BadRequest("SHOP_INTEGRATION_DISABLED", "自动集成尚未启用，请先保存为草稿")
	}
	if len(b.Params) == 0 {
		b.Params = json.RawMessage(`{}`)
	}
	var params map[string]json.RawMessage
	if len(b.Params) > 4096 || json.Unmarshal(b.Params, &params) != nil || params == nil || len(params) > 32 {
		return infraerrors.BadRequest("SHOP_ACTION_PARAMS_INVALID", "动作参数必须是4KB以内的JSON对象")
	}
	if b.Adapter == shopaction.Native {
		if p.Price != 6 || !validShopDaypassCatalog(*p) {
			return infraerrors.BadRequest("SHOP_DAYPASS_INVALID", "DeepSeek 自动交付商品的基础规格为6元、24小时")
		}
		var native shopDaypassParams
		if json.Unmarshal(b.Params, &native) != nil || (native.Duration != 0 && native.Duration != shopDaypassDaySeconds) || (native.Concurrency != 0 && native.Concurrency != 2) {
			return infraerrors.BadRequest("SHOP_DAYPASS_INVALID", "基础规格固定24小时、默认2并发")
		}
		b.Params = json.RawMessage(`{"duration_seconds":86400,"default_concurrency":2}`)
	}
	p.AutomaticFulfillment = true
	return nil
}

func (s *ShopService) findActionIntegration(b shopaction.Binding) (shopaction.Integration, bool) {
	for _, i := range s.actionsCfg.Integrations {
		if i.ID == b.IntegrationRef && i.Version == b.IntegrationVersion && i.Adapter == b.Adapter {
			return i, true
		}
	}
	return shopaction.Integration{}, false
}

func (s *ShopService) actionAdapter(b shopaction.Binding) (shopaction.Adapter, error) {
	return s.actionAdapterFor(b, false)
}

func (s *ShopService) actionAdapterFor(b shopaction.Binding, cleanup bool) (shopaction.Adapter, error) {
	i, ok := s.findActionIntegration(b)
	if !ok || !cleanup && (!s.actionsCfg.Enabled || !i.Enabled) {
		return nil, &shopaction.Failure{Kind: "INTEGRATION_PAUSED", Retryable: true}
	}
	if b.Integration == nil {
		return nil, &shopaction.Failure{Kind: "SNAPSHOT_INVALID"}
	}
	frozen := *b.Integration
	frozen.Enabled = i.Enabled
	if frozen != i || shopaction.ValidateIntegration(i) != nil {
		return nil, &shopaction.Failure{Kind: "INTEGRATION_VERSION_CHANGED"}
	}
	switch b.Adapter {
	case shopaction.Native:
		if s.daypass == nil || !cleanup && !s.daypassCfg.Enabled {
			return nil, &shopaction.Failure{Kind: "INTEGRATION_PAUSED", Retryable: true}
		}
		gateway := s.daypass
		if client, ok := s.daypass.(*shopDaypassClient); ok && client.endpoint != i.Endpoint {
			gateway = newShopDaypassGateway(s.daypassCfg.ProductID, i.Endpoint)
		}
		return &shopDaypassAdapter{gateway: gateway, productID: s.daypassCfg.ProductID, durationDays: shopNativeSKU(b.Params).selection().DurationDays}, nil
	case shopaction.HTTP:
		return s.webhook(i), nil
	default:
		return nil, &shopaction.Failure{Kind: "ADAPTER_UNSUPPORTED"}
	}
}

func (s *ShopService) requireActionsCheckout(ctx context.Context, p ShopProduct) error {
	if !shopAutomaticFulfillment(p) {
		return nil
	}
	if len(p.PaymentActions) != 1 || p.FulfillmentMode != ShopAccountDelivery || !validateActionSnapshot(p.PaymentActions[0]) {
		return infraerrors.ServiceUnavailable("SHOP_AUTOMATION_UNAVAILABLE", "自动交付配置待复核，请稍后重试")
	}
	a, err := s.actionAdapter(p.PaymentActions[0])
	if err == nil && p.PaymentActions[0].Adapter == shopaction.Native && p.ID != s.daypassCfg.ProductID {
		err = &shopaction.Failure{Kind: "DAYPASS_SKU_INVALID"}
	}
	if err == nil {
		err = a.Ready(ctx)
	}
	if err != nil {
		return infraerrors.ServiceUnavailable("SHOP_AUTOMATION_UNAVAILABLE", "自动交付暂不可用，请稍后重试")
	}
	return nil
}

func (s *ShopService) checkoutProduct(ctx context.Context, id int64) (*ShopProduct, error) {
	p, err := s.Product(ctx, id, true)
	if err != nil {
		return nil, err
	}
	if !p.Enabled {
		return nil, infraerrors.NotFound("SHOP_PRODUCT_NOT_FOUND", "商品不存在或已下架")
	}
	return p, nil
}

func validateActionSnapshot(b shopaction.Binding) bool {
	var params map[string]json.RawMessage
	if b.Adapter == shopaction.Native && shopNativeSKU(b.Params).Duration == 0 {
		return false
	}
	return b.BindingKey == "primary-delivery" && b.Integration != nil && b.Integration.ID == b.IntegrationRef &&
		b.Integration.Version == b.IntegrationVersion && b.Integration.Adapter == b.Adapter &&
		shopaction.ValidateIntegration(*b.Integration) == nil && len(b.Params) <= 4096 &&
		json.Unmarshal(b.Params, &params) == nil && params != nil && !strings.ContainsRune(string(b.Params), 0)
}
