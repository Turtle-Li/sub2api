package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/ent/shopproduct"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func shopOrderOutTradeNo(userID int64, keyHash string) string {
	digest := sha256.Sum256([]byte("sub2-shop-v1\n" + strconv.FormatInt(userID, 10) + "\n" + keyHash))
	return "sub2_shop_" + hex.EncodeToString(digest[:24])
}

// CreateShopOrder deliberately exposes no amount or entitlement fields. The
// existing public CreateOrder entry continues to reject this private type.
func (s *PaymentService) CreateShopOrder(ctx context.Context, productID int64, productRevision int, req CreateOrderRequest) (*CreateOrderResponse, error) {
	if err := s.shopService.ready(); err != nil {
		return nil, err
	}
	if req.PaymentType != payment.TypeAlipay && req.PaymentType != payment.TypeWxpay {
		return nil, infraerrors.BadRequest("SHOP_PAYMENT_METHOD_INVALID", "请选择支付宝或微信支付")
	}
	key, err := NormalizeIdempotencyKey(req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	req.IdempotencyKey = ""
	req.IdempotencyKeyHash = HashIdempotencyKey(key)
	req.OrderType = payment.OrderTypeManualShop
	if s.unifiedPayment != nil {
		req.ReturnURL = s.unifiedPayment.ReturnURL()
	}
	// Never bind secret customer input or short-lived OAuth credentials to checkout.
	req.OpenID = ""
	req.IsWeChatBrowser = false
	req.CouponCode = ""
	req.PlanID = 0
	req.shopProduct = &ShopProduct{ID: productID, Revision: productRevision}
	existing, err := s.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNoEQ(shopOrderOutTradeNo(req.UserID, req.IdempotencyKeyHash))).Only(ctx)
	if err == nil {
		return s.replayShopOrderRecord(ctx, existing, req)
	}
	if !dbent.IsNotFound(err) {
		return nil, fmt.Errorf("lookup shop checkout: %w", err)
	}
	p, err := s.shopService.checkoutProduct(ctx, productID)
	if err != nil {
		return nil, err
	}
	if p.Revision != productRevision {
		return nil, shopConflict()
	}
	selected, err := s.shopService.resolveShopSKU(*p, req.ShopSKU)
	if err != nil {
		return nil, err
	}
	p = &selected
	if err := s.shopService.requireActionsCheckout(ctx, *p); err != nil {
		return nil, err
	}
	req.shopProduct = p
	req.Amount = p.Price
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("get shop payment config: %w", err)
	}
	if !cfg.Enabled {
		return nil, infraerrors.Forbidden("PAYMENT_DISABLED", "payment system is disabled")
	}
	return s.createOrderWithConfig(ctx, req, cfg, nil)
}

func revalidateShopProductTx(ctx context.Context, client *dbent.Client, p *ShopProduct) error {
	if p == nil {
		return infraerrors.BadRequest("SHOP_PRODUCT_INVALID", "missing product")
	}
	q := client.ShopProduct.Query().Where(shopproduct.IDEQ(p.ID))
	if client.Driver().Dialect() == dialect.Postgres {
		q.ForUpdate()
	}
	row, err := q.Only(ctx)
	if dbent.IsNotFound(err) {
		return shopConflict()
	}
	if err != nil {
		return fmt.Errorf("lock shop product: %w", err)
	}
	if !row.Enabled || row.Revision != p.Revision {
		return shopConflict()
	}
	resolved, err := selectShopSKU(shopProductDTO(row), shopProductSKU(*p).selection())
	if err != nil || resolved.Price != p.Price || resolved.DurationValue != p.DurationValue || resolved.DurationUnit != p.DurationUnit {
		return shopConflict()
	}
	*p = resolved
	return nil
}

func (s *PaymentService) replayShopOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, error) {
	o, err := s.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNoEQ(shopOrderOutTradeNo(req.UserID, req.IdempotencyKeyHash))).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("reload shop checkout: %w", err)
	}
	return s.replayShopOrderRecord(ctx, o, req)
}

func (s *PaymentService) replayShopOrderRecord(ctx context.Context, o *dbent.PaymentOrder, req CreateOrderRequest) (*CreateOrderResponse, error) {
	if o.UserID != req.UserID || o.OrderType != payment.OrderTypeManualShop || o.PaymentType != req.PaymentType || req.shopProduct == nil {
		return nil, ErrIdempotencyKeyConflict
	}
	f, err := s.entClient.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(o.ID)).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("load shop checkout snapshot: %w", err)
	}
	p, err := shopSnapshot(f)
	if err != nil {
		return nil, err
	}
	if p.ID != req.shopProduct.ID || p.Revision != req.shopProduct.Revision {
		return nil, ErrIdempotencyKeyConflict
	}
	purchased := shopProductSKU(p).selection()
	selected := req.ShopSKU
	if purchased != (ShopSKUSelection{}) {
		selected = selected.withNativeDefaults()
	}
	if selected != purchased {
		return nil, ErrIdempotencyKeyConflict
	}
	if o.Status != OrderStatusPending || o.PaidAt != nil || !o.ExpiresAt.After(time.Now()) || psStringValue(o.PayURL) != "" || psStringValue(o.QrCode) != "" {
		resp := buildResetCardOrderResponse(o)
		s.hydrateCheckoutFrameURL(ctx, o, resp)
		return resp, nil
	}
	if paymentOrderExpiresInSeconds(o.ExpiresAt, time.Now()) < 300 {
		return nil, infraerrors.Conflict("SHOP_CHECKOUT_RECOVERY_REQUIRED", "请从我的订单恢复付款；当前订单不能重新创建收银台")
	}
	// The central gateway deduplicates creation by the deterministic product
	// order number. Repeating after a lost response cannot create a second charge.
	if s.unifiedPayment == nil || !s.unifiedPayment.Enabled() || !missingUnifiedPaymentOrderScopeMatchesGateway(psOrderProviderSnapshot(o), s.unifiedPayment) {
		return nil, infraerrors.ServiceUnavailable("SHOP_PAYMENT_BINDING_CHANGED", "原支付通道暂不可用，请稍后重试")
	}
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("get shop checkout config: %w", err)
	}
	if !cfg.Enabled {
		return nil, infraerrors.Forbidden("PAYMENT_DISABLED", "payment system is disabled")
	}
	req.shopProduct = &p
	req.Amount = p.Price
	req.ReturnURL = s.unifiedPayment.ReturnURL()
	sel := s.unifiedPayment.Selection(o.PaymentType)
	if sel == nil {
		return nil, infraerrors.ServiceUnavailable("SHOP_PAYMENT_UNAVAILABLE", "支付通道暂不可用")
	}
	return s.invokeProvider(ctx, o, req, cfg, p.Price, payment.FormatAmountForCurrency(o.PayAmount, "CNY"), o.PayAmount, nil, sel)
}
