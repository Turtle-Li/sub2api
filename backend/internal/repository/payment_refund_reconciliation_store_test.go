package repository

import (
	"context"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestPaymentRefundReconciliationStoreClaimScopesReviewedReservationsAndUsesLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	claimedAt := time.Now().UTC().Truncate(time.Microsecond)
	mock.ExpectQuery("(?s)FROM unified_payment_refund_attempts.*status = 'PENDING'.*needs_manual_review = FALSE.*entitlement_reserved = TRUE.*quote_revision <> ''.*refund_kind IN \\('balance', 'subscription'\\).*refund_kind = 'cancel_late_payment'.*balance_amount_minor = 0.*deduct_balance = FALSE.*reconciliation_available_at <= NOW\\(\\).*FOR UPDATE SKIP LOCKED.*RETURNING").
		WithArgs("worker-a", 2, int64(60)).
		WillReturnRows(sqlmock.NewRows([]string{"product_refund_no", "order_id", "reconciliation_attempts", "reconciliation_claimed_at"}).
			AddRow("sub2-refund-reconciliation-1", int64(7), 3, claimedAt))

	store := NewPaymentRefundReconciliationStore(db)
	candidates, err := store.ClaimReviewedEntitlementReservations(context.Background(), "worker-a", 2, time.Minute)
	require.NoError(t, err)
	require.Equal(t, "sub2-refund-reconciliation-1", candidates[0].ProductRefundNo)
	require.Equal(t, int64(7), candidates[0].OrderID)
	require.Equal(t, 3, candidates[0].Attempts)
	require.Equal(t, claimedAt, candidates[0].ClaimedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPaymentRefundReconciliationStoreFencesStaleLeaseTransitions(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := NewPaymentRefundReconciliationStore(db)
	claimedAt := time.Now().UTC().Truncate(time.Microsecond)
	next := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	mock.ExpectExec("(?s)UPDATE unified_payment_refund_attempts.*reconciliation_attempts = reconciliation_attempts \\+ 1.*reconciliation_claimed_by = \\$2.*reconciliation_claimed_at = \\$4.*reconciliation_claimed_at >= NOW\\(\\) - \\(\\$3 \\* INTERVAL '1 second'\\)").
		WithArgs("sub2-refund-reconciliation-1", "worker-a", int64(60), claimedAt, next, "provider confirmation pending").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.RetryClaim(context.Background(), "sub2-refund-reconciliation-1", "worker-a", claimedAt, time.Minute, next, "provider confirmation pending"))

	// A previous generation cannot clear or alter the successor's lease after a
	// crash/reclaim. RowsAffected=0 is a deliberate CAS fence, not success.
	mock.ExpectExec("(?s)UPDATE unified_payment_refund_attempts.*reconciliation_claimed_by = \\$2.*reconciliation_claimed_at = \\$4.*reconciliation_claimed_at >= NOW\\(\\) - \\(\\$3 \\* INTERVAL '1 second'\\)").
		WithArgs("sub2-refund-reconciliation-1", "stale-worker", int64(60), claimedAt).
		WillReturnResult(sqlmock.NewResult(0, 0))
	err = store.CompleteClaim(context.Background(), "sub2-refund-reconciliation-1", "stale-worker", claimedAt, time.Minute)
	require.ErrorContains(t, err, "cannot complete")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPaymentRefundReconciliationStoreStatsCountEveryReservedReviewedAttempt(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	oldest := time.Now().UTC().Add(-2 * time.Minute)
	mock.ExpectQuery("(?s)WITH scoped AS.*entitlement_reserved = TRUE.*quote_revision <> ''.*refund_kind IN \\('balance', 'subscription'\\).*refund_kind = 'cancel_late_payment'.*payment_local_cancellation_work.*COUNT\\(\\*\\).*COUNT\\(\\*\\) FILTER").
		WillReturnRows(sqlmock.NewRows([]string{"reserved", "late", "automatic", "oldest", "attempts", "last_error", "local_work", "reset_purchases"}).
			AddRow(int64(2), int64(1), int64(1), oldest, 4, "provider confirmation pending", int64(2), int64(3)))

	store := NewPaymentRefundReconciliationStore(db)
	stats, err := store.Stats(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 2, stats.EntitlementReservedReviewedPending)
	require.EqualValues(t, 1, stats.LateCancellationRefundPending)
	require.EqualValues(t, 2, stats.LocalCancellationWorkOutstanding)
	require.EqualValues(t, 3, stats.UnsettledResetCardPurchaseCount)
	require.EqualValues(t, 1, stats.AutomaticallyReconciledPending)
	require.NotNil(t, stats.OldestCreatedAt)
	require.Equal(t, oldest, *stats.OldestCreatedAt)
	require.Equal(t, 4, stats.MaxAttempts)
	require.Equal(t, "provider confirmation pending", stats.LastError)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPaymentRefundReconciliationStoreRejectsZeroGeneration(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := NewPaymentRefundReconciliationStore(db)
	require.ErrorContains(t, store.CompleteClaim(context.Background(), "refund", "worker", time.Time{}, time.Minute), "generation")
	require.ErrorContains(t, store.RetryClaim(context.Background(), "refund", "worker", time.Time{}, time.Minute, time.Now(), "retry"), "generation")
	require.NoError(t, mock.ExpectationsWereMet())
}
