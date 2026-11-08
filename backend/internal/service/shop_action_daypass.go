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
	skuVersion   int
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
	if a.skuVersion == 2 {
		gateway, ok := a.gateway.(interface {
			ReadySKU(context.Context, int) error
		})
		if !ok {
			return errShopDaypassUnavailable
		}
		return gateway.ReadySKU(ctx, a.durationDays)
	}
	return a.gateway.Ready(ctx, a.durationDays)
}

func daypassBinding(r shopaction.Request) shopDaypassBinding {
	binding := shopDaypassBinding{Instance: r.Source, OrderID: r.Order.ID, ProductID: r.Order.ProductID, PriceFen: r.Order.AmountFen, Currency: r.Order.Currency, PaidAt: r.Order.PaidAt.Unix()}
	sku := shopNativeSKU(r.Params)
	if sku.Version == 2 {
		binding.SKUVersion, binding.DurationSeconds, binding.Concurrency = 2, sku.Duration, sku.Concurrency
	}
	return binding
}

func (a *shopDaypassAdapter) Execute(ctx context.Context, r shopaction.Request) (shopaction.Result, error) {
	var result shopaction.Result
	sku := shopNativeSKU(r.Params)
	if sku.Duration == 0 || r.Order.ProductID != a.productID || r.Order.AmountFen != sku.priceFen() || r.Order.Currency != "CNY" {
		return result, &shopaction.Failure{Kind: "DAYPASS_SKU_INVALID"}
	}
	grant, err := a.gateway.Provision(ctx, daypassBinding(r))
	if errors.Is(err, errShopDaypassExpired) || errors.Is(err, errShopDaypassRevoked) {
		return result, &shopaction.Failure{Kind: "DAYPASS_EXPIRED_OR_REVOKED"}
	}
	if err != nil {
		return result, err
	}
	if grant == nil || grant.OrderID != r.Order.ID || grant.ProductID != r.Order.ProductID || grant.Instance != r.Source || grant.PaidAt != r.Order.PaidAt.Unix() || grant.ExpiresAt != grant.PaidAt+int64(sku.Duration) || grant.TokenID <= 0 || grant.Key == "" {
		return result, &shopaction.Failure{Kind: "RESULT_INVALID"}
	}
	content := fmt.Sprintf("DeepSeek API 不限量订阅卡\nBase URL：https://ds.turtleligpt.com/v1\nAPI Key：%s\n有效期：%d 天（自付款起算）\n到期时间：%s（UTC）\n模型：DeepSeek 系列（以 /v1/models 为准）\n%d并发；同一Key同时只能使用同一IP，停止调用15分钟后可切换网络。", grant.Key, sku.selection().DurationDays, time.Unix(grant.ExpiresAt, 0).UTC().Format("2006-01-02 15:04:05"), sku.Concurrency)
	return shopaction.Result{Version: 1, ActionID: r.ActionID, OrderID: r.Order.ID, Status: "completed", ReceiptID: strconv.Itoa(grant.TokenID), Delivery: shopaction.Delivery{Kind: "text", Content: content}}, nil
}

func (a *shopDaypassAdapter) Revoke(ctx context.Context, r shopaction.Request) error {
	if r.Order.ProductID != a.productID {
		return &shopaction.Failure{Kind: "DAYPASS_SKU_INVALID"}
	}
	return a.gateway.Revoke(ctx, daypassBinding(r))
}
