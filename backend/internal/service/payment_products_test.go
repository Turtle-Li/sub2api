package service

import (
	"context"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestNormalizePlanEntitlements(t *testing.T) {
	normalized, entitlements, err := normalizePlanEntitlements(map[string]any{
		"balance_bonus":          12.5,
		"reset_card_count":       2,
		"reset_card_expiry_days": 30,
		"concurrency":            5,
		"message":                "  Welcome bonus  ",
		"recommended":            true,
	})
	require.NoError(t, err)
	require.Equal(t, 12.5, entitlements.BalanceBonus)
	require.Equal(t, 2, entitlements.ResetCardCount)
	require.Equal(t, 30, entitlements.ResetCardExpiryDays)
	require.Equal(t, 5, entitlements.Concurrency)
	require.Equal(t, "Welcome bonus", entitlements.Message)
	require.True(t, entitlements.Recommended)
	require.Equal(t, float64(2), normalized["reset_card_count"])
	require.Equal(t, true, normalized["recommended"])
}

func TestNormalizePlanEntitlementsRejectsInvalidResetCardExpiry(t *testing.T) {
	_, _, err := normalizePlanEntitlements(map[string]any{
		"reset_card_count":       1,
		"reset_card_expiry_days": 0,
	})
	require.ErrorContains(t, err, "reset_card_expiry_days")
}

func TestPaymentEntitlementsRequireManualRefundForEveryNonReversibleBenefit(t *testing.T) {
	for _, tc := range []struct {
		name         string
		entitlements PlanEntitlements
	}{
		{name: "bonus balance", entitlements: PlanEntitlements{BalanceBonus: 0.01}},
		{name: "reset cards", entitlements: PlanEntitlements{ResetCardCount: 1}},
		{name: "concurrency", entitlements: PlanEntitlements{Concurrency: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, paymentEntitlementsRequireManualRefund(tc.entitlements))
		})
	}
	require.False(t, paymentEntitlementsRequireManualRefund(PlanEntitlements{}))
}

func TestPaymentOrderRequiresManualRefundVerifiesBalanceBonusWasCredited(t *testing.T) {
	base := func(credited any) *dbent.PaymentOrder {
		return &dbent.PaymentOrder{
			Amount: 100,
			ProductSnapshot: map[string]any{
				"kind":            paymentSnapshotKindBalance,
				"credited_amount": credited,
				"entitlements":    map[string]any{"balance_bonus": 5},
			},
		}
	}

	manual, err := paymentOrderRequiresManualRefund(base(float64(100)))
	require.NoError(t, err)
	require.False(t, manual, "a matching credited amount proves the bonus is reclaimable")

	for name, order := range map[string]*dbent.PaymentOrder{
		"missing credited amount":    base(nil),
		"mismatched credited amount": base(95),
		"one-cent credited mismatch": base(99.99),
		"non-finite credited amount": base("NaN"),
		"missing order amount": {
			ProductSnapshot: map[string]any{
				"kind":            paymentSnapshotKindBalance,
				"credited_amount": 100,
				"entitlements":    map[string]any{"balance_bonus": 5},
			},
		},
		"legacy non-balance snapshot": {
			Amount: 100,
			ProductSnapshot: map[string]any{
				"kind":            "subscription",
				"credited_amount": 100,
				"entitlements":    map[string]any{"balance_bonus": 5},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			manual, err := paymentOrderRequiresManualRefund(order)
			require.NoError(t, err)
			require.True(t, manual)
		})
	}
}

func TestPlanDiscountPercentAndPeriodLabel(t *testing.T) {
	original := 120.0
	require.Equal(t, 20.0, PlanDiscountPercent(96, &original))
	require.Zero(t, PlanDiscountPercent(120, &original))
	require.Equal(t, "quarter", PlanPeriodLabel(1, "quarter"))
	require.Equal(t, "year", PlanPeriodLabel(1, "year"))
	require.Equal(t, "quarter", PlanPeriodLabel(90, "days"))
	require.Equal(t, 90, psComputeValidityDays(1, "quarter"))
	require.Equal(t, 365, psComputeValidityDays(1, "year"))
}

func TestNormalizeRechargeOptionsFiltersAndSorts(t *testing.T) {
	options, intact := normalizeRechargeOptions(`[
		{"amount": 100, "original_price": 120, "label": "  Popular ", "balance_bonus": 8, "estimated_rate_multiplier": 0.9, "estimated_tokens": 12000000, "concurrency": 5, "recommended": true, "sort_order": 20, "enabled": true},
		{"amount": 10, "sort_order": 10, "enabled": true},
		{"amount": 50, "sort_order": 30, "enabled": false},
		{"amount": 0, "enabled": true},
		{"amount": 200, "balance_bonus": -1, "enabled": true}
	]`)
	require.False(t, intact, "two entries were dropped as invalid")
	require.Len(t, options, 3)
	require.Equal(t, 10.0, options[0].Amount)
	require.Equal(t, "Popular", options[1].Label)
	require.Equal(t, 120.0, options[1].OriginalPrice)
	require.Equal(t, 8.0, options[1].BalanceBonus)
	require.Equal(t, 0.9, options[1].EstimatedRateMultiplier)
	require.Equal(t, int64(12000000), options[1].EstimatedTokens)
	require.Equal(t, 5, options[1].Concurrency)
	require.True(t, options[1].Recommended)
	require.Equal(t, 16.67, rechargeOptionDiscountPercent(options[1]))
	require.Len(t, EnabledRechargeOptionsForCheckout(options), 2)

	legacy, legacyIntact := normalizeRechargeOptions(`[{"amount": 25}]`)
	require.True(t, legacyIntact)
	require.Len(t, legacy, 1)
}

// A corrupted setting must be reported, not silently normalized to "no tiers
// configured" — that would drop the fixed-tier requirement in order validation
// and reopen arbitrary top-up amounts.
func TestNormalizeRechargeOptionsReportsUnparseableSetting(t *testing.T) {
	options, intact := normalizeRechargeOptions(`{"amount": 25}`)
	require.False(t, intact)
	require.Empty(t, options)

	empty, emptyIntact := normalizeRechargeOptions("   ")
	require.True(t, emptyIntact, "an unset value is a valid custom-amount configuration")
	require.Empty(t, empty)
}

func TestRechargeModeForConfig(t *testing.T) {
	require.Equal(t, RechargeModeCustom, RechargeModeForConfig(&PaymentConfig{}))
	require.Equal(t, RechargeModeFixed, RechargeModeForConfig(&PaymentConfig{
		RechargeOptions: []RechargeOption{{Amount: 100, Enabled: true}},
	}))
	// Tiers exist but are all disabled: the server accepts free amounts again.
	require.Equal(t, RechargeModeCustom, RechargeModeForConfig(&PaymentConfig{
		RechargeOptions: []RechargeOption{{Amount: 100, Enabled: false}},
	}))
	// Corrupted config fails closed, so clients must not offer free amounts.
	require.Equal(t, RechargeModeFixed, RechargeModeForConfig(&PaymentConfig{RechargeOptionsInvalid: true}))
}

func TestCalculateRechargeCreditedAmountAddsConfiguredBonusAfterGlobalMultiplier(t *testing.T) {
	options := []RechargeOption{{Amount: 100, BalanceBonus: 8, Enabled: true}}
	require.Equal(t, 22.0, calculateRechargeCreditedAmount(100, 0.14, options))
	require.Equal(t, 14.0, calculateRechargeCreditedAmount(100, 0.14, nil))
	require.Equal(t, 14.0, calculateRechargeCreditedAmount(100, 0.14, []RechargeOption{{Amount: 100, BalanceBonus: 8, Enabled: false}}))
}

func TestEncodeRechargeOptionsRejectsInvalidBenefits(t *testing.T) {
	_, err := encodeRechargeOptions([]RechargeOption{{Amount: 100, BalanceBonus: -1, Enabled: true}})
	require.ErrorContains(t, err, "balance_bonus")

	_, err = encodeRechargeOptions([]RechargeOption{{Amount: 100, EstimatedTokens: -1, Enabled: true}})
	require.ErrorContains(t, err, "estimated_tokens")

	_, err = encodeRechargeOptions([]RechargeOption{
		{Amount: 100, Enabled: true},
		{Amount: 100.0000005, Enabled: true},
	})
	require.ErrorContains(t, err, "duplicated")
}

func TestValidateOrderInputRequiresConfiguredRechargeOption(t *testing.T) {
	service := &PaymentService{}
	cfg := &PaymentConfig{RechargeOptions: []RechargeOption{
		{Amount: 50, Enabled: true},
		{Amount: 100, Enabled: false},
	}}

	_, err := service.validateOrderInput(context.Background(), CreateOrderRequest{
		OrderType: "balance",
		Amount:    50,
	}, cfg)
	require.NoError(t, err)

	_, err = service.validateOrderInput(context.Background(), CreateOrderRequest{
		OrderType: "balance",
		Amount:    100,
	}, cfg)
	require.ErrorContains(t, err, "amount must match an enabled recharge option")

	_, err = service.validateOrderInput(context.Background(), CreateOrderRequest{
		OrderType: "balance",
		Amount:    75,
	}, &PaymentConfig{})
	require.NoError(t, err)
}

func TestBuildPaymentProductSnapshotCapturesEntitlements(t *testing.T) {
	original := 120.0
	plan := &dbent.SubscriptionPlan{
		ID:            9,
		Name:          "Quarterly Pro",
		ProductName:   "Pro quarter",
		Currency:      "CNY",
		Price:         96,
		OriginalPrice: &original,
		ValidityDays:  1,
		ValidityUnit:  "quarter",
		Entitlements: map[string]any{
			"balance_bonus":          8.0,
			"reset_card_count":       2,
			"reset_card_expiry_days": 30,
			"concurrency":            5,
		},
	}

	snapshot := buildPaymentProductSnapshot(plan, 96, 96, 90)
	require.Equal(t, "subscription", snapshot["kind"])
	require.Equal(t, 90, snapshot["subscription_days"])
	require.Equal(t, 20.0, snapshot["discount_percent"])
	require.Equal(t, plan.Entitlements, snapshot["entitlements"])
}

func TestBuildPaymentBalanceProductSnapshotUsesServerPresetOnly(t *testing.T) {
	options := []RechargeOption{{
		Amount: 50, OriginalPrice: 60, Label: "Popular", Description: "Best value",
		BalanceBonus: 5, EstimatedRateMultiplier: 0.9, EstimatedTokens: 8_000_000,
		Concurrency: 5, Enabled: true,
	}}
	snapshot := buildPaymentBalanceProductSnapshot(50, 55, 51.5, options)
	require.Equal(t, "balance", snapshot["kind"])
	require.Equal(t, "Popular", snapshot["label"])
	require.Equal(t, 16.67, snapshot["discount_percent"])
	require.Equal(t, 55.0, snapshot["credited_amount"])
	require.Equal(t, 0.9, snapshot["estimated_rate_multiplier"])
	require.Equal(t, int64(8_000_000), snapshot["estimated_tokens"])
	entitlements, ok := snapshot["entitlements"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 5.0, entitlements["balance_bonus"])
	require.Equal(t, 5, entitlements["concurrency"])

	manual := buildPaymentBalanceProductSnapshot(50.0001, 50.0001, 51.5, options)
	manualEntitlements, ok := manual["entitlements"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 0, manualEntitlements["concurrency"])

	disabled := buildPaymentBalanceProductSnapshot(50, 50, 51.5, []RechargeOption{{Amount: 50, Concurrency: 20, Enabled: false}})
	disabledEntitlements, ok := disabled["entitlements"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 0, disabledEntitlements["concurrency"])
}

func TestBalanceConcurrencyEntitlementIsMonotonicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().
		SetEmail("payment-concurrency@example.com").
		SetPasswordHash("hash").
		SetConcurrency(3).
		Save(ctx)
	require.NoError(t, err)
	order := &dbent.PaymentOrder{
		ID:     991,
		UserID: user.ID,
		ProductSnapshot: map[string]any{
			"entitlements": map[string]any{"concurrency": 5},
		},
	}

	require.NoError(t, grantPaymentOrderConcurrency(ctx, client, order))
	user, err = client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 5, user.Concurrency)

	// Repeating the same order is a no-op after its audit claim.
	require.NoError(t, grantPaymentOrderConcurrency(ctx, client, order))
	user, err = client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 5, user.Concurrency)

	// A delayed, lower-tier callback must not downgrade a user that later
	// received a higher administrative or paid concurrency value.
	_, err = client.User.UpdateOneID(user.ID).SetConcurrency(10).Save(ctx)
	require.NoError(t, err)
	lateLowerOrder := *order
	lateLowerOrder.ID = 992
	require.NoError(t, grantPaymentOrderConcurrency(ctx, client, &lateLowerOrder))
	user, err = client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 10, user.Concurrency)
}

func TestSubscriptionConcurrencyEntitlementIsMonotonicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().
		SetEmail("subscription-concurrency@example.com").
		SetPasswordHash("hash").
		SetConcurrency(3).
		Save(ctx)
	require.NoError(t, err)
	order := &dbent.PaymentOrder{
		ID:     993,
		UserID: user.ID,
		ProductSnapshot: map[string]any{
			"entitlements": map[string]any{"concurrency": 5},
		},
	}

	require.NoError(t, grantPaymentProductEntitlements(ctx, client, order, 1))
	require.NoError(t, grantPaymentProductEntitlements(ctx, client, order, 1))
	user, err = client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 5, user.Concurrency)
}

// Reset card expiry is a count plus a unit, mirroring the plan's own
// validity_days/validity_unit pair. Plans stored before units existed carry no
// unit and must keep meaning days.
func TestResetCardValidityDaysResolvesUnits(t *testing.T) {
	for _, tc := range []struct {
		name string
		unit string
		want int
	}{
		{name: "legacy plans have no unit", unit: "", want: 14},
		{name: "singular day", unit: "day", want: 14},
		{name: "plural days", unit: "days", want: 14},
		{name: "singular week", unit: "week", want: 98},
		{name: "plural weeks", unit: "weeks", want: 98},
		{name: "singular month", unit: "month", want: 420},
		{name: "plural months", unit: "months", want: 420},
		{name: "unknown units bill as days", unit: "fortnight", want: 14},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entitlements := PlanEntitlements{ResetCardCount: 1, ResetCardExpiryDays: 14, ResetCardExpiryUnit: tc.unit}
			require.Equal(t, tc.want, entitlements.ResetCardValidityDays())
		})
	}
}

func TestNormalizePlanEntitlementsBoundsResolvedResetCardValidity(t *testing.T) {
	_, entitlements, err := normalizePlanEntitlements(map[string]any{
		"reset_card_count":       2,
		"reset_card_expiry_days": 3,
		"reset_card_expiry_unit": "months",
	})
	require.NoError(t, err)
	require.Equal(t, resetCardExpiryUnitMonth, entitlements.ResetCardExpiryUnit)
	require.Equal(t, 90, entitlements.ResetCardValidityDays())

	// The cap applies to the resolved duration, so a large count in a large
	// unit is rejected just like the same duration expressed in days.
	_, _, err = normalizePlanEntitlements(map[string]any{
		"reset_card_count":       1,
		"reset_card_expiry_days": 200,
		"reset_card_expiry_unit": "months",
	})
	require.ErrorContains(t, err, "reset card validity must not exceed")

	// Without reset cards the period is meaningless and is cleared.
	_, cleared, err := normalizePlanEntitlements(map[string]any{
		"reset_card_count":       0,
		"reset_card_expiry_days": 30,
		"reset_card_expiry_unit": "weeks",
	})
	require.NoError(t, err)
	require.Zero(t, cleared.ResetCardExpiryDays)
	require.Equal(t, resetCardExpiryUnitDay, cleared.ResetCardExpiryUnit)
}
