//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPaymentRefundWalletComponentsPostgresConsumePaidFirst(t *testing.T) {
	ctx := context.Background()
	unique := uuid.NewString()
	created := mustCreateUser(t, integrationEntClient, &service.User{
		Email:    unique + "@wallet-refund.integration.test",
		Username: "wallet-refund-" + unique[:8],
		Balance:  100,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1`, created.ID)
	})

	// Trusted payment fulfillment explicitly identifies 40 credits as paid.
	require.NoError(t, integrationEntClient.User.UpdateOneID(created.ID).
		SetWalletAvailablePaid(40).Exec(ctx))
	// Generic spending consumes the paid component before gift/unattributed
	// credit. A later generic positive credit never manufactures new principal.
	require.NoError(t, integrationEntClient.User.UpdateOneID(created.ID).AddBalance(-30).Exec(ctx))
	require.NoError(t, integrationEntClient.User.UpdateOneID(created.ID).AddBalance(20).Exec(ctx))

	// Existing balance holds carry the paid component in both directions.
	require.NoError(t, integrationEntClient.User.UpdateOneID(created.ID).
		AddBalance(-10).AddFrozenBalance(10).Exec(ctx))
	require.NoError(t, integrationEntClient.User.UpdateOneID(created.ID).
		AddBalance(5).AddFrozenBalance(-5).Exec(ctx))

	got, err := integrationEntClient.User.Get(ctx, created.ID)
	require.NoError(t, err)
	require.InDelta(t, 85, got.Balance, 0.00000001)
	require.InDelta(t, 5, got.FrozenBalance, 0.00000001)
	require.InDelta(t, 5, got.WalletAvailablePaid, 0.00000001)
	require.InDelta(t, 5, got.WalletFrozenPaid, 0.00000001)
	require.Equal(t, int64(5), got.WalletComponentVersion)

	var events int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wallet_principal_events WHERE user_id = $1`, created.ID).Scan(&events))
	require.Zero(t, events, "ordinary usage and wallet holds must not add a second hot-path audit write")

	auditTx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = auditTx.Rollback() }()
	_, err = auditTx.ExecContext(ctx, `SELECT set_config('sub2api.wallet_event_kind', 'test_audited_adjustment', true)`)
	require.NoError(t, err)
	_, err = auditTx.ExecContext(ctx, `UPDATE users SET balance = balance + 1 WHERE id = $1`, created.ID)
	require.NoError(t, err)
	require.NoError(t, auditTx.Commit())

	var eventKind string
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT event_kind FROM wallet_principal_events WHERE user_id = $1 ORDER BY id DESC LIMIT 1`, created.ID).Scan(&eventKind))
	require.Equal(t, "test_audited_adjustment", eventKind)
}

func TestPaymentRefundSubscriptionHoldPostgresBlocksConcurrentTermMutation(t *testing.T) {
	ctx := context.Background()
	unique := uuid.NewString()
	createdUser := mustCreateUser(t, integrationEntClient, &service.User{
		Email:    unique + "@subscription-refund.integration.test",
		Username: "sub-refund-" + unique[:8],
	})
	createdGroup := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name:             "subscription-refund-" + unique,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	now := time.Now().UTC().Truncate(time.Second)
	createdSub := mustCreateSubscription(t, integrationEntClient, &service.UserSubscription{
		UserID: createdUser.ID, GroupID: createdGroup.ID,
		StartsAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(29 * 24 * time.Hour),
	})
	createdOrder, err := integrationEntClient.PaymentOrder.Create().
		SetUserID(createdUser.ID).
		SetUserEmail(createdUser.Email).
		SetUserName(createdUser.Username).
		SetAmount(120).
		SetPayAmount(120).
		SetFeeRate(0).
		SetRechargeCode("refund-hold-" + unique).
		SetOutTradeNo("refund_hold_" + unique).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("refund_hold_trade_" + unique).
		SetOrderType(payment.OrderTypeSubscription).
		SetStatus(service.OrderStatusCompleted).
		SetExpiresAt(now.Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("localhost").
		Save(ctx)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM payment_subscription_grants WHERE payment_order_id = $1`, createdOrder.ID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM payment_orders WHERE id = $1`, createdOrder.ID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM user_subscriptions WHERE id = $1`, createdSub.ID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM subscription_cache_invalidation_outbox
			WHERE user_id = $1 AND group_id = $2`,
			createdUser.ID, createdGroup.ID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM groups WHERE id = $1`, createdGroup.ID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1`, createdUser.ID)
	})

	_, err = integrationDB.ExecContext(ctx, `INSERT INTO payment_subscription_grants (
		payment_order_id, subscription_id, user_id, group_id, term_start_at,
		original_term_end_at, current_term_end_at, reserved_seconds,
		reserved_cash_minor
	) VALUES ($1,$2,$3,$4,$5,$6,$6,$7,$8)`, createdOrder.ID, createdSub.ID,
		createdUser.ID, createdGroup.ID, createdSub.StartsAt, createdSub.ExpiresAt, 3600, 500)
	require.NoError(t, err)

	_, err = integrationDB.ExecContext(ctx, `UPDATE user_subscriptions SET expires_at = expires_at + INTERVAL '1 day' WHERE id = $1`, createdSub.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), fmt.Sprintf("subscription %d has a pending refund entitlement hold", createdSub.ID))

	trusted, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = trusted.Rollback() }()
	_, err = trusted.ExecContext(ctx, `SELECT set_config('sub2api.subscription_refund_mutation', 'release', true)`)
	require.NoError(t, err)
	_, err = trusted.ExecContext(ctx, `UPDATE user_subscriptions SET expires_at = expires_at - INTERVAL '1 hour' WHERE id = $1`, createdSub.ID)
	require.NoError(t, err)
	require.NoError(t, trusted.Commit())

	var durableInvalidations int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM subscription_cache_invalidation_outbox
		WHERE user_id = $1 AND group_id = $2`,
		createdUser.ID, createdGroup.ID).Scan(&durableInvalidations))
	require.GreaterOrEqual(t, durableInvalidations, 1)
}
