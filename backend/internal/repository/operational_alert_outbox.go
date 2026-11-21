package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type operationalAlertOutbox struct{ db *sql.DB }

func NewOperationalAlertOutbox(db *sql.DB) service.OperationalAlertStore {
	return &operationalAlertOutbox{db: db}
}

func (s *operationalAlertOutbox) EnqueueOperationalAlert(ctx context.Context, alert service.OperationalAlert) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO operational_alert_outbox (component, stage, subject_id, code)
VALUES ($1, $2, $3, $4)
ON CONFLICT (component, stage, subject_id, code) DO NOTHING`, alert.Component, alert.Stage, alert.SubjectID, alert.Code)
	return err
}

func (s *operationalAlertOutbox) ClaimOperationalAlert(ctx context.Context) (*service.OperationalAlertDelivery, error) {
	delivery := service.OperationalAlertDelivery{Claim: uuid.NewString()}
	err := s.db.QueryRowContext(ctx, `
WITH candidate AS (
    SELECT id FROM operational_alert_outbox
    WHERE delivered_at IS NULL AND retry_at <= NOW()
    ORDER BY retry_at, id LIMIT 1 FOR UPDATE SKIP LOCKED
)
UPDATE operational_alert_outbox AS o
SET claim = $1, retry_at = NOW() + INTERVAL '30 seconds', attempts = LEAST(o.attempts, 999999) + 1
FROM candidate WHERE o.id = candidate.id
RETURNING o.id, o.component, o.stage, o.subject_id, o.code`, delivery.Claim).Scan(
		&delivery.ID, &delivery.Alert.Component, &delivery.Alert.Stage, &delivery.Alert.SubjectID, &delivery.Alert.Code)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &delivery, nil
}

func (s *operationalAlertOutbox) FinishOperationalAlert(ctx context.Context, delivery service.OperationalAlertDelivery, sent bool) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE operational_alert_outbox
SET claim = NULL, delivered_at = CASE WHEN $3 THEN NOW() ELSE NULL END,
    retry_at = NOW() + INTERVAL '30 seconds' * POWER(2, LEAST(attempts - 1, 7))
WHERE id = $1 AND claim = $2 AND delivered_at IS NULL`, delivery.ID, delivery.Claim, sent)
	return err
}
