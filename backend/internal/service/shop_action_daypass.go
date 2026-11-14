package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

type shopDaypassAdapter struct {
	gateway      shopDaypassGateway
	productID    int64
	durationDays int
	buyer        *shopaction.DaypassBuyer
}

func validShopDaypassProduct(p ShopProduct) bool {
	sku := shopProductSKU(p)
	return p.FulfillmentMode == ShopAccountDelivery && p.Currency == "CNY" && p.DurationUnit == "day" &&
		sku.Duration != 0 && p.DurationValue == sku.selection().DurationDays && p.Price == float64(sku.priceFen())/100
}

func validShopDaypassCatalog(p ShopProduct) bool {
	return p.FulfillmentMode == ShopAccountDelivery && p.Currency == "CNY" && p.DurationValue == 1 && p.DurationUnit == "day"
}

func (a *shopDaypassAdapter) Ready(ctx context.Context) error {
	return a.gateway.ReadyRenewal(ctx, a.durationDays)
}

func (a *shopDaypassAdapter) binding(r shopaction.Request) shopDaypassBinding {
	binding := shopDaypassBinding{Instance: r.Source, OrderID: r.Order.ID, ProductID: r.Order.ProductID, PriceFen: r.Order.AmountFen, Currency: r.Order.Currency, PaidAt: r.Order.PaidAt.Unix()}
	sku := shopNativeSKU(r.Params)
	if sku.Version == 2 {
		binding.SKUVersion, binding.DurationSeconds, binding.Concurrency = 2, sku.Duration, sku.Concurrency
	}
	if a.buyer != nil {
		binding.SKUVersion, binding.DurationSeconds, binding.Concurrency = shopDaypassRenewalVersion, sku.Duration, sku.Concurrency
		binding.Sub2UserID, binding.Sub2Username, binding.PriorOrderID = a.buyer.UserID, a.buyer.Username, a.buyer.PriorOrderID
		if a.buyer.Card != nil {
			binding.SKUVersion = shopDaypassCardsVersion
			binding.CardAction, binding.CardID = a.buyer.Card.Action, a.buyer.Card.CardID
		}
	}
	return binding
}

func (a *shopDaypassAdapter) Execute(ctx context.Context, r shopaction.Request) (shopaction.Result, error) {
	var result shopaction.Result
	sku := shopNativeSKU(r.Params)
	if sku.Duration == 0 || r.Order.ProductID != a.productID || r.Order.AmountFen != sku.priceFen() || r.Order.Currency != "CNY" {
		return result, &shopaction.Failure{Kind: "DAYPASS_SKU_INVALID"}
	}
	binding := a.binding(r)
	grant, err := a.gateway.Provision(ctx, binding)
	if errors.Is(err, errShopDaypassCard) {
		return result, &shopaction.Failure{Kind: "DAYPASS_CARD_UNAVAILABLE"}
	}
	if errors.Is(err, errShopDaypassExpired) || errors.Is(err, errShopDaypassRevoked) {
		return result, &shopaction.Failure{Kind: "DAYPASS_EXPIRED_OR_REVOKED"}
	}
	if err != nil {
		return result, err
	}
	if !validShopDaypassGrant(binding, grant, int64(sku.Duration)) || grant.TokenID <= 0 || grant.Key == "" {
		return result, &shopaction.Failure{Kind: "RESULT_INVALID"}
	}
	content := fmt.Sprintf("DeepSeek API 不限量订阅卡\nBase URL：https://ds.turtleligpt.com/v1\nAPI Key：%s\n有效期：%d 天（自付款起算）\n%s\n模型：DeepSeek 系列（以 /v1/models 为准）\n%d并发；同一Key同时只能使用同一IP，停止调用15分钟后可切换网络。", grant.Key, sku.selection().DurationDays, shopDeliveryExpiry(time.Unix(grant.ExpiresAt, 0)), sku.Concurrency)
	if a.buyer != nil {
		content = fmt.Sprintf("DeepSeek API 不限量订阅卡\nBase URL：https://ds.turtleligpt.com/v1\nAPI Key：%s\n本单时长：%d 天\n本单生效：%s（北京时间 UTC+8）\n%s\n本单档位：%d 并发。后续续费继续使用这把 Key。\n模型：DeepSeek 系列（以 /v1/models 为准）", grant.Key, sku.selection().DurationDays,
			time.Unix(grant.StartsAt, 0).In(shopDeliveryBeijing).Format(shopDeliveryTimeLayout), shopDeliveryExpiry(time.Unix(grant.ExpiresAt, 0)), sku.Concurrency)
	}
	return shopaction.Result{Version: 1, ActionID: r.ActionID, OrderID: r.Order.ID, Status: "completed", ReceiptID: strconv.Itoa(grant.TokenID), Delivery: shopaction.Delivery{Kind: "text", Content: content}}, nil
}

func (a *shopDaypassAdapter) Revoke(ctx context.Context, r shopaction.Request) error {
	if r.Order.ProductID != a.productID {
		return &shopaction.Failure{Kind: "DAYPASS_SKU_INVALID"}
	}
	return a.gateway.Revoke(ctx, a.binding(r))
}
