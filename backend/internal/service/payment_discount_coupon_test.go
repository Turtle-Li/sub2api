package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// installPaymentDiscountTestSchema installs a SQLite-compatible projection of
// migration 253. Production uses the PostgreSQL migration directly; this keeps
// the existing in-memory service fixtures useful for lifecycle coverage.
func installPaymentDiscountTestSchema(t *testing.T, client *dbent.Client) {
	t.Helper()
	_, err := client.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS payment_discount_codes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code TEXT NOT NULL UNIQUE,
			discount_type TEXT NOT NULL,
			discount_value NUMERIC NOT NULL,
			currency TEXT NOT NULL,
			max_uses INTEGER NOT NULL DEFAULT 0,
			per_user_max_uses INTEGER NOT NULL DEFAULT 1,
			target_user_id INTEGER NULL,
			starts_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			version INTEGER NOT NULL DEFAULT 1,
			notes TEXT NOT NULL DEFAULT '',
			order_types TEXT NOT NULL DEFAULT '["balance","subscription"]',
			plan_ids TEXT NOT NULL DEFAULT '[]',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			CHECK (discount_type IN ('fixed', 'percent')),
			CHECK (currency IN ('CNY', 'USD')),
			CHECK (max_uses >= 0 AND per_user_max_uses >= 0),
			CHECK (version > 0)
		);
		CREATE TABLE IF NOT EXISTS payment_discount_code_audits (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code_id INTEGER NOT NULL,
			admin_user_id INTEGER NOT NULL,
			action TEXT NOT NULL,
			detail TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (code_id) REFERENCES payment_discount_codes(id) ON DELETE RESTRICT,
			FOREIGN KEY (admin_user_id) REFERENCES users(id) ON DELETE RESTRICT,
			CHECK (action IN ('created', 'updated'))
		);
		CREATE TABLE IF NOT EXISTS payment_discount_uses (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code_id INTEGER NOT NULL,
			order_id INTEGER NOT NULL UNIQUE,
			user_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'reserved',
			original_amount NUMERIC NOT NULL,
			discount_amount NUMERIC NOT NULL,
			pay_amount NUMERIC NOT NULL,
			currency TEXT NOT NULL,
			quote_version INTEGER NOT NULL,
			quote_revision TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			idempotency_hash TEXT NOT NULL,
			quote_snapshot TEXT NOT NULL,
			response_snapshot TEXT NULL,
			reserved_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			consumed_at DATETIME NULL,
			released_at DATETIME NULL,
			paid_review_at DATETIME NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (code_id) REFERENCES payment_discount_codes(id) ON DELETE RESTRICT,
			FOREIGN KEY (order_id) REFERENCES payment_orders(id) ON DELETE RESTRICT,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
			UNIQUE (user_id, idempotency_hash),
			CHECK (status IN ('reserved', 'consumed', 'released', 'paid_review')),
			CHECK (currency IN ('CNY', 'USD'))
		);
		CREATE INDEX IF NOT EXISTS idx_payment_discount_uses_capacity
			ON payment_discount_uses (code_id, status);
		CREATE INDEX IF NOT EXISTS idx_payment_discount_uses_user_capacity
			ON payment_discount_uses (code_id, user_id, status);
		CREATE TABLE IF NOT EXISTS payment_discount_rate_limits (
			user_id INTEGER PRIMARY KEY,
			window_started_at DATETIME NOT NULL,
			attempt_count INTEGER NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
			CHECK (attempt_count BETWEEN 0 AND 20)
		);
	`)
	require.NoError(t, err)
}

func TestPaymentDiscountInputValidationAndCodeNormalization(t *testing.T) {
	for raw, want := range map[string]string{
		"save_2026": "SAVE_2026",
		"SAVE-2026": "SAVE-2026",
		"A1_B2-C3":  "A1_B2-C3",
	} {
		got, err := normalizePaymentDiscountCode(raw)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	for _, raw := range []string{"short", "SAVE 2026", "SAVEx2026!", "SAVE2026\u4e2d"} {
		_, err := normalizePaymentDiscountCode(raw)
		require.Error(t, err, raw)
	}
	random, err := generatePaymentDiscountCode()
	require.NoError(t, err)
	require.Len(t, random, 32)
	_, err = normalizePaymentDiscountCode(random)
	require.NoError(t, err)

	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	input := PaymentDiscountCodeInput{
		Code:          "save_2026",
		DiscountType:  "percent",
		DiscountValue: "12.34",
		Currency:      "usd",
		MaxUses:       0,
		StartsAt:      now.Add(-time.Minute),
		ExpiresAt:     now.Add(time.Hour),
		Enabled:       true,
	}
	normalized, err := normalizePaymentDiscountInput(input, false, now)
	require.NoError(t, err)
	require.Equal(t, "SAVE_2026", normalized.input.Code)
	require.Equal(t, "USD", normalized.input.Currency)
	require.Equal(t, "12.34", normalized.input.DiscountValue)
	require.NotNil(t, normalized.input.PerUserMaxUses)
	require.Equal(t, 1, *normalized.input.PerUserMaxUses)
	require.Equal(t, []string{payment.OrderTypeBalance, payment.OrderTypeSubscription}, normalized.input.OrderTypes)
	require.Empty(t, normalized.input.PlanIDs)

	for _, mutate := range []func(*PaymentDiscountCodeInput){
		func(v *PaymentDiscountCodeInput) { v.DiscountValue = "100.01" },
		func(v *PaymentDiscountCodeInput) { v.DiscountValue = "1.001" },
		func(v *PaymentDiscountCodeInput) { v.Currency = "EUR" },
		func(v *PaymentDiscountCodeInput) { v.ExpiresAt = now },
		func(v *PaymentDiscountCodeInput) { v.MaxUses = -1 },
		func(v *PaymentDiscountCodeInput) { v.OrderTypes = []string{} },
		func(v *PaymentDiscountCodeInput) { v.OrderTypes = []string{payment.OrderTypeResetCard} },
		func(v *PaymentDiscountCodeInput) {
			v.OrderTypes = []string{payment.OrderTypeBalance}
			v.PlanIDs = []int64{1}
		},
		func(v *PaymentDiscountCodeInput) { v.PlanIDs = []int64{0} },
	} {
		invalid := input
		mutate(&invalid)
		_, err := normalizePaymentDiscountInput(invalid, false, now)
		require.Error(t, err)
	}
}

func TestPaymentDiscountQuoteMathAndRateLimit(t *testing.T) {
	ctx := context.Background()
	client := newUnifiedRefundSQLiteClient(t)
	installPaymentDiscountTestSchema(t, client)
	user := createPaymentDiscountCouponTestUser(t, client, "quote")
	svc := &PaymentService{entClient: client}

	fixed, err := svc.SavePaymentDiscountCode(ctx, user.ID, 0, paymentDiscountCouponTestInput("FIXED2026", "fixed", "200.00", "CNY", 0, nil), 0)
	require.NoError(t, err)
	fixedQuote, err := quotePaymentDiscount(ctx, client, user.ID, fixed.Code, decimal.NewFromInt(100), "CNY", payment.OrderTypeBalance, 0, "fixed-bound", false)
	require.NoError(t, err)
	require.Equal(t, "100.00", fixedQuote.OriginalAmount)
	require.Equal(t, "99.00", fixedQuote.DiscountAmount)
	require.Equal(t, "1.00", fixedQuote.PayAmount)

	percent, err := svc.SavePaymentDiscountCode(ctx, user.ID, 0, paymentDiscountCouponTestInput("PERCENT26", "percent", "12.34", "USD", 0, nil), 0)
	require.NoError(t, err)
	percentQuote, err := quotePaymentDiscount(ctx, client, user.ID, percent.Code, decimal.NewFromInt(100), "USD", payment.OrderTypeBalance, 0, "percent-bound", false)
	require.NoError(t, err)
	require.Equal(t, "12.00", percentQuote.DiscountAmount)
	require.Equal(t, "88.00", percentQuote.PayAmount)

	ceilPercent, err := svc.SavePaymentDiscountCode(ctx, user.ID, 0, paymentDiscountCouponTestInput("CEIL2026", "percent", "20.00", "CNY", 0, nil), 0)
	require.NoError(t, err)
	ceilQuote, err := quotePaymentDiscount(ctx, client, user.ID, ceilPercent.Code, decimal.RequireFromString("99.00"), "CNY", payment.OrderTypeBalance, 0, "ceil-bound", false)
	require.NoError(t, err)
	require.Equal(t, "19.00", ceilQuote.DiscountAmount)
	require.Equal(t, "80.00", ceilQuote.PayAmount, "99 x 80%% settles at the next whole yuan")

	exactQuote, err := quotePaymentDiscount(ctx, client, user.ID, ceilPercent.Code, decimal.RequireFromString("100.00"), "CNY", payment.OrderTypeBalance, 0, "exact-bound", false)
	require.NoError(t, err)
	require.Equal(t, "80.00", exactQuote.PayAmount)

	noEffect, err := svc.SavePaymentDiscountCode(ctx, user.ID, 0, paymentDiscountCouponTestInput("NOEFFECT26", "fixed", "0.01", "CNY", 0, nil), 0)
	require.NoError(t, err)
	_, err = quotePaymentDiscount(ctx, client, user.ID, noEffect.Code, decimal.RequireFromString("99.00"), "CNY", payment.OrderTypeBalance, 0, "no-effect", false)
	require.ErrorIs(t, err, ErrPaymentDiscountInvalid)

	for i := 0; i < paymentDiscountRateLimit; i++ {
		require.NoError(t, checkPaymentDiscountRate(ctx, client, user.ID))
	}
	require.ErrorIs(t, checkPaymentDiscountRate(ctx, client, user.ID), ErrPaymentDiscountRateLimited)
}

func TestPaymentDiscountReservationReplayResponseAndActiveCapReduction(t *testing.T) {
	ctx := context.Background()
	client := newUnifiedRefundSQLiteClient(t)
	installPaymentDiscountTestSchema(t, client)
	user := createPaymentDiscountCouponTestUser(t, client, "ledger")
	svc := &PaymentService{entClient: client}
	noPerUserCap := 0
	coupon, err := svc.SavePaymentDiscountCode(ctx, user.ID, 0, paymentDiscountCouponTestInput("ACTIVE2026", "fixed", "20.00", "CNY", 2, &noPerUserCap), 0)
	require.NoError(t, err)

	first := createPaymentDiscountCouponTestOrder(t, client, user, "first")
	firstQuote := reservePaymentDiscountCouponTestUse(t, client, user.ID, first.ID, coupon.Code, "a", "b")
	replay, found, err := findPaymentDiscountReplay(ctx, client, user.ID, strings.Repeat("b", 64), strings.Repeat("a", 64))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, first.ID, replay.ID)
	require.ErrorIs(t, reservePaymentDiscount(ctx, client, first.ID, user.ID, firstQuote, strings.Repeat("c", 64), strings.Repeat("b", 64)), ErrIdempotencyKeyConflict)

	response := &CreateOrderResponse{
		OrderID:     first.ID,
		Amount:      100,
		PayAmount:   80,
		Status:      OrderStatusPending,
		PaymentType: payment.TypeAlipay,
		PaymentDiscount: map[string]any{
			"code_id":         float64(firstQuote.CodeID),
			"code":            firstQuote.Code,
			"original_amount": firstQuote.OriginalAmount,
			"discount_amount": firstQuote.DiscountAmount,
			"pay_amount":      firstQuote.PayAmount,
			"currency":        firstQuote.Currency,
		},
	}
	require.NoError(t, savePaymentDiscountResponse(ctx, client, first.ID, response))
	storedResponse, err := loadPaymentDiscountResponse(ctx, client, first.ID)
	require.NoError(t, err)
	require.Equal(t, response, storedResponse)

	second := createPaymentDiscountCouponTestOrder(t, client, user, "second")
	_ = reservePaymentDiscountCouponTestUse(t, client, user.ID, second.ID, coupon.Code, "d", "e")
	maxOne := 1
	_, err = svc.SavePaymentDiscountCode(ctx, user.ID, coupon.ID, paymentDiscountCouponTestInput("", "fixed", "20.00", "CNY", 1, &noPerUserCap), coupon.Version)
	require.Error(t, err, "two active uses cannot be hidden by counting distinct users")
	_, err = svc.SavePaymentDiscountCode(ctx, user.ID, coupon.ID, paymentDiscountCouponTestInput("", "fixed", "20.00", "CNY", 2, &maxOne), coupon.Version)
	require.Error(t, err, "a per-user reduction cannot fall below active uses")

	codes, total, err := svc.ListPaymentDiscountCodes(ctx, 1, 20, "active")
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, codes, 1)
	require.Equal(t, 2, codes[0].ReservedUses)
	uses, total, err := svc.ListPaymentDiscountUses(ctx, coupon.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, uses, 2)
	audits, total, err := svc.ListPaymentDiscountAudits(ctx, coupon.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, audits, 1)
}

func TestPaymentDiscountUsageListProjectsUsernameForSoftDeletedUser(t *testing.T) {
	ctx := context.Background()
	client := newUnifiedRefundSQLiteClient(t)
	installPaymentDiscountTestSchema(t, client)
	user := createPaymentDiscountCouponTestUser(t, client, "usage-projection")
	svc := &PaymentService{entClient: client}
	coupon, err := svc.SavePaymentDiscountCode(ctx, user.ID, 0, paymentDiscountCouponTestInput("USAGEPROJ26", "fixed", "20.00", "CNY", 0, nil), 0)
	require.NoError(t, err)
	order := createPaymentDiscountCouponTestOrder(t, client, user, "usage-projection")
	reservePaymentDiscountCouponTestUse(t, client, user.ID, order.ID, coupon.Code, "a", "b")

	_, err = client.User.UpdateOneID(user.ID).SetDeletedAt(time.Now().UTC()).Save(ctx)
	require.NoError(t, err)

	uses, total, err := svc.ListPaymentDiscountUses(ctx, coupon.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, uses, 1)
	require.Equal(t, user.Username, uses[0].Username)

	encoded, err := json.Marshal(uses[0])
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(encoded, &payload))
	require.Equal(t, user.Username, payload["username"])
	require.NotContains(t, payload, "user_email")
}

func TestPaymentDiscountScopeValidationAndReservationAuthority(t *testing.T) {
	ctx := context.Background()
	client := newUnifiedRefundSQLiteClient(t)
	installPaymentDiscountTestSchema(t, client)
	admin := createPaymentDiscountCouponTestUser(t, client, "scope-admin")
	user := createPaymentDiscountCouponTestUser(t, client, "scope-user")
	plan := createPaymentDiscountCouponTestPlan(t, client, "scope")
	svc := &PaymentService{entClient: client}

	balanceInput := paymentDiscountCouponTestInput("BALANCE26", "fixed", "20.00", "CNY", 0, nil)
	balanceInput.OrderTypes = []string{"balance", "balance"}
	balance, err := svc.SavePaymentDiscountCode(ctx, admin.ID, 0, balanceInput, 0)
	require.NoError(t, err)
	require.Equal(t, []string{payment.OrderTypeBalance}, balance.OrderTypes)
	require.Empty(t, balance.PlanIDs)

	_, err = quotePaymentDiscount(ctx, client, user.ID, balance.Code, decimal.NewFromInt(100), "CNY", payment.OrderTypeBalance, 0, "balance-scope", false)
	require.NoError(t, err)
	_, err = quotePaymentDiscount(ctx, client, user.ID, balance.Code, decimal.NewFromInt(100), "CNY", payment.OrderTypeSubscription, plan.ID, "subscription-scope", false)
	require.ErrorIs(t, err, ErrPaymentDiscountInvalid)
	_, err = quotePaymentDiscount(ctx, client, user.ID, balance.Code, decimal.NewFromInt(100), "CNY", payment.OrderTypeResetCard, plan.ID, "reset-scope", false)
	require.ErrorIs(t, err, ErrPaymentDiscountInvalid)

	subscriptionInput := paymentDiscountCouponTestInput("SUBSCOPE26", "percent", "20.00", "CNY", 0, nil)
	subscriptionInput.OrderTypes = []string{"subscription", "subscription"}
	subscriptionInput.PlanIDs = []int64{plan.ID, plan.ID}
	subscription, err := svc.SavePaymentDiscountCode(ctx, admin.ID, 0, subscriptionInput, 0)
	require.NoError(t, err)
	require.Equal(t, []string{payment.OrderTypeSubscription}, subscription.OrderTypes)
	require.Equal(t, []int64{plan.ID}, subscription.PlanIDs)

	quote, err := quotePaymentDiscount(ctx, client, user.ID, subscription.Code, decimal.NewFromInt(100), "CNY", payment.OrderTypeSubscription, plan.ID, "subscription-plan", false)
	require.NoError(t, err)
	require.Equal(t, "80.00", quote.PayAmount)
	_, err = quotePaymentDiscount(ctx, client, user.ID, subscription.Code, decimal.NewFromInt(100), "CNY", payment.OrderTypeSubscription, plan.ID+1, "wrong-plan", false)
	require.ErrorIs(t, err, ErrPaymentDiscountInvalid)
	_, err = quotePaymentDiscount(ctx, client, user.ID, subscription.Code, decimal.NewFromInt(100), "CNY", payment.OrderTypeBalance, 0, "wrong-type", false)
	require.ErrorIs(t, err, ErrPaymentDiscountInvalid)

	updatedInput := paymentDiscountCouponTestInput("", "percent", "20.00", "CNY", 0, nil)
	updated, err := svc.SavePaymentDiscountCode(ctx, admin.ID, subscription.ID, updatedInput, subscription.Version)
	require.NoError(t, err)
	require.Equal(t, subscription.OrderTypes, updated.OrderTypes, "omitted update scope must not widen a restricted coupon")
	require.Equal(t, subscription.PlanIDs, updated.PlanIDs)
	emptyTypes := updatedInput
	emptyTypes.OrderTypes = []string{}
	_, err = svc.SavePaymentDiscountCode(ctx, admin.ID, updated.ID, emptyTypes, updated.Version)
	require.Error(t, err)
	badScope := paymentDiscountCouponTestInput("BADPLAN26", "fixed", "20.00", "CNY", 0, nil)
	badScope.OrderTypes = []string{payment.OrderTypeSubscription}
	badScope.PlanIDs = []int64{plan.ID + 99999}
	_, err = svc.SavePaymentDiscountCode(ctx, admin.ID, 0, badScope, 0)
	require.Error(t, err)
	badScope = paymentDiscountCouponTestInput("BADTYPE26", "fixed", "20.00", "CNY", 0, nil)
	badScope.OrderTypes = []string{payment.OrderTypeBalance}
	badScope.PlanIDs = []int64{plan.ID}
	_, err = svc.SavePaymentDiscountCode(ctx, admin.ID, 0, badScope, 0)
	require.Error(t, err)

	audits, total, err := svc.ListPaymentDiscountAudits(ctx, subscription.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 2, total)
	var detail struct {
		After struct {
			OrderTypes []string `json:"order_types"`
			PlanIDs    []int64  `json:"plan_ids"`
		} `json:"after"`
	}
	require.NoError(t, json.Unmarshal(audits[1].Detail, &detail))
	require.Equal(t, []string{payment.OrderTypeSubscription}, detail.After.OrderTypes)
	require.Equal(t, []int64{plan.ID}, detail.After.PlanIDs)

	// A caller may hold a valid balance quote, but reservation derives scope
	// from the durable order row and therefore cannot smuggle it into a plan.
	subscriptionOrder := createPaymentDiscountCouponTestSubscriptionOrder(t, client, user, plan, "authority")
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	txCtx := dbent.NewTxContext(ctx, tx)
	balanceQuote, err := quotePaymentDiscount(txCtx, tx.Client(), user.ID, balance.Code, decimal.NewFromInt(100), "CNY", payment.OrderTypeBalance, 0, "authority-balance", true)
	require.NoError(t, err)
	err = reservePaymentDiscount(txCtx, tx.Client(), subscriptionOrder.ID, user.ID, balanceQuote, strings.Repeat("1", 64), strings.Repeat("2", 64))
	require.ErrorIs(t, err, ErrPaymentDiscountInvalid)
	require.NoError(t, tx.Rollback())

	// Existing replay is immutable. A later scope edit only controls new sales.
	balanceOrder := createPaymentDiscountCouponTestOrder(t, client, user, "scope-replay")
	firstQuote := reservePaymentDiscountCouponTestUse(t, client, user.ID, balanceOrder.ID, balance.Code, "3", "4")
	moveToSubscription := paymentDiscountCouponTestInput("", "fixed", "20.00", "CNY", 0, nil)
	moveToSubscription.OrderTypes = []string{payment.OrderTypeSubscription}
	balanceUpdated, err := svc.SavePaymentDiscountCode(ctx, admin.ID, balance.ID, moveToSubscription, balance.Version)
	require.NoError(t, err)
	require.Equal(t, []string{payment.OrderTypeSubscription}, balanceUpdated.OrderTypes)
	_, err = svc.SavePaymentDiscountCode(ctx, admin.ID, balance.ID, moveToSubscription, balance.Version)
	require.ErrorIs(t, err, ErrPaymentDiscountVersionConflict, "scope edits remain covered by the configuration CAS")
	require.NoError(t, reservePaymentDiscount(ctx, client, balanceOrder.ID, user.ID, firstQuote, strings.Repeat("3", 64), strings.Repeat("4", 64)))
	_, err = quotePaymentDiscount(ctx, client, user.ID, balance.Code, decimal.NewFromInt(100), "CNY", payment.OrderTypeBalance, 0, "after-scope-edit", false)
	require.ErrorIs(t, err, ErrPaymentDiscountInvalid)
}

func paymentDiscountCouponTestInput(code, discountType, discountValue, currency string, maxUses int, perUser *int) PaymentDiscountCodeInput {
	return PaymentDiscountCodeInput{
		Code:           code,
		DiscountType:   discountType,
		DiscountValue:  discountValue,
		Currency:       currency,
		MaxUses:        maxUses,
		PerUserMaxUses: perUser,
		StartsAt:       time.Now().UTC().Add(-time.Minute),
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
		Enabled:        true,
	}
}

func createPaymentDiscountCouponTestUser(t *testing.T, client *dbent.Client, prefix string) *dbent.User {
	t.Helper()
	identifier := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	user, err := client.User.Create().
		SetEmail(identifier + "@example.test").
		SetPasswordHash("test").
		SetUsername(identifier).
		Save(context.Background())
	require.NoError(t, err)
	return user
}

func createPaymentDiscountCouponTestOrder(t *testing.T, client *dbent.Client, user *dbent.User, suffix string) *dbent.PaymentOrder {
	t.Helper()
	identifier := fmt.Sprintf("coupon-%s-%d", suffix, time.Now().UnixNano())
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(100).
		SetPayAmount(80).
		SetFeeRate(0).
		SetRechargeCode(identifier).
		SetOutTradeNo(identifier).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().UTC().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("coupon.test").
		Save(context.Background())
	require.NoError(t, err)
	return order
}

func createPaymentDiscountCouponTestPlan(t *testing.T, client *dbent.Client, suffix string) *dbent.SubscriptionPlan {
	t.Helper()
	plan, err := client.SubscriptionPlan.Create().
		SetGroupID(1).
		SetName("coupon plan " + suffix).
		SetPrice(100).
		SetCurrency("CNY").
		Save(context.Background())
	require.NoError(t, err)
	return plan
}

func createPaymentDiscountCouponTestSubscriptionOrder(t *testing.T, client *dbent.Client, user *dbent.User, plan *dbent.SubscriptionPlan, suffix string) *dbent.PaymentOrder {
	t.Helper()
	identifier := fmt.Sprintf("coupon-sub-%s-%d", suffix, time.Now().UnixNano())
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(100).
		SetPayAmount(80).
		SetFeeRate(0).
		SetRechargeCode(identifier).
		SetOutTradeNo(identifier).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeSubscription).
		SetPlanID(plan.ID).
		SetStatus(OrderStatusPending).
		SetExpiresAt(time.Now().UTC().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("coupon.test").
		Save(context.Background())
	require.NoError(t, err)
	return order
}

func reservePaymentDiscountCouponTestUse(t *testing.T, client *dbent.Client, userID, orderID int64, code, requestCharacter, idempotencyCharacter string) *PaymentDiscountQuote {
	t.Helper()
	ctx := context.Background()
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	txCtx := dbent.NewTxContext(ctx, tx)
	quote, err := quotePaymentDiscount(txCtx, tx.Client(), userID, code, decimal.NewFromInt(100), "CNY", payment.OrderTypeBalance, 0, "coupon-test-binding", true)
	require.NoError(t, err)
	err = reservePaymentDiscount(txCtx, tx.Client(), orderID, userID, quote, strings.Repeat(requestCharacter, 64), strings.Repeat(idempotencyCharacter, 64))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return quote
}
