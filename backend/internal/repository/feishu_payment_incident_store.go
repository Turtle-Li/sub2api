package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

// feishuPaymentIncidentStore owns only the durable operational-notification
// tables. It reads payment and refund truth but never mutates financial rows.
type feishuPaymentIncidentStore struct {
	db *sql.DB
}

const feishuPaymentIncidentReminderInterval = time.Hour

func NewFeishuPaymentIncidentStore(db *sql.DB) service.FeishuPaymentIncidentStore {
	return &feishuPaymentIncidentStore{db: db}
}

func (s *feishuPaymentIncidentStore) ListRefundFenceCandidates(ctx context.Context, limit int) ([]int64, error) {
	if err := s.available(limit); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT payment_order.id
		FROM payment_orders AS payment_order
		LEFT JOIN feishu_payment_incidents AS incident
			ON incident.incident_key = ('refund-review:' || payment_order.id::text)
			AND incident.status = 'OPEN'
		WHERE payment_order.status NOT IN ('REFUNDED', 'REFUND_FAILED')
			AND (
				EXISTS (
					SELECT 1 FROM unified_payment_refund_attempts AS refund_attempt
					WHERE refund_attempt.order_id = payment_order.id
						AND refund_attempt.needs_manual_review = TRUE
				)
				OR EXISTS (
					SELECT 1 FROM unified_payment_refund_events AS refund_event
					WHERE refund_event.order_id = payment_order.id
						AND refund_event.action IN ('UNIFIED_REFUND_UNCORRELATED', 'UNIFIED_PAYMENT_EVENT_REJECTED')
				)
				OR EXISTS (
					SELECT 1 FROM payment_audit_logs AS audit_log
					WHERE audit_log.order_id = payment_order.id::text
						AND audit_log.action = 'UNIFIED_PAYMENT_EVENT_REJECTED'
				)
			)
		ORDER BY COALESCE(incident.last_observed_at, payment_order.paid_at, payment_order.created_at), payment_order.id
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanFeishuPaymentOrderIDs(rows)
}

func (s *feishuPaymentIncidentStore) ListPaidIncompleteCandidates(ctx context.Context, before time.Time, limit int) ([]int64, error) {
	if err := s.available(limit); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT payment_order.id
		FROM payment_orders AS payment_order
		LEFT JOIN feishu_payment_incidents AS incident
			ON incident.incident_key = ('paid-incomplete:' || payment_order.id::text)
			AND incident.status = 'OPEN'
		WHERE payment_order.paid_at IS NOT NULL
			AND payment_order.paid_at <= $1
			AND payment_order.status IN ('PAID', 'FAILED', 'RECHARGING')
		ORDER BY COALESCE(incident.last_observed_at, payment_order.paid_at), payment_order.id
		LIMIT $2
	`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanFeishuPaymentOrderIDs(rows)
}

func scanFeishuPaymentOrderIDs(rows *sql.Rows) ([]int64, error) {
	var result []int64
	for rows.Next() {
		var orderID int64
		if err := rows.Scan(&orderID); err != nil {
			return nil, err
		}
		result = append(result, orderID)
	}
	return result, rows.Err()
}

func (s *feishuPaymentIncidentStore) LoadRefundFenceState(ctx context.Context, orderID int64) (service.FeishuPaymentRefundFenceState, error) {
	if err := s.available(1); err != nil {
		return service.FeishuPaymentRefundFenceState{}, err
	}
	var (
		status string
		fenced bool
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT payment_order.status,
			(
				EXISTS (
					SELECT 1 FROM unified_payment_refund_attempts AS refund_attempt
					WHERE refund_attempt.order_id = payment_order.id
						AND refund_attempt.needs_manual_review = TRUE
				)
				OR EXISTS (
					SELECT 1 FROM unified_payment_refund_events AS refund_event
					WHERE refund_event.order_id = payment_order.id
						AND refund_event.action IN ('UNIFIED_REFUND_UNCORRELATED', 'UNIFIED_PAYMENT_EVENT_REJECTED')
				)
				OR EXISTS (
					SELECT 1 FROM payment_audit_logs AS audit_log
					WHERE audit_log.order_id = payment_order.id::text
						AND audit_log.action = 'UNIFIED_PAYMENT_EVENT_REJECTED'
				)
			)
		FROM payment_orders AS payment_order
		WHERE payment_order.id = $1
	`, orderID).Scan(&status, &fenced)
	if errors.Is(err, sql.ErrNoRows) {
		return service.FeishuPaymentRefundFenceState{}, nil
	}
	if err != nil {
		return service.FeishuPaymentRefundFenceState{}, err
	}
	return service.FeishuPaymentRefundFenceState{
		Exists:         true,
		Fenced:         fenced,
		RefundTerminal: status == "REFUNDED" || status == "REFUND_FAILED",
	}, nil
}

func (s *feishuPaymentIncidentStore) LoadPaymentOrderState(ctx context.Context, orderID int64) (service.FeishuPaymentOrderState, error) {
	if err := s.available(1); err != nil {
		return service.FeishuPaymentOrderState{}, err
	}
	var (
		status string
		paidAt sql.NullTime
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT status, paid_at FROM payment_orders WHERE id = $1
	`, orderID).Scan(&status, &paidAt)
	if errors.Is(err, sql.ErrNoRows) {
		return service.FeishuPaymentOrderState{}, nil
	}
	if err != nil {
		return service.FeishuPaymentOrderState{}, err
	}
	state := service.FeishuPaymentOrderState{Exists: true, Status: status}
	if paidAt.Valid {
		value := paidAt.Time.UTC()
		state.PaidAt = &value
	}
	return state, nil
}

func (s *feishuPaymentIncidentStore) Observe(ctx context.Context, kind service.FeishuPaymentIncidentKind, subjectOrderID int64, now time.Time) error {
	if err := s.available(1); err != nil {
		return err
	}
	key, err := feishuPaymentIncidentKey(kind, subjectOrderID)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockFeishuPaymentIncidentKey(ctx, tx, key); err != nil {
		return err
	}

	incident, err := loadOpenFeishuPaymentIncidentForUpdate(ctx, tx, key)
	if err != nil {
		return err
	}
	if incident == nil {
		generation, err := nextFeishuPaymentIncidentGeneration(ctx, tx, key)
		if err != nil {
			return err
		}
		id := uuid.NewString()
		err = tx.QueryRowContext(ctx, `
			INSERT INTO feishu_payment_incidents (
				id, incident_key, incident_type, subject_order_id, generation, status,
				opened_at, last_observed_at, last_checked_at, created_at, updated_at
			) VALUES ($1::uuid, $2, $3, $4, $5, 'OPEN', $6, $6, $6, $6, $6)
			ON CONFLICT DO NOTHING
			RETURNING id::text
		`, id, key, string(kind), subjectOrderID, generation, now).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			incident, err = loadOpenFeishuPaymentIncidentForUpdate(ctx, tx, key)
			if err != nil {
				return err
			}
			if incident == nil {
				return errors.New("feishu payment incident open conflict")
			}
		} else if err != nil {
			return err
		} else {
			incident = &feishuPaymentIncidentRow{
				id: id, key: key, kind: kind, subjectOrderID: subjectOrderID,
				generation: generation, openedAt: now,
			}
			if err := insertFeishuPaymentDelivery(ctx, tx, incident.id, 1, service.FeishuPaymentDeliveryOpen, now); err != nil {
				return err
			}
		}
	}
	if incident != nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE feishu_payment_incidents
			SET last_observed_at = $2, last_checked_at = $2, updated_at = $2
			WHERE id = $1::uuid AND status = 'OPEN'
		`, incident.id, now); err != nil {
			return err
		}
		if err := enqueueFeishuPaymentReminderIfDue(ctx, tx, incident, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *feishuPaymentIncidentStore) TouchOpen(ctx context.Context, incidentID string, now time.Time) error {
	if err := s.available(1); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	incident, err := loadFeishuPaymentIncidentForUpdate(ctx, tx, incidentID)
	if err != nil {
		return err
	}
	if incident == nil || incident.status != feishuPaymentOpenStatus {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE feishu_payment_incidents
		SET last_checked_at = $2, updated_at = $2
		WHERE id = $1::uuid AND status = 'OPEN'
	`, incident.id, now); err != nil {
		return err
	}
	if err := enqueueFeishuPaymentReminderIfDue(ctx, tx, incident, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *feishuPaymentIncidentStore) ListOpenForRecheck(ctx context.Context, limit int) ([]service.FeishuPaymentIncident, error) {
	if err := s.available(limit); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, incident_key, incident_type,
			COALESCE(subject_order_id, 0), generation, status
		FROM feishu_payment_incidents
		WHERE status = 'OPEN'
		ORDER BY last_checked_at, opened_at, id
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []service.FeishuPaymentIncident
	for rows.Next() {
		var incident service.FeishuPaymentIncident
		var kind string
		if err := rows.Scan(&incident.ID, &incident.Key, &kind, &incident.SubjectOrderID, &incident.Generation, &incident.Status); err != nil {
			return nil, err
		}
		incident.Kind = service.FeishuPaymentIncidentKind(kind)
		result = append(result, incident)
	}
	return result, rows.Err()
}

func (s *feishuPaymentIncidentStore) Resolve(ctx context.Context, incidentID string, now time.Time) error {
	if err := s.available(1); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	incident, err := loadFeishuPaymentIncidentForUpdate(ctx, tx, incidentID)
	if err != nil {
		return err
	}
	if incident == nil || incident.status != feishuPaymentOpenStatus {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE feishu_payment_incidents
		SET status = 'RESOLVED', resolved_at = $2, last_checked_at = $2, updated_at = $2
		WHERE id = $1::uuid AND status = 'OPEN'
	`, incident.id, now); err != nil {
		return err
	}
	// Once resolution is durable, every not-yet-terminal reminder is obsolete.
	// Clearing a claimed token prevents an expired or in-flight worker from
	// blocking the ordered RESOLVED delivery after its lease would otherwise end.
	if _, err := tx.ExecContext(ctx, `
		UPDATE feishu_payment_incident_deliveries
		SET status = 'SUPPRESSED', claim_token = NULL, claimed_at = NULL,
			lease_expires_at = NULL, last_error_code = 'obsolete_reminder', updated_at = $2
		WHERE incident_id = $1::uuid AND delivery_kind = 'REMINDER' AND status IN ('PENDING', 'CLAIMED')
	`, incident.id, now); err != nil {
		return err
	}
	sequence, err := nextFeishuPaymentDeliverySequence(ctx, tx, incident.id)
	if err != nil {
		return err
	}
	if err := insertFeishuPaymentDelivery(ctx, tx, incident.id, sequence, service.FeishuPaymentDeliveryResolved, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *feishuPaymentIncidentStore) ClaimDue(ctx context.Context, limit int, lease time.Duration) ([]service.FeishuPaymentDelivery, error) {
	if err := s.available(limit); err != nil {
		return nil, err
	}
	if lease <= 0 {
		return nil, errors.New("feishu payment delivery lease must be positive")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := suppressObsoleteFeishuPaymentReminders(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		WITH authoritative_clock AS MATERIALIZED (
			SELECT clock_timestamp() AS now
		)
		SELECT delivery.id::text, delivery.incident_id::text, incident.incident_key,
			incident.incident_type, COALESCE(incident.subject_order_id, 0), incident.generation, incident.opened_at,
			delivery.delivery_kind, delivery.sequence, delivery.attempts
		FROM feishu_payment_incident_deliveries AS delivery
		JOIN feishu_payment_incidents AS incident ON incident.id = delivery.incident_id
		CROSS JOIN authoritative_clock
		WHERE (
				(delivery.status = 'PENDING' AND delivery.not_before <= authoritative_clock.now)
				OR (delivery.status = 'CLAIMED' AND delivery.lease_expires_at <= authoritative_clock.now)
			)
			AND NOT (delivery.delivery_kind = 'REMINDER' AND incident.status <> 'OPEN')
			AND NOT EXISTS (
				SELECT 1 FROM feishu_payment_incident_deliveries AS previous
				WHERE previous.incident_id = delivery.incident_id
					AND previous.sequence < delivery.sequence
					AND previous.status NOT IN ('DELIVERED', 'SUPPRESSED')
			)
		ORDER BY CASE WHEN delivery.status = 'PENDING' THEN delivery.not_before ELSE delivery.lease_expires_at END,
			delivery.created_at, delivery.id
		LIMIT $1
		FOR UPDATE OF delivery SKIP LOCKED
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var candidates []service.FeishuPaymentDelivery
	for rows.Next() {
		delivery, err := scanFeishuPaymentDelivery(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	claimed := make([]service.FeishuPaymentDelivery, 0, len(candidates))
	for _, delivery := range candidates {
		updated, err := claimFeishuPaymentDeliveryLocked(ctx, tx, delivery, lease)
		if err != nil {
			return nil, err
		}
		if updated != nil {
			claimed = append(claimed, *updated)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *feishuPaymentIncidentStore) ClaimExact(ctx context.Context, deliveryID string, lease time.Duration) (*service.FeishuPaymentDelivery, error) {
	if err := s.available(1); err != nil {
		return nil, err
	}
	if lease <= 0 {
		return nil, errors.New("feishu payment delivery lease must be positive")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	row := tx.QueryRowContext(ctx, `
		SELECT delivery.id::text, delivery.incident_id::text, incident.incident_key,
			incident.incident_type, COALESCE(incident.subject_order_id, 0), incident.generation, incident.opened_at,
			delivery.delivery_kind, delivery.sequence, delivery.attempts
		FROM feishu_payment_incident_deliveries AS delivery
		JOIN feishu_payment_incidents AS incident ON incident.id = delivery.incident_id
		WHERE delivery.id = $1::uuid
			AND NOT (delivery.delivery_kind = 'REMINDER' AND incident.status <> 'OPEN')
			AND NOT EXISTS (
				SELECT 1 FROM feishu_payment_incident_deliveries AS previous
				WHERE previous.incident_id = delivery.incident_id
					AND previous.sequence < delivery.sequence
					AND previous.status NOT IN ('DELIVERED', 'SUPPRESSED')
			)
		FOR UPDATE OF delivery
	`, deliveryID)
	delivery, err := scanFeishuPaymentDeliveryRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	updated, err := claimFeishuPaymentDeliveryLocked(ctx, tx, *delivery, lease)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *feishuPaymentIncidentStore) CanSend(ctx context.Context, deliveryID, claimToken string) (bool, error) {
	if err := s.available(1); err != nil {
		return false, err
	}
	var id string
	err := s.db.QueryRowContext(ctx, `
		WITH authoritative_clock AS MATERIALIZED (
			SELECT clock_timestamp() AS now
		)
		UPDATE feishu_payment_incident_deliveries AS delivery
		SET updated_at = authoritative_clock.now
		FROM feishu_payment_incidents AS incident
		CROSS JOIN authoritative_clock
		WHERE delivery.id = $1::uuid AND delivery.claim_token = $2::uuid
			AND delivery.status = 'CLAIMED' AND incident.id = delivery.incident_id
			AND delivery.lease_expires_at > authoritative_clock.now
			AND (delivery.delivery_kind <> 'REMINDER' OR incident.status = 'OPEN')
		RETURNING delivery.id::text
	`, deliveryID, claimToken).Scan(&id)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	// If an in-flight reminder became obsolete before its sender performs the
	// CAS check, suppress it immediately while its lease is still live. An
	// expired or mismatched token remains untouched for lease recovery.
	_, suppressErr := s.db.ExecContext(ctx, `
		WITH authoritative_clock AS MATERIALIZED (
			SELECT clock_timestamp() AS now
		)
		UPDATE feishu_payment_incident_deliveries AS delivery
		SET status = 'SUPPRESSED', claim_token = NULL, claimed_at = NULL,
			lease_expires_at = NULL, last_error_code = 'obsolete_reminder', updated_at = authoritative_clock.now
		FROM feishu_payment_incidents AS incident
		CROSS JOIN authoritative_clock
		WHERE delivery.id = $1::uuid AND delivery.claim_token = $2::uuid
			AND delivery.status = 'CLAIMED' AND delivery.delivery_kind = 'REMINDER'
			AND incident.id = delivery.incident_id AND incident.status <> 'OPEN'
			AND delivery.lease_expires_at > authoritative_clock.now
	`, deliveryID, claimToken)
	if suppressErr != nil {
		return false, suppressErr
	}
	return false, nil
}

func (s *feishuPaymentIncidentStore) MarkDelivered(ctx context.Context, deliveryID, claimToken string) error {
	if err := s.available(1); err != nil {
		return err
	}
	var id string
	err := s.db.QueryRowContext(ctx, `
		WITH authoritative_clock AS MATERIALIZED (
			SELECT clock_timestamp() AS now
		)
		UPDATE feishu_payment_incident_deliveries AS delivery
		SET status = 'DELIVERED', delivered_at = authoritative_clock.now, claim_token = NULL,
			claimed_at = NULL, lease_expires_at = NULL, last_error_code = NULL, updated_at = authoritative_clock.now
		FROM authoritative_clock
		WHERE delivery.id = $1::uuid AND delivery.claim_token = $2::uuid AND delivery.status = 'CLAIMED'
			AND delivery.lease_expires_at > authoritative_clock.now
		RETURNING delivery.id::text
	`, deliveryID, claimToken).Scan(&id)
	return feishuPaymentDeliveryTransitionError(err)
}

func (s *feishuPaymentIncidentStore) MarkFailed(ctx context.Context, deliveryID, claimToken string, backoff time.Duration, errorCode string) error {
	if err := s.available(1); err != nil {
		return err
	}
	if backoff < 0 {
		return errors.New("feishu payment delivery backoff must not be negative")
	}
	var id string
	err := s.db.QueryRowContext(ctx, `
		WITH authoritative_clock AS MATERIALIZED (
			SELECT clock_timestamp() AS now
		)
		UPDATE feishu_payment_incident_deliveries AS delivery
		SET status = CASE
				WHEN delivery.delivery_kind = 'REMINDER' AND incident.status <> 'OPEN' THEN 'SUPPRESSED'
				ELSE 'PENDING'
			END,
			not_before = CASE
					WHEN delivery.delivery_kind = 'REMINDER' AND incident.status <> 'OPEN' THEN delivery.not_before
					ELSE authoritative_clock.now + ($3::bigint * INTERVAL '1 microsecond')
				END,
			claim_token = NULL, claimed_at = NULL, lease_expires_at = NULL,
			last_error_code = CASE
				WHEN delivery.delivery_kind = 'REMINDER' AND incident.status <> 'OPEN' THEN 'obsolete_reminder'
				ELSE $4
			END,
			updated_at = authoritative_clock.now
		FROM feishu_payment_incidents AS incident
		CROSS JOIN authoritative_clock
		WHERE delivery.id = $1::uuid AND delivery.claim_token = $2::uuid
			AND delivery.status = 'CLAIMED' AND incident.id = delivery.incident_id
			AND delivery.lease_expires_at > authoritative_clock.now
		RETURNING delivery.id::text
	`, deliveryID, claimToken, backoff.Microseconds(), errorCode).Scan(&id)
	return feishuPaymentDeliveryTransitionError(err)
}

func feishuPaymentDeliveryTransitionError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrFeishuPaymentDeliveryLeaseLost
	}
	return err
}

func (s *feishuPaymentIncidentStore) EnqueueTest(ctx context.Context, now time.Time) (string, error) {
	if err := s.available(1); err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	incidentID := uuid.NewString()
	deliveryID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO feishu_payment_incidents (
			id, incident_key, incident_type, generation, status,
			opened_at, resolved_at, last_observed_at, last_checked_at, created_at, updated_at
		) VALUES ($1::uuid, $2, 'TEST_NOTIFICATION', 1, 'CLOSED', $3, $3, $3, $3, $3, $3)
	`, incidentID, "test-notification:"+incidentID, now); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO feishu_payment_incident_deliveries (
			id, incident_id, sequence, delivery_kind, status, not_before, created_at, updated_at
		) VALUES ($1::uuid, $2::uuid, 1, 'TEST', 'PENDING', $3, $3, $3)
	`, deliveryID, incidentID, now); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return deliveryID, nil
}

func (s *feishuPaymentIncidentStore) DeliveryStatus(ctx context.Context, deliveryID string) (string, error) {
	if err := s.available(1); err != nil {
		return "", err
	}
	var status string
	err := s.db.QueryRowContext(ctx, `
		SELECT status FROM feishu_payment_incident_deliveries WHERE id = $1::uuid
	`, deliveryID).Scan(&status)
	return status, err
}

const feishuPaymentOpenStatus = "OPEN"

type feishuPaymentIncidentRow struct {
	id             string
	key            string
	kind           service.FeishuPaymentIncidentKind
	subjectOrderID int64
	generation     int
	status         string
	openedAt       time.Time
	lastReminderAt sql.NullTime
}

func (s *feishuPaymentIncidentStore) available(limit int) error {
	if s == nil || s.db == nil {
		return errors.New("feishu payment incident store unavailable")
	}
	if limit <= 0 {
		return errors.New("feishu payment incident limit must be positive")
	}
	return nil
}

func feishuPaymentIncidentKey(kind service.FeishuPaymentIncidentKind, orderID int64) (string, error) {
	if orderID <= 0 {
		return "", errors.New("feishu payment incident order id must be positive")
	}
	switch kind {
	case service.FeishuPaymentIncidentRefundReview:
		return fmt.Sprintf("refund-review:%d", orderID), nil
	case service.FeishuPaymentIncidentPaidIncomplete:
		return fmt.Sprintf("paid-incomplete:%d", orderID), nil
	default:
		return "", errors.New("unsupported feishu payment incident kind")
	}
}

func lockFeishuPaymentIncidentKey(ctx context.Context, tx *sql.Tx, key string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, key)
	return err
}

func loadOpenFeishuPaymentIncidentForUpdate(ctx context.Context, tx *sql.Tx, key string) (*feishuPaymentIncidentRow, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id::text, incident_key, incident_type, COALESCE(subject_order_id, 0), generation,
			status, opened_at, last_reminder_enqueued_at
		FROM feishu_payment_incidents
		WHERE incident_key = $1 AND status = 'OPEN'
		FOR UPDATE
	`, key)
	return scanFeishuPaymentIncidentRow(row)
}

func loadFeishuPaymentIncidentForUpdate(ctx context.Context, tx *sql.Tx, incidentID string) (*feishuPaymentIncidentRow, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id::text, incident_key, incident_type, COALESCE(subject_order_id, 0), generation,
			status, opened_at, last_reminder_enqueued_at
		FROM feishu_payment_incidents
		WHERE id = $1::uuid
		FOR UPDATE
	`, incidentID)
	return scanFeishuPaymentIncidentRow(row)
}

func scanFeishuPaymentIncidentRow(row *sql.Row) (*feishuPaymentIncidentRow, error) {
	incident := &feishuPaymentIncidentRow{}
	var kind string
	err := row.Scan(&incident.id, &incident.key, &kind, &incident.subjectOrderID, &incident.generation,
		&incident.status, &incident.openedAt, &incident.lastReminderAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	incident.kind = service.FeishuPaymentIncidentKind(kind)
	return incident, nil
}

func nextFeishuPaymentIncidentGeneration(ctx context.Context, tx *sql.Tx, key string) (int, error) {
	var generation int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(generation), 0) + 1
		FROM feishu_payment_incidents WHERE incident_key = $1
	`, key).Scan(&generation); err != nil {
		return 0, err
	}
	return generation, nil
}

func enqueueFeishuPaymentReminderIfDue(ctx context.Context, tx *sql.Tx, incident *feishuPaymentIncidentRow, now time.Time) error {
	if incident == nil || incident.status != feishuPaymentOpenStatus {
		return nil
	}
	last := incident.openedAt
	if incident.lastReminderAt.Valid {
		last = incident.lastReminderAt.Time
	}
	if now.Before(last.Add(feishuPaymentIncidentReminderInterval)) {
		return nil
	}
	var outstanding bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM feishu_payment_incident_deliveries
			WHERE incident_id = $1::uuid AND delivery_kind = 'REMINDER'
				AND status IN ('PENDING', 'CLAIMED')
		)
	`, incident.id).Scan(&outstanding); err != nil {
		return err
	}
	if outstanding {
		return nil
	}
	sequence, err := nextFeishuPaymentDeliverySequence(ctx, tx, incident.id)
	if err != nil {
		return err
	}
	if err := insertFeishuPaymentDelivery(ctx, tx, incident.id, sequence, service.FeishuPaymentDeliveryReminder, now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE feishu_payment_incidents
		SET last_reminder_enqueued_at = $2, updated_at = $2
		WHERE id = $1::uuid AND status = 'OPEN'
	`, incident.id, now)
	return err
}

func nextFeishuPaymentDeliverySequence(ctx context.Context, tx *sql.Tx, incidentID string) (int64, error) {
	var sequence int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence), 0) + 1
		FROM feishu_payment_incident_deliveries WHERE incident_id = $1::uuid
	`, incidentID).Scan(&sequence); err != nil {
		return 0, err
	}
	return sequence, nil
}

func insertFeishuPaymentDelivery(ctx context.Context, tx *sql.Tx, incidentID string, sequence int64, kind service.FeishuPaymentDeliveryKind, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO feishu_payment_incident_deliveries (
			id, incident_id, sequence, delivery_kind, status, not_before, created_at, updated_at
		) VALUES ($1::uuid, $2::uuid, $3, $4, 'PENDING', $5, $5, $5)
	`, uuid.NewString(), incidentID, sequence, string(kind), now)
	return err
}

func suppressObsoleteFeishuPaymentReminders(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE feishu_payment_incident_deliveries AS delivery
		SET status = 'SUPPRESSED', claim_token = NULL, claimed_at = NULL,
			lease_expires_at = NULL, last_error_code = 'obsolete_reminder', updated_at = clock_timestamp()
		FROM feishu_payment_incidents AS incident
		WHERE delivery.incident_id = incident.id AND delivery.delivery_kind = 'REMINDER'
			AND delivery.status IN ('PENDING', 'CLAIMED') AND incident.status <> 'OPEN'
	`)
	return err
}

func scanFeishuPaymentDelivery(rows *sql.Rows) (service.FeishuPaymentDelivery, error) {
	var (
		delivery    service.FeishuPaymentDelivery
		kind        string
		deliverKind string
	)
	err := rows.Scan(&delivery.ID, &delivery.IncidentID, &delivery.IncidentKey, &kind,
		&delivery.SubjectOrderID, &delivery.Generation, &delivery.OpenedAt, &deliverKind, &delivery.Sequence, &delivery.Attempts)
	if err != nil {
		return service.FeishuPaymentDelivery{}, err
	}
	delivery.IncidentKind = service.FeishuPaymentIncidentKind(kind)
	delivery.Kind = service.FeishuPaymentDeliveryKind(deliverKind)
	return delivery, nil
}

func scanFeishuPaymentDeliveryRow(row *sql.Row) (*service.FeishuPaymentDelivery, error) {
	delivery := &service.FeishuPaymentDelivery{}
	var kind, deliveryKind string
	err := row.Scan(&delivery.ID, &delivery.IncidentID, &delivery.IncidentKey, &kind,
		&delivery.SubjectOrderID, &delivery.Generation, &delivery.OpenedAt, &deliveryKind, &delivery.Sequence, &delivery.Attempts)
	if err != nil {
		return nil, err
	}
	delivery.IncidentKind = service.FeishuPaymentIncidentKind(kind)
	delivery.Kind = service.FeishuPaymentDeliveryKind(deliveryKind)
	return delivery, nil
}

func claimFeishuPaymentDeliveryLocked(ctx context.Context, tx *sql.Tx, delivery service.FeishuPaymentDelivery, lease time.Duration) (*service.FeishuPaymentDelivery, error) {
	claimToken := uuid.NewString()
	var attempts int
	err := tx.QueryRowContext(ctx, `
		WITH authoritative_clock AS MATERIALIZED (
			SELECT clock_timestamp() AS now
		)
		UPDATE feishu_payment_incident_deliveries
		SET status = 'CLAIMED', claim_token = $2::uuid, claimed_at = authoritative_clock.now,
			lease_expires_at = authoritative_clock.now + ($3::bigint * INTERVAL '1 microsecond'),
			attempts = attempts + 1, updated_at = authoritative_clock.now
		FROM authoritative_clock
		WHERE feishu_payment_incident_deliveries.id = $1::uuid
			AND (
				(status = 'PENDING' AND not_before <= authoritative_clock.now)
				OR (status = 'CLAIMED' AND lease_expires_at <= authoritative_clock.now)
			)
		RETURNING attempts
	`, delivery.ID, claimToken, lease.Microseconds()).Scan(&attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	delivery.Attempts = attempts
	delivery.ClaimToken = claimToken
	return &delivery, nil
}
