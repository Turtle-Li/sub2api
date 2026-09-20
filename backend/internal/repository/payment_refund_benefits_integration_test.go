//go:build integration

package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type paymentRefundBenefitPGFixture struct {
	t       *testing.T
	ctx     context.Context
	client  *dbent.Client
	user    *service.User
	group   *service.Group
	sub     *service.UserSubscription
	orderID []int64
}

func newPaymentRefundBenefitPGFixture(t *testing.T, concurrency int) *paymentRefundBenefitPGFixture {
	t.Helper()
	ctx := context.Background()
	unique := uuid.NewString()
	fixture := &paymentRefundBenefitPGFixture{t: t, ctx: ctx, client: integrationEntClient}
	fixture.user = mustCreateUser(t, fixture.client, &service.User{
		Email:       "refund-benefit-" + unique + "@integration.test",
		Username:    "refund-benefit-" + unique[:8],
		Concurrency: concurrency,
	})
	t.Cleanup(fixture.cleanup)
	return fixture
}

func (f *paymentRefundBenefitPGFixture) cleanup() {
	ctx := context.Background()
	// Child link/event rows must go before their immutable source. The source
	// deliberately uses RESTRICT FKs so a real refund cannot lose evidence.
	for _, query := range []string{
		`DELETE FROM payment_refund_benefit_reset_card_grants WHERE source_id IN (SELECT id FROM payment_refund_benefit_sources WHERE user_id = $1)`,
		`DELETE FROM payment_refund_concurrency_events WHERE user_id = $1`,
		`DELETE FROM payment_refund_benefit_sources WHERE user_id = $1`,
		`DELETE FROM subscription_reset_card_purchases WHERE user_id = $1`,
		`DELETE FROM subscription_reset_grants WHERE user_id = $1`,
		`DELETE FROM subscription_reset_card_issuances WHERE schedule_id IN (SELECT id FROM subscription_reset_card_schedules WHERE user_id = $1)`,
		`DELETE FROM subscription_reset_card_schedules WHERE user_id = $1`,
		`DELETE FROM payment_subscription_grants WHERE user_id = $1`,
		`DELETE FROM unified_payment_refund_events WHERE order_id IN (SELECT id FROM payment_orders WHERE user_id = $1)`,
		`DELETE FROM unified_payment_refund_attempts WHERE order_id IN (SELECT id FROM payment_orders WHERE user_id = $1)`,
		`DELETE FROM payment_audit_logs WHERE order_id IN (SELECT id::text FROM payment_orders WHERE user_id = $1)`,
		`DELETE FROM payment_orders WHERE user_id = $1`,
		`DELETE FROM subscription_cache_invalidation_outbox WHERE user_id = $1`,
		`DELETE FROM user_allowed_groups WHERE user_id = $1`,
	} {
		if _, err := integrationDB.ExecContext(ctx, query, f.user.ID); err != nil {
			f.t.Errorf("refund benefit fixture cleanup: %v", err)
		}
	}
	if f.sub != nil {
		if _, err := integrationDB.ExecContext(ctx, `DELETE FROM user_subscriptions WHERE id = $1`, f.sub.ID); err != nil {
			f.t.Errorf("refund benefit subscription cleanup: %v", err)
		}
	}
	if f.group != nil {
		if _, err := integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id = $1`, f.group.ID); err != nil {
			f.t.Errorf("refund benefit group cleanup: %v", err)
		}
	}
	if _, err := integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, f.user.ID); err != nil {
		f.t.Errorf("refund benefit user cleanup: %v", err)
	}
}

func (f *paymentRefundBenefitPGFixture) ensureSubscription() {
	f.t.Helper()
	if f.group != nil {
		return
	}
	unique := uuid.NewString()
	f.group = mustCreateGroup(f.t, f.client, &service.Group{
		Name:             "refund-benefit-group-" + unique[:16],
		Platform:         service.PlatformOpenAI,
		SubscriptionType: service.SubscriptionTypeSubscription,
	})
	now := time.Now().UTC().Truncate(time.Microsecond)
	f.sub = mustCreateSubscription(f.t, f.client, &service.UserSubscription{
		UserID: f.user.ID, GroupID: f.group.ID,
		StartsAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(0, 4, 0),
	})
}

func (f *paymentRefundBenefitPGFixture) newOrder(orderType string) *dbent.PaymentOrder {
	f.t.Helper()
	unique := uuid.NewString()
	create := f.client.PaymentOrder.Create().
		SetUserID(f.user.ID).
		SetUserEmail(f.user.Email).
		SetUserName(f.user.Username).
		SetAmount(10).
		SetPayAmount(10).
		SetFeeRate(0).
		SetRechargeCode("p19-" + unique[:20]).
		SetOutTradeNo("p19-" + unique[:20]).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("p19-trade-" + unique[:20]).
		SetOrderType(orderType).
		SetStatus(service.OrderStatusCompleted).
		SetPaidAt(time.Now().UTC().Add(-time.Minute)).
		SetExpiresAt(time.Now().UTC().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("integration.test")
	if orderType == payment.OrderTypeSubscription {
		f.ensureSubscription()
		create.SetSubscriptionGroupID(f.group.ID)
	}
	order, err := create.Save(f.ctx)
	require.NoError(f.t, err)
	f.orderID = append(f.orderID, order.ID)
	return order
}

func (f *paymentRefundBenefitPGFixture) insertSource(
	order *dbent.PaymentOrder,
	resetCards int,
	deliveryMode string,
	concurrencyBefore int,
	concurrencyTarget int,
) int64 {
	f.t.Helper()
	var (
		grantOrderID   any
		subscriptionID any
		groupID        any
		before         any
		after          any
	)
	if order.OrderType == payment.OrderTypeSubscription {
		f.ensureSubscription()
		grantOrderID, subscriptionID, groupID = order.ID, f.sub.ID, f.group.ID
	}
	if concurrencyTarget > 0 {
		before = concurrencyBefore
		after = concurrencyBefore
		if concurrencyTarget > concurrencyBefore {
			after = concurrencyTarget
		}
	}
	var sourceID int64
	err := integrationDB.QueryRowContext(f.ctx, `INSERT INTO payment_refund_benefit_sources (
		payment_order_id, subscription_grant_order_id, user_id, subscription_id, group_id,
		reset_cards_committed, reset_card_delivery_mode, concurrency_before,
		concurrency_target, concurrency_after_grant, state
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'ACTIVE') RETURNING id`,
		order.ID, grantOrderID, f.user.ID, subscriptionID, groupID, resetCards, deliveryMode,
		before, concurrencyTarget, after).Scan(&sourceID)
	require.NoError(f.t, err)
	return sourceID
}

func (f *paymentRefundBenefitPGFixture) insertAttempt(orderID int64) string {
	f.t.Helper()
	refundNo := "p19-refund-" + uuid.NewString()
	_, err := integrationDB.ExecContext(f.ctx, `INSERT INTO unified_payment_refund_attempts (
		product_refund_no, order_id, payment_order_id, idempotency_key, environment,
		organization_id, product_id, app_id, payment_method, amount_fen,
		balance_amount_minor, deduct_balance, force_refund, reason_summary, status
	) VALUES ($1,$2,$3,$4,'sandbox',$5,$6,'p19-integration','alipay',1,1,FALSE,FALSE,'test refund','PENDING')`,
		refundNo, orderID, uuid.NewString(), "p19-idempotency-"+uuid.NewString(), uuid.NewString(), uuid.NewString())
	require.NoError(f.t, err)
	return refundNo
}

func reservePaymentRefundBenefitSource(t *testing.T, ctx context.Context, sourceID int64, refundNo string) {
	t.Helper()
	_, err := integrationDB.ExecContext(ctx, `UPDATE payment_refund_benefit_sources
		SET state = 'RESERVED', reserved_product_refund_no = $2,
			reservation_proof_digest = $3, reserved_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1`, sourceID, refundNo, strings.Repeat("a", 64))
	require.NoError(t, err)
}

func releasePaymentRefundBenefitSource(t *testing.T, ctx context.Context, sourceID int64, refundNo string) {
	t.Helper()
	_, err := integrationDB.ExecContext(ctx, `UPDATE payment_refund_benefit_sources
		SET state = 'ACTIVE', reserved_product_refund_no = NULL,
			reservation_proof_digest = NULL, reserved_at = NULL, revoked_at = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND reserved_product_refund_no = $2`, sourceID, refundNo)
	require.NoError(t, err)
}

func recomputePaymentRefundBenefitConcurrency(t *testing.T, ctx context.Context, userID int64) int {
	t.Helper()
	var value int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT sub2api_recompute_user_concurrency($1)`, userID).Scan(&value))
	return value
}

func TestPaymentRefundBenefitPostgresConcurrencyReplayPreservesLaterEvents(t *testing.T) {
	fixture := newPaymentRefundBenefitPGFixture(t, 2)
	ctx := fixture.ctx
	firstOrder := fixture.newOrder(payment.OrderTypeBalance)
	firstSource := fixture.insertSource(firstOrder, 0, "none", 2, 5)
	var before, after int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT before_concurrency, after_concurrency FROM sub2api_apply_payment_concurrency_max($1,$2,$3)`,
		fixture.user.ID, 5, firstSource).Scan(&before, &after))
	require.Equal(t, 2, before)
	require.Equal(t, 5, after)

	secondOrder := fixture.newOrder(payment.OrderTypeBalance)
	secondSource := fixture.insertSource(secondOrder, 0, "none", 5, 8)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT before_concurrency, after_concurrency FROM sub2api_apply_payment_concurrency_max($1,$2,$3)`,
		fixture.user.ID, 8, secondSource).Scan(&before, &after))
	require.Equal(t, 5, before)
	require.Equal(t, 8, after)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT before_concurrency, after_concurrency FROM sub2api_apply_user_concurrency_delta($1,$2)`,
		fixture.user.ID, 1).Scan(&before, &after))
	require.Equal(t, 8, before)
	require.Equal(t, 9, after)

	refundNo := fixture.insertAttempt(secondOrder.ID)
	reservePaymentRefundBenefitSource(t, ctx, secondSource, refundNo)
	// The later +1 must replay after the remaining source, not preserve the
	// held source's target of eight. This is the bug a before/after restore has.
	require.Equal(t, 6, recomputePaymentRefundBenefitConcurrency(t, ctx, fixture.user.ID))

	// An administrator override during the pending refund becomes a later SET.
	// It remains authoritative after the failed refund releases the source.
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT before_concurrency, after_concurrency FROM sub2api_set_user_concurrency($1,$2)`,
		fixture.user.ID, 4).Scan(&before, &after))
	require.Equal(t, 6, before)
	require.Equal(t, 4, after)
	releasePaymentRefundBenefitSource(t, ctx, secondSource, refundNo)
	require.Equal(t, 4, recomputePaymentRefundBenefitConcurrency(t, ctx, fixture.user.ID))

	// Explicit SET no-ops and clipped DELTAs are durable semantic events, not
	// silently omitted because their stored value happens to stay unchanged.
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT before_concurrency, after_concurrency FROM sub2api_set_user_concurrency($1,$2)`,
		fixture.user.ID, 4).Scan(&before, &after))
	require.Equal(t, 4, before)
	require.Equal(t, 4, after)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT before_concurrency, after_concurrency FROM sub2api_apply_user_concurrency_delta($1,$2)`,
		fixture.user.ID, -100).Scan(&before, &after))
	require.Equal(t, 4, before)
	require.Zero(t, after)
	require.Zero(t, recomputePaymentRefundBenefitConcurrency(t, ctx, fixture.user.ID))

	var requestedDelta, noopSets int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT requested_delta
		FROM payment_refund_concurrency_events
		WHERE user_id = $1 AND event_kind = 'DELTA' ORDER BY event_seq DESC LIMIT 1`, fixture.user.ID).Scan(&requestedDelta))
	require.Equal(t, -100, requestedDelta)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM payment_refund_concurrency_events
		WHERE user_id = $1 AND event_kind = 'SET' AND target_concurrency = 4`, fixture.user.ID).Scan(&noopSets))
	require.Equal(t, 2, noopSets)

	var revision int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT fence_revision FROM payment_refund_concurrency_baselines WHERE user_id = $1`, fixture.user.ID).Scan(&revision))
	require.Greater(t, revision, int64(0))
}

func TestPaymentRefundBenefitPostgresRawWriterCreatesUnattributedBarrier(t *testing.T) {
	fixture := newPaymentRefundBenefitPGFixture(t, 2)
	ctx := fixture.ctx

	beforeBarrierOrder := fixture.newOrder(payment.OrderTypeBalance)
	beforeBarrierSource := fixture.insertSource(beforeBarrierOrder, 0, "none", 2, 5)
	var before, after int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT before_concurrency, after_concurrency FROM sub2api_apply_payment_concurrency_max($1,$2,$3)`,
		fixture.user.ID, 5, beforeBarrierSource).Scan(&before, &after))
	require.Equal(t, 2, before)
	require.Equal(t, 5, after)

	// An untyped historical writer is not an administrator SET. The trigger
	// stores the observed result as a barrier, which prevents a source before
	// it from being inferred as safely removable by the service replay.
	_, err := integrationDB.ExecContext(ctx, `UPDATE users SET concurrency = 7 WHERE id = $1`, fixture.user.ID)
	require.NoError(t, err)
	var kind string
	var target, observedBefore, observedAfter int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT event_kind, target_concurrency, before_concurrency, after_concurrency
		FROM payment_refund_concurrency_events WHERE user_id = $1 ORDER BY event_seq DESC LIMIT 1`, fixture.user.ID).
		Scan(&kind, &target, &observedBefore, &observedAfter))
	require.Equal(t, "UNATTRIBUTED", kind)
	require.Equal(t, 7, target)
	require.Equal(t, 5, observedBefore)
	require.Equal(t, 7, observedAfter)

	afterBarrierOrder := fixture.newOrder(payment.OrderTypeBalance)
	afterBarrierSource := fixture.insertSource(afterBarrierOrder, 0, "none", 7, 9)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT before_concurrency, after_concurrency FROM sub2api_apply_payment_concurrency_max($1,$2,$3)`,
		fixture.user.ID, 9, afterBarrierSource).Scan(&before, &after))
	require.Equal(t, 7, before)
	require.Equal(t, 9, after)

	refundNo := fixture.insertAttempt(afterBarrierOrder.ID)
	reservePaymentRefundBenefitSource(t, ctx, afterBarrierSource, refundNo)
	// The later payment source is replayable from the raw observed value. Its
	// reservation removes only its own cap and leaves the barrier's actual 7.
	require.Equal(t, 7, recomputePaymentRefundBenefitConcurrency(t, ctx, fixture.user.ID))
	releasePaymentRefundBenefitSource(t, ctx, afterBarrierSource, refundNo)
	require.Equal(t, 9, recomputePaymentRefundBenefitConcurrency(t, ctx, fixture.user.ID))
}

func (f *paymentRefundBenefitPGFixture) insertDirectGrant(orderID *int64, issuanceID *int64, quantity int) int64 {
	f.t.Helper()
	f.ensureSubscription()
	var paymentOrderID any
	if orderID != nil {
		paymentOrderID = *orderID
	}
	var scheduleIssuanceID any
	if issuanceID != nil {
		scheduleIssuanceID = *issuanceID
	}
	var grantID int64
	err := integrationDB.QueryRowContext(f.ctx, `INSERT INTO subscription_reset_grants (
		subscription_id, user_id, group_id, quantity, used_count, expires_at,
		payment_order_id, schedule_issuance_id, tier_snapshot_resolved, created_at, updated_at
	) VALUES ($1,$2,$3,$4,0,$5,$6,$7,TRUE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) RETURNING id`,
		f.sub.ID, f.user.ID, f.group.ID, quantity, time.Now().UTC().Add(24*time.Hour), paymentOrderID, scheduleIssuanceID).Scan(&grantID)
	require.NoError(f.t, err)
	return grantID
}

func (f *paymentRefundBenefitPGFixture) insertMonthlySchedule(orderID int64, occurrences int) int64 {
	f.t.Helper()
	f.ensureSubscription()
	now := time.Now().UTC().Truncate(time.Microsecond)
	anchorDay := now.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Day()
	var scheduleID int64
	err := integrationDB.QueryRowContext(f.ctx, `INSERT INTO subscription_reset_card_schedules (
		payment_order_id, subscription_id, user_id, group_id, source_plan_id,
		anchor_at, anchor_timezone, anchor_day, term_end_at, occurrence_count,
		cards_per_occurrence, card_validity_days, next_occurrence, next_due_at, status
	) VALUES ($1,$2,$3,$4,1,$5,'Asia/Shanghai',$6,$7,$8,1,14,0,$5,'active') RETURNING id`,
		orderID, f.sub.ID, f.user.ID, f.group.ID, now, anchorDay, now.AddDate(0, occurrences, 1), occurrences).Scan(&scheduleID)
	require.NoError(f.t, err)
	return scheduleID
}

type paymentRefundBenefitQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func insertMonthlyIssuance(t *testing.T, ctx context.Context, executor paymentRefundBenefitQueryRower, scheduleID int64, occurrence int) int64 {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	var issuanceID int64
	require.NoError(t, executor.QueryRowContext(ctx, `INSERT INTO subscription_reset_card_issuances (
		schedule_id, occurrence_index, due_at, window_end_at, status, skip_reason, issued_at
	) VALUES ($1,$2,$3,$4,'issued',NULL,$3) RETURNING id`,
		scheduleID, occurrence, now, now.Add(24*time.Hour)).Scan(&issuanceID))
	return issuanceID
}

func insertScheduledGrant(t *testing.T, ctx context.Context, executor paymentRefundBenefitQueryRower, fixture *paymentRefundBenefitPGFixture, issuanceID int64) int64 {
	t.Helper()
	var grantID int64
	require.NoError(t, executor.QueryRowContext(ctx, `INSERT INTO subscription_reset_grants (
		subscription_id, user_id, group_id, quantity, used_count, expires_at,
		schedule_issuance_id, tier_snapshot_resolved, created_at, updated_at
	) VALUES ($1,$2,$3,1,0,$4,$5,TRUE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) RETURNING id`,
		fixture.sub.ID, fixture.user.ID, fixture.group.ID, time.Now().UTC().Add(24*time.Hour), issuanceID).Scan(&grantID))
	return grantID
}

func TestPaymentRefundBenefitPostgresSourceMapsCardsAndBlocksUseWhileHeld(t *testing.T) {
	fixture := newPaymentRefundBenefitPGFixture(t, 2)
	fixture.ensureSubscription()
	ctx := fixture.ctx
	order := fixture.newOrder(payment.OrderTypeSubscription)
	sourceID := fixture.insertSource(order, 2, "immediate", 0, 0)
	orderID := order.ID
	grantID := fixture.insertDirectGrant(&orderID, nil, 2)

	var mapped int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM payment_refund_benefit_reset_card_grants
		WHERE source_id = $1 AND reset_card_grant_id = $2`, sourceID, grantID).Scan(&mapped))
	require.Equal(t, 1, mapped)

	refundNo := fixture.insertAttempt(order.ID)
	// Hold the source in one transaction. The old consumer path locks the card
	// first; the source trigger's FOR SHARE must wait, then reject it after the
	// refund commits, rather than allowing a use to slip through.
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE payment_refund_benefit_sources
		SET state = 'RESERVED', reserved_product_refund_no = $2,
			reservation_proof_digest = $3, reserved_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1`, sourceID, refundNo, strings.Repeat("b", 64))
	require.NoError(t, err)
	usedResult := make(chan error, 1)
	go func() {
		_, useErr := integrationDB.ExecContext(context.Background(),
			`UPDATE subscription_reset_grants SET used_count = used_count + 1 WHERE id = $1`, grantID)
		usedResult <- useErr
	}()
	select {
	case useErr := <-usedResult:
		t.Fatalf("card use must wait for held source lock, returned early: %v", useErr)
	case <-time.After(125 * time.Millisecond):
	}
	require.NoError(t, tx.Commit())
	useErr := <-usedResult
	require.Error(t, useErr)
	require.Contains(t, useErr.Error(), "held or revoked")

	releasePaymentRefundBenefitSource(t, ctx, sourceID, refundNo)
	_, err = integrationDB.ExecContext(ctx, `UPDATE subscription_reset_grants SET used_count = used_count + 1 WHERE id = $1`, grantID)
	require.NoError(t, err)

	// A reset-card purchase can carry payment_order_id too. The source-link
	// trigger must still leave it alone because it is a purchased card, not a
	// subscription entitlement gift.
	externalOrder := fixture.newOrder(payment.OrderTypeResetCard)
	externalSource := fixture.insertSource(externalOrder, 1, "immediate", 0, 0)
	externalOrderID := externalOrder.ID
	fixture.insertDirectGrant(&externalOrderID, nil, 1)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM payment_refund_benefit_reset_card_grants WHERE source_id = $1`, externalSource).Scan(&mapped))
	require.Zero(t, mapped)
}

func TestPaymentRefundBenefitPostgresMonthlyHoldRetainsFutureGiftAndRevocationBlocksIt(t *testing.T) {
	fixture := newPaymentRefundBenefitPGFixture(t, 2)
	fixture.ensureSubscription()
	ctx := fixture.ctx
	order := fixture.newOrder(payment.OrderTypeSubscription)
	sourceID := fixture.insertSource(order, 3, "monthly", 0, 0)
	scheduleID := fixture.insertMonthlySchedule(order.ID, 3)
	firstIssuance := insertMonthlyIssuance(t, ctx, integrationDB, scheduleID, 0)
	insertScheduledGrant(t, ctx, integrationDB, fixture, firstIssuance)

	refundNo := fixture.insertAttempt(order.ID)
	reservePaymentRefundBenefitSource(t, ctx, sourceID, refundNo)

	// The scheduler writes the issuance and grant in one transaction. A held
	// source rolls the entire append back, preserving the active schedule and
	// its future window for a later failed-refund release.
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	secondIssuance := insertMonthlyIssuance(t, ctx, tx, scheduleID, 1)
	var ignored int64
	err = tx.QueryRowContext(ctx, `INSERT INTO subscription_reset_grants (
		subscription_id, user_id, group_id, quantity, used_count, expires_at,
		schedule_issuance_id, tier_snapshot_resolved, created_at, updated_at
	) VALUES ($1,$2,$3,1,0,$4,$5,TRUE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) RETURNING id`,
		fixture.sub.ID, fixture.user.ID, fixture.group.ID, time.Now().UTC().Add(24*time.Hour), secondIssuance).Scan(&ignored)
	require.Error(t, err)
	require.Contains(t, err.Error(), "held or revoked")
	require.NoError(t, tx.Rollback())

	var status string
	var nextOccurrence int
	var issuances int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT status, next_occurrence FROM subscription_reset_card_schedules WHERE id = $1`, scheduleID).Scan(&status, &nextOccurrence))
	require.Equal(t, "active", status)
	require.Zero(t, nextOccurrence)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM subscription_reset_card_issuances WHERE schedule_id = $1`, scheduleID).Scan(&issuances))
	require.Equal(t, 1, issuances)

	releasePaymentRefundBenefitSource(t, ctx, sourceID, refundNo)
	tx, err = integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	secondIssuance = insertMonthlyIssuance(t, ctx, tx, scheduleID, 1)
	insertScheduledGrant(t, ctx, tx, fixture, secondIssuance)
	require.NoError(t, tx.Commit())

	reservePaymentRefundBenefitSource(t, ctx, sourceID, refundNo)
	_, err = integrationDB.ExecContext(ctx, `UPDATE payment_refund_benefit_sources
		SET state = 'REVOKED', revoked_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND reserved_product_refund_no = $2`, sourceID, refundNo)
	require.NoError(t, err)
	tx, err = integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	thirdIssuance := insertMonthlyIssuance(t, ctx, tx, scheduleID, 2)
	err = tx.QueryRowContext(ctx, `INSERT INTO subscription_reset_grants (
		subscription_id, user_id, group_id, quantity, used_count, expires_at,
		schedule_issuance_id, tier_snapshot_resolved, created_at, updated_at
	) VALUES ($1,$2,$3,1,0,$4,$5,TRUE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) RETURNING id`,
		fixture.sub.ID, fixture.user.ID, fixture.group.ID, time.Now().UTC().Add(24*time.Hour), thirdIssuance).Scan(&ignored)
	require.Error(t, err)
	require.Contains(t, err.Error(), "held or revoked")
	require.NoError(t, tx.Rollback())

	var linked int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM payment_refund_benefit_reset_card_grants WHERE source_id = $1`, sourceID).Scan(&linked))
	require.Equal(t, 2, linked)
}
