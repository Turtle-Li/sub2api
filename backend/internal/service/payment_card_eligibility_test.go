package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestNormalizePurchaseRulesCanonicalizesAudienceAndThreshold(t *testing.T) {
	threshold := 120.50
	rules, err := normalizePurchaseRules(&PurchaseRules{
		VisibleUserIDs:   []int64{7, 3, 7, 9},
		MinTotalRecharge: &threshold,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{7, 3, 9}, rules.VisibleUserIDs)
	require.NotNil(t, rules.MinTotalRecharge)
	require.Equal(t, 120.50, *rules.MinTotalRecharge)

	for _, invalid := range []*PurchaseRules{
		{VisibleUserIDs: []int64{0}},
		{VisibleUserIDs: []int64{-1}},
		{MinTotalRecharge: float64Pointer(-0.01)},
		{MinTotalRecharge: float64Pointer(1.001)},
		{MinTotalRecharge: float64Pointer(math.NaN())},
	} {
		_, err := normalizePurchaseRules(invalid)
		require.Error(t, err)
	}
}

func TestCompletedCNYBalanceRechargeTotalUsesNetDurablePayments(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().SetEmail("eligibility-total@example.test").SetPasswordHash("hash").SetUsername("eligibility-total").Save(ctx)
	require.NoError(t, err)

	// Bonus balance is represented by amount=110 while the customer actually
	// paid 100. The eligibility total must use pay_amount, not credited amount.
	createEligibilityPaymentOrder(t, client, user.ID, 110, 100, 0, "CNY", true, OrderStatusCompleted)
	// A half refund of a 200-credit order maps to a 50 CNY gateway refund.
	createEligibilityPaymentOrder(t, client, user.ID, 200, 100, 100, "CNY", true, OrderStatusCompleted)
	// A full refund contributes zero.
	createEligibilityPaymentOrder(t, client, user.ID, 110, 100, 110, "CNY", true, OrderStatusRefunded)
	// Foreign currency has no verified conversion under this accounting contract.
	createEligibilityPaymentOrder(t, client, user.ID, 100, 100, 0, "USD", true, OrderStatusCompleted)
	// A status alone is not durable fulfillment evidence.
	createEligibilityPaymentOrder(t, client, user.ID, 100, 100, 0, "CNY", false, OrderStatusCompleted)
	// completed_at is the authority even if a legacy status was not updated.
	createEligibilityPaymentOrder(t, client, user.ID, 100, 25, 0, "CNY", true, OrderStatusPending)

	total, err := completedCNYBalanceRechargeTotal(ctx, client, user.ID)
	require.NoError(t, err)
	require.True(t, total.Equal(decimal.RequireFromString("175.00")))
}

func TestCustomerPaymentCatalogFiltersAudienceAndStripsRules(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	viewer, err := client.User.Create().SetEmail("eligibility-viewer@example.test").SetPasswordHash("hash").SetUsername("eligibility-viewer").Save(ctx)
	require.NoError(t, err)
	hiddenUser, err := client.User.Create().SetEmail("eligibility-hidden@example.test").SetPasswordHash("hash").SetUsername("eligibility-hidden").Save(ctx)
	require.NoError(t, err)
	group, err := client.Group.Create().
		SetName("eligibility group").
		SetPlatform(PlatformOpenAI).
		SetStatus(StatusActive).
		SetSubscriptionType(SubscriptionTypeSubscription).
		Save(ctx)
	require.NoError(t, err)

	minimum := 200.0
	resetMinimum := 250.0
	visiblePlan, err := client.SubscriptionPlan.Create().
		SetGroupID(group.ID).
		SetName("visible plan").
		SetPrice(120).
		SetValidityDays(1).
		SetValidityUnit("month").
		SetForSale(true).
		SetEntitlements(map[string]any{
			"purchase_rules": map[string]any{
				"visible_user_ids":   []int64{viewer.ID},
				"min_total_recharge": minimum,
			},
			"reset_card_purchase_rules": map[string]any{
				"visible_user_ids":   []int64{viewer.ID},
				"min_total_recharge": resetMinimum,
			},
		}).
		Save(ctx)
	require.NoError(t, err)
	hiddenPlan, err := client.SubscriptionPlan.Create().
		SetGroupID(group.ID).
		SetName("hidden plan").
		SetPrice(550).
		SetValidityDays(1).
		SetValidityUnit("month").
		SetForSale(true).
		SetEntitlements(map[string]any{"purchase_rules": map[string]any{"visible_user_ids": []int64{hiddenUser.ID}}}).
		Save(ctx)
	require.NoError(t, err)
	createEligibilityPaymentOrder(t, client, viewer.ID, 100, 100, 0, "CNY", true, OrderStatusCompleted)

	minimumRecharge := 150.0
	catalog, err := projectCustomerPaymentCatalog(ctx, client, viewer.ID, []*dbent.SubscriptionPlan{visiblePlan, hiddenPlan}, []RechargeOption{
		{Amount: 99, Enabled: true, PurchaseRules: &PurchaseRules{VisibleUserIDs: []int64{viewer.ID}, MinTotalRecharge: &minimumRecharge}},
		{Amount: 199, Enabled: true, PurchaseRules: &PurchaseRules{VisibleUserIDs: []int64{hiddenUser.ID}}},
	})
	require.NoError(t, err)
	require.Len(t, catalog.Plans, 1)
	require.Equal(t, visiblePlan.ID, catalog.Plans[0].Plan().ID)
	require.Nil(t, catalog.Plans[0].Entitlements.PurchaseRules)
	require.Nil(t, catalog.Plans[0].Entitlements.ResetCardPurchaseRules)
	require.False(t, catalog.Plans[0].Eligibility.CanPurchase)
	require.Equal(t, "minimum_recharge", catalog.Plans[0].Eligibility.Reason)
	require.True(t, catalog.Plans[0].ResetCardEligibility.Visible)
	require.False(t, catalog.Plans[0].ResetCardEligibility.CanPurchase)
	require.Len(t, catalog.RechargeOptions, 1)
	require.Nil(t, catalog.RechargeOptions[0].Option.PurchaseRules)
	require.NotNil(t, catalog.RechargeOptions[0].Option.Eligibility)
	require.False(t, catalog.RechargeOptions[0].Eligibility.CanPurchase)

	serialized, err := json.Marshal(struct {
		Entitlements    PlanEntitlements `json:"entitlements"`
		RechargeOptions []RechargeOption `json:"recharge_options"`
	}{
		Entitlements:    catalog.Plans[0].Entitlements,
		RechargeOptions: []RechargeOption{catalog.RechargeOptions[0].Option},
	})
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "visible_user_ids")
	require.NotContains(t, string(serialized), "purchase_rules")
}

func TestValidatePurchaseRulesRejectsHiddenDirectCheckout(t *testing.T) {
	err := validatePurchaseRulesForUser(context.Background(), nil, 7, &PurchaseRules{VisibleUserIDs: []int64{8}})
	require.Error(t, err)
	require.Equal(t, "PURCHASE_NOT_ALLOWED", infraerrors.Reason(err))
}

func createEligibilityPaymentOrder(t *testing.T, client *dbent.Client, userID int64, amount, payAmount, refundAmount float64, currency string, completed bool, status string) {
	t.Helper()
	now := time.Now().UTC()
	builder := client.PaymentOrder.Create().
		SetUserID(userID).
		SetUserEmail(fmt.Sprintf("eligibility-%d@example.test", userID)).
		SetUserName("eligibility").
		SetAmount(amount).
		SetPayAmount(payAmount).
		SetFeeRate(0).
		SetRefundAmount(refundAmount).
		SetRechargeCode(fmt.Sprintf("eligibility-%d", time.Now().UnixNano())).
		SetOutTradeNo(fmt.Sprintf("eligibility-out-%d", time.Now().UnixNano())).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo(fmt.Sprintf("eligibility-trade-%d", time.Now().UnixNano())).
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(status).
		SetExpiresAt(now.Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("eligibility.test")
	if currency != "" {
		builder.SetProviderSnapshot(map[string]any{"currency": currency})
	}
	if completed {
		builder.SetCompletedAt(now)
	}
	_, err := builder.Save(context.Background())
	require.NoError(t, err)
}

func float64Pointer(value float64) *float64 {
	return &value
}
