package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrResetCardPurchaseUnavailable = infraerrors.Conflict("RESET_CARD_PURCHASE_UNAVAILABLE", "reset card purchase is unavailable for this subscription")
	ErrResetCardPriceInvalid        = infraerrors.Conflict("RESET_CARD_PRICE_INVALID", "reset card price is unavailable")
	ErrResetCardCurrencyUnsupported = infraerrors.Conflict("RESET_CARD_UNSUPPORTED_CURRENCY", "reset card purchase requires a CNY monthly plan")
	ErrResetCardQuoteChanged        = infraerrors.Conflict("RESET_CARD_QUOTE_CHANGED", "reset card quote changed; request a new quote")
	ErrResetCardPurchaseKeyInvalid  = infraerrors.BadRequest("RESET_CARD_PURCHASE_KEY_INVALID", "purchase_key must be a non-nil UUID")
	ErrResetCardPurchaseKeyConflict = infraerrors.Conflict("RESET_CARD_PURCHASE_KEY_CONFLICT", "purchase_key was already used for a different reset card purchase")
	ErrResetCardInsufficientBalance = infraerrors.Conflict("RESET_CARD_INSUFFICIENT_BALANCE", "insufficient available balance for reset card purchase")
	ErrResetCardPaymentDisabled     = infraerrors.Forbidden("PAYMENT_DISABLED", "payment system is disabled")
	ErrResetCardUserInactive        = infraerrors.Forbidden("USER_INACTIVE", "user account is disabled")
)

const resetCardPurchasePriceScale int32 = 2

// SubscriptionResetCardQuote is the server-derived, short-lived price for one
// reset card. The monthly plan remains the source of truth at purchase time.
type SubscriptionResetCardQuote struct {
	SubscriptionID int64     `json:"subscription_id"`
	GroupID        int64     `json:"group_id"`
	PlanID         int64     `json:"plan_id"`
	MonthlyPrice   float64   `json:"monthly_price"`
	Price          float64   `json:"price"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type PurchaseSubscriptionResetCardInput struct {
	UserID         int64
	SubscriptionID int64
	ExpectedPlanID int64
	ExpectedPrice  float64
	PurchaseKey    string
}

// PurchaseSubscriptionResetCardResult is deliberately independent from a
// payment_order. This is an internal-credit debit, never an external payment
// gateway order or a synthetic provider transaction.
type PurchaseSubscriptionResetCardResult struct {
	PurchaseID     int64     `json:"purchase_id"`
	SubscriptionID int64     `json:"subscription_id"`
	Price          float64   `json:"price"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type resetCardPurchaseSubscription struct {
	groupID          int64
	expiresAt        time.Time
	subscriptionStat string
	groupStatus      string
	subscriptionType string
	userStatus       string
}

type resetCardPurchasePlan struct {
	id       int64
	price    decimal.Decimal
	currency string
}

type resetCardPurchaseRecord struct {
	id             int64
	subscriptionID int64
	planID         int64
	price          decimal.Decimal
	expiresAt      time.Time
}

// GetResetCardQuote validates the caller's current subscription and derives a
// price only when its group has exactly one active for-sale monthly plan. A
// monthly plan is either one month or thirty days, matching psComputeValidityDays.
func (s *SubscriptionService) GetResetCardQuote(ctx context.Context, userID, subscriptionID int64) (*SubscriptionResetCardQuote, error) {
	if s == nil || s.entClient == nil {
		return nil, ErrResetCardPurchaseUnavailable
	}
	if userID <= 0 || subscriptionID <= 0 {
		return nil, ErrSubscriptionNotFound
	}

	now := s.resetCardPurchaseNow()
	subscription, found, err := loadResetCardPurchaseSubscription(ctx, s.entClient, userID, subscriptionID, false)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSubscriptionNotFound
	}
	if err := validateResetCardPurchaseSubscription(subscription, now); err != nil {
		return nil, err
	}

	plan, err := loadSingleMonthlyResetCardPlan(ctx, s.entClient, subscription.groupID, false)
	if err != nil {
		return nil, err
	}
	if err := validateResetCardPurchasePlanCurrency(plan.currency); err != nil {
		return nil, err
	}
	price, err := resetCardPriceForMonthlyPlan(plan.price)
	if err != nil {
		return nil, err
	}

	return &SubscriptionResetCardQuote{
		SubscriptionID: subscriptionID,
		GroupID:        subscription.groupID,
		PlanID:         plan.id,
		MonthlyPrice:   plan.price.InexactFloat64(),
		Price:          price.InexactFloat64(),
		ExpiresAt:      subscription.expiresAt,
	}, nil
}

// PurchaseResetCard atomically debits available internal balance, creates one
// reset-card grant, and records the durable purchase. Reusing the same key
// returns the committed record before checking mutable subscription or payment
// state, so a response lost after commit is safe to retry.
func (s *SubscriptionService) PurchaseResetCard(ctx context.Context, input PurchaseSubscriptionResetCardInput) (*PurchaseSubscriptionResetCardResult, error) {
	if s == nil || s.entClient == nil {
		return nil, ErrResetCardPurchaseUnavailable
	}
	if input.UserID <= 0 || input.SubscriptionID <= 0 {
		return nil, ErrSubscriptionNotFound
	}
	purchaseKey, err := normalizeResetCardPurchaseKey(input.PurchaseKey)
	if err != nil {
		return nil, err
	}
	expectedPrice, err := normalizeResetCardExpectedPrice(input.ExpectedPrice)
	if err != nil {
		return nil, err
	}

	var result *PurchaseSubscriptionResetCardResult
	err = s.withResetCardPurchaseTx(ctx, func(txCtx context.Context, client *dbent.Client) error {
		// Preserve the conflict response even when a reused key names an absent
		// subscription. Purchase records are immutable; the locked lookup below
		// still arbitrates requests whose original purchase has not committed yet.
		prior, priorFound, err := loadResetCardPurchaseRecord(txCtx, client, input.UserID, purchaseKey)
		if err != nil {
			return err
		}
		if priorFound && (prior.subscriptionID != input.SubscriptionID || prior.planID != input.ExpectedPlanID || !prior.price.Equal(expectedPrice)) {
			return ErrResetCardPurchaseKeyConflict
		}
		// Subscription fulfillment renews the subscription before granting user
		// benefits. Match its subscription -> user order to avoid a lock cycle.
		// Lock identity without eligibility filters so historical replays remain
		// possible after expiry, suspension or soft deletion.
		if err := lockResetCardPurchaseSubscriptionIdentity(txCtx, client, input.UserID, input.SubscriptionID); err != nil {
			return err
		}
		// The user row serializes every balance debit for this user. It is locked
		// before the purchase-key lookup so two same-key requests cannot create a
		// second grant even if one caller retries while the other is committing.
		if _, err := lockResetCardPurchaseUser(txCtx, client, input.UserID); err != nil {
			return err
		}

		existing, found, err := loadResetCardPurchaseRecord(txCtx, client, input.UserID, purchaseKey)
		if err != nil {
			return err
		}
		if found {
			if existing.subscriptionID != input.SubscriptionID || existing.planID != input.ExpectedPlanID || !existing.price.Equal(expectedPrice) {
				return ErrResetCardPurchaseKeyConflict
			}
			result = resetCardPurchaseResult(existing)
			// A replay is also a safe recovery point if the first committed
			// purchase could not reach Redis to invalidate its old balance.
			s.scheduleResetCardPurchaseBalanceInvalidation(txCtx, input.UserID)
			return nil
		}

		// This is the same persisted switch used by normal checkout. The row lock
		// keeps an administrator's toggle from crossing this financial commit.
		enabled, err := lockResetCardPaymentEnabled(txCtx, client)
		if err != nil {
			return err
		}
		if !enabled {
			return ErrResetCardPaymentDisabled
		}

		now := s.resetCardPurchaseNow()
		subscription, found, err := loadResetCardPurchaseSubscription(txCtx, client, input.UserID, input.SubscriptionID, true)
		if err != nil {
			return err
		}
		if !found {
			return ErrSubscriptionNotFound
		}
		if err := validateResetCardPurchaseSubscription(subscription, now); err != nil {
			return err
		}

		// A table lock is intentionally short-lived. FOR UPDATE protects the
		// selected plan row, while SHARE also prevents a concurrent add/remove of
		// another monthly source from changing the exact-one invariant mid-purchase.
		if _, err := client.ExecContext(txCtx, "LOCK TABLE subscription_plans IN SHARE MODE"); err != nil {
			return fmt.Errorf("lock reset card plan source: %w", err)
		}
		plan, err := loadSingleMonthlyResetCardPlan(txCtx, client, subscription.groupID, true)
		if err != nil {
			return err
		}
		if err := validateResetCardPurchasePlanCurrency(plan.currency); err != nil {
			return err
		}
		price, err := resetCardPriceForMonthlyPlan(plan.price)
		if err != nil {
			return err
		}
		if plan.id != input.ExpectedPlanID || !price.Equal(expectedPrice) {
			return ErrResetCardQuoteChanged
		}

		if err := debitResetCardPurchaseBalance(txCtx, client, input.UserID, price); err != nil {
			return err
		}
		grantID, err := insertPurchasedResetCardGrant(txCtx, client, input.UserID, input.SubscriptionID, subscription.groupID, subscription.expiresAt, now)
		if err != nil {
			return err
		}
		record, err := insertResetCardPurchaseRecord(txCtx, client, input.UserID, input.SubscriptionID, subscription.groupID, plan.id, price, purchaseKey, grantID, subscription.expiresAt, now)
		if err != nil {
			return err
		}
		result = resetCardPurchaseResult(record)
		s.scheduleResetCardPurchaseBalanceInvalidation(txCtx, input.UserID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *SubscriptionService) resetCardPurchaseNow() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *SubscriptionService) withResetCardPurchaseTx(ctx context.Context, fn func(context.Context, *dbent.Client) error) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return fn(ctx, tx.Client())
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin reset card purchase transaction: %w", err)
	}
	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx, tx.Client()); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reset card purchase transaction: %w", err)
	}
	return nil
}

// scheduleResetCardPurchaseBalanceInvalidation runs only after its surrounding
// Ent transaction commits. Cache failures are logged asynchronously: the
// purchase is already durable and a retry with the same purchase key will
// schedule another invalidation without charging the user again.
func (s *SubscriptionService) scheduleResetCardPurchaseBalanceInvalidation(ctx context.Context, userID int64) {
	if s == nil || s.billingCacheService == nil || userID <= 0 {
		return
	}
	dispatch := func() {
		go func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					slog.Error("panic invalidating balance cache after reset card purchase", "user_id", userID, "recover", recovered)
				}
			}()
			cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.billingCacheService.InvalidateUserBalance(cacheCtx, userID); err != nil {
				slog.Warn("invalidate balance cache after reset card purchase failed", "user_id", userID, "error", err)
			}
		}()
	}
	if tx := dbent.TxFromContext(ctx); tx != nil {
		tx.OnCommit(func(next dbent.Committer) dbent.Committer {
			return dbent.CommitFunc(func(commitCtx context.Context, committedTx *dbent.Tx) error {
				if err := next.Commit(commitCtx, committedTx); err != nil {
					return err
				}
				dispatch()
				return nil
			})
		})
		return
	}
	dispatch()
}

func normalizeResetCardPurchaseKey(raw string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == uuid.Nil {
		return "", ErrResetCardPurchaseKeyInvalid
	}
	return parsed.String(), nil
}

func normalizeResetCardExpectedPrice(raw float64) (decimal.Decimal, error) {
	if math.IsNaN(raw) || math.IsInf(raw, 0) || raw <= 0 {
		return decimal.Decimal{}, ErrResetCardPriceInvalid
	}
	value := decimal.NewFromFloat(raw)
	if !value.Equal(value.Round(resetCardPurchasePriceScale)) {
		return decimal.Decimal{}, ErrResetCardPriceInvalid
	}
	return value, nil
}

func resetCardPriceForMonthlyPlan(monthlyPrice decimal.Decimal) (decimal.Decimal, error) {
	if !monthlyPrice.IsPositive() {
		return decimal.Decimal{}, ErrResetCardPriceInvalid
	}
	price := monthlyPrice.Div(decimal.NewFromInt(3)).Round(resetCardPurchasePriceScale)
	if !price.IsPositive() {
		return decimal.Decimal{}, ErrResetCardPriceInvalid
	}
	return price, nil
}

// The wallet's fixed parity is 1 CNY = 1 internal credit. A plan currency is
// display-only in ordinary checkout, so an unlabeled or foreign-currency plan
// cannot safely be converted into a wallet debit here. Require explicit CNY.
func validateResetCardPurchasePlanCurrency(currency string) error {
	if !strings.EqualFold(strings.TrimSpace(currency), payment.DefaultPaymentCurrency) {
		return ErrResetCardCurrencyUnsupported
	}
	return nil
}

func loadResetCardPurchaseSubscription(ctx context.Context, client *dbent.Client, userID, subscriptionID int64, forUpdate bool) (resetCardPurchaseSubscription, bool, error) {
	query := `
		SELECT us.group_id, us.expires_at, us.status, g.status, g.subscription_type, u.status
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id AND u.deleted_at IS NULL
		JOIN groups g ON g.id = us.group_id AND g.deleted_at IS NULL
		WHERE us.id = $1 AND us.user_id = $2 AND us.deleted_at IS NULL
	`
	if forUpdate {
		query += " FOR UPDATE OF us, g"
	}
	rows, err := client.QueryContext(ctx, query, subscriptionID, userID)
	if err != nil {
		return resetCardPurchaseSubscription{}, false, fmt.Errorf("load reset card purchase subscription: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return resetCardPurchaseSubscription{}, false, err
		}
		return resetCardPurchaseSubscription{}, false, nil
	}
	var subscription resetCardPurchaseSubscription
	if err := rows.Scan(&subscription.groupID, &subscription.expiresAt, &subscription.subscriptionStat, &subscription.groupStatus, &subscription.subscriptionType, &subscription.userStatus); err != nil {
		return resetCardPurchaseSubscription{}, false, fmt.Errorf("scan reset card purchase subscription: %w", err)
	}
	if err := rows.Err(); err != nil {
		return resetCardPurchaseSubscription{}, false, err
	}
	return subscription, true, nil
}

func validateResetCardPurchaseSubscription(subscription resetCardPurchaseSubscription, now time.Time) error {
	if subscription.userStatus != StatusActive {
		return ErrResetCardUserInactive
	}
	if !subscription.expiresAt.After(now) || subscription.subscriptionStat == SubscriptionStatusExpired {
		return ErrSubscriptionExpired
	}
	if subscription.subscriptionStat != SubscriptionStatusActive {
		return ErrSubscriptionSuspended
	}
	if subscription.subscriptionType != SubscriptionTypeSubscription {
		return ErrGroupNotSubscriptionType
	}
	if subscription.groupStatus != StatusActive {
		return ErrResetCardGroupInactive
	}
	return nil
}

func loadSingleMonthlyResetCardPlan(ctx context.Context, client *dbent.Client, groupID int64, forUpdate bool) (resetCardPurchasePlan, error) {
	query := `
		SELECT id, price::text, currency
		FROM subscription_plans
		WHERE group_id = $1
			AND for_sale = TRUE
			AND (
				(validity_days = 1 AND lower(validity_unit) IN ('month', 'months'))
				OR (validity_days = 30 AND lower(validity_unit) IN ('day', 'days'))
			)
		ORDER BY id ASC
	`
	if forUpdate {
		query += " FOR UPDATE"
	}
	rows, err := client.QueryContext(ctx, query, groupID)
	if err != nil {
		return resetCardPurchasePlan{}, fmt.Errorf("load reset card monthly plan: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var plans []resetCardPurchasePlan
	for rows.Next() {
		var (
			plan  resetCardPurchasePlan
			price string
		)
		if err := rows.Scan(&plan.id, &price, &plan.currency); err != nil {
			return resetCardPurchasePlan{}, fmt.Errorf("scan reset card monthly plan: %w", err)
		}
		parsed, err := decimal.NewFromString(price)
		if err != nil {
			return resetCardPurchasePlan{}, fmt.Errorf("parse reset card monthly plan price: %w", err)
		}
		plan.price = parsed
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return resetCardPurchasePlan{}, err
	}
	if len(plans) != 1 {
		return resetCardPurchasePlan{}, ErrResetCardPurchaseUnavailable
	}
	return plans[0], nil
}

func lockResetCardPurchaseSubscriptionIdentity(ctx context.Context, client *dbent.Client, userID, subscriptionID int64) error {
	rows, err := client.QueryContext(ctx, `SELECT id FROM user_subscriptions WHERE id = $1 AND user_id = $2 FOR UPDATE`, subscriptionID, userID)
	if err != nil {
		return fmt.Errorf("lock reset card subscription identity: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return ErrSubscriptionNotFound
	}
	return nil
}

func lockResetCardPurchaseUser(ctx context.Context, client *dbent.Client, userID int64) (string, error) {
	rows, err := client.QueryContext(ctx, `
		SELECT status
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, userID)
	if err != nil {
		return "", fmt.Errorf("lock reset card purchase user: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", err
		}
		return "", ErrUserNotFound
	}
	var status string
	if err := rows.Scan(&status); err != nil {
		return "", fmt.Errorf("scan reset card purchase user: %w", err)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return status, nil
}

func lockResetCardPaymentEnabled(ctx context.Context, client *dbent.Client) (bool, error) {
	rows, err := client.QueryContext(ctx, `
		SELECT value
		FROM settings
		WHERE key = $1
		FOR SHARE
	`, SettingPaymentEnabled)
	if err != nil {
		return false, fmt.Errorf("lock payment enabled setting: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	var value string
	if err := rows.Scan(&value); err != nil {
		return false, fmt.Errorf("scan payment enabled setting: %w", err)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return value == "true", nil
}

func loadResetCardPurchaseRecord(ctx context.Context, client *dbent.Client, userID int64, purchaseKey string) (resetCardPurchaseRecord, bool, error) {
	rows, err := client.QueryContext(ctx, `
		SELECT id, subscription_id, plan_id, price::text, expires_at
		FROM subscription_reset_card_purchases
		WHERE user_id = $1 AND purchase_key = $2::uuid
	`, userID, purchaseKey)
	if err != nil {
		return resetCardPurchaseRecord{}, false, fmt.Errorf("load reset card purchase replay: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return resetCardPurchaseRecord{}, false, err
		}
		return resetCardPurchaseRecord{}, false, nil
	}
	var (
		record resetCardPurchaseRecord
		price  string
	)
	if err := rows.Scan(&record.id, &record.subscriptionID, &record.planID, &price, &record.expiresAt); err != nil {
		return resetCardPurchaseRecord{}, false, fmt.Errorf("scan reset card purchase replay: %w", err)
	}
	parsed, err := decimal.NewFromString(price)
	if err != nil {
		return resetCardPurchaseRecord{}, false, fmt.Errorf("parse reset card purchase replay price: %w", err)
	}
	record.price = parsed
	if err := rows.Err(); err != nil {
		return resetCardPurchaseRecord{}, false, err
	}
	return record, true, nil
}

func debitResetCardPurchaseBalance(ctx context.Context, client *dbent.Client, userID int64, price decimal.Decimal) error {
	rows, err := client.QueryContext(ctx, `
		UPDATE users
		SET balance = balance - $1::numeric,
			updated_at = NOW()
		WHERE id = $2
			AND deleted_at IS NULL
			AND status = $3
			AND balance >= $1::numeric
		RETURNING balance
	`, price.StringFixed(resetCardPurchasePriceScale), userID, StatusActive)
	if err != nil {
		return fmt.Errorf("debit reset card purchase balance: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		var ignored string
		if err := rows.Scan(&ignored); err != nil {
			return fmt.Errorf("scan reset card purchase balance: %w", err)
		}
		return rows.Err()
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return ErrResetCardInsufficientBalance
}

func insertPurchasedResetCardGrant(ctx context.Context, client *dbent.Client, userID, subscriptionID, groupID int64, expiresAt, now time.Time) (int64, error) {
	rows, err := client.QueryContext(ctx, `
		INSERT INTO subscription_reset_grants (
			subscription_id, user_id, group_id, quantity, used_count,
			expires_at, issued_by, created_at, updated_at
		)
		VALUES ($1, $2, $3, 1, 0, $4, NULL, $5, $5)
		RETURNING id
	`, subscriptionID, userID, groupID, expiresAt, now)
	if err != nil {
		return 0, fmt.Errorf("insert purchased reset card grant: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("insert purchased reset card grant returned no row")
	}
	var grantID int64
	if err := rows.Scan(&grantID); err != nil {
		return 0, fmt.Errorf("scan purchased reset card grant: %w", err)
	}
	return grantID, rows.Err()
}

func insertResetCardPurchaseRecord(ctx context.Context, client *dbent.Client, userID, subscriptionID, groupID, planID int64, price decimal.Decimal, purchaseKey string, grantID int64, expiresAt, now time.Time) (resetCardPurchaseRecord, error) {
	rows, err := client.QueryContext(ctx, `
		INSERT INTO subscription_reset_card_purchases (
			user_id, subscription_id, group_id, plan_id, price,
			purchase_key, grant_id, expires_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5::numeric, $6::uuid, $7, $8, $9)
		RETURNING id, subscription_id, plan_id, price::text, expires_at
	`, userID, subscriptionID, groupID, planID, price.StringFixed(resetCardPurchasePriceScale), purchaseKey, grantID, expiresAt, now)
	if err != nil {
		return resetCardPurchaseRecord{}, fmt.Errorf("insert reset card purchase record: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return resetCardPurchaseRecord{}, err
		}
		return resetCardPurchaseRecord{}, fmt.Errorf("insert reset card purchase record returned no row")
	}
	var (
		record resetCardPurchaseRecord
		stored string
	)
	if err := rows.Scan(&record.id, &record.subscriptionID, &record.planID, &stored, &record.expiresAt); err != nil {
		return resetCardPurchaseRecord{}, fmt.Errorf("scan reset card purchase record: %w", err)
	}
	parsed, err := decimal.NewFromString(stored)
	if err != nil {
		return resetCardPurchaseRecord{}, fmt.Errorf("parse reset card purchase record price: %w", err)
	}
	record.price = parsed
	return record, rows.Err()
}

func resetCardPurchaseResult(record resetCardPurchaseRecord) *PurchaseSubscriptionResetCardResult {
	return &PurchaseSubscriptionResetCardResult{
		PurchaseID:     record.id,
		SubscriptionID: record.subscriptionID,
		Price:          record.price.InexactFloat64(),
		ExpiresAt:      record.expiresAt,
	}
}
