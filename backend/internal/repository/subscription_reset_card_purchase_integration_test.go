//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type resetCardPurchaseFixture struct {
	client       *dbent.Client
	service      *service.SubscriptionService
	user         *service.User
	group        *service.Group
	subscription *service.UserSubscription
}

func newResetCardPurchaseFixture(t *testing.T, balance float64) *resetCardPurchaseFixture {
	t.Helper()
	ctx := context.Background()
	client := testEntClient(t)
	stamp := time.Now().UnixNano()
	user := mustCreateUser(t, client, &service.User{
		Email:   fmt.Sprintf("reset-purchase-%d@example.com", stamp),
		Balance: balance,
	})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             fmt.Sprintf("reset-purchase-group-%d", stamp),
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	subscription := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID:    user.ID,
		GroupID:   group.ID,
		Status:    service.SubscriptionStatusActive,
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond),
	})

	setResetCardPurchasePaymentEnabled(t, true)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM subscription_reset_card_purchases WHERE user_id = $1", user.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM subscription_reset_grants WHERE user_id = $1", user.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM subscription_plans WHERE group_id = $1", group.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM user_subscriptions WHERE id = $1", subscription.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", group.ID)
	})

	return &resetCardPurchaseFixture{
		client:       client,
		service:      service.NewSubscriptionService(nil, nil, nil, client, nil),
		user:         user,
		group:        group,
		subscription: subscription,
	}
}

func setResetCardPurchasePaymentEnabled(t *testing.T, enabled bool) {
	t.Helper()
	ctx := context.Background()
	var previous sql.NullString
	err := integrationDB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = $1", service.SettingPaymentEnabled).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		require.NoError(t, err)
	}
	value := "false"
	if enabled {
		value = "true"
	}
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at
	`, service.SettingPaymentEnabled, value)
	require.NoError(t, err)
	t.Cleanup(func() {
		if previous.Valid {
			_, _ = integrationDB.ExecContext(ctx, `
				INSERT INTO settings (key, value, updated_at)
				VALUES ($1, $2, NOW())
				ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at
			`, service.SettingPaymentEnabled, previous.String)
			return
		}
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM settings WHERE key = $1", service.SettingPaymentEnabled)
	})
}

func createResetCardPurchasePlan(t *testing.T, f *resetCardPurchaseFixture, price float64, validityDays int, validityUnit string) *dbent.SubscriptionPlan {
	t.Helper()
	plan, err := f.client.SubscriptionPlan.Create().
		SetGroupID(f.group.ID).
		SetName(fmt.Sprintf("reset-purchase-plan-%d", time.Now().UnixNano())).
		SetPrice(price).
		SetCurrency("CNY").
		SetValidityDays(validityDays).
		SetValidityUnit(validityUnit).
		SetForSale(true).
		Save(context.Background())
	require.NoError(t, err)
	return plan
}

func purchaseResetCard(t *testing.T, f *resetCardPurchaseFixture, quote *service.SubscriptionResetCardQuote, key string) *service.PurchaseSubscriptionResetCardResult {
	t.Helper()
	result, err := f.service.PurchaseResetCard(context.Background(), service.PurchaseSubscriptionResetCardInput{
		UserID:         f.user.ID,
		SubscriptionID: f.subscription.ID,
		ExpectedPlanID: quote.PlanID,
		ExpectedPrice:  quote.Price,
		PurchaseKey:    key,
	})
	require.NoError(t, err)
	return result
}

func resetCardPurchaseCounts(t *testing.T, userID int64) (purchases, grants int) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM subscription_reset_card_purchases WHERE user_id = $1", userID).Scan(&purchases))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM subscription_reset_grants WHERE user_id = $1", userID).Scan(&grants))
	return purchases, grants
}

func resetCardPurchaseBalance(t *testing.T, userID int64) (balance, frozen float64) {
	t.Helper()
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), "SELECT balance, frozen_balance FROM users WHERE id = $1", userID).Scan(&balance, &frozen))
	return balance, frozen
}

func TestSubscriptionResetCardPurchaseQuoteAndDurableReplay(t *testing.T) {
	f := newResetCardPurchaseFixture(t, 100)
	plan := createResetCardPurchasePlan(t, f, 120, 1, "month")

	quote, err := f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
	require.NoError(t, err)
	require.Equal(t, f.subscription.ID, quote.SubscriptionID)
	require.Equal(t, f.group.ID, quote.GroupID)
	require.Equal(t, plan.ID, quote.PlanID)
	require.Equal(t, 120.0, quote.MonthlyPrice)
	require.Equal(t, 40.0, quote.Price)
	require.Equal(t, f.subscription.ExpiresAt, quote.ExpiresAt)

	key := uuid.NewString()
	first := purchaseResetCard(t, f, quote, key)
	require.Equal(t, f.subscription.ID, first.SubscriptionID)
	require.Equal(t, 40.0, first.Price)
	require.Equal(t, f.subscription.ExpiresAt, first.ExpiresAt)
	balance, _ := resetCardPurchaseBalance(t, f.user.ID)
	require.Equal(t, 60.0, balance)

	// A response can be lost after commit. The same durable body key must replay
	// without consulting the now-mutable plan or charging balance again.
	_, err = f.client.SubscriptionPlan.UpdateOneID(plan.ID).SetPrice(150).Save(context.Background())
	require.NoError(t, err)
	replayed := purchaseResetCard(t, f, quote, key)
	require.Equal(t, first, replayed)
	balance, frozen := resetCardPurchaseBalance(t, f.user.ID)
	require.Equal(t, 60.0, balance)
	require.Zero(t, frozen)
	purchases, grants := resetCardPurchaseCounts(t, f.user.ID)
	require.Equal(t, 1, purchases)
	require.Equal(t, 1, grants)

	_, err = f.service.PurchaseResetCard(context.Background(), service.PurchaseSubscriptionResetCardInput{
		UserID:         f.user.ID,
		SubscriptionID: f.subscription.ID + 99,
		ExpectedPlanID: quote.PlanID,
		ExpectedPrice:  quote.Price,
		PurchaseKey:    key,
	})
	require.ErrorIs(t, err, service.ErrResetCardPurchaseKeyConflict)
}

func TestSubscriptionResetCardQuoteMonthlyPriceSelectionAndRounding(t *testing.T) {
	tests := []struct {
		name  string
		price float64
		want  float64
	}{
		{name: "plus monthly", price: 120, want: 40},
		{name: "five x monthly", price: 550, want: 183.33},
		{name: "round cents", price: 10.01, want: 3.34},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newResetCardPurchaseFixture(t, 1000)
			createResetCardPurchasePlan(t, f, tc.price, 1, "months")
			quote, err := f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
			require.NoError(t, err)
			require.Equal(t, tc.price, quote.MonthlyPrice)
			require.Equal(t, tc.want, quote.Price)
		})
	}
}

func TestSubscriptionResetCardQuoteFailsClosedForMonthlyPlanSource(t *testing.T) {
	t.Run("no monthly plan", func(t *testing.T) {
		f := newResetCardPurchaseFixture(t, 100)
		createResetCardPurchasePlan(t, f, 120, 7, "day")
		_, err := f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
		require.ErrorIs(t, err, service.ErrResetCardPurchaseUnavailable)
	})
	t.Run("multiple monthly plans", func(t *testing.T) {
		f := newResetCardPurchaseFixture(t, 100)
		createResetCardPurchasePlan(t, f, 120, 1, "month")
		createResetCardPurchasePlan(t, f, 550, 30, "days")
		_, err := f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
		require.ErrorIs(t, err, service.ErrResetCardPurchaseUnavailable)
	})
	t.Run("zero price", func(t *testing.T) {
		f := newResetCardPurchaseFixture(t, 100)
		createResetCardPurchasePlan(t, f, 0, 1, "month")
		_, err := f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
		require.ErrorIs(t, err, service.ErrResetCardPriceInvalid)
	})
	t.Run("unlabeled or non CNY plan", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			currency string
		}{
			{name: "unlabeled", currency: ""},
			{name: "USD", currency: "USD"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newResetCardPurchaseFixture(t, 100)
				plan := createResetCardPurchasePlan(t, f, 120, 1, "month")
				_, err := f.client.SubscriptionPlan.UpdateOneID(plan.ID).SetCurrency(tc.currency).Save(context.Background())
				require.NoError(t, err)
				_, err = f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
				require.ErrorIs(t, err, service.ErrResetCardCurrencyUnsupported)
			})
		}
	})
}

func TestSubscriptionResetCardPurchaseChecksOwnershipAndEligibility(t *testing.T) {
	t.Run("other user cannot quote", func(t *testing.T) {
		f := newResetCardPurchaseFixture(t, 100)
		createResetCardPurchasePlan(t, f, 120, 1, "month")
		other := mustCreateUser(t, f.client, &service.User{Email: fmt.Sprintf("reset-purchase-other-%d@example.com", time.Now().UnixNano())})
		t.Cleanup(func() {
			_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", other.ID)
		})
		_, err := f.service.GetResetCardQuote(context.Background(), other.ID, f.subscription.ID)
		require.ErrorIs(t, err, service.ErrSubscriptionNotFound)
	})
	t.Run("expired subscription", func(t *testing.T) {
		f := newResetCardPurchaseFixture(t, 100)
		createResetCardPurchasePlan(t, f, 120, 1, "month")
		_, err := f.client.UserSubscription.UpdateOneID(f.subscription.ID).SetExpiresAt(time.Now().Add(-time.Second)).Save(context.Background())
		require.NoError(t, err)
		_, err = f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
		require.ErrorIs(t, err, service.ErrSubscriptionExpired)
	})
	t.Run("suspended subscription", func(t *testing.T) {
		f := newResetCardPurchaseFixture(t, 100)
		createResetCardPurchasePlan(t, f, 120, 1, "month")
		_, err := f.client.UserSubscription.UpdateOneID(f.subscription.ID).SetStatus(service.SubscriptionStatusSuspended).Save(context.Background())
		require.NoError(t, err)
		_, err = f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
		require.ErrorIs(t, err, service.ErrSubscriptionSuspended)
	})
	t.Run("inactive group", func(t *testing.T) {
		f := newResetCardPurchaseFixture(t, 100)
		createResetCardPurchasePlan(t, f, 120, 1, "month")
		_, err := f.client.Group.UpdateOneID(f.group.ID).SetStatus(service.StatusDisabled).Save(context.Background())
		require.NoError(t, err)
		_, err = f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
		require.ErrorIs(t, err, service.ErrResetCardGroupInactive)
	})
	t.Run("inactive user", func(t *testing.T) {
		f := newResetCardPurchaseFixture(t, 100)
		createResetCardPurchasePlan(t, f, 120, 1, "month")
		_, err := f.client.User.UpdateOneID(f.user.ID).SetStatus(service.StatusDisabled).Save(context.Background())
		require.NoError(t, err)
		_, err = f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
		require.ErrorIs(t, err, service.ErrResetCardUserInactive)
	})
}

func TestSubscriptionResetCardPurchaseFailsForDisabledPaymentOrInsufficientBalance(t *testing.T) {
	t.Run("payment disabled", func(t *testing.T) {
		f := newResetCardPurchaseFixture(t, 100)
		createResetCardPurchasePlan(t, f, 120, 1, "month")
		quote, err := f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
		require.NoError(t, err)
		setResetCardPurchasePaymentEnabled(t, false)
		_, err = f.service.PurchaseResetCard(context.Background(), service.PurchaseSubscriptionResetCardInput{
			UserID: f.user.ID, SubscriptionID: f.subscription.ID, ExpectedPlanID: quote.PlanID, ExpectedPrice: quote.Price, PurchaseKey: uuid.NewString(),
		})
		require.ErrorIs(t, err, service.ErrResetCardPaymentDisabled)
		purchases, grants := resetCardPurchaseCounts(t, f.user.ID)
		require.Zero(t, purchases)
		require.Zero(t, grants)
	})
	t.Run("insufficient available balance keeps frozen funds unchanged", func(t *testing.T) {
		f := newResetCardPurchaseFixture(t, 39)
		_, err := f.client.User.UpdateOneID(f.user.ID).SetFrozenBalance(8).Save(context.Background())
		require.NoError(t, err)
		createResetCardPurchasePlan(t, f, 120, 1, "month")
		quote, err := f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
		require.NoError(t, err)
		_, err = f.service.PurchaseResetCard(context.Background(), service.PurchaseSubscriptionResetCardInput{
			UserID: f.user.ID, SubscriptionID: f.subscription.ID, ExpectedPlanID: quote.PlanID, ExpectedPrice: quote.Price, PurchaseKey: uuid.NewString(),
		})
		require.ErrorIs(t, err, service.ErrResetCardInsufficientBalance)
		balance, frozen := resetCardPurchaseBalance(t, f.user.ID)
		require.Equal(t, 39.0, balance)
		require.Equal(t, 8.0, frozen)
	})
}

func TestSubscriptionResetCardPurchaseRejectsChangedQuoteAndRollsBack(t *testing.T) {
	t.Run("changed quote", func(t *testing.T) {
		f := newResetCardPurchaseFixture(t, 100)
		plan := createResetCardPurchasePlan(t, f, 120, 1, "month")
		quote, err := f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
		require.NoError(t, err)
		_, err = f.client.SubscriptionPlan.UpdateOneID(plan.ID).SetPrice(150).Save(context.Background())
		require.NoError(t, err)
		_, err = f.service.PurchaseResetCard(context.Background(), service.PurchaseSubscriptionResetCardInput{
			UserID: f.user.ID, SubscriptionID: f.subscription.ID, ExpectedPlanID: quote.PlanID, ExpectedPrice: quote.Price, PurchaseKey: uuid.NewString(),
		})
		require.ErrorIs(t, err, service.ErrResetCardQuoteChanged)
		balance, _ := resetCardPurchaseBalance(t, f.user.ID)
		require.Equal(t, 100.0, balance)
		purchases, grants := resetCardPurchaseCounts(t, f.user.ID)
		require.Zero(t, purchases)
		require.Zero(t, grants)
	})
	t.Run("grant failure rolls back debit", func(t *testing.T) {
		f := newResetCardPurchaseFixture(t, 100)
		createResetCardPurchasePlan(t, f, 120, 1, "month")
		quote, err := f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
		require.NoError(t, err)
		triggerName := fmt.Sprintf("reset_card_purchase_fail_%d", f.user.ID)
		functionName := fmt.Sprintf("reset_card_purchase_fail_fn_%d", f.user.ID)
		_, err = integrationDB.ExecContext(context.Background(), fmt.Sprintf(`
			CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN
				RAISE EXCEPTION 'test reset card purchase grant failure';
			END;
			$$;
			CREATE TRIGGER %s BEFORE INSERT ON subscription_reset_grants
			FOR EACH ROW WHEN (NEW.user_id = %d) EXECUTE FUNCTION %s();
		`, functionName, triggerName, f.user.ID, functionName))
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON subscription_reset_grants; DROP FUNCTION IF EXISTS %s();", triggerName, functionName))
		})
		_, err = f.service.PurchaseResetCard(context.Background(), service.PurchaseSubscriptionResetCardInput{
			UserID: f.user.ID, SubscriptionID: f.subscription.ID, ExpectedPlanID: quote.PlanID, ExpectedPrice: quote.Price, PurchaseKey: uuid.NewString(),
		})
		require.Error(t, err)
		balance, _ := resetCardPurchaseBalance(t, f.user.ID)
		require.Equal(t, 100.0, balance)
		purchases, grants := resetCardPurchaseCounts(t, f.user.ID)
		require.Zero(t, purchases)
		require.Zero(t, grants)
	})
}

func TestSubscriptionResetCardPurchaseConcurrentRequests(t *testing.T) {
	t.Run("same key replays one committed purchase", func(t *testing.T) {
		f := newResetCardPurchaseFixture(t, 80)
		createResetCardPurchasePlan(t, f, 120, 1, "month")
		quote, err := f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
		require.NoError(t, err)
		key := uuid.NewString()
		var wg sync.WaitGroup
		results := make(chan *service.PurchaseSubscriptionResetCardResult, 6)
		errs := make(chan error, 6)
		for range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				result, purchaseErr := f.service.PurchaseResetCard(context.Background(), service.PurchaseSubscriptionResetCardInput{
					UserID: f.user.ID, SubscriptionID: f.subscription.ID, ExpectedPlanID: quote.PlanID, ExpectedPrice: quote.Price, PurchaseKey: key,
				})
				results <- result
				errs <- purchaseErr
			}()
		}
		wg.Wait()
		close(results)
		close(errs)
		var firstID int64
		for result := range results {
			require.NotNil(t, result)
			if firstID == 0 {
				firstID = result.PurchaseID
			}
			require.Equal(t, firstID, result.PurchaseID)
		}
		for purchaseErr := range errs {
			require.NoError(t, purchaseErr)
		}
		balance, _ := resetCardPurchaseBalance(t, f.user.ID)
		require.Equal(t, 40.0, balance)
		purchases, grants := resetCardPurchaseCounts(t, f.user.ID)
		require.Equal(t, 1, purchases)
		require.Equal(t, 1, grants)
	})
	t.Run("distinct keys cannot overdraw", func(t *testing.T) {
		f := newResetCardPurchaseFixture(t, 40)
		createResetCardPurchasePlan(t, f, 120, 1, "month")
		quote, err := f.service.GetResetCardQuote(context.Background(), f.user.ID, f.subscription.ID)
		require.NoError(t, err)
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, purchaseErr := f.service.PurchaseResetCard(context.Background(), service.PurchaseSubscriptionResetCardInput{
					UserID: f.user.ID, SubscriptionID: f.subscription.ID, ExpectedPlanID: quote.PlanID, ExpectedPrice: quote.Price, PurchaseKey: uuid.NewString(),
				})
				errs <- purchaseErr
			}()
		}
		wg.Wait()
		close(errs)
		var success, insufficient int
		for purchaseErr := range errs {
			switch {
			case purchaseErr == nil:
				success++
			case errors.Is(purchaseErr, service.ErrResetCardInsufficientBalance):
				insufficient++
			default:
				t.Fatalf("unexpected concurrent purchase error: %v", purchaseErr)
			}
		}
		require.Equal(t, 1, success)
		require.Equal(t, 1, insufficient)
		balance, _ := resetCardPurchaseBalance(t, f.user.ID)
		require.Zero(t, balance)
		purchases, grants := resetCardPurchaseCounts(t, f.user.ID)
		require.Equal(t, 1, purchases)
		require.Equal(t, 1, grants)
	})
}

// Exercise the renewal -> user-benefit lock order used by payment fulfillment
// while a reset-card purchase targets the same subscription and wallet.
func TestSubscriptionResetCardPurchaseDoesNotInvertRenewalLocks(t *testing.T) {
	f := newResetCardPurchaseFixture(t, 100)
	createResetCardPurchasePlan(t, f, 120, 1, "month")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	quote, err := f.service.GetResetCardQuote(ctx, f.user.ID, f.subscription.ID)
	require.NoError(t, err)
	renewalTx, err := f.client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = renewalTx.Rollback() }()
	renewalCtx := dbent.NewTxContext(ctx, renewalTx)
	renewalService := service.NewSubscriptionService(NewGroupRepository(f.client, integrationDB), NewUserSubscriptionRepository(f.client), nil, f.client, nil)
	renewed, extended, err := renewalService.AssignOrExtendSubscription(renewalCtx, &service.AssignSubscriptionInput{UserID: f.user.ID, GroupID: f.group.ID, ValidityDays: 30})
	require.NoError(t, err)
	require.True(t, extended)
	purchaseTx, err := f.client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = purchaseTx.Rollback() }()
	rows, err := purchaseTx.Client().QueryContext(ctx, "SELECT pg_backend_pid()")
	require.NoError(t, err)
	require.True(t, rows.Next())
	var purchasePID int
	require.NoError(t, rows.Scan(&purchasePID))
	require.NoError(t, rows.Close())
	done := make(chan error, 1)
	go func() {
		result, purchaseErr := f.service.PurchaseResetCard(dbent.NewTxContext(ctx, purchaseTx), service.PurchaseSubscriptionResetCardInput{UserID: f.user.ID, SubscriptionID: f.subscription.ID, ExpectedPlanID: quote.PlanID, ExpectedPrice: quote.Price, PurchaseKey: uuid.NewString()})
		if purchaseErr == nil && !result.ExpiresAt.Equal(renewed.ExpiresAt) {
			purchaseErr = fmt.Errorf("grant did not use renewed expiry")
		}
		if purchaseErr == nil {
			purchaseErr = purchaseTx.Commit()
		}
		done <- purchaseErr
	}()
	require.Eventually(t, func() bool {
		var waiting bool
		queryErr := integrationDB.QueryRowContext(ctx, "SELECT COALESCE(wait_event_type = 'Lock',false) FROM pg_stat_activity WHERE pid=$1", purchasePID).Scan(&waiting)
		return queryErr == nil && waiting
	}, 3*time.Second, 10*time.Millisecond, "purchase must reach the subscription lock held by renewal")
	// NOWAIT turns the previous lock cycle into a deterministic test failure,
	// rather than relying on PostgreSQL's deadlock victim choice.
	lockRows, err := renewalTx.Client().QueryContext(renewalCtx, "SELECT id FROM users WHERE id=$1 FOR UPDATE NOWAIT", f.user.ID)
	require.NoError(t, err, "purchase must not hold wallet while waiting for renewal")
	require.NoError(t, lockRows.Close())
	require.NoError(t, renewalTx.Client().User.UpdateOneID(f.user.ID).AddBalance(10).Exec(renewalCtx))
	require.NoError(t, renewalTx.Commit())
	require.NoError(t, <-done)
	balance, _ := resetCardPurchaseBalance(t, f.user.ID)
	require.Equal(t, 70.0, balance)
	purchases, grants := resetCardPurchaseCounts(t, f.user.ID)
	require.Equal(t, 1, purchases)
	require.Equal(t, 1, grants)
}
