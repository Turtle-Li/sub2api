//go:build integration

package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPaymentRefundReconciliationStorePostgresLeaseReclaimFencesStaleWorker(t *testing.T) {
	ctx := context.Background()
	fixture := newPaymentFulfillmentRecoveryPostgresFixture(t)
	user := fixture.createUser(0)
	order := fixture.createPaidBalanceOrder(user, 1, service.OrderStatusCompleted, time.Now().UTC())
	refundNo := "refund-reconcile-" + uuid.NewString()

	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO unified_payment_refund_attempts (
			product_refund_no, order_id, payment_order_id, idempotency_key, environment,
			organization_id, product_id, app_id, payment_method, amount_fen,
			balance_amount_minor, deduct_balance, force_refund, reason_summary, status,
			refund_kind, quote_revision, entitlement_reserved
		) VALUES ($1, $2, $3::uuid, $4, 'sandbox', $5::uuid, $6::uuid, 'app.test', 'alipay', 100,
			100, FALSE, FALSE, 'reconciliation test', 'PENDING', 'balance', 'reviewed-quote-test', TRUE)
	`, refundNo, order.ID, uuid.NewString(), "idempotency-"+uuid.NewString(), uuid.NewString(), uuid.NewString())
	require.NoError(t, err)

	storeA := NewPaymentRefundReconciliationStore(integrationDB)
	storeB := NewPaymentRefundReconciliationStore(integrationDB)
	type claimResult struct {
		candidates []service.PaymentRefundReconciliationCandidate
		err        error
	}
	results := make(chan claimResult, 2)
	var wg sync.WaitGroup
	for _, workerID := range []string{"worker-a", "worker-b"} {
		workerID := workerID
		wg.Add(1)
		go func() {
			defer wg.Done()
			store := storeA
			if workerID == "worker-b" {
				store = storeB
			}
			candidates, claimErr := store.ClaimReviewedEntitlementReservations(ctx, workerID, 1, time.Minute)
			results <- claimResult{candidates: candidates, err: claimErr}
		}()
	}
	wg.Wait()
	close(results)

	all := make([]service.PaymentRefundReconciliationCandidate, 0, 1)
	for result := range results {
		require.NoError(t, result.err)
		all = append(all, result.candidates...)
	}
	require.Len(t, all, 1, "SKIP LOCKED must expose a live reservation to one worker")
	require.Equal(t, refundNo, all[0].ProductRefundNo)

	var firstOwner string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT reconciliation_claimed_by FROM unified_payment_refund_attempts WHERE product_refund_no = $1
	`, refundNo).Scan(&firstOwner))
	require.Contains(t, []string{"worker-a", "worker-b"}, firstOwner)

	// Model a process crash. The database clock owns lease expiry, and the
	// successor receives an exclusive new owner token before it can reconcile.
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE unified_payment_refund_attempts
		SET reconciliation_claimed_at = clock_timestamp() - INTERVAL '61 seconds'
		WHERE product_refund_no = $1
	`, refundNo)
	require.NoError(t, err)
	recovered, err := storeA.ClaimReviewedEntitlementReservations(ctx, "worker-recovered", 1, time.Minute)
	require.NoError(t, err)
	require.Len(t, recovered, 1)
	require.Equal(t, refundNo, recovered[0].ProductRefundNo)
	require.Equal(t, all[0].Attempts, recovered[0].Attempts, "only retry scheduling increments attempts")

	// A stale process may neither release nor mutate the recovered worker's
	// lease. This CAS fence prevents a late process from making the reservation
	// immediately available while its successor is querying the provider.
	err = storeB.CompleteClaim(ctx, refundNo, firstOwner, time.Minute)
	require.ErrorContains(t, err, "cannot complete")
	require.NoError(t, storeA.CompleteClaim(ctx, refundNo, "worker-recovered", time.Minute))
}
