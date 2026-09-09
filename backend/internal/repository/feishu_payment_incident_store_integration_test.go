//go:build integration

package repository

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestFeishuPaymentIncidentStorePostgresConcurrentOpenClaimRecoveryAndReopen(t *testing.T) {
	ctx := context.Background()
	fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
	user := fixture.createUser(0)
	order := fixture.createPaidBalanceOrder(user, 7, service.OrderStatusPaid, time.Now().UTC())
	cleanupFeishuPaymentIncidentRows(t, order.ID)
	store := NewFeishuPaymentIncidentStore(integrationDB)
	now := time.Now().UTC().Truncate(time.Microsecond)
	var beforeStatus string
	var beforeUpdatedAt time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT status, updated_at FROM payment_orders WHERE id = $1
	`, order.ID).Scan(&beforeStatus, &beforeUpdatedAt))

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.Observe(ctx, service.FeishuPaymentIncidentPaidIncomplete, order.ID, now)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	var incidents, deliveries int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM feishu_payment_incidents
		WHERE incident_key = $1
	`, "paid-incomplete:"+int64String(order.ID)).Scan(&incidents))
	require.Equal(t, 1, incidents)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM feishu_payment_incident_deliveries AS delivery
		JOIN feishu_payment_incidents AS incident ON incident.id = delivery.incident_id
		WHERE incident.incident_key = $1 AND delivery.delivery_kind = 'OPEN'
	`, "paid-incomplete:"+int64String(order.ID)).Scan(&deliveries))
	require.Equal(t, 1, deliveries)
	var afterStatus string
	var afterUpdatedAt time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT status, updated_at FROM payment_orders WHERE id = $1
	`, order.ID).Scan(&afterStatus, &afterUpdatedAt))
	require.Equal(t, beforeStatus, afterStatus)
	require.Equal(t, beforeUpdatedAt, afterUpdatedAt, "incident discovery is read-only for payment state")

	// Two independent store instances race for the same durable delivery. The
	// PostgreSQL SKIP LOCKED claim and token CAS must expose it to one sender.
	storeA := NewFeishuPaymentIncidentStore(integrationDB)
	storeB := NewFeishuPaymentIncidentStore(integrationDB)
	type claimResult struct {
		deliveries []service.FeishuPaymentDelivery
		err        error
	}
	claims := make(chan claimResult, 2)
	for _, candidate := range []service.FeishuPaymentIncidentStore{storeA, storeB} {
		candidate := candidate
		go func() {
			claimed, err := candidate.ClaimDue(ctx, 1, 30*time.Second)
			claims <- claimResult{deliveries: claimed, err: err}
		}()
	}
	first, second := <-claims, <-claims
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	claimed := append(first.deliveries, second.deliveries...)
	require.Len(t, claimed, 1)
	require.Equal(t, 1, claimed[0].Attempts)
	firstToken := claimed[0].ClaimToken

	// A worker crash leaves the claim recoverable after its database-authoritative
	// lease. A stale token cannot acknowledge or overwrite the successor's
	// delivery state.
	_, err := integrationDB.ExecContext(ctx, `
		UPDATE feishu_payment_incident_deliveries
		SET lease_expires_at = clock_timestamp() - INTERVAL '1 second'
		WHERE id = $1::uuid
	`, claimed[0].ID)
	require.NoError(t, err)
	recovered, err := store.ClaimDue(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, recovered, 1)
	require.Equal(t, claimed[0].ID, recovered[0].ID)
	require.NotEqual(t, firstToken, recovered[0].ClaimToken)
	require.Equal(t, 2, recovered[0].Attempts)
	require.ErrorIs(t, store.MarkDelivered(ctx, claimed[0].ID, firstToken), service.ErrFeishuPaymentDeliveryLeaseLost)
	var status string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status FROM feishu_payment_incident_deliveries WHERE id = $1::uuid`, claimed[0].ID).Scan(&status))
	require.Equal(t, "CLAIMED", status)
	require.NoError(t, store.MarkDelivered(ctx, recovered[0].ID, recovered[0].ClaimToken))

	var incidentID string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT id::text FROM feishu_payment_incidents WHERE incident_key = $1 AND status = 'OPEN'
	`, "paid-incomplete:"+int64String(order.ID)).Scan(&incidentID))
	reminderAt := now.Add(time.Hour + time.Minute)
	require.NoError(t, store.TouchOpen(ctx, incidentID, reminderAt))
	require.NoError(t, store.TouchOpen(ctx, incidentID, reminderAt))
	var reminders int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM feishu_payment_incident_deliveries
		WHERE incident_id = $1::uuid AND delivery_kind = 'REMINDER' AND status = 'PENDING'
	`, incidentID).Scan(&reminders))
	require.Equal(t, 1, reminders, "hourly reminder creates at most one outstanding delivery")
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE feishu_payment_incident_deliveries
		SET not_before = clock_timestamp() - INTERVAL '1 second'
		WHERE incident_id = $1::uuid AND delivery_kind = 'REMINDER' AND status = 'PENDING'
	`, incidentID)
	require.NoError(t, err)
	claimedReminder, err := store.ClaimDue(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, claimedReminder, 1)
	require.Equal(t, service.FeishuPaymentDeliveryReminder, claimedReminder[0].Kind)
	require.NoError(t, store.Resolve(ctx, incidentID, time.Now().UTC().Truncate(time.Microsecond)))
	var reminderStatus string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT status FROM feishu_payment_incident_deliveries
		WHERE incident_id = $1::uuid AND delivery_kind = 'REMINDER'
	`, incidentID).Scan(&reminderStatus))
	require.Equal(t, "SUPPRESSED", reminderStatus, "resolution suppresses an obsolete claimed reminder")
	resolved, err := store.ClaimDue(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	require.Equal(t, service.FeishuPaymentDeliveryResolved, resolved[0].Kind)
	require.NoError(t, store.MarkDelivered(ctx, resolved[0].ID, resolved[0].ClaimToken))

	// The same condition may recur after an authoritative resolution. It becomes
	// a new generation rather than mutating the resolved audit history.
	require.NoError(t, store.Observe(ctx, service.FeishuPaymentIncidentPaidIncomplete, order.ID, reminderAt.Add(time.Minute)))
	var generations []int
	rows, err := integrationDB.QueryContext(ctx, `
		SELECT generation FROM feishu_payment_incidents
		WHERE incident_key = $1 ORDER BY generation
	`, "paid-incomplete:"+int64String(order.ID))
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var generation int
		require.NoError(t, rows.Scan(&generation))
		generations = append(generations, generation)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []int{1, 2}, generations)
}

func TestFeishuPaymentIncidentStorePostgresExpiredLeaseFencesSendAndTransitions(t *testing.T) {
	ctx := context.Background()
	fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
	user := fixture.createUser(0)
	store := NewFeishuPaymentIncidentStore(integrationDB)

	// A live lease may schedule its retry. This executes the same
	// database-authoritative clock path used after a failed Feishu request.
	retryOrder := fixture.createPaidBalanceOrder(user, 6, service.OrderStatusPaid, time.Now().UTC())
	cleanupFeishuPaymentIncidentRows(t, retryOrder.ID)
	require.NoError(t, store.Observe(ctx, service.FeishuPaymentIncidentPaidIncomplete, retryOrder.ID, time.Now().UTC()))
	retryClaim, err := store.ClaimDue(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, retryClaim, 1)
	require.NoError(t, store.MarkFailed(ctx, retryClaim[0].ID, retryClaim[0].ClaimToken, 5*time.Second, "feishu_delivery_failed"))
	var retryStatus string
	var retryNotBefore, retryDatabaseNow time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT status, not_before, clock_timestamp()
		FROM feishu_payment_incident_deliveries WHERE id = $1::uuid
	`, retryClaim[0].ID).Scan(&retryStatus, &retryNotBefore, &retryDatabaseNow))
	require.Equal(t, "PENDING", retryStatus)
	require.True(t, retryNotBefore.After(retryDatabaseNow.Add(3*time.Second)), "retry delay starts from the database completion clock")

	order := fixture.createPaidBalanceOrder(user, 7, service.OrderStatusPaid, time.Now().UTC())
	cleanupFeishuPaymentIncidentRows(t, order.ID)
	require.NoError(t, store.Observe(ctx, service.FeishuPaymentIncidentPaidIncomplete, order.ID, time.Now().UTC()))

	claimed, err := store.ClaimDue(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	first := claimed[0]
	canSend, err := store.CanSend(ctx, first.ID, first.ClaimToken)
	require.NoError(t, err)
	require.True(t, canSend)
	var leaseExpiresAt, databaseNow time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT lease_expires_at, clock_timestamp()
		FROM feishu_payment_incident_deliveries WHERE id = $1::uuid
	`, first.ID).Scan(&leaseExpiresAt, &databaseNow))
	require.True(t, leaseExpiresAt.After(databaseNow.Add(10*time.Second)), "claim lease begins from the database claim clock")

	// Expire this owner without allowing a peer to reclaim it yet. The sender
	// fence must reject outbound work and both terminal transitions must leave
	// the durable row untouched.
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE feishu_payment_incident_deliveries
		SET lease_expires_at = clock_timestamp() - INTERVAL '1 second'
		WHERE id = $1::uuid
	`, first.ID)
	require.NoError(t, err)
	canSend, err = store.CanSend(ctx, first.ID, first.ClaimToken)
	require.NoError(t, err)
	require.False(t, canSend)
	require.ErrorIs(t, store.MarkDelivered(ctx, first.ID, first.ClaimToken), service.ErrFeishuPaymentDeliveryLeaseLost)
	require.ErrorIs(t, store.MarkFailed(ctx, first.ID, first.ClaimToken, time.Minute, "feishu_delivery_failed"), service.ErrFeishuPaymentDeliveryLeaseLost)
	var status, claimToken string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT status, claim_token::text FROM feishu_payment_incident_deliveries WHERE id = $1::uuid
	`, first.ID).Scan(&status, &claimToken))
	require.Equal(t, "CLAIMED", status)
	require.Equal(t, first.ClaimToken, claimToken)

	// A subsequent claimant may reclaim the expired delivery. Its new token is
	// the only one that can complete it; the original owner remains fenced.
	reclaimed, err := store.ClaimDue(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, reclaimed, 1)
	require.Equal(t, first.ID, reclaimed[0].ID)
	require.NotEqual(t, first.ClaimToken, reclaimed[0].ClaimToken)
	require.Equal(t, first.Attempts+1, reclaimed[0].Attempts)
	require.ErrorIs(t, store.MarkDelivered(ctx, first.ID, first.ClaimToken), service.ErrFeishuPaymentDeliveryLeaseLost)
	require.NoError(t, store.MarkDelivered(ctx, reclaimed[0].ID, reclaimed[0].ClaimToken))
}

func TestFeishuPaymentIncidentStorePostgresSourcesPaidAtAndFairBoundedScans(t *testing.T) {
	ctx := context.Background()
	fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
	user := fixture.createUser(0)
	orders := []*struct{ id int64 }{}
	for range 3 {
		order := fixture.createPaidBalanceOrder(user, 3, service.OrderStatusPaid, time.Now().UTC())
		cleanupFeishuPaymentIncidentRows(t, order.ID)
		orders = append(orders, &struct{ id int64 }{id: order.ID})
		// The current update time must not hide an order whose trusted paid_at is
		// older than ten minutes.
		_, err := integrationDB.ExecContext(ctx, `
			UPDATE payment_orders SET paid_at = $2, updated_at = $3 WHERE id = $1
		`, order.ID, time.Now().UTC().Add(-100*365*24*time.Hour), time.Now().UTC())
		require.NoError(t, err)
	}
	store := NewFeishuPaymentIncidentStore(integrationDB)
	now := time.Now().UTC().Truncate(time.Microsecond)
	first, err := store.ListPaidIncompleteCandidates(ctx, now.Add(-10*time.Minute), 2)
	require.NoError(t, err)
	require.Len(t, first, 2)
	for _, orderID := range first {
		require.NoError(t, store.Observe(ctx, service.FeishuPaymentIncidentPaidIncomplete, orderID, now))
	}
	second, err := store.ListPaidIncompleteCandidates(ctx, now.Add(-10*time.Minute), 2)
	require.NoError(t, err)
	remaining := remainingOrderID(orders, first)
	require.Contains(t, second, remaining, "updated incidents must yield to an unobserved eligible order")

	orderID := orders[0].id
	state, err := store.LoadRefundFenceState(ctx, orderID)
	require.NoError(t, err)
	require.False(t, state.Fenced)
	insertFeishuRefundAttempt(t, orderID, true)
	state, err = store.LoadRefundFenceState(ctx, orderID)
	require.NoError(t, err)
	require.True(t, state.Fenced)
	_, err = integrationDB.ExecContext(ctx, `UPDATE unified_payment_refund_attempts SET needs_manual_review = FALSE WHERE order_id = $1`, orderID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO unified_payment_refund_events (id, order_id, action, detail)
		VALUES ($1::uuid, $2, 'UNIFIED_REFUND_UNCORRELATED', '{}')
	`, uuid.NewString(), orderID)
	require.NoError(t, err)
	state, err = store.LoadRefundFenceState(ctx, orderID)
	require.NoError(t, err)
	require.True(t, state.Fenced, "immutable refund evidence remains a fence after a mutable flag is cleared")
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM unified_payment_refund_events WHERE order_id = $1`, orderID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO payment_audit_logs (order_id, action, detail, operator)
		VALUES ($1, 'UNIFIED_PAYMENT_EVENT_REJECTED', '{}', 'test')
	`, int64String(orderID))
	require.NoError(t, err)
	state, err = store.LoadRefundFenceState(ctx, orderID)
	require.NoError(t, err)
	require.True(t, state.Fenced, "payment audit rejection is durable fence evidence too")
	require.False(t, state.RefundTerminal)
	_, err = integrationDB.ExecContext(ctx, `UPDATE payment_orders SET status = 'REFUND_PENDING' WHERE id = $1`, orderID)
	require.NoError(t, err)
	state, err = store.LoadRefundFenceState(ctx, orderID)
	require.NoError(t, err)
	require.False(t, state.RefundTerminal, "pending refund is not a resolution")
	_, err = integrationDB.ExecContext(ctx, `UPDATE payment_orders SET status = 'REFUNDED' WHERE id = $1`, orderID)
	require.NoError(t, err)
	state, err = store.LoadRefundFenceState(ctx, orderID)
	require.NoError(t, err)
	require.True(t, state.RefundTerminal)
}

func TestFeishuPaymentIncidentStorePostgresObserveRollsBackIncidentWithDeliveryFailure(t *testing.T) {
	ctx := context.Background()
	fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
	user := fixture.createUser(0)
	order := fixture.createPaidBalanceOrder(user, 5, service.OrderStatusPaid, time.Now().UTC())
	cleanupFeishuPaymentIncidentRows(t, order.ID)
	_, err := integrationDB.ExecContext(ctx, `
		CREATE OR REPLACE FUNCTION feishu_payment_incident_test_reject_delivery()
		RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test delivery insert failure'; END; $$;
		CREATE TRIGGER feishu_payment_incident_test_reject_delivery
		BEFORE INSERT ON feishu_payment_incident_deliveries
		FOR EACH ROW EXECUTE FUNCTION feishu_payment_incident_test_reject_delivery();
	`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS feishu_payment_incident_test_reject_delivery ON feishu_payment_incident_deliveries`)
		_, _ = integrationDB.ExecContext(context.Background(), `DROP FUNCTION IF EXISTS feishu_payment_incident_test_reject_delivery()`)
	})
	store := NewFeishuPaymentIncidentStore(integrationDB)
	err = store.Observe(ctx, service.FeishuPaymentIncidentPaidIncomplete, order.ID, time.Now().UTC())
	require.Error(t, err)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM feishu_payment_incidents WHERE incident_key = $1
	`, "paid-incomplete:"+int64String(order.ID)).Scan(&count))
	require.Zero(t, count, "incident and first delivery must commit atomically")
}

func cleanupFeishuPaymentIncidentRows(t *testing.T, orderID int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, err := integrationDB.ExecContext(ctx, `
			DELETE FROM feishu_payment_incident_deliveries
			WHERE incident_id IN (SELECT id FROM feishu_payment_incidents WHERE subject_order_id = $1)
		`, orderID)
		if err != nil {
			t.Errorf("clean feishu payment deliveries for %d: %v", orderID, err)
		}
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM feishu_payment_incidents WHERE subject_order_id = $1`, orderID)
		if err != nil {
			t.Errorf("clean feishu payment incidents for %d: %v", orderID, err)
		}
	})
}

func insertFeishuRefundAttempt(t *testing.T, orderID int64, needsReview bool) {
	t.Helper()
	_, err := integrationDB.ExecContext(context.Background(), `
		INSERT INTO unified_payment_refund_attempts (
			product_refund_no, order_id, payment_order_id, idempotency_key, environment,
			organization_id, product_id, app_id, payment_method, amount_fen,
			balance_amount_minor, deduct_balance, force_refund, reason_summary, status, needs_manual_review
		) VALUES ($1, $2, $3::uuid, $4, 'sandbox', $5::uuid, $6::uuid, 'app.test', 'alipay', 100,
			100, FALSE, FALSE, 'test', 'PENDING', $7)
	`, "refund-"+uuid.NewString(), orderID, uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), needsReview)
	require.NoError(t, err)
}

func remainingOrderID(orders []*struct{ id int64 }, observed []int64) int64 {
	seen := make(map[int64]struct{}, len(observed))
	for _, id := range observed {
		seen[id] = struct{}{}
	}
	for _, order := range orders {
		if _, ok := seen[order.id]; !ok {
			return order.id
		}
	}
	return 0
}

func int64String(value int64) string {
	return strconv.FormatInt(value, 10)
}
