//go:build integration

package service

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// refundBenefitPGFenceCache is a narrow in-memory Redis contract double. The
// test still runs the production fence service and Ent transaction callbacks;
// only the external cache transport is local to keep the PG lifecycle test
// deterministic.
type refundBenefitPGFenceCache struct {
	mu          sync.Mutex
	markers     map[int64]UserConcurrencyAuthorizationFenceMutationMarker
	projections map[int64]UserConcurrencyAuthorizationFenceProjection
	begins      int
	finishes    int
}

func newRefundBenefitPGFenceCache() *refundBenefitPGFenceCache {
	return &refundBenefitPGFenceCache{
		markers:     make(map[int64]UserConcurrencyAuthorizationFenceMutationMarker),
		projections: make(map[int64]UserConcurrencyAuthorizationFenceProjection),
	}
}

func (c *refundBenefitPGFenceCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (c *refundBenefitPGFenceCache) ReleaseAccountSlot(context.Context, int64, string) error {
	return nil
}
func (c *refundBenefitPGFenceCache) GetAccountConcurrency(context.Context, int64) (int, error) {
	return 0, nil
}
func (c *refundBenefitPGFenceCache) GetAccountConcurrencyBatch(context.Context, []int64) (map[int64]int, error) {
	return map[int64]int{}, nil
}
func (c *refundBenefitPGFenceCache) IncrementAccountWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}
func (c *refundBenefitPGFenceCache) DecrementAccountWaitCount(context.Context, int64) error {
	return nil
}
func (c *refundBenefitPGFenceCache) GetAccountWaitingCount(context.Context, int64) (int, error) {
	return 0, nil
}
func (c *refundBenefitPGFenceCache) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (c *refundBenefitPGFenceCache) ReleaseUserSlot(context.Context, int64, string) error { return nil }
func (c *refundBenefitPGFenceCache) GetUserConcurrency(context.Context, int64) (int, error) {
	return 0, nil
}
func (c *refundBenefitPGFenceCache) IncrementWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}
func (c *refundBenefitPGFenceCache) DecrementWaitCount(context.Context, int64) error { return nil }
func (c *refundBenefitPGFenceCache) GetAccountsLoadBatch(context.Context, []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	return map[int64]*AccountLoadInfo{}, nil
}
func (c *refundBenefitPGFenceCache) GetUsersLoadBatch(context.Context, []UserWithConcurrency) (map[int64]*UserLoadInfo, error) {
	return map[int64]*UserLoadInfo{}, nil
}
func (c *refundBenefitPGFenceCache) CleanupExpiredAccountSlots(context.Context, int64) error {
	return nil
}
func (c *refundBenefitPGFenceCache) CleanupExpiredAccountSlotKeys(context.Context) error { return nil }
func (c *refundBenefitPGFenceCache) CleanupStaleProcessSlots(context.Context, string) error {
	return nil
}

func (c *refundBenefitPGFenceCache) EnableUserConcurrencyAuthorizationFence() {}
func (c *refundBenefitPGFenceCache) SetUserConcurrencyAuthorizationCeiling(_ context.Context, userID int64, projection UserConcurrencyAuthorizationFenceProjection) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.projections[userID] = projection
	return true, nil
}
func (c *refundBenefitPGFenceCache) UserConcurrencyAuthorizationFenceReady(context.Context) (bool, error) {
	return true, nil
}
func (c *refundBenefitPGFenceCache) BeginUserConcurrencyAuthorizationFenceReconcile(context.Context) (string, error) {
	return uuid.NewString(), nil
}
func (c *refundBenefitPGFenceCache) FinishUserConcurrencyAuthorizationFenceReconcile(context.Context, string) (bool, error) {
	return true, nil
}
func (c *refundBenefitPGFenceCache) BeginUserConcurrencyAuthorizationFenceMutation(_ context.Context, userID int64, token string, expectedRevision int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if expectedRevision <= 0 {
		expectedRevision = c.projections[userID].Revision + 1
	}
	c.markers[userID] = UserConcurrencyAuthorizationFenceMutationMarker{
		Token:            token,
		ExpectedRevision: expectedRevision,
	}
	c.begins++
	return nil
}
func (c *refundBenefitPGFenceCache) FinishUserConcurrencyAuthorizationFenceMutation(_ context.Context, userID int64, token string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	current, ok := c.markers[userID]
	if !ok || current.Token != token {
		return false, nil
	}
	delete(c.markers, userID)
	c.finishes++
	return true, nil
}
func (c *refundBenefitPGFenceCache) CaptureUserConcurrencyAuthorizationFenceMutation(_ context.Context, userID int64) (UserConcurrencyAuthorizationFenceMutationMarker, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	marker, ok := c.markers[userID]
	return marker, ok, nil
}
func (c *refundBenefitPGFenceCache) CaptureUserConcurrencyAuthorizationFenceMutations(_ context.Context) (map[int64]UserConcurrencyAuthorizationFenceMutationMarker, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	markers := make(map[int64]UserConcurrencyAuthorizationFenceMutationMarker, len(c.markers))
	for userID, marker := range c.markers {
		markers[userID] = marker
	}
	return markers, nil
}
func (c *refundBenefitPGFenceCache) ClearUserConcurrencyAuthorizationFenceMutationIfUnchanged(_ context.Context, userID int64, marker UserConcurrencyAuthorizationFenceMutationMarker, observedRevision int64) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	current, ok := c.markers[userID]
	if !ok || current != marker || observedRevision < marker.ExpectedRevision {
		return false, nil
	}
	delete(c.markers, userID)
	return true, nil
}

func applyAllMigrationsForRefundBenefitPGTest(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS atlas_schema_revisions (
		version TEXT PRIMARY KEY, description TEXT NOT NULL, type INTEGER NOT NULL, applied INTEGER NOT NULL DEFAULT 0,
		total INTEGER NOT NULL DEFAULT 0, executed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, execution_time BIGINT NOT NULL DEFAULT 0,
		error TEXT NULL, error_stmt TEXT NULL, hash TEXT NOT NULL DEFAULT '', partial_hashes TEXT[] NULL, operator_version TEXT NULL
	)`); err != nil {
		return err
	}
	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, name := range files {
		content, err := migrations.FS.ReadFile(name)
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, "_notx.sql") {
			// PostgreSQL treats a multi-statement simple-query string as one
			// implicit transaction. Execute each concurrent-index statement
			// separately, matching the production migration runner.
			for index, statement := range strings.Split(string(content), ";") {
				statement = strings.TrimSpace(statement)
				if statement == "" || strings.HasPrefix(statement, "--") {
					continue
				}
				if _, err := db.ExecContext(ctx, statement); err != nil {
					return fmt.Errorf("apply %s statement %d: %w", name, index+1, err)
				}
			}
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit %s: %w", name, err)
		}
	}
	return nil
}

func newRefundBenefitPGTestService(t *testing.T) (*dbent.Client, *sql.DB, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	container, err := tcpostgres.Run(ctx, "postgres:18-alpine", tcpostgres.WithDatabase("sub2api_refund_benefit_test"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, applyAllMigrationsForRefundBenefitPGTest(ctx, db))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return client, db, ctx
}

func TestPaymentRefundBenefitPostgresServiceLifecycleUsesRealReserveCaptureRelease(t *testing.T) {
	client, db, ctx := newRefundBenefitPGTestService(t)
	unique := uuid.NewString()
	var userID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO users (email, password_hash, role, status, balance, concurrency)
		VALUES ($1,'test','user','active',100,2) RETURNING id`, "p19-service-"+unique+"@test.invalid").Scan(&userID))
	order, err := client.PaymentOrder.Create().
		SetUserID(userID).
		SetUserEmail("p19-service-" + unique + "@test.invalid").
		SetUserName("p19-service").
		SetAmount(100).
		SetPayAmount(10).
		SetFeeRate(0).
		SetRechargeCode("p19-" + unique[:16]).
		SetOutTradeNo("p19-" + unique[:16]).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("p19-trade-" + unique[:16]).
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusCompleted).
		SetPaidAt(time.Now().UTC().Add(-time.Minute)).
		SetExpiresAt(time.Now().UTC().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("integration.test").
		SetProductSnapshot(map[string]any{
			"schema_version":     2,
			"kind":               paymentSnapshotKindBalance,
			"credited_amount":    100.0,
			"paid_credit_amount": 10.0,
			"gift_credit_amount": 90.0,
			"entitlements": map[string]any{
				"balance_bonus": 90.0,
				"concurrency":   5,
			},
		}).
		Save(ctx)
	require.NoError(t, err)
	require.NoError(t, RecordPaymentWalletFunding(ctx, client, PaymentWalletFundingInput{
		OrderID: order.ID, UserID: userID, PaidCredit: 10, GiftCredit: 90, CashPaidMinor: 1000, Currency: payment.DefaultPaymentCurrency,
	}))
	_, err = db.ExecContext(ctx, `UPDATE users SET wallet_available_paid=10 WHERE id=$1`, userID)
	require.NoError(t, err)
	var sourceID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO payment_refund_benefit_sources (
		payment_order_id, user_id, source_origin, reset_cards_committed,
		reset_card_delivery_mode, concurrency_before, concurrency_target,
		concurrency_after_grant, state)
		VALUES ($1,$2,'fulfillment',0,'none',2,5,5,'ACTIVE') RETURNING id`, order.ID, userID).Scan(&sourceID))
	var before, after int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT before_concurrency, after_concurrency FROM sub2api_apply_payment_concurrency_max($1,5,$2)`, userID, sourceID).Scan(&before, &after))
	require.Equal(t, 2, before)
	require.Equal(t, 5, after)
	order, err = client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	state, err := reviewPaymentRefundBenefits(ctx, client, order, nil, nil, false)
	require.NoError(t, err)
	require.NotNil(t, state)

	cache := newRefundBenefitPGFenceCache()
	fence := NewConcurrencyService(cache)
	fence.userAuthorizationFenceEnabled.Store(true)
	fence.userAuthorizationFenceState = func(context.Context) (map[int64]UserConcurrencyAuthorizationFenceProjection, error) {
		var ceiling int
		var revision int64
		err := db.QueryRowContext(ctx, `SELECT concurrency, fence_revision FROM users JOIN payment_refund_concurrency_baselines ON payment_refund_concurrency_baselines.user_id=users.id WHERE users.id=$1`, userID).Scan(&ceiling, &revision)
		return map[int64]UserConcurrencyAuthorizationFenceProjection{userID: {Ceiling: ceiling, Revision: revision}}, err
	}
	fence.userAuthorizationFenceUser = func(context.Context, int64) (UserConcurrencyAuthorizationFenceProjection, bool, error) {
		var ceiling int
		var revision int64
		err := db.QueryRowContext(ctx, `SELECT concurrency, fence_revision FROM users JOIN payment_refund_concurrency_baselines ON payment_refund_concurrency_baselines.user_id=users.id WHERE users.id=$1`, userID).Scan(&ceiling, &revision)
		return UserConcurrencyAuthorizationFenceProjection{Ceiling: ceiling, Revision: revision}, true, err
	}

	attempt := &unifiedRefundAttempt{
		ProductRefundNo: "p19-service-refund-" + unique,
		PaymentOrderID:  uuid.NewString(),
		OrderID:         order.ID,
		IdempotencyKey:  "p19-service-idempotency-" + unique,
		Environment:     "sandbox", OrganizationID: uuid.NewString(), ProductID: uuid.NewString(), AppID: uuid.NewString(),
		PaymentMethod: "alipay", AmountFen: 1000, BalanceAmountMinor: 1000,
		ReasonCode: "customer_request", ReasonSummary: "integration", Status: unifiedRefundPending,
		RefundKind: refundReviewKindBalance, QuoteRevision: state.ProofDigest,
		WalletPaidAmount: 10, WalletGiftAmount: 90, EntitlementReserved: true,
	}

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	txCtx := dbent.NewTxContext(ctx, tx)
	require.NoError(t, insertUnifiedRefundAttempt(txCtx, tx.Client(), attempt))
	require.NoError(t, reserveReviewedRefundBenefits(txCtx, tx.Client(), fence, order, &RefundReview{benefitState: state}, attempt))
	require.NoError(t, tx.Commit())
	require.Eventually(t, func() bool { cache.mu.Lock(); defer cache.mu.Unlock(); return len(cache.markers) == 0 }, 5*time.Second, 20*time.Millisecond)
	var sourceState string
	var current int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT state FROM payment_refund_benefit_sources WHERE id=$1`, sourceID).Scan(&sourceState))
	require.Equal(t, refundBenefitStateReserved, sourceState)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT concurrency FROM users WHERE id=$1`, userID).Scan(&current))
	require.Equal(t, 2, current)

	var auditDetail string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT detail FROM unified_payment_refund_events WHERE order_id=$1 AND action='UNIFIED_REFUND_BENEFITS_RESERVED' ORDER BY created_at DESC LIMIT 1`, order.ID).Scan(&auditDetail))
	require.Contains(t, auditDetail, `"concurrency_current":5`)
	require.Contains(t, auditDetail, `"concurrency_after":2`)

	// A later explicit SET to zero is authoritative while the payment source
	// is held. The release path must preserve that observed zero in both the
	// durable user row and its lifecycle audit instead of treating zero as
	// missing and restoring the old cap.
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT before_concurrency, after_concurrency FROM sub2api_set_user_concurrency($1,0)`, userID).Scan(&before, &after))
	require.Equal(t, 2, before)
	require.Zero(t, after)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT concurrency FROM users WHERE id=$1`, userID).Scan(&current))
	require.Zero(t, current)

	attempt, err = loadUnifiedRefundAttempt(ctx, client, order.ID, attempt.ProductRefundNo)
	require.NoError(t, err)
	tx, err = client.Tx(ctx)
	require.NoError(t, err)
	txCtx = dbent.NewTxContext(ctx, tx)
	order, err = tx.Client().PaymentOrder.Get(txCtx, order.ID)
	require.NoError(t, err)
	require.NoError(t, captureReviewedRefundBenefits(txCtx, tx.Client(), fence, order, attempt))
	require.NoError(t, tx.Commit())
	require.Eventually(t, func() bool { cache.mu.Lock(); defer cache.mu.Unlock(); return len(cache.markers) == 0 }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT state FROM payment_refund_benefit_sources WHERE id=$1`, sourceID).Scan(&sourceState))
	require.Equal(t, refundBenefitStateRevoked, sourceState)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT concurrency FROM users WHERE id=$1`, userID).Scan(&current))
	require.Zero(t, current)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT detail FROM unified_payment_refund_events WHERE order_id=$1 AND action='UNIFIED_REFUND_BENEFITS_CAPTURED' ORDER BY created_at DESC LIMIT 1`, order.ID).Scan(&auditDetail))
	require.Contains(t, auditDetail, `"concurrency_current":0`)
	require.Contains(t, auditDetail, `"concurrency_after":0`)

	// Exercise the failure/release terminal path with a separate payment
	// source. The first source is immutable after capture, so a second source
	// is the production-shaped way to cover a later failed provider attempt.
	releaseOrder, err := client.PaymentOrder.Create().
		SetUserID(userID).
		SetUserEmail("p19-service-" + unique + "@test.invalid").
		SetUserName("p19-service").
		SetAmount(100).
		SetPayAmount(10).
		SetFeeRate(0).
		SetRechargeCode("p19-release-" + unique[:12]).
		SetOutTradeNo("p19-release-" + unique[:12]).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("p19-release-trade-" + unique[:12]).
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusCompleted).
		SetPaidAt(time.Now().UTC().Add(-time.Minute)).
		SetExpiresAt(time.Now().UTC().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("integration.test").
		SetProductSnapshot(map[string]any{
			"schema_version":     2,
			"kind":               paymentSnapshotKindBalance,
			"credited_amount":    100.0,
			"paid_credit_amount": 10.0,
			"gift_credit_amount": 90.0,
			"entitlements": map[string]any{
				"balance_bonus": 90.0,
				"concurrency":   5,
			},
		}).
		Save(ctx)
	require.NoError(t, err)
	require.NoError(t, RecordPaymentWalletFunding(ctx, client, PaymentWalletFundingInput{
		OrderID: releaseOrder.ID, UserID: userID, PaidCredit: 10, GiftCredit: 90, CashPaidMinor: 1000, Currency: payment.DefaultPaymentCurrency,
	}))
	var releaseSourceID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO payment_refund_benefit_sources (
		payment_order_id, user_id, source_origin, reset_cards_committed,
		reset_card_delivery_mode, concurrency_before, concurrency_target,
		concurrency_after_grant, state)
		VALUES ($1,$2,'fulfillment',0,'none',0,5,5,'ACTIVE') RETURNING id`, releaseOrder.ID, userID).Scan(&releaseSourceID))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT before_concurrency, after_concurrency FROM sub2api_apply_payment_concurrency_max($1,5,$2)`, userID, releaseSourceID).Scan(&before, &after))
	require.Zero(t, before)
	require.Equal(t, 5, after)
	releaseState, err := reviewPaymentRefundBenefits(ctx, client, releaseOrder, nil, nil, false)
	require.NoError(t, err)
	require.NotNil(t, releaseState)
	releaseAttempt := &unifiedRefundAttempt{
		ProductRefundNo: "p19-service-release-" + unique,
		PaymentOrderID:  uuid.NewString(),
		OrderID:         releaseOrder.ID,
		IdempotencyKey:  "p19-service-release-idempotency-" + unique,
		Environment:     "sandbox", OrganizationID: uuid.NewString(), ProductID: uuid.NewString(), AppID: uuid.NewString(),
		PaymentMethod: "alipay", AmountFen: 1000, BalanceAmountMinor: 1000,
		ReasonCode: "customer_request", ReasonSummary: "integration", Status: unifiedRefundPending,
		RefundKind: refundReviewKindBalance, QuoteRevision: releaseState.ProofDigest,
		WalletPaidAmount: 10, WalletGiftAmount: 90, EntitlementReserved: true,
	}
	tx, err = client.Tx(ctx)
	require.NoError(t, err)
	txCtx = dbent.NewTxContext(ctx, tx)
	require.NoError(t, insertUnifiedRefundAttempt(txCtx, tx.Client(), releaseAttempt))
	require.NoError(t, reserveReviewedRefundBenefits(txCtx, tx.Client(), fence, releaseOrder, &RefundReview{benefitState: releaseState}, releaseAttempt))
	require.NoError(t, tx.Commit())
	require.Eventually(t, func() bool { cache.mu.Lock(); defer cache.mu.Unlock(); return len(cache.markers) == 0 }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT state FROM payment_refund_benefit_sources WHERE id=$1`, releaseSourceID).Scan(&sourceState))
	require.Equal(t, refundBenefitStateReserved, sourceState)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT concurrency FROM users WHERE id=$1`, userID).Scan(&current))
	require.Zero(t, current)

	// Preserve a semantic no-op SET at the zero frontier before the release.
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT before_concurrency, after_concurrency FROM sub2api_set_user_concurrency($1,0)`, userID).Scan(&before, &after))
	require.Zero(t, before)
	require.Zero(t, after)
	tx, err = client.Tx(ctx)
	require.NoError(t, err)
	txCtx = dbent.NewTxContext(ctx, tx)
	releaseOrder, err = tx.Client().PaymentOrder.Get(txCtx, releaseOrder.ID)
	require.NoError(t, err)
	require.NoError(t, releaseReviewedRefundBenefits(txCtx, tx.Client(), fence, releaseOrder, releaseAttempt))
	require.NoError(t, tx.Commit())
	require.Eventually(t, func() bool { cache.mu.Lock(); defer cache.mu.Unlock(); return len(cache.markers) == 0 }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT state FROM payment_refund_benefit_sources WHERE id=$1`, releaseSourceID).Scan(&sourceState))
	require.Equal(t, refundBenefitStateActive, sourceState)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT concurrency FROM users WHERE id=$1`, userID).Scan(&current))
	require.Zero(t, current)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT detail FROM unified_payment_refund_events WHERE order_id=$1 AND action='UNIFIED_REFUND_BENEFITS_RELEASED' ORDER BY created_at DESC LIMIT 1`, releaseOrder.ID).Scan(&auditDetail))
	require.Contains(t, auditDetail, `"concurrency_current":0`)
	require.Contains(t, auditDetail, `"concurrency_after":0`)
	cache.mu.Lock()
	require.GreaterOrEqual(t, cache.begins, 4)
	require.Equal(t, cache.begins, cache.finishes)
	cache.mu.Unlock()
}

func TestPaymentRefundBenefitPostgresServiceLegacyBalancePreservesHigherCap(t *testing.T) {
	client, db, ctx := newRefundBenefitPGTestService(t)
	unique := uuid.NewString()
	var userID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO users (email, password_hash, role, status, balance, concurrency)
		VALUES ($1,'test','user','active',100,9) RETURNING id`, "p19-legacy-"+unique+"@test.invalid").Scan(&userID))

	// Seed the durable event baseline without changing the higher cap. A legacy
	// review must never manufacture a pre-grant value or a PAYMENT_MAX event.
	var before, after int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT before_concurrency, after_concurrency FROM sub2api_set_user_concurrency($1,9)`, userID).Scan(&before, &after))
	require.Equal(t, 9, before)
	require.Equal(t, 9, after)

	order, err := client.PaymentOrder.Create().
		SetUserID(userID).
		SetUserEmail("p19-legacy-" + unique + "@test.invalid").
		SetUserName("p19-legacy").
		SetAmount(100).
		SetPayAmount(10).
		SetFeeRate(0).
		SetRechargeCode("p19-legacy-" + unique[:12]).
		SetOutTradeNo("p19-legacy-" + unique[:12]).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("p19-legacy-trade-" + unique[:12]).
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusCompleted).
		SetPaidAt(time.Now().UTC().Add(-time.Minute)).
		SetExpiresAt(time.Now().UTC().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("integration.test").
		SetProductSnapshot(map[string]any{
			"schema_version":     2,
			"kind":               paymentSnapshotKindBalance,
			"credited_amount":    100.0,
			"paid_credit_amount": 10.0,
			"gift_credit_amount": 90.0,
			"entitlements": map[string]any{
				"balance_bonus": 90.0,
				"concurrency":   5,
			},
		}).
		Save(ctx)
	require.NoError(t, err)
	require.NoError(t, RecordPaymentWalletFunding(ctx, client, PaymentWalletFundingInput{
		OrderID: order.ID, UserID: userID, PaidCredit: 10, GiftCredit: 90, CashPaidMinor: 1000, Currency: payment.DefaultPaymentCurrency,
	}))

	// The same source-less order is rejected at the post-P19 cutoff.
	_, err = reviewPaymentRefundBenefits(ctx, client, order, nil, nil, false)
	require.ErrorIs(t, err, errRefundBenefitProvenanceMissing)

	// Make this historical for the isolated test database. The wallet ledger
	// and immutable snapshot are enough for the narrow legacy route, but the
	// already-higher cap remains untouched and has no fabricated before value.
	_, err = db.ExecContext(ctx, `UPDATE payment_refund_benefit_rollout
		SET cutover_at = CURRENT_TIMESTAMP + INTERVAL '1 hour' WHERE singleton = TRUE`)
	require.NoError(t, err)
	state, err := reviewPaymentRefundBenefits(ctx, client, order, nil, nil, false)
	require.NoError(t, err)
	require.NotNil(t, state)
	require.True(t, state.Legacy)
	require.Equal(t, "legacy_card_fk", state.Source.SourceOrigin)
	require.Zero(t, state.Source.ConcurrencyTarget)
	require.Nil(t, state.Source.ConcurrencyBefore)
	require.Nil(t, state.Source.ConcurrencyAfterGrant)
	require.Equal(t, 9, state.ConcurrencyCurrent)
	require.Equal(t, 9, state.ConcurrencyAfter)

	var sources, paymentEvents int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_refund_benefit_sources WHERE payment_order_id=$1`, order.ID).Scan(&sources))
	require.Zero(t, sources)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_refund_concurrency_events
		WHERE user_id=$1 AND event_kind='PAYMENT_MAX'`, userID).Scan(&paymentEvents))
	require.Zero(t, paymentEvents)
}
