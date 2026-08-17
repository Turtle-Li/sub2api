//go:build integration

package service

import (
	"context"
	"database/sql"
	"strconv"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestSubscriptionPurchasePlatformAdvisoryLockSerializesConcurrentOrders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	container, err := tcpostgres.Run(
		ctx,
		"postgres:18-alpine",
		tcpostgres.WithDatabase("sub2api_payment_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(ctx))

	driver := entsql.OpenDB(dialect.Postgres, db)
	client := dbent.NewClient(dbent.Driver(driver))
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(ctx))

	user, err := client.User.Create().
		SetEmail("subscription-lock-integration@example.com").
		SetPasswordHash("hash").
		Save(ctx)
	require.NoError(t, err)

	firstTx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = firstTx.Rollback() }()
	firstCtx := dbent.NewTxContext(ctx, firstTx)
	require.NoError(t, lockSubscriptionPurchasePlatform(firstCtx, firstTx, user.ID, "openai"))
	_, err = firstTx.Client().PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(30).
		SetPayAmount(30).
		SetFeeRate(0).
		SetRechargeCode("subscription-lock-first-code").
		SetOutTradeNo("subscription-lock-first-order").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeSubscription).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("test.local").
		SetProductSnapshot(map[string]interface{}{"platform": "openai"}).
		Save(firstCtx)
	require.NoError(t, err)

	secondTx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = secondTx.Rollback() }()
	secondCtx := dbent.NewTxContext(ctx, secondTx)
	started := make(chan struct{})
	acquired := make(chan error, 1)
	go func() {
		close(started)
		acquired <- lockSubscriptionPurchasePlatform(secondCtx, secondTx, user.ID, "OPENAI")
	}()
	<-started

	select {
	case lockErr := <-acquired:
		require.NoError(t, lockErr)
		t.Fatal("second transaction acquired the same user/platform lock before the first committed")
	case <-time.After(250 * time.Millisecond):
	}

	require.NoError(t, firstTx.Commit())
	select {
	case lockErr := <-acquired:
		require.NoError(t, lockErr)
	case <-time.After(3 * time.Second):
		t.Fatal("second transaction did not acquire the user/platform lock after the first committed")
	}

	found, err := hasPendingSubscriptionPurchase(secondCtx, secondTx, user.ID, "openai")
	require.NoError(t, err)
	require.True(t, found, "second transaction must observe the first committed payable order")
	require.NoError(t, secondTx.Rollback())

	_, err = client.ExecContext(ctx, `
		CREATE TABLE subscription_reset_grants (
			id BIGSERIAL PRIMARY KEY,
			subscription_id BIGINT NOT NULL,
			user_id BIGINT NOT NULL,
			group_id BIGINT NOT NULL,
			quantity INTEGER NOT NULL,
			used_count INTEGER NOT NULL DEFAULT 0,
			expires_at TIMESTAMPTZ NOT NULL,
			issued_by BIGINT,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		)
	`)
	require.NoError(t, err)
	group, err := client.Group.Create().
		SetName("subscription-reset-lock-group").
		SetPlatform("openai").
		SetStatus(StatusActive).
		SetSubscriptionType(SubscriptionTypeSubscription).
		Save(ctx)
	require.NoError(t, err)
	planID := int64(901)
	planPrice := 30.0
	planDays := 30
	subscription, err := client.UserSubscription.Create().
		SetUserID(user.ID).
		SetGroupID(group.ID).
		SetPlatform("openai").
		SetPlanID(planID).
		SetPlanPrice(planPrice).
		SetPlanValidityDays(planDays).
		SetStartsAt(time.Now().Add(-time.Hour)).
		SetExpiresAt(time.Now().Add(24 * time.Hour)).
		SetStatus(SubscriptionStatusActive).
		Save(ctx)
	require.NoError(t, err)
	resetOrder, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(30).
		SetPayAmount(30).
		SetFeeRate(0).
		SetRechargeCode("subscription-reset-lock-code").
		SetOutTradeNo("subscription-reset-lock-order").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("reset-trade").
		SetOrderType(payment.OrderTypeSubscriptionResetCards).
		SetStatus(OrderStatusPaid).
		SetPaidAt(time.Now()).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetSubscriptionGroupID(group.ID).
		SetPlanID(planID).
		SetProductSnapshot(map[string]interface{}{
			"kind":                      "subscription_reset_cards",
			"subscription_id":           subscription.ID,
			"group_id":                  group.ID,
			"platform":                  "openai",
			"quantity":                  3,
			"validity_days":             resetCardPurchaseValidityDays,
			"unit_price":                10.0,
			"source_plan_id":            planID,
			"source_plan_price":         planPrice,
			"source_plan_validity_days": planDays,
		}).
		SetClientIP("127.0.0.1").
		SetSrcHost("test.local").
		Save(ctx)
	require.NoError(t, err)

	grantErrors := make(chan error, 2)
	var grantWG sync.WaitGroup
	for range 2 {
		grantWG.Add(1)
		go func() {
			defer grantWG.Done()
			_, grantErr := (&PaymentService{entClient: client}).grantPurchasedResetCards(ctx, resetOrder)
			grantErrors <- grantErr
		}()
	}
	grantWG.Wait()
	close(grantErrors)
	for grantErr := range grantErrors {
		require.NoError(t, grantErr)
	}

	rows, err := client.QueryContext(ctx, "SELECT COUNT(*) FROM subscription_reset_grants WHERE subscription_id = $1", subscription.ID)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var grantCount int
	require.NoError(t, rows.Scan(&grantCount))
	require.NoError(t, rows.Err())
	require.Equal(t, 1, grantCount, "concurrent fulfillment must insert one reset-card grant")
	auditCount, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(resetOrder.ID, 10)), paymentauditlog.ActionEQ("SUBSCRIPTION_RESET_CARDS_GRANTED")).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, auditCount, "concurrent fulfillment must write one grant audit")
}
