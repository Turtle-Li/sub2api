package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type unifiedPaymentWebhookInbox struct{ db *sql.DB }

func NewUnifiedPaymentWebhookInboxStore(db *sql.DB) service.UnifiedWebhookInboxStore {
	return &unifiedPaymentWebhookInbox{db: db}
}

type unifiedInboxRow struct {
	eventID        string
	paymentOrderID string
	productOrderNo string
	sequence       int64
	eventType      string
	bodySHA256     string
	status         string
	updatedAt      time.Time
}

type unifiedInboxCursor struct {
	productOrderNo       string
	maxProcessedSequence int64
	activeEventID        string
	activeSequence       int64
	activeFresh          bool
}

func (r *unifiedPaymentWebhookInbox) Claim(ctx context.Context, record service.UnifiedWebhookInboxRecord, staleAfter time.Duration) (string, error) {
	if r == nil || r.db == nil || staleAfter <= 0 {
		return "", errors.New("unified webhook inbox unavailable")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	cursor, err := lockUnifiedInboxCursor(ctx, tx, record, staleAfter)
	if err != nil {
		return "", err
	}
	if cursor == nil || cursor.productOrderNo != record.ProductOrderNo {
		return commitUnifiedInboxClaim(tx, service.UnifiedWebhookClaimConflict)
	}

	row, err := loadUnifiedInboxRowForUpdate(ctx, tx, record.EventID, record.PaymentOrderID, record.Sequence)
	if err != nil {
		return "", err
	}
	if row != nil && !sameUnifiedInboxIdentity(*row, record) {
		return commitUnifiedInboxClaim(tx, service.UnifiedWebhookClaimConflict)
	}
	if row != nil && (row.status == "PROCESSED" || row.status == "REJECTED") {
		return commitUnifiedInboxClaim(tx, service.UnifiedWebhookClaimDuplicate)
	}

	// The payment-webhook.v1 contract requires a durable per-order maximum
	// sequence. A late lower sequence is evidence we retain, but it must never
	// execute product state transitions after a newer event was committed.
	if record.Sequence <= cursor.maxProcessedSequence {
		if row == nil {
			inserted, insertErr := insertUnifiedInboxRow(ctx, tx, record, "PROCESSED", "obsolete_sequence")
			if insertErr != nil {
				return "", insertErr
			}
			if !inserted {
				return commitUnifiedInboxClaim(tx, service.UnifiedWebhookClaimConflict)
			}
		} else if _, err := tx.ExecContext(ctx, `
			UPDATE unified_payment_webhook_inbox
			SET status = 'PROCESSED', last_error_code = 'obsolete_sequence',
				processed_at = clock_timestamp(), updated_at = clock_timestamp()
			WHERE event_id = $1::uuid
		`, record.EventID); err != nil {
			return "", err
		}
		if err := clearUnifiedInboxCursor(ctx, tx, record.PaymentOrderID, record.EventID); err != nil {
			return "", err
		}
		return commitUnifiedInboxClaim(tx, service.UnifiedWebhookClaimDuplicate)
	}

	if cursor.activeEventID != "" && !strings.EqualFold(cursor.activeEventID, record.EventID) {
		return commitUnifiedInboxClaim(tx, service.UnifiedWebhookClaimBusy)
	}
	if cursor.activeEventID != "" && cursor.activeSequence != record.Sequence {
		return commitUnifiedInboxClaim(tx, service.UnifiedWebhookClaimConflict)
	}
	if row != nil && row.status == "PROCESSING" && cursor.activeEventID != "" && cursor.activeFresh {
		return commitUnifiedInboxClaim(tx, service.UnifiedWebhookClaimBusy)
	}

	if row == nil {
		inserted, insertErr := insertUnifiedInboxRow(ctx, tx, record, "PROCESSING", "")
		if insertErr != nil {
			return "", insertErr
		}
		if !inserted {
			return commitUnifiedInboxClaim(tx, service.UnifiedWebhookClaimConflict)
		}
	} else {
		switch row.status {
		case "PROCESSING", "RETRYABLE_FAILED":
		case "PROCESSED", "REJECTED":
			return commitUnifiedInboxClaim(tx, service.UnifiedWebhookClaimDuplicate)
		default:
			return "", fmt.Errorf("unknown unified webhook inbox status %q", row.status)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE unified_payment_webhook_inbox
			SET status = 'PROCESSING', attempts = attempts + 1,
				last_error_code = NULL, processed_at = NULL, updated_at = clock_timestamp()
			WHERE event_id = $1::uuid
		`, record.EventID); err != nil {
			return "", err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE unified_payment_webhook_cursor
		SET active_event_id = $2::uuid, active_sequence = $3,
			active_updated_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE payment_order_id = $1::uuid
	`, record.PaymentOrderID, record.EventID, record.Sequence); err != nil {
		return "", err
	}
	return commitUnifiedInboxClaim(tx, service.UnifiedWebhookClaimNew)
}

func lockUnifiedInboxCursor(ctx context.Context, tx *sql.Tx, record service.UnifiedWebhookInboxRecord, staleAfter time.Duration) (*unifiedInboxCursor, error) {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO unified_payment_webhook_cursor (payment_order_id, product_order_no)
		VALUES ($1::uuid, $2)
		ON CONFLICT DO NOTHING
	`, record.PaymentOrderID, record.ProductOrderNo); err != nil {
		return nil, err
	}
	cursor := &unifiedInboxCursor{}
	err := tx.QueryRowContext(ctx, `
		SELECT product_order_no, max_processed_sequence,
			COALESCE(active_event_id::text, ''), COALESCE(active_sequence, 0),
			COALESCE(active_updated_at > clock_timestamp() - ($2::bigint * interval '1 millisecond'), false)
		FROM unified_payment_webhook_cursor
		WHERE payment_order_id = $1::uuid
		FOR UPDATE
	`, record.PaymentOrderID, staleAfter.Milliseconds()).Scan(
		&cursor.productOrderNo, &cursor.maxProcessedSequence,
		&cursor.activeEventID, &cursor.activeSequence, &cursor.activeFresh,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cursor, nil
}

func insertUnifiedInboxRow(ctx context.Context, tx *sql.Tx, record service.UnifiedWebhookInboxRecord, status, errorCode string) (bool, error) {
	var insertedID string
	err := tx.QueryRowContext(ctx, `
		INSERT INTO unified_payment_webhook_inbox (
			event_id, payment_order_id, product_order_no, sequence,
			event_type, body_sha256, status, last_error_code,
			occurred_at, processed_at
		) VALUES (
			$1::uuid, $2::uuid, $3, $4, $5, $6, $7::varchar(24), NULLIF($8::varchar(80), ''), $9,
			CASE WHEN $7::varchar(24) IN ('PROCESSED', 'REJECTED') THEN clock_timestamp() ELSE NULL END
		)
		ON CONFLICT DO NOTHING
		RETURNING event_id::text
	`, record.EventID, record.PaymentOrderID, record.ProductOrderNo, record.Sequence,
		record.EventType, record.BodySHA256, status, errorCode, record.OccurredAt).Scan(&insertedID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func commitUnifiedInboxClaim(tx *sql.Tx, claim string) (string, error) {
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return claim, nil
}

func clearUnifiedInboxCursor(ctx context.Context, tx *sql.Tx, paymentOrderID, eventID string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE unified_payment_webhook_cursor
		SET active_event_id = CASE WHEN active_event_id = $2::uuid THEN NULL ELSE active_event_id END,
			active_sequence = CASE WHEN active_event_id = $2::uuid THEN NULL ELSE active_sequence END,
			active_updated_at = CASE WHEN active_event_id = $2::uuid THEN NULL ELSE active_updated_at END,
			updated_at = clock_timestamp()
		WHERE payment_order_id = $1::uuid
	`, paymentOrderID, eventID)
	return err
}

func loadUnifiedInboxRowForUpdate(ctx context.Context, tx *sql.Tx, eventID, paymentOrderID string, sequence int64) (*unifiedInboxRow, error) {
	query := `
		SELECT event_id::text, payment_order_id::text, product_order_no,
			sequence, event_type, body_sha256, status, updated_at
		FROM unified_payment_webhook_inbox
		WHERE event_id = $1::uuid
		FOR UPDATE
	`
	row, err := scanUnifiedInboxRow(ctx, tx, query, eventID)
	if !errors.Is(err, sql.ErrNoRows) {
		return row, err
	}
	query = `
		SELECT event_id::text, payment_order_id::text, product_order_no,
			sequence, event_type, body_sha256, status, updated_at
		FROM unified_payment_webhook_inbox
		WHERE payment_order_id = $1::uuid AND sequence = $2
		FOR UPDATE
	`
	row, err = scanUnifiedInboxRow(ctx, tx, query, paymentOrderID, sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return row, err
}

func scanUnifiedInboxRow(ctx context.Context, tx *sql.Tx, query string, args ...any) (*unifiedInboxRow, error) {
	row := &unifiedInboxRow{}
	err := tx.QueryRowContext(ctx, query, args...).Scan(
		&row.eventID, &row.paymentOrderID, &row.productOrderNo, &row.sequence,
		&row.eventType, &row.bodySHA256, &row.status, &row.updatedAt,
	)
	if err != nil {
		return nil, err
	}
	return row, nil
}

func sameUnifiedInboxIdentity(row unifiedInboxRow, record service.UnifiedWebhookInboxRecord) bool {
	return strings.EqualFold(row.eventID, record.EventID) &&
		strings.EqualFold(row.paymentOrderID, record.PaymentOrderID) &&
		row.productOrderNo == record.ProductOrderNo && row.sequence == record.Sequence &&
		row.eventType == record.EventType && row.bodySHA256 == record.BodySHA256
}

func (r *unifiedPaymentWebhookInbox) MarkProcessed(ctx context.Context, eventID string) error {
	return r.transitionTerminal(ctx, eventID, "PROCESSED", "")
}

func (r *unifiedPaymentWebhookInbox) MarkRejected(ctx context.Context, eventID, errorCode string) error {
	return r.transitionTerminal(ctx, eventID, "REJECTED", errorCode)
}

func (r *unifiedPaymentWebhookInbox) transitionTerminal(ctx context.Context, eventID, status, errorCode string) error {
	if r == nil || r.db == nil || !validInboxErrorCode(errorCode) || (status != "PROCESSED" && status != "REJECTED") {
		return errors.New("invalid unified webhook inbox transition")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	paymentOrderID, err := lookupUnifiedInboxPaymentOrderID(ctx, tx, eventID)
	if err != nil {
		return err
	}
	var activeEventID string
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(active_event_id::text, '')
		FROM unified_payment_webhook_cursor
		WHERE payment_order_id = $1::uuid
		FOR UPDATE
	`, paymentOrderID).Scan(&activeEventID); err != nil {
		return err
	}
	row, err := loadUnifiedInboxRowByEventIDForUpdate(ctx, tx, eventID)
	if err != nil {
		return err
	}
	if row.status == status || (status == "PROCESSED" && row.status == "REJECTED") {
		return tx.Commit()
	}
	if row.status != "PROCESSING" && row.status != "RETRYABLE_FAILED" {
		return fmt.Errorf("unified webhook inbox transition conflict: %s to %s", row.status, status)
	}
	if activeEventID != "" && !strings.EqualFold(activeEventID, eventID) {
		return fmt.Errorf("unified webhook cursor owned by another event")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE unified_payment_webhook_inbox
		SET status = $2, last_error_code = NULLIF($3, ''),
			processed_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE event_id = $1::uuid
	`, eventID, status, errorCode); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE unified_payment_webhook_cursor
		SET max_processed_sequence = GREATEST(max_processed_sequence, $2),
			active_event_id = NULL, active_sequence = NULL, active_updated_at = NULL,
			updated_at = clock_timestamp()
		WHERE payment_order_id = $1::uuid
	`, row.paymentOrderID, row.sequence); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *unifiedPaymentWebhookInbox) MarkRetryableFailure(ctx context.Context, eventID, errorCode string) error {
	if r == nil || r.db == nil || !validInboxErrorCode(errorCode) || errorCode == "" {
		return errors.New("invalid unified webhook inbox transition")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	paymentOrderID, err := lookupUnifiedInboxPaymentOrderID(ctx, tx, eventID)
	if err != nil {
		return err
	}
	var activeEventID string
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(active_event_id::text, '')
		FROM unified_payment_webhook_cursor
		WHERE payment_order_id = $1::uuid
		FOR UPDATE
	`, paymentOrderID).Scan(&activeEventID); err != nil {
		return err
	}
	row, err := loadUnifiedInboxRowByEventIDForUpdate(ctx, tx, eventID)
	if err != nil {
		return err
	}
	if row.status == "RETRYABLE_FAILED" {
		return tx.Commit()
	}
	if row.status != "PROCESSING" {
		return fmt.Errorf("unified webhook inbox transition conflict: %s to RETRYABLE_FAILED", row.status)
	}
	if !strings.EqualFold(activeEventID, eventID) {
		return fmt.Errorf("unified webhook cursor does not own retryable event")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE unified_payment_webhook_inbox
		SET status = 'RETRYABLE_FAILED', last_error_code = $2,
			updated_at = clock_timestamp()
		WHERE event_id = $1::uuid
	`, eventID, errorCode); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE unified_payment_webhook_cursor
		SET active_updated_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE payment_order_id = $1::uuid AND active_event_id = $2::uuid
	`, row.paymentOrderID, eventID); err != nil {
		return err
	}
	return tx.Commit()
}

func loadUnifiedInboxRowByEventIDForUpdate(ctx context.Context, tx *sql.Tx, eventID string) (*unifiedInboxRow, error) {
	return scanUnifiedInboxRow(ctx, tx, `
		SELECT event_id::text, payment_order_id::text, product_order_no,
			sequence, event_type, body_sha256, status, updated_at
		FROM unified_payment_webhook_inbox
		WHERE event_id = $1::uuid
		FOR UPDATE
	`, eventID)
}

func lookupUnifiedInboxPaymentOrderID(ctx context.Context, tx *sql.Tx, eventID string) (string, error) {
	var paymentOrderID string
	if err := tx.QueryRowContext(ctx, `
		SELECT payment_order_id::text
		FROM unified_payment_webhook_inbox
		WHERE event_id = $1::uuid
	`, eventID).Scan(&paymentOrderID); err != nil {
		return "", err
	}
	return paymentOrderID, nil
}

func validInboxErrorCode(value string) bool {
	if len(value) > 80 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}
