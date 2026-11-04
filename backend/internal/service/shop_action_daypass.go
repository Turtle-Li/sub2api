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
	gateway   shopDaypassGateway
	productID int64
}

func validShopDaypassProduct(p ShopProduct) bool {
	return p.FulfillmentMode == ShopAccountDelivery && p.Currency == "CNY" && p.Price == 6 && p.DurationValue == 1 && p.DurationUnit == "day"
}

func (a *shopDaypassAdapter) Ready(ctx context.Context) error { return a.gateway.Ready(ctx) }

func daypassBinding(r shopaction.Request) shopDaypassBinding {
	return shopDaypassBinding{Instance: r.Source, OrderID: r.Order.ID, ProductID: r.Order.ProductID, PriceFen: r.Order.AmountFen, Currency: r.Order.Currency, PaidAt: r.Order.PaidAt.Unix()}
}

func (a *shopDaypassAdapter) Execute(ctx context.Context, r shopaction.Request) (shopaction.Result, error) {
	var result shopaction.Result
	if r.Order.ProductID != a.productID || r.Order.AmountFen != 600 || r.Order.Currency != "CNY" {
		return result, &shopaction.Failure{Kind: "DAYPASS_SKU_INVALID"}
	}
	grant, err := a.gateway.Provision(ctx, daypassBinding(r))
	if errors.Is(err, errShopDaypassExpired) || errors.Is(err, errShopDaypassRevoked) {
		return result, &shopaction.Failure{Kind: "DAYPASS_EXPIRED_OR_REVOKED"}
	}
	if err != nil {
		return result, err
	}
	if grant == nil || grant.OrderID != r.Order.ID || grant.ProductID != r.Order.ProductID || grant.Instance != r.Source || grant.PaidAt != r.Order.PaidAt.Unix() || grant.ExpiresAt != grant.PaidAt+86400 || grant.TokenID <= 0 || grant.Key == "" {
		return result, &shopaction.Failure{Kind: "RESULT_INVALID"}
	}
	content := fmt.Sprintf("DeepSeek 24小时 API 日卡\nBase URL：https://ds.turtleligpt.com/v1\nAPI Key：%s\n到期时间：%s（UTC）\n模型：deepseek-v4.1-flash、deepseek-v4-pro\n默认2并发；同一Key同时只能使用同一IP，停止调用15分钟后可切换网络。\n无需注册或登录New API。", grant.Key, time.Unix(grant.ExpiresAt, 0).UTC().Format("2006-01-02 15:04:05"))
	return shopaction.Result{Version: 1, ActionID: r.ActionID, OrderID: r.Order.ID, Status: "completed", ReceiptID: strconv.Itoa(grant.TokenID), Delivery: shopaction.Delivery{Kind: "text", Content: content}}, nil
}

func (a *shopDaypassAdapter) Revoke(ctx context.Context, r shopaction.Request) error {
	if r.Order.ProductID != a.productID {
		return &shopaction.Failure{Kind: "DAYPASS_SKU_INVALID"}
	}
	return a.gateway.Revoke(ctx, daypassBinding(r))
}
