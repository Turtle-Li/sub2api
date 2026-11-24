package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *opsRepository) ClaimAlertDelivery(ctx context.Context, now time.Time, token string, lease time.Duration) (*service.OpsAlertDelivery, error) {
	var delivery service.OpsAlertDelivery
	err := r.db.QueryRowContext(ctx, `
WITH candidate AS (
  SELECT id FROM ops_alert_deliveries
  WHERE status = 'pending' AND next_attempt_at <= $1 AND (claimed_until IS NULL OR claimed_until <= $1)
  ORDER BY next_attempt_at, id LIMIT 1 FOR UPDATE SKIP LOCKED
)
UPDATE ops_alert_deliveries d SET claim_token = $2, claimed_until = $3, updated_at = $1
FROM candidate c WHERE d.id = c.id
RETURNING d.id, d.event_id, d.channel, d.target, d.attempt_count, d.claim_token, d.created_at`, now, token, now.Add(lease)).Scan(
		&delivery.ID, &delivery.EventID, &delivery.Channel, &delivery.Target, &delivery.AttemptCount, &delivery.ClaimToken, &delivery.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &delivery, nil
}

func (r *opsRepository) StartAlertDelivery(ctx context.Context, delivery *service.OpsAlertDelivery, now time.Time) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
UPDATE ops_alert_deliveries SET attempt_count = attempt_count + 1, updated_at = $3
WHERE id = $1 AND claim_token = $2 AND claimed_until > $3 AND status = 'pending'
  AND attempt_count < $4 AND created_at > $5`, delivery.ID, delivery.ClaimToken, now,
		service.OpsAlertDeliveryMaxAttempts, now.Add(-service.OpsAlertDeliveryLifetime))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (r *opsRepository) FinishAlertDelivery(ctx context.Context, delivery *service.OpsAlertDelivery, outcome service.OpsAlertDeliveryOutcome) (bool, error) {
	switch outcome.Status {
	case service.OpsAlertDeliveryPending, service.OpsAlertDeliverySkipped, service.OpsAlertDeliveryFailed, service.OpsAlertDeliverySent:
	default:
		return false, fmt.Errorf("invalid alert delivery outcome")
	}
	// A stale claim cannot acknowledge delivery or set either compatibility flag.
	// Both writes commit in one statement so a send never leaves a split state.
	var finished bool
	err := r.db.QueryRowContext(ctx, `
WITH finished AS (
  UPDATE ops_alert_deliveries SET status = $3, next_attempt_at = $4, last_error = $5,
    sent_at = CASE WHEN $3 = 'sent' THEN $6 ELSE NULL END,
    claim_token = NULL, claimed_until = NULL, updated_at = $6
  WHERE id = $1 AND claim_token = $2 AND claimed_until > $6 AND status = 'pending'
  RETURNING event_id, channel, status
), event_flags AS (
  UPDATE ops_alert_events e SET email_sent = (e.email_sent OR f.channel = 'email'),
    feishu_sent = (e.feishu_sent OR f.channel = 'feishu')
  FROM finished f WHERE f.event_id = e.id AND f.status = 'sent'
)
SELECT EXISTS (SELECT 1 FROM finished)`, delivery.ID, delivery.ClaimToken, outcome.Status,
		outcome.NextAttempt, opsNullString(outcome.ErrorCode), outcome.Now).Scan(&finished)
	return finished, err
}
