package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestQuoteSubscriptionPurchase_ChargesOnlyProratedUpgradeDifference(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().
		SetEmail("upgrade-quote@example.com").
		SetPasswordHash("hash").
		Save(ctx)
	require.NoError(t, err)
	currentGroup, err := client.Group.Create().SetName("OpenAI Standard").SetPlatform("openai").Save(ctx)
	require.NoError(t, err)
	targetGroup, err := client.Group.Create().SetName("OpenAI Pro").SetPlatform("openai").Save(ctx)
	require.NoError(t, err)

	currentPrice := 30.0
	currentDays := 30
	_, err = client.UserSubscription.Create().
		SetUserID(user.ID).
		SetGroupID(currentGroup.ID).
		SetPlatform("openai").
		SetStartsAt(time.Now().AddDate(0, 0, -15)).
		SetExpiresAt(time.Now().AddDate(0, 0, 15)).
		SetStatus(SubscriptionStatusActive).
		SetPlanPrice(currentPrice).
		SetPlanValidityDays(currentDays).
		Save(ctx)
	require.NoError(t, err)

	svc := &PaymentService{
		entClient: client,
		groupRepo: &subscriptionGroupRepoStub{group: &Group{
			ID: targetGroup.ID, Platform: "openai", Status: "active", SubscriptionType: SubscriptionTypeSubscription,
		}},
	}
	quote, err := svc.quoteSubscriptionPurchase(ctx, user.ID, &dbent.SubscriptionPlan{
		ID: 99, GroupID: targetGroup.ID, Price: 90, ValidityDays: 30, ValidityUnit: "day", ForSale: true,
	}, false)

	require.NoError(t, err)
	require.Equal(t, subscriptionPurchaseOperationUpgrade, quote.Operation)
	// Standard costs $1/day and Pro costs $3/day. With 15 remaining whole
	// days, the customer pays only the $30 difference and retains the expiry.
	require.Equal(t, 30.0, quote.Amount)
	require.Equal(t, 30, quote.SubscriptionDays)
}

func TestQuoteResetCardPurchase_UsesStoredSubscriptionPriceAndTwoWeekValidity(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().
		SetEmail("reset-card-quote@example.com").
		SetPasswordHash("hash").
		Save(ctx)
	require.NoError(t, err)
	group, err := client.Group.Create().SetName("Anthropic Pro").SetPlatform("anthropic").Save(ctx)
	require.NoError(t, err)

	planID := int64(12)
	planPrice := 30.0
	subscription, err := client.UserSubscription.Create().
		SetUserID(user.ID).
		SetGroupID(group.ID).
		SetPlatform("anthropic").
		SetStartsAt(time.Now()).
		SetExpiresAt(time.Now().AddDate(0, 0, 30)).
		SetStatus(SubscriptionStatusActive).
		SetPlanID(planID).
		SetPlanPrice(planPrice).
		SetPlanValidityDays(30).
		Save(ctx)
	require.NoError(t, err)

	svc := &PaymentService{entClient: client}
	quote, err := svc.quoteResetCardPurchase(ctx, user.ID, subscription.ID, 2, false)

	require.NoError(t, err)
	require.Equal(t, 10.0, quote.UnitPrice)
	require.Equal(t, 20.0, quote.Amount)
	snapshot := buildResetCardPurchaseProductSnapshot(quote, 20)
	require.Equal(t, resetCardPurchaseValidityDays, snapshot["validity_days"])
	require.Equal(t, 2, snapshot["quantity"])
}

func TestQuoteSubscriptionPurchase_ExpiredPlatformSubscriptionResubscribesAtFullPrice(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().
		SetEmail("resubscribe-quote@example.com").
		SetPasswordHash("hash").
		Save(ctx)
	require.NoError(t, err)
	oldGroup, err := client.Group.Create().SetName("OpenAI Legacy").SetPlatform("openai").Save(ctx)
	require.NoError(t, err)
	targetGroup, err := client.Group.Create().SetName("OpenAI Pro").SetPlatform("openai").Save(ctx)
	require.NoError(t, err)
	_, err = client.UserSubscription.Create().
		SetUserID(user.ID).
		SetGroupID(oldGroup.ID).
		SetPlatform("openai").
		SetStartsAt(time.Now().AddDate(0, 0, -60)).
		SetExpiresAt(time.Now().AddDate(0, 0, -30)).
		SetStatus(SubscriptionStatusExpired).
		Save(ctx)
	require.NoError(t, err)

	svc := &PaymentService{
		entClient: client,
		groupRepo: &subscriptionGroupRepoStub{group: &Group{
			ID: targetGroup.ID, Platform: "openai", Status: "active", SubscriptionType: SubscriptionTypeSubscription,
		}},
	}
	quote, err := svc.quoteSubscriptionPurchase(ctx, user.ID, &dbent.SubscriptionPlan{
		ID: 100, GroupID: targetGroup.ID, Price: 90, ValidityDays: 30, ValidityUnit: "day", ForSale: true,
	}, false)

	require.NoError(t, err)
	require.Equal(t, subscriptionPurchaseOperationResubscribe, quote.Operation)
	require.Equal(t, 90.0, quote.Amount)
	require.Equal(t, 30, quote.SubscriptionDays)
}

func TestApplyPaymentSubscriptionTerm_UpgradeKeepsExpiryAndUsedQuota(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().
		SetEmail("upgrade-preserves-quota@example.com").
		SetPasswordHash("hash").
		Save(ctx)
	require.NoError(t, err)
	oldGroup, err := client.Group.Create().SetName("OpenAI Standard").SetPlatform("openai").Save(ctx)
	require.NoError(t, err)
	targetGroup, err := client.Group.Create().SetName("OpenAI Pro").SetPlatform("openai").Save(ctx)
	require.NoError(t, err)
	startsAt := time.Now().AddDate(0, 0, -10).Truncate(time.Second)
	expiresAt := time.Now().AddDate(0, 0, 20).Truncate(time.Second)
	windowStart := startsAt.Add(2 * time.Hour)
	oldPlanID := int64(11)
	existing, err := client.UserSubscription.Create().
		SetUserID(user.ID).
		SetGroupID(oldGroup.ID).
		SetPlatform("openai").
		SetPlanID(oldPlanID).
		SetPlanPrice(30).
		SetPlanValidityDays(30).
		SetStartsAt(startsAt).
		SetExpiresAt(expiresAt).
		SetStatus(SubscriptionStatusActive).
		SetDailyWindowStart(windowStart).
		SetWeeklyWindowStart(windowStart).
		SetMonthlyWindowStart(windowStart).
		SetDailyUsageUsd(4).
		SetWeeklyUsageUsd(8).
		SetMonthlyUsageUsd(12).
		Save(ctx)
	require.NoError(t, err)
	targetPlanID := int64(22)
	order := &dbent.PaymentOrder{ID: 501, UserID: user.ID}

	err = applyPaymentSubscriptionTerm(
		ctx,
		client,
		existing,
		order,
		targetGroup.ID,
		"openai",
		&targetPlanID,
		90,
		30,
		subscriptionPurchaseOperationUpgrade,
		existing.ID,
		paymentSubscriptionOrderNote(order.ID),
	)
	require.NoError(t, err)

	upgraded, err := client.UserSubscription.Get(ctx, existing.ID)
	require.NoError(t, err)
	require.Equal(t, targetGroup.ID, upgraded.GroupID)
	require.Equal(t, "openai", upgraded.Platform)
	require.Equal(t, expiresAt, upgraded.ExpiresAt)
	require.Equal(t, startsAt, upgraded.StartsAt)
	require.Equal(t, SubscriptionStatusActive, upgraded.Status)
	require.Equal(t, 4.0, upgraded.DailyUsageUsd)
	require.Equal(t, 8.0, upgraded.WeeklyUsageUsd)
	require.Equal(t, 12.0, upgraded.MonthlyUsageUsd)
	require.Equal(t, windowStart, *upgraded.DailyWindowStart)
	require.Equal(t, windowStart, *upgraded.WeeklyWindowStart)
	require.Equal(t, windowStart, *upgraded.MonthlyWindowStart)
	require.Equal(t, targetPlanID, *upgraded.PlanID)
	require.Equal(t, 90.0, *upgraded.PlanPrice)
	require.Equal(t, 30, *upgraded.PlanValidityDays)
}

func TestValidatePaymentSubscriptionSourceRejectsChangedCommercialTerms(t *testing.T) {
	planID := int64(11)
	validityDays := 30
	subscription := &dbent.UserSubscription{
		ID:               7,
		GroupID:          9,
		PlanID:           &planID,
		PlanPrice:        floatPtr(30),
		PlanValidityDays: &validityDays,
	}
	snapshot := map[string]interface{}{
		"source_subscription_id":    float64(subscription.ID),
		"source_group_id":           float64(subscription.GroupID),
		"source_plan_id":            float64(planID),
		"source_plan_price":         30.0,
		"source_plan_validity_days": 30,
	}
	require.NoError(t, validatePaymentSubscriptionSource(subscription, snapshot))

	changedPrice := *subscription
	changedPrice.PlanPrice = floatPtr(45)
	err := validatePaymentSubscriptionSource(&changedPrice, snapshot)
	require.Error(t, err)
	require.Contains(t, err.Error(), "source subscription price changed")
}

func TestValidatePurchasedResetCardSourceRejectsChangedTerms(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*dbent.UserSubscription, map[string]interface{})
		amount     float64
		wantReason string
	}{
		{name: "unchanged terms", amount: 30},
		{name: "missing source price", amount: 30, mutate: func(_ *dbent.UserSubscription, snapshot map[string]interface{}) {
			delete(snapshot, "source_plan_price")
		}, wantReason: "INVALID_PRODUCT_SNAPSHOT"},
		{name: "plan changed", amount: 30, mutate: func(subscription *dbent.UserSubscription, _ map[string]interface{}) {
			changed := int64(8)
			subscription.PlanID = &changed
		}, wantReason: "SUBSCRIPTION_PURCHASE_TERMS_CHANGED"},
		{name: "price changed", amount: 30, mutate: func(subscription *dbent.UserSubscription, _ map[string]interface{}) {
			changed := 60.0
			subscription.PlanPrice = &changed
		}, wantReason: "SUBSCRIPTION_PURCHASE_TERMS_CHANGED"},
		{name: "validity changed", amount: 30, mutate: func(subscription *dbent.UserSubscription, _ map[string]interface{}) {
			changed := 90
			subscription.PlanValidityDays = &changed
		}, wantReason: "SUBSCRIPTION_PURCHASE_TERMS_CHANGED"},
		{name: "unit price tampered", amount: 30, mutate: func(_ *dbent.UserSubscription, snapshot map[string]interface{}) {
			snapshot["unit_price"] = 1.0
		}, wantReason: "SUBSCRIPTION_PURCHASE_TERMS_CHANGED"},
		{name: "order amount tampered", amount: 1, wantReason: "SUBSCRIPTION_PURCHASE_TERMS_CHANGED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planID := int64(7)
			days := 30
			price := 30.0
			subscription := &dbent.UserSubscription{PlanID: &planID, PlanPrice: &price, PlanValidityDays: &days, Platform: "openai", GroupID: 9}
			snapshot := map[string]interface{}{
				"source_plan_id":            float64(planID),
				"source_plan_price":         30.0,
				"source_plan_validity_days": 30,
				"unit_price":                10.0,
			}
			if tt.mutate != nil {
				tt.mutate(subscription, snapshot)
			}
			err := validatePurchasedResetCardSource(subscription, snapshot, tt.amount, 3)
			if tt.wantReason == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Equal(t, tt.wantReason, infraerrors.Reason(err))
		})
	}
}

func TestHasPendingSubscriptionPurchaseMatchesPaymentFactsAndGrace(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	now := time.Now()
	tests := []struct {
		name      string
		status    string
		expiresAt time.Time
		updatedAt time.Time
		paid      bool
		legacy    bool
		want      bool
	}{
		{name: "pending before expiry", status: OrderStatusPending, expiresAt: now.Add(time.Hour), want: true},
		{name: "legacy pending derives platform from group", status: OrderStatusPending, expiresAt: now.Add(time.Hour), legacy: true, want: true},
		{name: "pending inside callback grace", status: OrderStatusPending, expiresAt: now.Add(-2 * time.Minute), want: true},
		{name: "pending beyond callback grace", status: OrderStatusPending, expiresAt: now.Add(-paymentGraceMinutes*time.Minute - time.Second), want: false},
		{name: "paid", status: OrderStatusPaid, expiresAt: now.Add(-time.Hour), paid: true, want: true},
		{name: "recharging", status: OrderStatusRecharging, expiresAt: now.Add(-time.Hour), paid: true, want: true},
		{name: "paid fulfillment failed", status: OrderStatusFailed, expiresAt: now.Add(-time.Hour), paid: true, want: true},
		{name: "provider creation failed inside callback grace", status: OrderStatusFailed, expiresAt: now.Add(-2 * time.Minute), want: true},
		{name: "provider creation failed beyond callback grace", status: OrderStatusFailed, expiresAt: now.Add(-paymentGraceMinutes*time.Minute - time.Second), want: false},
		{name: "recently expired", status: OrderStatusExpired, expiresAt: now.Add(-time.Hour), updatedAt: now.Add(-2 * time.Minute), want: true},
		{name: "expired beyond callback grace", status: OrderStatusExpired, expiresAt: now.Add(-time.Hour), updatedAt: now.Add(-paymentGraceMinutes*time.Minute - time.Second), want: false},
		{name: "recently cancelled", status: OrderStatusCancelled, expiresAt: now.Add(time.Hour), updatedAt: now.Add(-2 * time.Minute), want: true},
		{name: "cancelled beyond callback grace", status: OrderStatusCancelled, expiresAt: now.Add(time.Hour), updatedAt: now.Add(-paymentGraceMinutes*time.Minute - time.Second), want: false},
		{name: "completed", status: OrderStatusCompleted, expiresAt: now.Add(-time.Hour), paid: true, want: false},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, err := client.User.Create().
				SetEmail(fmt.Sprintf("pending-platform-%d@example.com", i)).
				SetPasswordHash("hash").
				Save(ctx)
			require.NoError(t, err)
			productSnapshot := map[string]interface{}{"platform": "openai"}
			var groupID int64
			if tt.legacy {
				group, groupErr := client.Group.Create().
					SetName(fmt.Sprintf("legacy-pending-group-%d", i)).
					SetPlatform("openai").
					Save(ctx)
				require.NoError(t, groupErr)
				groupID = group.ID
				productSnapshot = map[string]interface{}{}
			}
			create := client.PaymentOrder.Create().
				SetUserID(user.ID).
				SetUserEmail(user.Email).
				SetUserName(user.Username).
				SetAmount(30).
				SetPayAmount(30).
				SetFeeRate(0).
				SetRechargeCode(fmt.Sprintf("pending-platform-code-%d", i)).
				SetOutTradeNo(fmt.Sprintf("pending-platform-order-%d", i)).
				SetPaymentType(payment.TypeAlipay).
				SetPaymentTradeNo("").
				SetOrderType(payment.OrderTypeSubscription).
				SetStatus(tt.status).
				SetExpiresAt(tt.expiresAt).
				SetClientIP("127.0.0.1").
				SetSrcHost("test.local").
				SetProductSnapshot(productSnapshot)
			if groupID > 0 {
				create.SetSubscriptionGroupID(groupID)
			}
			if !tt.updatedAt.IsZero() {
				create.SetUpdatedAt(tt.updatedAt)
			}
			if tt.paid {
				create.SetPaidAt(now).SetPaymentTradeNo(fmt.Sprintf("trade-%d", i))
			}
			_, err = create.Save(ctx)
			require.NoError(t, err)

			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			found, err := hasPendingSubscriptionPurchase(dbent.NewTxContext(ctx, tx), tx, user.ID, "openai")
			require.NoError(t, err)
			require.Equal(t, tt.want, found)
			otherPlatform, err := hasPendingSubscriptionPurchase(dbent.NewTxContext(ctx, tx), tx, user.ID, "anthropic")
			require.NoError(t, err)
			require.False(t, otherPlatform)
			require.NoError(t, tx.Rollback())
		})
	}
}

func floatPtr(value float64) *float64 {
	return &value
}
