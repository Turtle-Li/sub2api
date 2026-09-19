//go:build unit

package service

import (
	"context"
	"net/url"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestResetCardQuantityUsesMinorUnitTotalAndBindsReplay(t *testing.T) {
	t.Parallel()

	quantity, useOnPurchase, err := normalizeResetCardPurchaseTerms(payment.OrderTypeResetCard, 0, false)
	require.NoError(t, err)
	require.Equal(t, resetCardDefaultQuantity, quantity)
	require.False(t, useOnPurchase)
	_, _, err = normalizeResetCardPurchaseTerms(payment.OrderTypeResetCard, resetCardMaximumQuantity+1, false)
	require.Error(t, err)
	_, _, err = normalizeResetCardPurchaseTerms(payment.OrderTypeBalance, 2, false)
	require.Error(t, err)
	_, _, err = normalizeResetCardPurchaseTerms(payment.OrderTypeBalance, 0, true)
	require.Error(t, err)

	total, totalMinor, err := resetCardTotalFromUnitPrice(12.34, 3)
	require.NoError(t, err)
	require.Equal(t, int64(3702), totalMinor)
	require.Equal(t, 37.02, total)
	_, err = validateResetCardRequestAmount(37.01, 12.34, 3)
	require.ErrorIs(t, err, ErrResetCardQuoteChanged)
	validatedTotal, err := validateResetCardRequestAmount(37.02, 12.34, 3)
	require.NoError(t, err)
	require.Equal(t, total, validatedTotal)

	keyHash := HashIdempotencyKey("reset-card-quantity-replay")
	planID := int64(7)
	req := CreateOrderRequest{
		UserID:                 11,
		Amount:                 total,
		PaymentType:            payment.TypeAlipay,
		OrderType:              payment.OrderTypeResetCard,
		PlanID:                 planID,
		SubscriptionID:         42,
		ResetCardQuantity:      3,
		ResetCardUseOnPurchase: true,
		IdempotencyKeyHash:     keyHash,
	}
	snapshot := buildPaymentResetCardProductSnapshot(&resetCardOrderSnapshotSource{
		plan:                &dbent.SubscriptionPlan{ID: planID, GroupID: 4, Currency: payment.DefaultPaymentCurrency},
		subscriptionID:      req.SubscriptionID,
		groupID:             4,
		monthlyPrice:        37.02,
		unitPrice:           12.34,
		price:               total,
		quantity:            req.ResetCardQuantity,
		useOnPurchase:       req.ResetCardUseOnPurchase,
		subscriptionExpires: time.Now().UTC().Add(time.Hour),
		idempotencyKeyHash:  keyHash,
	}, total)
	terms, err := resetCardSnapshotPurchaseTerms(snapshot)
	require.NoError(t, err)
	require.Equal(t, 3, terms.quantity)
	require.Equal(t, 12.34, terms.unitPrice)
	require.Equal(t, total, terms.totalAmount)
	require.True(t, terms.useOnPurchase)

	order := &dbent.PaymentOrder{
		UserID:          req.UserID,
		Amount:          total,
		PaymentType:     req.PaymentType,
		OrderType:       req.OrderType,
		OutTradeNo:      resetCardOrderOutTradeNo(req.UserID, keyHash),
		ProductSnapshot: snapshot,
	}
	require.NoError(t, validateResetCardOrderRecord(order, req, nil))

	changedQuantity := req
	changedQuantity.ResetCardQuantity = 1
	require.ErrorIs(t, validateResetCardOrderRecord(order, changedQuantity, nil), ErrIdempotencyKeyConflict)
	changedUseOnPurchase := req
	changedUseOnPurchase.ResetCardUseOnPurchase = false
	require.ErrorIs(t, validateResetCardOrderRecord(order, changedUseOnPurchase, nil), ErrIdempotencyKeyConflict)
	changedAmount := req
	changedAmount.Amount = 37.01
	require.ErrorIs(t, validateResetCardOrderRecord(order, changedAmount, nil), ErrIdempotencyKeyConflict)

	oauthStartURL, err := buildWeChatPaymentOAuthStartURL(req, "snsapi_base")
	require.NoError(t, err)
	parsedOAuthStartURL, err := url.Parse(oauthStartURL)
	require.NoError(t, err)
	oauthQuery := parsedOAuthStartURL.Query()
	require.Equal(t, "3", oauthQuery.Get("reset_card_quantity"))
	require.Equal(t, "true", oauthQuery.Get("reset_card_use_on_purchase"))
	require.Equal(t, keyHash, oauthQuery.Get("idempotency_key_hash"))
}

func TestResetCardQuantityFulfillmentAutoUsesOnlyNewGrant(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	ensureResetCardPaymentGrantTable(t, ctx, client)

	svc, order, target, now := newResetCardQuantityFulfillmentFixture(t, ctx, client, 3, true)
	require.NoError(t, svc.executeFulfillment(ctx, order.ID))

	var quantity, usedCount int
	rows, err := client.QueryContext(ctx, `SELECT quantity,used_count FROM subscription_reset_grants WHERE payment_order_id=$1`, order.ID)
	require.NoError(t, err)
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&quantity, &usedCount))
	require.NoError(t, rows.Close())
	require.Equal(t, 3, quantity)
	require.Equal(t, 1, usedCount, "automatic use must consume only the newly granted card once")

	reloadedTarget, err := client.UserSubscription.Get(ctx, target.ID)
	require.NoError(t, err)
	require.Zero(t, reloadedTarget.DailyUsageUsd)
	require.Zero(t, reloadedTarget.WeeklyUsageUsd)
	require.Zero(t, reloadedTarget.MonthlyUsageUsd)
	require.NotNil(t, reloadedTarget.DailyWindowStart)
	require.NotNil(t, reloadedTarget.WeeklyWindowStart)
	require.NotNil(t, reloadedTarget.MonthlyWindowStart)
	require.True(t, reloadedTarget.DailyWindowStart.Equal(startOfDay(now)))
	require.True(t, reloadedTarget.WeeklyWindowStart.Equal(startOfDay(now)))
	require.True(t, reloadedTarget.MonthlyWindowStart.Equal(startOfDay(now)))

	usedAudits, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(int64String(order.ID)),
		paymentauditlog.ActionEQ("RESET_CARD_AUTO_USED"),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, usedAudits)

	// A callback/recovery retry sees the completed order and cannot consume a
	// second card from the quantity-three grant.
	require.NoError(t, svc.executeFulfillment(ctx, order.ID))
	rows, err = client.QueryContext(ctx, `SELECT quantity,used_count FROM subscription_reset_grants WHERE payment_order_id=$1`, order.ID)
	require.NoError(t, err)
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&quantity, &usedCount))
	require.NoError(t, rows.Close())
	require.Equal(t, 3, quantity)
	require.Equal(t, 1, usedCount)
}

func TestResetCardQuantityAutoUseSkipsUnavailableTargetButKeepsGrant(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, ctx context.Context, client *dbent.Client, target *dbent.UserSubscription, now time.Time)
	}{
		{
			name: "suspended",
			mutate: func(t *testing.T, ctx context.Context, client *dbent.Client, target *dbent.UserSubscription, _ time.Time) {
				t.Helper()
				_, err := client.UserSubscription.UpdateOneID(target.ID).SetStatus(SubscriptionStatusSuspended).Save(ctx)
				require.NoError(t, err)
			},
		},
		{
			name: "expired",
			mutate: func(t *testing.T, ctx context.Context, client *dbent.Client, target *dbent.UserSubscription, now time.Time) {
				t.Helper()
				_, err := client.UserSubscription.UpdateOneID(target.ID).SetExpiresAt(now.Add(-time.Minute)).Save(ctx)
				require.NoError(t, err)
			},
		},
		{
			name: "group changed",
			mutate: func(t *testing.T, ctx context.Context, client *dbent.Client, target *dbent.UserSubscription, _ time.Time) {
				t.Helper()
				group, err := client.Group.Create().SetName("reset-card-auto-use-replacement-group").Save(ctx)
				require.NoError(t, err)
				_, err = client.UserSubscription.UpdateOneID(target.ID).SetGroupID(group.ID).Save(ctx)
				require.NoError(t, err)
			},
		},
		{
			name: "soft deleted",
			mutate: func(t *testing.T, ctx context.Context, client *dbent.Client, target *dbent.UserSubscription, now time.Time) {
				t.Helper()
				_, err := client.UserSubscription.UpdateOneID(target.ID).SetDeletedAt(now).Save(ctx)
				require.NoError(t, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
			ensureResetCardPaymentGrantTable(t, ctx, client)
			svc, order, target, now := newResetCardQuantityFulfillmentFixture(t, ctx, client, 3, true)
			tt.mutate(t, ctx, client, target, now)

			require.NoError(t, svc.executeFulfillment(ctx, order.ID))
			var quantity, usedCount int
			rows, err := client.QueryContext(ctx, `SELECT quantity,used_count FROM subscription_reset_grants WHERE payment_order_id=$1`, order.ID)
			require.NoError(t, err)
			require.True(t, rows.Next())
			require.NoError(t, rows.Scan(&quantity, &usedCount))
			require.NoError(t, rows.Close())
			require.Equal(t, 3, quantity)
			require.Zero(t, usedCount)

			skippedAudits, err := client.PaymentAuditLog.Query().Where(
				paymentauditlog.OrderIDEQ(int64String(order.ID)),
				paymentauditlog.ActionEQ("RESET_CARD_AUTO_USE_NOT_PERFORMED"),
			).Count(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, skippedAudits)
			reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusCompleted, reloaded.Status)
		})
	}
}

func TestResetCardQuantityRemainsManualRefundOnly(t *testing.T) {
	manual, err := paymentOrderRequiresManualRefund(&dbent.PaymentOrder{
		OrderType: payment.OrderTypeResetCard,
		ProductSnapshot: map[string]any{
			"kind":     "reset_card",
			"quantity": 99,
		},
	})
	require.NoError(t, err)
	require.True(t, manual)
}

func TestResetCardQuantityIsRetainedInSanitizedOrderSnapshot(t *testing.T) {
	snapshot := SanitizedPaymentOrderProductSnapshot(&dbent.PaymentOrder{
		OrderType: payment.OrderTypeResetCard,
		ProductSnapshot: map[string]any{
			"kind":            "reset_card",
			"price":           37.02,
			"quantity":        3,
			"unit_price":      12.34,
			"use_on_purchase": true,
		},
	})
	require.Equal(t, 37.02, snapshot["price"])
	require.Equal(t, 3, snapshot["quantity"])
	require.Equal(t, 12.34, snapshot["unit_price"])
	require.Equal(t, true, snapshot["use_on_purchase"])
}

func newResetCardQuantityFulfillmentFixture(t *testing.T, ctx context.Context, client *dbent.Client, quantity int, useOnPurchase bool) (*PaymentService, *dbent.PaymentOrder, *dbent.UserSubscription, time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	order := createPaymentFulfillmentExpiredResetCardOrder(t, ctx, client)
	group, err := client.Group.Create().
		SetName("reset-card-quantity-fulfillment-group").
		SetPlatform(PlatformOpenAI).
		SetStatus(StatusActive).
		SetSubscriptionType(SubscriptionTypeSubscription).
		Save(ctx)
	require.NoError(t, err)
	target, err := client.UserSubscription.Create().
		SetUserID(order.UserID).
		SetGroupID(group.ID).
		SetStartsAt(now.Add(-time.Hour)).
		SetExpiresAt(now.Add(2 * time.Hour)).
		SetStatus(SubscriptionStatusActive).
		SetDailyUsageUsd(3.25).
		SetWeeklyUsageUsd(4.5).
		SetMonthlyUsageUsd(5.75).
		SetDailyWindowStart(now.Add(-24 * time.Hour)).
		SetWeeklyWindowStart(now.Add(-7 * 24 * time.Hour)).
		SetMonthlyWindowStart(now.Add(-30 * 24 * time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	total, _, err := resetCardTotalFromUnitPrice(20, quantity)
	require.NoError(t, err)
	snapshot := clonePaymentOrderSnapshot(order.ProductSnapshot)
	snapshot["schema_version"] = 2
	snapshot["subscription_id"] = target.ID
	snapshot["group_id"] = group.ID
	snapshot["subscription_expires_at"] = now.Add(2 * time.Hour).Format(time.RFC3339Nano)
	snapshot["unit_price"] = 20.0
	snapshot["price"] = total
	snapshot["order_amount"] = total
	snapshot["pay_amount"] = total
	snapshot["quantity"] = quantity
	snapshot["use_on_purchase"] = useOnPurchase
	snapshot["reset_card_tier"] = map[string]any{
		"family_key":     "gpt",
		"tier_rank":      2,
		"source_plan_id": int64(7),
	}
	order, err = client.PaymentOrder.UpdateOneID(order.ID).
		SetAmount(total).
		SetPayAmount(total).
		SetSubscriptionGroupID(group.ID).
		SetProductSnapshot(snapshot).
		Save(ctx)
	require.NoError(t, err)

	return &PaymentService{entClient: client, resetCardNow: func() time.Time { return now }}, order, target, now
}

func int64String(value int64) string {
	return strconv.FormatInt(value, 10)
}
