package service

import (
	"context"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestPaymentDiscountSnapshotKeepsDiscountOutOfRefundAndReferralPrincipal(t *testing.T) {
	order := &dbent.PaymentOrder{ID: 1, UserID: 2, OrderType: payment.OrderTypeBalance, Amount: 120, PayAmount: 81, ProductSnapshot: map[string]any{"schema_version": 2, "credited_amount": 120.0, "paid_credit_amount": 100.0, "gift_credit_amount": 20.0}}
	quote := &PaymentDiscountQuote{CodeID: 3, Code: "TEST2026", OriginalAmount: "101.25", DiscountAmount: "20.25", PayAmount: "81.00", Currency: "CNY"}
	require.NoError(t, applyPaymentDiscountSnapshot(order, quote))
	require.Equal(t, 80.0, order.ProductSnapshot["paid_credit_amount"])
	require.Equal(t, 40.0, order.ProductSnapshot["gift_credit_amount"])
	require.Equal(t, 80.0, affiliateRebateBaseAmount(order))
	public := SanitizedPaymentOrderProductSnapshot(order)
	discount, ok := public["payment_discount"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "20.25", discount["discount_amount"])
	require.NotContains(t, discount, "revision")
	require.NotContains(t, discount, "original_paid_credit_amount")
	subscription := &dbent.PaymentOrder{OrderType: payment.OrderTypeSubscription, Amount: 120, PayAmount: 81, ProductSnapshot: map[string]any{}}
	require.NoError(t, applyPaymentDiscountSnapshot(subscription, quote))
	require.Equal(t, 96.0, affiliateRebateBaseAmount(subscription))
	require.Equal(t, 120.0, subscription.Amount)
}

func TestPaymentDiscountBindingRejectsProductUserOrMethodSubstitution(t *testing.T) {
	base := CreateOrderRequest{UserID: 1, OrderType: payment.OrderTypeSubscription, PlanID: 3, Amount: 120, PaymentType: payment.TypeAlipay, CouponCode: "SAVE2026"}
	fingerprint := paymentDiscountRequestBinding(base)
	for _, mutate := range []func(*CreateOrderRequest){func(r *CreateOrderRequest) { r.UserID++ }, func(r *CreateOrderRequest) { r.PlanID++ }, func(r *CreateOrderRequest) { r.Amount++ }, func(r *CreateOrderRequest) { r.PaymentType = payment.TypeWxpay }, func(r *CreateOrderRequest) { r.CouponCode = "OTHER2026" }} {
		other := base
		mutate(&other)
		require.NotEqual(t, fingerprint, paymentDiscountRequestBinding(other))
	}
	req := base
	req.CouponRevision = strings.Repeat("a", 64)
	req.IdempotencyKey = "coupon-attempt-12345678"
	require.NoError(t, normalizePaymentDiscountRequest(&req))
	require.Len(t, req.IdempotencyKeyHash, 64)
	req.IdempotencyKey = ""
	req.IdempotencyKeyHash = "forged"
	require.Error(t, normalizePaymentDiscountRequest(&req))
}

func TestPaymentDiscountRejectsResetCardAtEveryNewOrderBoundary(t *testing.T) {
	ctx := context.Background()
	req := CreateOrderRequest{OrderType: payment.OrderTypeResetCard, CouponCode: "SAVE2026"}
	require.ErrorIs(t, normalizePaymentDiscountRequest(&req), ErrPaymentDiscountInvalid)

	svc := &PaymentService{}
	_, err := svc.QuotePaymentDiscount(ctx, req)
	require.ErrorIs(t, err, ErrPaymentDiscountInvalid)
	_, err = svc.CreateOrder(ctx, req)
	require.ErrorIs(t, err, ErrPaymentDiscountInvalid)
	_, _, err = svc.createOrderInTxWithOptions(ctx, req, nil, nil, nil, 0, 0, 0, 0, nil, nil)
	require.ErrorIs(t, err, ErrPaymentDiscountInvalid)
}

func newDiscountIntegrationOrder(t *testing.T, svc *PaymentService, user *dbent.User, code string, key string) (*dbent.PaymentOrder, error) {
	t.Helper()
	ctx := context.Background()
	req := CreateOrderRequest{UserID: user.ID, Amount: 100, OrderType: payment.OrderTypeBalance, PaymentType: payment.TypeAlipay, CouponCode: code, IdempotencyKeyHash: strings.Repeat(key, 64)}
	q, err := quotePaymentDiscount(ctx, svc.entClient, user.ID, code, decimal.NewFromInt(100), "CNY", req.OrderType, req.PlanID, paymentDiscountRequestBinding(req), false)
	if err != nil {
		return nil, err
	}
	req.CouponRevision = q.Revision
	req.couponQuote = q
	pay, _ := decimal.NewFromString(q.PayAmount)
	return svc.createOrderInTx(ctx, req, &User{ID: user.ID, Email: user.Email, Username: user.Username}, nil, &PaymentConfig{MaxPendingOrders: 100, OrderTimeoutMin: 30, BalanceRechargeMultiplier: 1}, 100, 100, 0, pay.InexactFloat64(), nil)
}

func TestPaymentDiscountReleaseLatePaidCapacityAndRetryFence(t *testing.T) {
	client := newUnifiedRefundSQLiteClient(t)
	installPaymentDiscountTestSchema(t, client)
	ctx := context.Background()
	actor, err := client.User.Create().SetEmail("coupon-integration@example.test").SetPasswordHash("test").SetUsername("coupon").Save(ctx)
	require.NoError(t, err)
	svc := &PaymentService{entClient: client}
	perUser := 1
	coupon, err := svc.SavePaymentDiscountCode(ctx, actor.ID, 0, PaymentDiscountCodeInput{Code: "SAVE2026", DiscountType: "fixed", DiscountValue: "20.00", Currency: "CNY", MaxUses: 1, PerUserMaxUses: &perUser, StartsAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour), Enabled: true}, 0)
	require.NoError(t, err)
	first, err := newDiscountIntegrationOrder(t, svc, actor, coupon.Code, "a")
	require.NoError(t, err)
	require.Equal(t, 80.0, first.PayAmount)
	require.Equal(t, 100.0, first.Amount)
	require.Equal(t, 80.0, first.ProductSnapshot["paid_credit_amount"])
	_, err = svc.markDiscountOrderPaid(ctx, first, "wrong-amount", 79.99)
	require.Error(t, err)
	unpaid, err := client.PaymentOrder.Get(ctx, first.ID)
	require.NoError(t, err)
	require.Nil(t, unpaid.PaidAt)
	require.Equal(t, OrderStatusPending, unpaid.Status)
	_, err = newDiscountIntegrationOrder(t, svc, actor, coupon.Code, "b")
	require.Error(t, err)
	count, err := svc.cancelDiscountOrder(ctx, first, OrderStatusCancelled)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	second, err := newDiscountIntegrationOrder(t, svc, actor, coupon.Code, "b")
	require.NoError(t, err)
	// A formerly pending cancellation used to route a delayed coupon payment
	// into a manual coupon-capacity hold. Explicit local cancellation is now an
	// immutable admission fence: late money is handled by the separate refund
	// path and must never reserve the released coupon again.
	allowed, err := svc.markDiscountOrderPaid(ctx, first, "late-trade", 80)
	require.Error(t, err)
	require.False(t, allowed)
	held, err := client.PaymentOrder.Get(ctx, first.ID)
	require.NoError(t, err)
	require.Nil(t, held.PaidAt)
	require.Equal(t, OrderStatusCancelled, held.Status)
	allowed, err = svc.markDiscountOrderPaid(ctx, second, "normal-trade", 80)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, err = svc.markDiscountOrderPaid(ctx, second, "normal-trade", 80)
	require.NoError(t, err)
	require.False(t, allowed)
	uses, total, err := svc.ListPaymentDiscountUses(ctx, coupon.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 2, total)
	statuses := map[string]int{}
	for _, u := range uses {
		statuses[u.Status]++
	}
	require.Equal(t, 1, statuses["consumed"])
	require.Equal(t, 1, statuses["released"])
}
