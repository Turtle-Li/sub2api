//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPaymentRefundReconciliationStoreStatsBlocksRollbackForUnsettledResetCardPurchases(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	stamp := uuid.NewString()
	user := mustCreateUser(t, client, &service.User{
		Email:        "reset-rollback-" + stamp + "@integration.test",
		Username:     "reset-rollback-" + stamp[:8],
		PasswordHash: "test-only-password-hash",
		Status:       service.StatusActive,
	})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM payment_audit_logs WHERE order_id IN (SELECT id::text FROM payment_orders WHERE user_id = $1)`, user.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM payment_orders WHERE user_id = $1`, user.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, user.ID)
		require.NoError(t, err)
	})

	now := time.Now().UTC().Truncate(time.Microsecond)
	createOrder := func(orderType, status string, schemaVersion any, paidAt *time.Time) *dbent.PaymentOrder {
		t.Helper()
		id := uuid.NewString()
		snapshot := map[string]any{"schema_version": schemaVersion, "kind": orderType}
		if orderType == payment.OrderTypeResetCard {
			snapshot["quantity"] = 2
			snapshot["use_on_purchase"] = true
		}
		builder := client.PaymentOrder.Create().
			SetUserID(user.ID).
			SetUserEmail(user.Email).
			SetUserName(user.Username).
			SetAmount(40).
			SetPayAmount(40).
			SetFeeRate(0).
			SetRechargeCode("reset-rollback-" + id).
			SetOutTradeNo("reset-rollback-" + id).
			SetPaymentType(payment.TypeAlipay).
			SetPaymentTradeNo("trade-" + id).
			SetOrderType(orderType).
			SetStatus(status).
			SetProductSnapshot(snapshot).
			SetExpiresAt(now.Add(time.Hour)).
			SetClientIP("127.0.0.1").
			SetSrcHost("integration.test")
		if paidAt != nil {
			builder.SetPaidAt(*paidAt)
		}
		order, err := builder.Save(ctx)
		require.NoError(t, err)
		return order
	}
	stats := func() service.PaymentRefundReconciliationStats {
		t.Helper()
		value, err := NewPaymentRefundReconciliationStore(integrationDB).Stats(ctx)
		require.NoError(t, err)
		return value
	}

	baseline := stats().UnsettledResetCardPurchaseCount

	// A new reset-card purchase can carry multi-card or automatic-use promises.
	// Every non-terminal fulfillment state must block a rollback, even before a
	// provider callback has populated paid_at.
	createOrder(payment.OrderTypeResetCard, payment.OrderStatusPending, 2, nil)
	createOrder(payment.OrderTypeResetCard, payment.OrderStatusPaid, 2, &now)
	createOrder(payment.OrderTypeResetCard, payment.OrderStatusRecharging, 2, &now)
	createOrder(payment.OrderTypeResetCard, payment.OrderStatusFailed, 2, nil)
	require.EqualValues(t, baseline+4, stats().UnsettledResetCardPurchaseCount)

	// Terminal orders, legacy snapshots, and other products cannot carry the v2
	// reset-card delivery contract and therefore must not keep the release gate closed.
	for _, status := range []string{
		payment.OrderStatusCompleted,
		payment.OrderStatusRefunded,
		payment.OrderStatusPartiallyRefunded,
	} {
		createOrder(payment.OrderTypeResetCard, status, 2, &now)
	}
	createOrder(payment.OrderTypeResetCard, payment.OrderStatusCancelled, 2, nil)
	createOrder(payment.OrderTypeResetCard, payment.OrderStatusExpired, 2, nil)
	createOrder(payment.OrderTypeResetCard, payment.OrderStatusPending, 1, &now)
	createOrder(payment.OrderTypeResetCard, payment.OrderStatusPending, nil, nil)
	createOrder(payment.OrderTypeBalance, payment.OrderStatusPending, 2, nil)
	createOrder(payment.OrderTypeSubscription, payment.OrderStatusPaid, 2, &now)
	require.EqualValues(t, baseline+4, stats().UnsettledResetCardPurchaseCount)

	// A terminal label alone is not proof that a payment did not settle. Retain
	// paid cancellations/expirations and future snapshots until a new runtime has
	// had a chance to reconcile their grants.
	createOrder(payment.OrderTypeResetCard, payment.OrderStatusCancelled, 2, &now)
	createOrder(payment.OrderTypeResetCard, payment.OrderStatusExpired, 2, &now)
	createOrder(payment.OrderTypeResetCard, payment.OrderStatusPending, 3, nil)
	require.EqualValues(t, baseline+7, stats().UnsettledResetCardPurchaseCount)

	// Conservatively fence all new schemas, including one-card purchases with
	// no auto-use promise, and any unrecognized lifecycle state.
	single := createOrder(payment.OrderTypeResetCard, payment.OrderStatusPending, 2, nil)
	_, err := client.PaymentOrder.UpdateOneID(single.ID).SetProductSnapshot(map[string]any{
		"schema_version": 2, "kind": payment.OrderTypeResetCard,
		"quantity": 1, "use_on_purchase": false,
	}).Save(ctx)
	require.NoError(t, err)
	createOrder(payment.OrderTypeResetCard, "FUTURE_PENDING", 2, nil)
	require.EqualValues(t, baseline+9, stats().UnsettledResetCardPurchaseCount)
}
