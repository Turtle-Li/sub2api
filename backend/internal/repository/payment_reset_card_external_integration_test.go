//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type resetCardExternalPGSettings struct {
	service.SettingRepository
	values map[string]string
}

func (r *resetCardExternalPGSettings) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (r *resetCardExternalPGSettings) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = r.values[key]
	}
	return result, nil
}

type resetCardExternalPGFixture struct {
	service      *service.PaymentService
	central      *ownerTestPGCentral
	user         *service.User
	group        *service.Group
	plan         *dbent.SubscriptionPlan
	subscription *service.UserSubscription
}

func newResetCardExternalPGFixture(t *testing.T) *resetCardExternalPGFixture {
	t.Helper()
	ctx := context.Background()
	client := testEntClient(t)
	stamp := uuid.NewString()
	user := mustCreateUser(t, client, &service.User{
		Email:    "reset-external-" + stamp + "@integration.test",
		Username: "reset-external-user",
		Status:   service.StatusActive,
	})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             "reset-external-group-" + stamp,
		Platform:         service.PlatformOpenAI,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	plan, err := client.SubscriptionPlan.Create().
		SetGroupID(group.ID).
		SetName("Plus monthly").
		SetPrice(120).
		SetCurrency(payment.DefaultPaymentCurrency).
		SetValidityDays(1).
		SetValidityUnit("month").
		SetForSale(true).
		SetEntitlements(map[string]any{"reset_card_purchase_price": 40.0}).
		Save(ctx)
	require.NoError(t, err)
	subscription := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:    user.ID,
		GroupID:   group.ID,
		Status:    service.SubscriptionStatusActive,
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond),
	})

	settings := &resetCardExternalPGSettings{values: map[string]string{
		service.SettingPaymentEnabled:                    "true",
		service.SettingPaymentEntryEnabled:               "false",
		service.SettingOrderTimeoutMinutes:               "5",
		service.SettingMaxPendingOrders:                  "20",
		service.SettingBalancePayDisabled:                "false",
		service.SettingBalanceRechargeMult:               "1",
		service.SettingRechargeFeeRate:                   "0",
		service.SettingRechargeOptions:                   "[]",
		service.SettingEnabledPaymentTypes:               payment.TypeAlipay,
		service.SettingPaymentVisibleMethodAlipayEnabled: "true",
		service.SettingPaymentVisibleMethodAlipaySource:  service.VisibleMethodSourceUnifiedAlipay,
	}}
	configService := service.NewPaymentConfigService(client, settings, nil)
	subscriptionService := service.NewSubscriptionService(nil, nil, nil, client, nil)
	central := newOwnerTestPGCentral(t)
	paymentService := service.NewPaymentService(
		client,
		payment.NewRegistry(),
		nil,
		nil,
		subscriptionService,
		configService,
		NewUserRepository(client, integrationDB),
		NewGroupRepository(client, integrationDB),
		nil,
	)
	paymentService.SetUnifiedPayment(central.gateway(t), nil)

	fixture := &resetCardExternalPGFixture{
		service:      paymentService,
		central:      central,
		user:         user,
		group:        group,
		plan:         plan,
		subscription: subscription,
	}
	t.Cleanup(func() { fixture.cleanup(t) })
	return fixture
}

func (f *resetCardExternalPGFixture) cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	queries := []struct {
		query string
		arg   any
	}{
		{`DELETE FROM payment_audit_logs WHERE order_id IN (SELECT id::text FROM payment_orders WHERE user_id = $1)`, f.user.ID},
		{`DELETE FROM subscription_reset_card_purchases WHERE user_id = $1`, f.user.ID},
		{`DELETE FROM subscription_reset_grants WHERE user_id = $1`, f.user.ID},
		{`DELETE FROM payment_orders WHERE user_id = $1`, f.user.ID},
		{`DELETE FROM user_subscriptions WHERE id = $1`, f.subscription.ID},
		{`DELETE FROM subscription_plans WHERE id = $1`, f.plan.ID},
		{`DELETE FROM users WHERE id = $1`, f.user.ID},
		{`DELETE FROM groups WHERE id = $1`, f.group.ID},
	}
	for _, item := range queries {
		_, err := integrationDB.ExecContext(ctx, item.query, item.arg)
		require.NoError(t, err)
	}
}

func (f *resetCardExternalPGFixture) request(key string) service.CreateOrderRequest {
	return service.CreateOrderRequest{
		UserID:         f.user.ID,
		Amount:         40,
		PaymentType:    payment.TypeAlipay,
		OrderType:      payment.OrderTypeResetCard,
		PlanID:         f.plan.ID,
		SubscriptionID: f.subscription.ID,
		IdempotencyKey: key,
		ClientIP:       "127.0.0.1",
		SrcHost:        "www.turtleligpt.com",
		ReturnURL:      ownerTestPGReturnURL,
	}
}

func TestResetCardExternalOrderPostgresPricingReplayAndFulfillment(t *testing.T) {
	fixture := newResetCardExternalPGFixture(t)
	ctx := context.Background()
	request := fixture.request("reset-card-external-replay-0001")

	created, err := fixture.service.CreateOrder(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 40.0, created.Amount)
	require.Equal(t, 40.0, created.PayAmount)
	require.NotEmpty(t, created.PayURL)

	replayed, err := fixture.service.CreateOrder(ctx, request)
	require.NoError(t, err)
	require.Equal(t, created.OrderID, replayed.OrderID)
	require.Equal(t, created.OutTradeNo, replayed.OutTradeNo)
	require.Equal(t, created.PayURL, replayed.PayURL)

	bodies, keys, remoteOrders := fixture.central.snapshot()
	require.Len(t, bodies, 1)
	require.Len(t, keys, 1)
	require.Equal(t, 1, remoteOrders)
	var sent ownerTestPGCentralRequest
	require.NoError(t, json.Unmarshal([]byte(bodies[0]), &sent))
	require.Equal(t, int64(4000), sent.AmountFen)
	require.Equal(t, payment.OrderTypeResetCard, sent.OrderType)
	require.Equal(t, "sub2:create:"+created.OutTradeNo, keys[0])

	stored, err := fixture.service.GetOrder(ctx, created.OrderID, fixture.user.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderTypeResetCard, stored.OrderType)
	require.Nil(t, stored.SubscriptionDays)
	require.False(t, stored.ExpiresAt.After(fixture.subscription.ExpiresAt), "reset-card checkout must not outlive its target subscription")
	require.Equal(t, 40.0, stored.ProductSnapshot["price"])
	require.Equal(t, 120.0, stored.ProductSnapshot["monthly_price"])
	require.Equal(t, float64(fixture.subscription.ID), stored.ProductSnapshot["subscription_id"])

	conflicting := request
	conflicting.Amount = 41
	_, err = fixture.service.CreateOrder(ctx, conflicting)
	require.Equal(t, "IDEMPOTENCY_KEY_CONFLICT", infraerrors.Reason(err))

	remotePaymentOrderID, ok := stored.ProviderSnapshot["payment_order_id"].(string)
	require.True(t, ok)
	notification := &payment.PaymentNotification{
		TradeNo: "reset-card-provider-trade-0001",
		OrderID: created.OutTradeNo,
		Amount:  created.PayAmount,
		Status:  payment.NotificationStatusSuccess,
		Metadata: map[string]string{
			"payment_order_id": remotePaymentOrderID,
			"environment":      "live",
			"organization_id":  ownerTestPGOrganizationID,
			"product_id":       ownerTestPGProductID,
			"app_id":           ownerTestPGAppID,
		},
	}
	require.NoError(t, fixture.service.HandlePaymentNotification(ctx, notification, payment.TypeUnifiedPay))
	// A provider retry must observe the completed order and remain a no-op.
	require.NoError(t, fixture.service.HandlePaymentNotification(ctx, notification, payment.TypeUnifiedPay))

	completed, err := integrationEntClient.PaymentOrder.Get(ctx, created.OrderID)
	require.NoError(t, err)
	require.Equal(t, service.OrderStatusCompleted, completed.Status)
	require.NotNil(t, completed.PaidAt)
	var grants, purchases, grantAudits int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM subscription_reset_grants WHERE payment_order_id = $1`, created.OrderID).Scan(&grants))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM subscription_reset_card_purchases WHERE user_id = $1`, fixture.user.ID).Scan(&purchases))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_audit_logs WHERE order_id = $1::text AND action = 'RESET_CARD_GRANTED'`, created.OrderID).Scan(&grantAudits))
	require.Equal(t, 1, grants)
	require.Zero(t, purchases, "external payment fulfillment must not create a wallet purchase ledger row")
	require.Equal(t, 1, grantAudits)

	_, err = fixture.service.CreateOrder(ctx, request)
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM subscription_reset_grants WHERE payment_order_id = $1`, created.OrderID).Scan(&grants))
	require.Equal(t, 1, grants)
}

func TestResetCardExternalOrderPostgresConcurrentCreateUsesOneLocalOrder(t *testing.T) {
	fixture := newResetCardExternalPGFixture(t)
	ctx := context.Background()
	request := fixture.request("reset-card-external-concurrent-0001")

	const callers = 6
	type result struct {
		response *service.CreateOrderResponse
		err      error
	}
	results := make(chan result, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := fixture.service.CreateOrder(ctx, request)
			results <- result{response: response, err: err}
		}()
	}
	wg.Wait()
	close(results)

	var orderID int64
	for result := range results {
		require.NoError(t, result.err)
		require.NotNil(t, result.response)
		if orderID == 0 {
			orderID = result.response.OrderID
		}
		require.Equal(t, orderID, result.response.OrderID)
	}
	count, err := integrationEntClient.PaymentOrder.Query().Where(
		paymentorder.UserIDEQ(fixture.user.ID),
		paymentorder.OrderTypeEQ(payment.OrderTypeResetCard),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	_, _, remoteOrders := fixture.central.snapshot()
	require.Equal(t, 1, remoteOrders)
}

func TestResetCardExternalOrderPostgresFulfillsCheckoutTimeEligibilityAfterExpiry(t *testing.T) {
	fixture := newResetCardExternalPGFixture(t)
	ctx := context.Background()
	request := fixture.request("reset-card-external-expiry-0001")

	created, err := fixture.service.CreateOrder(ctx, request)
	require.NoError(t, err)
	stored, err := integrationEntClient.PaymentOrder.Get(ctx, created.OrderID)
	require.NoError(t, err)
	remotePaymentOrderID, ok := stored.ProviderSnapshot["payment_order_id"].(string)
	require.True(t, ok)
	snapshotExpiryText, ok := stored.ProductSnapshot["subscription_expires_at"].(string)
	require.True(t, ok)
	snapshotExpiry, err := time.Parse(time.RFC3339Nano, snapshotExpiryText)
	require.NoError(t, err)

	_, err = integrationEntClient.UserSubscription.UpdateOneID(fixture.subscription.ID).
		SetStatus(service.SubscriptionStatusExpired).
		SetExpiresAt(time.Now().UTC().Add(-time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	notification := &payment.PaymentNotification{
		TradeNo: "reset-card-provider-trade-expired",
		OrderID: created.OutTradeNo,
		Amount:  created.PayAmount,
		Status:  payment.NotificationStatusSuccess,
		Metadata: map[string]string{
			"payment_order_id": remotePaymentOrderID,
			"environment":      "live",
			"organization_id":  ownerTestPGOrganizationID,
			"product_id":       ownerTestPGProductID,
			"app_id":           ownerTestPGAppID,
		},
	}
	require.NoError(t, fixture.service.HandlePaymentNotification(ctx, notification, payment.TypeUnifiedPay))

	completed, err := integrationEntClient.PaymentOrder.Get(ctx, created.OrderID)
	require.NoError(t, err)
	require.Equal(t, service.OrderStatusCompleted, completed.Status)
	var grantExpiry time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT expires_at
		FROM subscription_reset_grants
		WHERE payment_order_id = $1
	`, created.OrderID).Scan(&grantExpiry))
	require.WithinDuration(t, snapshotExpiry, grantExpiry, time.Microsecond)
}

func TestResetCardPaymentGrantUniqueIndexRejectsDuplicateOrderGrant(t *testing.T) {
	fixture := newResetCardExternalPGFixture(t)
	ctx := context.Background()
	response, err := fixture.service.CreateOrder(ctx, fixture.request("reset-card-external-unique-0001"))
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	insert := `INSERT INTO subscription_reset_grants
		(subscription_id,user_id,group_id,quantity,used_count,expires_at,payment_order_id,created_at,updated_at)
		VALUES ($1,$2,$3,1,0,$4,$5,$6,$6)`
	_, err = integrationDB.ExecContext(ctx, insert, fixture.subscription.ID, fixture.user.ID, fixture.group.ID, fixture.subscription.ExpiresAt, response.OrderID, now)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, insert, fixture.subscription.ID, fixture.user.ID, fixture.group.ID, fixture.subscription.ExpiresAt, response.OrderID, now.Add(time.Microsecond))
	require.Error(t, err)
	require.Contains(t, fmt.Sprint(err), "idx_subscription_reset_grants_payment_order_unique")
}
