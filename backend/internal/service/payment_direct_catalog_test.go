//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// Exercise the link-only catalog configuration through existing admission and
// projection boundaries; hiding a navigation link is never authorization.
func TestDirectPaymentCatalogRestrictsProductsAndPreservesAmounts(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	viewer, err := client.User.Create().SetEmail("direct-viewer@example.test").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	other, err := client.User.Create().SetEmail("direct-other@example.test").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	group, err := client.Group.Create().SetName("direct Plus").SetPlatform(PlatformOpenAI).
		SetStatus(StatusActive).SetSubscriptionType(SubscriptionTypeSubscription).Save(ctx)
	require.NoError(t, err)
	oldPlan, err := client.SubscriptionPlan.Create().SetGroupID(group.ID).SetName("normal Plus").
		SetPrice(120).SetValidityDays(1).SetValidityUnit("month").SetForSale(false).Save(ctx)
	require.NoError(t, err)
	plan, err := client.SubscriptionPlan.Create().SetGroupID(group.ID).SetName("direct Plus").
		SetPrice(0.1).SetCurrency("CNY").SetValidityDays(1).SetValidityUnit("month").SetForSale(true).
		SetEntitlements(map[string]any{"purchase_rules": map[string]any{"visible_user_ids": []int64{viewer.ID}}, "reset_card_purchase_price": 40}).Save(ctx)
	require.NoError(t, err)
	options := []RechargeOption{
		{Amount: 5, Enabled: false}, {Amount: 49, Enabled: false}, {Amount: 99, Enabled: false},
		{Amount: 199, Enabled: false}, {Amount: 399, Enabled: false}, {Amount: 599, Enabled: false},
		{Amount: 0.1, BalanceBonus: 99.9, Enabled: true, PurchaseRules: &PurchaseRules{VisibleUserIDs: []int64{viewer.ID}}},
	}
	settingsRepo := &paymentConfigSettingRepoStub{values: map[string]string{SettingPaymentEnabled: "false"}}
	cfgSvc := &PaymentConfigService{entClient: client, settingRepo: settingsRepo}
	cfg := &PaymentConfig{Enabled: true, EntryEnabled: false, MinAmount: 0.1, BalanceRechargeMultiplier: 1, RechargeOptions: options}
	svc := &PaymentService{entClient: client, configService: cfgSvc, groupRepo: subscriptionCheckoutGroupRepoStub{group: &Group{
		ID: group.ID, Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription,
	}}}

	visible, err := cfgSvc.CustomerPaymentCatalogForUser(ctx, viewer.ID, options)
	require.NoError(t, err)
	require.Len(t, visible.Plans, 1)
	require.Equal(t, plan.ID, visible.Plans[0].Plan().ID)
	require.Equal(t, 1, visible.Plans[0].Plan().ValidityDays)
	require.Equal(t, "month", visible.Plans[0].Plan().ValidityUnit)
	require.Len(t, visible.RechargeOptions, 1)
	require.Nil(t, visible.Plans[0].Entitlements.PurchaseRules)
	require.Nil(t, visible.RechargeOptions[0].Option.PurchaseRules)
	encoded, err := json.Marshal(visible)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "visible_user_ids")

	hidden, err := cfgSvc.CustomerPaymentCatalogForUser(ctx, other.ID, options)
	require.NoError(t, err)
	require.Empty(t, hidden.Plans)
	require.Empty(t, hidden.RechargeOptions)
	require.Equal(t, RechargeModeFixed, RechargeModeForConfig(cfg), "mode uses the configured catalog, not the viewer's empty projection")
	for _, amount := range []float64{0.1, 5, 49, 99, 199, 399, 599, 100, 1} {
		_, err = svc.validateOrderInput(ctx, CreateOrderRequest{UserID: other.ID, OrderType: payment.OrderTypeBalance, Amount: amount}, cfg)
		require.Error(t, err, "non-audience user must not buy amount %v", amount)
	}
	for _, planID := range []int64{oldPlan.ID, plan.ID} {
		_, err = svc.validateSubOrder(ctx, CreateOrderRequest{UserID: other.ID, PlanID: planID})
		require.Error(t, err)
	}
	_, err = svc.validateOrderInput(ctx, CreateOrderRequest{UserID: viewer.ID, OrderType: payment.OrderTypeBalance, Amount: 0.1}, cfg)
	require.NoError(t, err)
	_, err = svc.validateSubOrder(ctx, CreateOrderRequest{UserID: viewer.ID, PlanID: plan.ID})
	require.NoError(t, err)
	require.Equal(t, float64(100), calculateRechargeCreditedAmount(0.1, 1, options))
	for _, orderType := range []string{payment.OrderTypeBalance, payment.OrderTypeSubscription} {
		amountText, payAmount, amountErr := calculateCreateOrderPayAmountForOrderType(0.1, 0, "CNY", orderType, 0)
		require.NoError(t, amountErr)
		require.Equal(t, "0.10", amountText)
		require.Equal(t, 0.1, payAmount)
		_, err = svc.CreateOrder(ctx, CreateOrderRequest{UserID: viewer.ID, OrderType: orderType, Amount: 0.1, PlanID: plan.ID})
		require.Equal(t, "PAYMENT_DISABLED", infraerrors.Reason(err), "global switch still closes the direct link")
	}

	createEligibilityPaymentOrder(t, client, viewer.ID, 100, 0.1, 0, "CNY", true, OrderStatusCompleted)
	total, err := completedCNYBalanceRechargeTotal(ctx, client, viewer.ID)
	require.NoError(t, err)
	require.True(t, total.Equal(decimal.RequireFromString("0.1")), "100 credited units must not become 100 CNY net paid")
}
