package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// paymentRefundReconciliationStore owns only worker lease metadata. Financial
// state and provider correlation stay in the existing refund attempt and
// entitlement transactions owned by PaymentService.
type paymentRefundReconciliationStore struct {
	db *sql.DB
}

func NewPaymentRefundReconciliationStore(db *sql.DB) service.PaymentRefundReconciliationStore {
	return &paymentRefundReconciliationStore{db: db}
}

func (r *paymentRefundReconciliationStore) ClaimReviewedEntitlementReservations(
	ctx context.Context,
	workerID string,
	limit int,
	lease time.Duration,
) ([]service.PaymentRefundReconciliationCandidate, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("payment refund reconciliation database unavailable")
	}
	if workerID == "" {
		return nil, errors.New("payment refund reconciliation worker id is required")
	}
	if limit <= 0 {
		limit = 1
	}
	leaseSeconds := int64(lease / time.Second)
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}

	rows, err := r.db.QueryContext(ctx, `
		WITH candidates AS (
			SELECT product_refund_no
			FROM unified_payment_refund_attempts
			WHERE status = 'PENDING'
			  AND entitlement_reserved = TRUE
			  AND needs_manual_review = FALSE
			  AND quote_revision <> ''
			  AND refund_kind IN ('balance', 'subscription')
			  AND reconciliation_available_at <= NOW()
			  AND (
				reconciliation_claimed_at IS NULL
				OR reconciliation_claimed_at < NOW() - ($3 * INTERVAL '1 second')
			  )
			ORDER BY reconciliation_available_at ASC, created_at ASC, product_refund_no ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE unified_payment_refund_attempts AS attempt
		SET reconciliation_claimed_at = NOW(),
			reconciliation_claimed_by = $1,
			reconciliation_updated_at = NOW()
		FROM candidates
		WHERE attempt.product_refund_no = candidates.product_refund_no
		RETURNING attempt.product_refund_no, attempt.order_id, attempt.reconciliation_attempts
	`, workerID, limit, leaseSeconds)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	candidates := make([]service.PaymentRefundReconciliationCandidate, 0, limit)
	for rows.Next() {
		var candidate service.PaymentRefundReconciliationCandidate
		if err := rows.Scan(&candidate.ProductRefundNo, &candidate.OrderID, &candidate.Attempts); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func (r *paymentRefundReconciliationStore) CompleteClaim(
	ctx context.Context,
	productRefundNo string,
	workerID string,
	lease time.Duration,
) error {
	return r.updateClaim(ctx, productRefundNo, workerID, lease, `
		SET reconciliation_claimed_at = NULL,
			reconciliation_claimed_by = NULL,
			reconciliation_last_error = NULL,
			reconciliation_updated_at = NOW()
	`)
}

func (r *paymentRefundReconciliationStore) RetryClaim(
	ctx context.Context,
	productRefundNo string,
	workerID string,
	lease time.Duration,
	availableAt time.Time,
	lastError string,
) error {
	if r == nil || r.db == nil {
		return errors.New("payment refund reconciliation database unavailable")
	}
	if productRefundNo == "" || workerID == "" {
		return errors.New("payment refund reconciliation claim identity is required")
	}
	leaseSeconds := int64(lease / time.Second)
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE unified_payment_refund_attempts
		SET reconciliation_attempts = reconciliation_attempts + 1,
			reconciliation_available_at = $4,
			reconciliation_claimed_at = NULL,
			reconciliation_claimed_by = NULL,
			reconciliation_last_error = $5,
			reconciliation_updated_at = NOW()
		WHERE product_refund_no = $1
		  AND reconciliation_claimed_by = $2
		  AND reconciliation_claimed_at >= NOW() - ($3 * INTERVAL '1 second')
	`, productRefundNo, workerID, leaseSeconds, availableAt.UTC(), lastError)
	if err != nil {
		return err
	}
	return requirePaymentRefundReconciliationClaim(result, productRefundNo, "retry")
}

func (r *paymentRefundReconciliationStore) updateClaim(
	ctx context.Context,
	productRefundNo string,
	workerID string,
	lease time.Duration,
	setClause string,
) error {
	if r == nil || r.db == nil {
		return errors.New("payment refund reconciliation database unavailable")
	}
	if productRefundNo == "" || workerID == "" {
		return errors.New("payment refund reconciliation claim identity is required")
	}
	leaseSeconds := int64(lease / time.Second)
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE unified_payment_refund_attempts
		`+setClause+`
		WHERE product_refund_no = $1
		  AND reconciliation_claimed_by = $2
		  AND reconciliation_claimed_at >= NOW() - ($3 * INTERVAL '1 second')
	`, productRefundNo, workerID, leaseSeconds)
	if err != nil {
		return err
	}
	return requirePaymentRefundReconciliationClaim(result, productRefundNo, "complete")
}

func (r *paymentRefundReconciliationStore) Stats(ctx context.Context) (service.PaymentRefundReconciliationStats, error) {
	if r == nil || r.db == nil {
		return service.PaymentRefundReconciliationStats{}, errors.New("payment refund reconciliation database unavailable")
	}
	var (
		stats     service.PaymentRefundReconciliationStats
		oldest    sql.NullTime
		lastError sql.NullString
	)
	err := r.db.QueryRowContext(ctx, `
		WITH scoped AS (
			SELECT product_refund_no, status, needs_manual_review, created_at,
				reconciliation_attempts, reconciliation_last_error,
				reconciliation_updated_at
			FROM unified_payment_refund_attempts
			WHERE entitlement_reserved = TRUE
			  AND quote_revision <> ''
			  AND refund_kind IN ('balance', 'subscription')
		)
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'PENDING' AND needs_manual_review = FALSE),
			MIN(created_at),
			COALESCE(MAX(reconciliation_attempts), 0),
			(
				SELECT reconciliation_last_error
				FROM scoped
				WHERE reconciliation_last_error IS NOT NULL
				ORDER BY reconciliation_updated_at DESC, product_refund_no DESC
				LIMIT 1
			),
			(
				SELECT COUNT(*) FROM payment_orders
				WHERE order_type = 'reset_card'
				  AND COALESCE(product_snapshot->>'schema_version', '1') <> '1'
				  AND NOT (
					status IN ('COMPLETED', 'REFUNDED', 'PARTIALLY_REFUNDED')
					OR (status IN ('CANCELLED', 'EXPIRED') AND paid_at IS NULL)
				  )
			)
		FROM scoped
	`).Scan(
		&stats.EntitlementReservedReviewedPending,
		&stats.AutomaticallyReconciledPending,
		&oldest,
		&stats.MaxAttempts,
		&lastError,
		&stats.UnsettledResetCardPurchaseCount,
	)
	if err != nil {
		return stats, err
	}
	if oldest.Valid {
		value := oldest.Time.UTC()
		stats.OldestCreatedAt = &value
	}
	if lastError.Valid {
		stats.LastError = lastError.String
	}
	return stats, nil
}

func requirePaymentRefundReconciliationClaim(result sql.Result, productRefundNo, action string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("payment refund reconciliation claim %q cannot %s", productRefundNo, action)
	}
	return nil
}
