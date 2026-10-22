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

type paidFallbackOpsStore struct{ db *sql.DB }

func NewPaidFallbackOpsStore(db *sql.DB) service.PaidFallbackOpsStore {
	return &paidFallbackOpsStore{db: db}
}

func (s *paidFallbackOpsStore) StartAttempt(ctx context.Context, value service.PaidFallbackAttempt) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO paid_fallback_attempts
 (id,inbound_request_id,api_key_id,account_id,group_id,public_model,forward_model,upstream_model,transport,turn_index)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, value.ID, value.InboundRequestID, value.APIKeyID, value.AccountID, value.GroupID, value.PublicModel, value.ForwardModel, value.UpstreamModel, value.Transport, value.Turn)
	return err
}

func (s *paidFallbackOpsStore) FinishAttempt(ctx context.Context, id, outcome string, status int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE paid_fallback_attempts SET outcome=$2,http_status=$3,finished_at=clock_timestamp() WHERE id=$1 AND outcome='in_flight'`, id, outcome, status)
	return err
}

func (s *paidFallbackOpsStore) Advance(ctx context.Context, settings service.PaidFallbackNotificationSettings, recipients []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(708911264)`).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE paid_fallback_deliveries SET suppressed_at=clock_timestamp(),lease_expires_at=NULL WHERE delivered_at IS NULL AND suppressed_at IS NULL AND created_at<clock_timestamp()-INTERVAL '24 hours' AND (lease_expires_at IS NULL OR lease_expires_at<=clock_timestamp())`); err != nil {
		return err
	}
	unfinishedAfter := settings.UnfinishedAfterSeconds
	if unfinishedAfter <= 0 {
		unfinishedAfter = 900
	}
	if _, err := tx.ExecContext(ctx, `UPDATE paid_fallback_attempts SET outcome='unknown',finished_at=clock_timestamp() WHERE outcome='in_flight' AND started_at < clock_timestamp()-make_interval(secs=>$1)`, unfinishedAfter); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT group_id,public_model FROM paid_fallback_attempts WHERE incident_id IS NULL AND outcome NOT IN ('in_flight','not_sent') GROUP BY group_id,public_model ORDER BY MIN(started_at) LIMIT 100`)
	if err != nil {
		return err
	}
	type scope struct {
		group int64
		model string
	}
	scopes := []scope{}
	for rows.Next() {
		var item scope
		if err := rows.Scan(&item.group, &item.model); err != nil {
			_ = rows.Close()
			return err
		}
		scopes = append(scopes, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range scopes {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id::text FROM paid_fallback_incidents WHERE group_id=$1 AND public_model=$2 AND resolved_at IS NULL`, item.group, item.model).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			id = uuid.NewString()
			_, err = tx.ExecContext(ctx, `INSERT INTO paid_fallback_incidents(id,group_id,public_model,generation,opened_at,last_attempt_at)
    SELECT $1,$2,$3,COALESCE((SELECT MAX(generation) FROM paid_fallback_incidents WHERE group_id=$2 AND public_model=$3),0)+1,MIN(started_at),MAX(started_at)
    FROM paid_fallback_attempts WHERE incident_id IS NULL AND outcome NOT IN ('in_flight','not_sent') AND group_id=$2 AND public_model=$3`, id, item.group, item.model)
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `WITH attached AS (
   UPDATE paid_fallback_attempts SET incident_id=$1 WHERE incident_id IS NULL AND outcome NOT IN ('in_flight','not_sent') AND group_id=$2 AND public_model=$3 RETURNING started_at
  ) UPDATE paid_fallback_incidents SET last_attempt_at=GREATEST(last_attempt_at,(SELECT MAX(started_at) FROM attached)) WHERE id=$1`, id, item.group, item.model)
		if err != nil {
			return err
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT id::text FROM paid_fallback_incidents WHERE resolved_at IS NULL ORDER BY COALESCE(last_checked_at,opened_at),id LIMIT 100`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.advanceIncident(ctx, tx, id, settings, recipients); err != nil {
			return err
		}
	}
	if err := s.advanceDailyCosts(ctx, tx, settings, recipients); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *paidFallbackOpsStore) advanceIncident(ctx context.Context, tx *sql.Tx, id string, settings service.PaidFallbackNotificationSettings, recipients []string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE paid_fallback_incidents SET last_checked_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		return err
	}
	var group int64
	var model string
	var last, now time.Time
	var reminder *time.Time
	var recoverable bool
	err := tx.QueryRowContext(ctx, `SELECT group_id,public_model,last_attempt_at,last_reminder_at,clock_timestamp(),
  last_attempt_at < clock_timestamp()-make_interval(secs => $2) AND
  NOT EXISTS(SELECT 1 FROM paid_fallback_attempts a WHERE a.group_id=i.group_id AND a.public_model=i.public_model AND a.incident_id IS NULL AND a.outcome<>'not_sent') AND
  EXISTS(SELECT 1 FROM usage_logs u WHERE u.group_id=i.group_id AND COALESCE(NULLIF(BTRIM(u.requested_model),''),u.model)=i.public_model AND u.paid_fallback=FALSE AND u.created_at>i.last_attempt_at)
  FROM paid_fallback_incidents i WHERE id=$1`, id, settings.RecoveryQuietSeconds).Scan(&group, &model, &last, &reminder, &now, &recoverable)
	if err != nil {
		return err
	}
	enqueue := func(kind, key string) error {
		if len(recipients) == 0 {
			return nil
		}
		summary, err := paidFallbackIncidentSummary(ctx, tx, id, group, model)
		if err != nil {
			return err
		}
		for _, recipient := range recipients {
			_, err := tx.ExecContext(ctx, `INSERT INTO paid_fallback_deliveries(id,incident_id,event_key,kind,recipient,summary) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(event_key,recipient) DO NOTHING`, uuid.NewString(), id, key, kind, recipient, summary)
			if err != nil {
				return err
			}
		}
		return nil
	}
	if recoverable {
		if _, err := tx.ExecContext(ctx, `UPDATE paid_fallback_incidents SET resolved_at=clock_timestamp() WHERE id=$1 AND resolved_at IS NULL`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE paid_fallback_deliveries SET suppressed_at=clock_timestamp(),lease_expires_at=NULL WHERE incident_id=$1 AND kind IN ('open','reminder') AND delivered_at IS NULL AND suppressed_at IS NULL AND (lease_expires_at IS NULL OR lease_expires_at<=clock_timestamp())`, id); err != nil {
			return err
		}
		return enqueue("recovered", "recovered:"+id)
	}
	recent := now.Sub(last) <= time.Duration(settings.ReminderMinutes)*time.Minute
	if len(recipients) > 0 && recent && (reminder == nil || now.Sub(*reminder) >= time.Duration(settings.ReminderMinutes)*time.Minute) {
		kind, key := "reminder", fmt.Sprintf("reminder:%s:%d", id, now.Unix()/int64(settings.ReminderMinutes*60))
		if reminder == nil {
			kind, key = "open", "open:"+id
		}
		if err := enqueue(kind, key); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE paid_fallback_incidents SET last_reminder_at=clock_timestamp() WHERE id=$1`, id); err != nil {
			return err
		}
	}

	return nil
}

func (s *paidFallbackOpsStore) advanceDailyCosts(ctx context.Context, tx *sql.Tx, settings service.PaidFallbackNotificationSettings, recipients []string) error {
	if settings.DailyCostUSD == nil || len(recipients) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `WITH costs AS (
	 SELECT group_id,COALESCE(NULLIF(BTRIM(requested_model),''),model) AS public_model,SUM(COALESCE(account_stats_cost,total_cost)*COALESCE(account_rate_multiplier,1)) AS cost
	 FROM usage_logs WHERE paid_fallback=TRUE AND created_at>=date_trunc('day',clock_timestamp() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
	 AND created_at<(date_trunc('day',clock_timestamp() AT TIME ZONE 'UTC')+INTERVAL '1 day') AT TIME ZONE 'UTC'
	 GROUP BY group_id,COALESCE(NULLIF(BTRIM(requested_model),''),model)
	) SELECT i.id::text,c.group_id,c.public_model,c.cost,to_char(clock_timestamp() AT TIME ZONE 'UTC','YYYY-MM-DD')
	FROM costs c JOIN LATERAL(SELECT id FROM paid_fallback_incidents WHERE group_id=c.group_id AND public_model=c.public_model ORDER BY generation DESC LIMIT 1)i ON TRUE WHERE c.cost >= $1`, *settings.DailyCostUSD)
	if err != nil {
		return err
	}
	type costRow struct {
		id    string
		group int64
		model string
		cost  float64
		day   string
	}
	items := []costRow{}
	for rows.Next() {
		var item costRow
		if err := rows.Scan(&item.id, &item.group, &item.model, &item.cost, &item.day); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		summary, err := paidFallbackIncidentSummary(ctx, tx, item.id, item.group, item.model)
		if err != nil {
			return err
		}
		summary += fmt.Sprintf("; UTC day %s recorded account cost USD %.8f (soft alert threshold %.8f)", item.day, item.cost, *settings.DailyCostUSD)
		for _, recipient := range recipients {
			_, err := tx.ExecContext(ctx, `INSERT INTO paid_fallback_deliveries(id,incident_id,event_key,kind,recipient,summary) VALUES($1,$2,$3,'daily_cost',$4,$5) ON CONFLICT(event_key,recipient) DO NOTHING`, uuid.NewString(), item.id, fmt.Sprintf("daily_cost:%d:%s:%s", item.group, item.model, item.day), recipient, summary)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func paidFallbackIncidentSummary(ctx context.Context, tx *sql.Tx, id string, group int64, model string) (string, error) {
	var attempts, unknown int64
	var cost float64
	err := tx.QueryRowContext(ctx, `WITH matched AS (
 SELECT DISTINCT u.id,COALESCE(u.account_stats_cost,u.total_cost)*COALESCE(u.account_rate_multiplier,1) AS cost
 FROM paid_fallback_attempts a JOIN usage_logs u ON u.request_id='client:'||a.inbound_request_id AND u.api_key_id=a.api_key_id AND u.account_id=a.account_id
 WHERE a.incident_id=$1 AND a.transport='http' AND u.paid_fallback=TRUE AND u.request_id<>''
 ), totals AS (SELECT COUNT(*) AS attempts FROM paid_fallback_attempts WHERE incident_id=$1)
 SELECT attempts,GREATEST(0,attempts-COUNT(matched.id)),COALESCE(SUM(matched.cost),0) FROM totals LEFT JOIN matched ON TRUE GROUP BY attempts`, id).Scan(&attempts, &unknown, &cost)
	return fmt.Sprintf("Group %d; model %s; attempts %d; recorded matched account cost USD %.8f; unknown-cost attempts %d. Account cost is an estimate, not a supplier invoice.", group, model, attempts, cost, unknown), err
}

func (s *paidFallbackOpsStore) Claim(ctx context.Context, limit int) (*service.PaidFallbackDelivery, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(708911265)`); err != nil {
		return nil, err
	}
	var reservations int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM paid_fallback_deliveries WHERE delivered_at>clock_timestamp()-INTERVAL '1 hour' OR (delivered_at IS NULL AND suppressed_at IS NULL AND lease_expires_at>clock_timestamp())`).Scan(&reservations); err != nil {
		return nil, err
	}
	if limit > 0 && reservations >= limit {
		return nil, nil
	}
	result := &service.PaidFallbackDelivery{ClaimToken: uuid.NewString()}
	err = tx.QueryRowContext(ctx, `WITH candidate AS (
  SELECT d.id FROM paid_fallback_deliveries d WHERE delivered_at IS NULL AND suppressed_at IS NULL AND next_attempt_at<=clock_timestamp() AND (lease_expires_at IS NULL OR lease_expires_at<clock_timestamp())
  AND (d.kind<>'recovered' OR EXISTS(SELECT 1 FROM paid_fallback_deliveries sent WHERE sent.incident_id=d.incident_id AND sent.recipient=d.recipient AND sent.kind IN ('open','reminder') AND sent.delivered_at IS NOT NULL))
  ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1
 ) UPDATE paid_fallback_deliveries d SET claim_token=$1,lease_expires_at=clock_timestamp()+INTERVAL '2 minutes',attempts=attempts+1
 FROM candidate c WHERE d.id=c.id RETURNING d.id::text,d.recipient,d.kind,d.summary`, result.ClaimToken).Scan(&result.ID, &result.Recipient, &result.Kind, &result.Summary)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *paidFallbackOpsStore) CanSend(ctx context.Context, id, token string) (bool, error) {
	var allowed bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM paid_fallback_deliveries d JOIN paid_fallback_incidents i ON i.id=d.incident_id WHERE d.id=$1 AND d.claim_token=$2 AND d.delivered_at IS NULL AND d.suppressed_at IS NULL AND d.lease_expires_at>clock_timestamp() AND d.created_at>=clock_timestamp()-INTERVAL '24 hours' AND (d.kind NOT IN ('open','reminder') OR i.resolved_at IS NULL))`, id, token).Scan(&allowed)
	return allowed, err
}

func (s *paidFallbackOpsStore) Complete(ctx context.Context, id, token string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE paid_fallback_deliveries SET delivered_at=clock_timestamp(),lease_expires_at=NULL WHERE id=$1 AND claim_token=$2 AND delivered_at IS NULL AND suppressed_at IS NULL AND lease_expires_at>clock_timestamp()`, id, token)
	return paidFallbackClaimResult(result, err)
}

func (s *paidFallbackOpsStore) Retry(ctx context.Context, id, token string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE paid_fallback_deliveries SET next_attempt_at=clock_timestamp()+make_interval(secs=>LEAST(3600,30*power(2,LEAST(attempts,7)))::int),lease_expires_at=NULL,claim_token=NULL WHERE id=$1 AND claim_token=$2 AND delivered_at IS NULL AND suppressed_at IS NULL AND lease_expires_at>clock_timestamp()`, id, token)
	return paidFallbackClaimResult(result, err)
}

func (s *paidFallbackOpsStore) Suppress(ctx context.Context, id, token string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE paid_fallback_deliveries SET suppressed_at=clock_timestamp(),lease_expires_at=NULL WHERE id=$1 AND claim_token=$2 AND delivered_at IS NULL AND suppressed_at IS NULL AND lease_expires_at>clock_timestamp()`, id, token)
	return paidFallbackClaimResult(result, err)
}

func paidFallbackClaimResult(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("paid fallback delivery claim lost")
	}
	return nil
}

func (s *paidFallbackOpsStore) Report(ctx context.Context) (*service.PaidFallbackReport, error) {
	report := &service.PaidFallbackReport{Attempts: []service.PaidFallbackAttempt{}, Incidents: []service.PaidFallbackIncident{}}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,inbound_request_id,api_key_id,account_id,group_id,public_model,forward_model,upstream_model,transport,turn_index,started_at,outcome,http_status FROM paid_fallback_attempts ORDER BY started_at DESC,id DESC LIMIT 30`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item service.PaidFallbackAttempt
		if err := rows.Scan(&item.ID, &item.InboundRequestID, &item.APIKeyID, &item.AccountID, &item.GroupID, &item.PublicModel, &item.ForwardModel, &item.UpstreamModel, &item.Transport, &item.Turn, &item.StartedAt, &item.Outcome, &item.HTTPStatus); err != nil {
			_ = rows.Close()
			return nil, err
		}
		report.Attempts = append(report.Attempts, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT i.id::text,i.group_id,i.public_model,i.generation,i.opened_at,i.last_attempt_at,i.resolved_at,
 counts.attempts,GREATEST(0,counts.attempts-costs.known),costs.cost
 FROM (SELECT * FROM paid_fallback_incidents ORDER BY last_attempt_at DESC,id DESC LIMIT 20) i
 CROSS JOIN LATERAL (SELECT COUNT(*) AS attempts FROM paid_fallback_attempts a WHERE a.incident_id=i.id) counts
 CROSS JOIN LATERAL (SELECT COUNT(*) AS known,COALESCE(SUM(m.cost),0) AS cost FROM (
   SELECT DISTINCT u.id,COALESCE(u.account_stats_cost,u.total_cost)*COALESCE(u.account_rate_multiplier,1) AS cost
   FROM paid_fallback_attempts a JOIN usage_logs u ON u.request_id='client:'||a.inbound_request_id AND u.api_key_id=a.api_key_id AND u.account_id=a.account_id
   WHERE a.incident_id=i.id AND a.transport='http' AND u.paid_fallback=TRUE AND u.request_id<>''
 ) m) costs ORDER BY i.last_attempt_at DESC,i.id DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item service.PaidFallbackIncident
		if err := rows.Scan(&item.ID, &item.GroupID, &item.PublicModel, &item.Generation, &item.OpenedAt, &item.LastAttemptAt, &item.ResolvedAt, &item.Attempts, &item.UnknownCostAttempts, &item.RecordedCostUSD); err != nil {
			return nil, err
		}
		report.Incidents = append(report.Incidents, item)
	}
	return report, rows.Err()
}
