package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const opsAlertRuleColumns = `id, name, COALESCE(description, ''), enabled, COALESCE(severity, ''),
  metric_type, operator, threshold, window_minutes, sustained_minutes, cooldown_minutes,
  COALESCE(notify_email, true), notify_feishu, notify_emails, COALESCE(custom_message, ''),
  filters, last_triggered_at, created_at, updated_at`

func (r *opsRepository) ListAlertRules(ctx context.Context) ([]*service.OpsAlertRule, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+opsAlertRuleColumns+` FROM ops_alert_rules ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []*service.OpsAlertRule{}
	for rows.Next() {
		rule, err := scanOpsAlertRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (r *opsRepository) GetAlertRuleByID(ctx context.Context, id int64) (*service.OpsAlertRule, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+opsAlertRuleColumns+` FROM ops_alert_rules WHERE id = $1`, id)
	rule, err := scanOpsAlertRule(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return rule, err
}

func scanOpsAlertRule(row opsAlertEventRow) (*service.OpsAlertRule, error) {
	var rule service.OpsAlertRule
	var filters, recipients []byte
	var lastTriggeredAt sql.NullTime
	if err := row.Scan(&rule.ID, &rule.Name, &rule.Description, &rule.Enabled, &rule.Severity,
		&rule.MetricType, &rule.Operator, &rule.Threshold, &rule.WindowMinutes, &rule.SustainedMinutes,
		&rule.CooldownMinutes, &rule.NotifyEmail, &rule.NotifyFeishu, &recipients, &rule.CustomMessage,
		&filters, &lastTriggeredAt, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
		return nil, err
	}
	if lastTriggeredAt.Valid {
		rule.LastTriggeredAt = &lastTriggeredAt.Time
	}
	if len(recipients) > 0 {
		if err := json.Unmarshal(recipients, &rule.NotifyEmails); err != nil {
			return nil, err
		}
	}
	if rule.NotifyEmails == nil {
		rule.NotifyEmails = []string{}
	}
	if len(filters) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(filters))
		decoder.UseNumber()
		var decoded map[string]any
		if err := decoder.Decode(&decoded); err == nil {
			rule.Filters = decoded
		}
	}
	return &rule, nil
}

func opsAlertRuleArgs(input *service.OpsAlertRule) ([]any, error) {
	filters, err := opsNullJSONMap(input.Filters)
	if err != nil {
		return nil, err
	}
	var recipients any
	if len(input.NotifyEmails) > 0 {
		encoded, err := json.Marshal(input.NotifyEmails)
		if err != nil {
			return nil, err
		}
		recipients = string(encoded)
	}
	return []any{strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), input.Enabled,
		strings.TrimSpace(input.Severity), strings.TrimSpace(input.MetricType), strings.TrimSpace(input.Operator),
		input.Threshold, input.WindowMinutes, input.SustainedMinutes, input.CooldownMinutes,
		input.NotifyEmail, filters, input.NotifyFeishu, recipients, opsNullString(input.CustomMessage)}, nil
}

func (r *opsRepository) CreateAlertRule(ctx context.Context, input *service.OpsAlertRule) (*service.OpsAlertRule, error) {
	if r == nil || r.db == nil || input == nil {
		return nil, fmt.Errorf("missing ops repository or rule")
	}
	args, err := opsAlertRuleArgs(input)
	if err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `
INSERT INTO ops_alert_rules (name, description, enabled, severity, metric_type, operator, threshold,
  window_minutes, sustained_minutes, cooldown_minutes, notify_email, filters, notify_feishu, notify_emails, custom_message)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
RETURNING `+opsAlertRuleColumns, args...)
	return scanOpsAlertRule(row)
}

func (r *opsRepository) UpdateAlertRule(ctx context.Context, input *service.OpsAlertRule) (*service.OpsAlertRule, error) {
	if r == nil || r.db == nil || input == nil || input.ID <= 0 {
		return nil, fmt.Errorf("missing ops repository or valid rule")
	}
	args, err := opsAlertRuleArgs(input)
	if err != nil {
		return nil, err
	}
	mask := input.NotificationUpdate
	args = append(args, input.ID, mask == nil || mask.Feishu, mask == nil || mask.Emails, mask == nil || mask.CustomMessage)
	row := r.db.QueryRowContext(ctx, `
UPDATE ops_alert_rules SET name = $1, description = $2, enabled = $3, severity = $4, metric_type = $5,
  operator = $6, threshold = $7, window_minutes = $8, sustained_minutes = $9, cooldown_minutes = $10,
  notify_email = $11, filters = $12,
  notify_feishu = CASE WHEN $17 THEN $13 ELSE notify_feishu END,
  notify_emails = CASE WHEN $18 THEN $14 ELSE notify_emails END,
  custom_message = CASE WHEN $19 THEN $15 ELSE custom_message END,
  updated_at = NOW()
WHERE id = $16
RETURNING `+opsAlertRuleColumns, args...)
	return scanOpsAlertRule(row)
}

func (r *opsRepository) DeleteAlertRule(ctx context.Context, id int64) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("nil ops repository")
	}
	if id <= 0 {
		return fmt.Errorf("invalid id")
	}

	res, err := r.db.ExecContext(ctx, "DELETE FROM ops_alert_rules WHERE id = $1", id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

const opsAlertEventColumns = `id, COALESCE(rule_id, 0), COALESCE(severity, ''), COALESCE(status, ''),
  COALESCE(title, ''), COALESCE(description, ''), metric_value, threshold_value, dimensions,
  fired_at, resolved_at, email_sent, feishu_sent, created_at`

const opsAlertDeliveryPlanKey = "_ops_delivery_plan_v1"

// Prefer an outstanding/failed recipient over an aggregate sent flag. This
// lets the dashboard show a partial email delivery that still needs attention.
func opsAlertEventDeliveryStatus(column string) string {
	return `COALESCE((SELECT CASE
  WHEN bool_or(d.status = 'pending') THEN 'pending'
  WHEN bool_or(d.status = 'failed') THEN 'failed'
  WHEN bool_or(d.status = 'sent') THEN 'sent'
  ELSE 'disabled' END
  FROM ops_alert_deliveries d WHERE d.event_id = ops_alert_events.id AND d.channel = '` + column + `'
  HAVING COUNT(*) > 0), CASE WHEN ` + column + `_sent THEN 'sent'
  WHEN dimensions @> '{"` + opsAlertDeliveryPlanKey + `":true}'::jsonb THEN 'disabled' ELSE 'unknown' END)`
}

var opsAlertEventRead = `SELECT ` + opsAlertEventColumns + `,
  (SELECT a.name FROM accounts a WHERE a.deleted_at IS NULL AND a.id =
    CASE WHEN dimensions->>'latest_account_id' ~ '^[0-9]{1,19}$' THEN
      CASE WHEN (dimensions->>'latest_account_id')::numeric <= 9223372036854775807
        THEN (dimensions->>'latest_account_id')::bigint END END), ` +
	opsAlertEventDeliveryStatus("email") + `, ` + opsAlertEventDeliveryStatus("feishu") + `
FROM ops_alert_events `

// Expand each retained upstream attempt, never mixing it with the denormalized
// last attempt or the final client status. Only rows without an array use the
// legacy columns, including legacy rows with JSON null or a non-array value.
const countAccountUpstreamErrorsSQL = `
WITH matched AS MATERIALIZED (
  SELECT e.id, hop.*
  FROM ops_error_logs e
  CROSS JOIN LATERAL (
    SELECT
      CASE WHEN item->>'account_id' ~ '^[0-9]{1,19}$' THEN
        CASE WHEN (item->>'account_id')::numeric BETWEEN 1 AND 9223372036854775807
          THEN (item->>'account_id')::bigint END END AS account_id,
      CASE WHEN item->>'upstream_status_code' ~ '^[45][0-9]{2}$'
        THEN (item->>'upstream_status_code')::int END AS status_code,
      item->>'provider_code' AS provider_code,
      item->>'platform' AS platform,
      item->>'message' AS message,
      item->>'detail' AS detail,
      COALESCE(CASE WHEN item->>'at_unix_ms' ~ '^[1-9][0-9]{0,15}$'
        THEN (item->>'at_unix_ms')::bigint END, (EXTRACT(EPOCH FROM e.created_at) * 1000)::bigint) AS at_unix_ms,
      ord
    FROM jsonb_array_elements(CASE WHEN jsonb_typeof(e.upstream_errors) = 'array'
      THEN e.upstream_errors ELSE '[]'::jsonb END) WITH ORDINALITY AS a(item, ord)
    WHERE jsonb_typeof(item) = 'object'
    UNION ALL
    SELECT e.account_id, e.upstream_status_code, e.provider_error_code, e.platform,
      e.upstream_error_message, e.upstream_error_detail, (EXTRACT(EPOCH FROM e.created_at) * 1000)::bigint, 0
    WHERE jsonb_typeof(e.upstream_errors) IS DISTINCT FROM 'array'
  ) hop
  WHERE e.created_at >= $1 AND e.created_at < $2
    AND ($3::bigint IS NULL OR e.group_id = $3)
    AND ($4::text = '' OR hop.platform = $4)
    AND hop.status_code BETWEEN 400 AND 599
    AND (COALESCE(cardinality($5::bigint[]), 0) = 0 OR hop.account_id = ANY($5))
    AND (COALESCE(cardinality($6::int[]), 0) = 0 OR hop.status_code = ANY($6))
    AND (COALESCE(cardinality($7::text[]), 0) = 0 OR hop.provider_code = ANY($7))
    AND (COALESCE(cardinality($8::text[]), 0) = 0 OR EXISTS (
      SELECT 1 FROM unnest($8::text[]) AS keyword(pattern)
      WHERE hop.message ILIKE keyword.pattern ESCAPE E'\\' OR hop.detail ILIKE keyword.pattern ESCAPE E'\\'
    ))
)
SELECT summary.match_count, COALESCE(latest.account_id, 0), COALESCE(latest.status_code, 0),
  COALESCE(latest.provider_code, ''), COALESCE(latest.platform, ''), summary.matched_account_ids
FROM (
  SELECT COUNT(*) AS match_count,
    COALESCE(array_agg(DISTINCT account_id ORDER BY account_id) FILTER (WHERE account_id > 0), ARRAY[]::bigint[]) AS matched_account_ids
  FROM matched
) summary
LEFT JOIN LATERAL (
  SELECT account_id, status_code, provider_code, platform
  FROM matched ORDER BY at_unix_ms DESC, id DESC, ord DESC LIMIT 1
) latest ON TRUE`

var opsAlertKeywordEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func (r *opsRepository) CountAccountUpstreamErrors(ctx context.Context, filter service.OpsAccountUpstreamErrorFilter) (service.OpsAccountUpstreamErrorMatch, error) {
	var match service.OpsAccountUpstreamErrorMatch
	if r == nil || r.db == nil || !filter.Selective() || !filter.End.After(filter.Start) || filter.End.Sub(filter.Start) > time.Hour {
		return match, fmt.Errorf("invalid account upstream alert query bounds")
	}
	patterns := make([]string, 0, len(filter.ErrorKeywords))
	for _, keyword := range filter.ErrorKeywords {
		patterns = append(patterns, "%"+opsAlertKeywordEscaper.Replace(keyword)+"%")
	}
	err := r.db.QueryRowContext(ctx, countAccountUpstreamErrorsSQL, filter.Start, filter.End,
		opsNullInt64(filter.GroupID), filter.Platform, pq.Array(filter.AccountIDs), pq.Array(filter.UpstreamStatusCodes),
		pq.Array(filter.ProviderCodes), pq.Array(patterns)).Scan(&match.Count, &match.LatestAccountID,
		&match.LatestUpstreamStatusCode, &match.LatestProviderCode, &match.LatestPlatform, pq.Array(&match.MatchedAccountIDs))
	match.LatestProviderCode = validOpsProviderErrorCode(match.LatestProviderCode)
	return match, err
}

func (r *opsRepository) ListAlertEvents(ctx context.Context, filter *service.OpsAlertEventFilter) ([]*service.OpsAlertEvent, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if filter == nil {
		filter = &service.OpsAlertEventFilter{}
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 500)
	where, args := buildOpsAlertEventsWhere(filter)
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, opsAlertEventRead+where+` ORDER BY fired_at DESC, id DESC LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []*service.OpsAlertEvent{}
	for rows.Next() {
		event, err := scanOpsAlertEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (r *opsRepository) GetAlertEventByID(ctx context.Context, eventID int64) (*service.OpsAlertEvent, error) {
	return r.readAlertEvent(ctx, eventID, `WHERE id = $1`)
}

func (r *opsRepository) GetActiveAlertEvent(ctx context.Context, ruleID int64) (*service.OpsAlertEvent, error) {
	return r.readAlertEvent(ctx, ruleID, `WHERE rule_id = $1 AND status = 'firing' ORDER BY fired_at DESC, id DESC LIMIT 1`)
}

func (r *opsRepository) GetLatestAlertEvent(ctx context.Context, ruleID int64) (*service.OpsAlertEvent, error) {
	return r.readAlertEvent(ctx, ruleID, `WHERE rule_id = $1 ORDER BY fired_at DESC, id DESC LIMIT 1`)
}

func (r *opsRepository) readAlertEvent(ctx context.Context, id int64, where string) (*service.OpsAlertEvent, error) {
	if r == nil || r.db == nil || id <= 0 {
		return nil, fmt.Errorf("missing ops repository or valid event/rule ID")
	}
	event, err := scanOpsAlertEvent(r.db.QueryRowContext(ctx, opsAlertEventRead+where, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return event, err
}

func (r *opsRepository) CreateAlertEvent(ctx context.Context, event *service.OpsAlertEvent) (*service.OpsAlertEvent, error) {
	if r == nil || r.db == nil || event == nil {
		return nil, fmt.Errorf("missing ops repository or event")
	}
	dimensions := event.Dimensions
	if event.DeliveryPlan != nil {
		dimensions = make(map[string]any, len(event.Dimensions)+1)
		maps.Copy(dimensions, event.Dimensions)
		dimensions[opsAlertDeliveryPlanKey] = true
	}
	dimensionsArg, err := opsNullJSONMap(dimensions)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO ops_alert_events (rule_id, severity, status, title, description,
  metric_value, threshold_value, dimensions, fired_at, resolved_at, email_sent, feishu_sent)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
		opsNullInt64(&event.RuleID), opsNullString(event.Severity), opsNullString(event.Status), opsNullString(event.Title),
		opsNullString(event.Description), opsNullFloat64(event.MetricValue), opsNullFloat64(event.ThresholdValue),
		dimensionsArg, event.FiredAt, opsNullTime(event.ResolvedAt), event.EmailSent, event.FeishuSent).Scan(&id)
	if err != nil {
		return nil, err
	}
	for _, target := range event.DeliveryPlan {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO ops_alert_deliveries (event_id, channel, target) VALUES ($1,$2,$3)
ON CONFLICT (event_id, channel, target) DO NOTHING`, id, target.Channel, target.Target); err != nil {
			return nil, err
		}
	}
	created, err := scanOpsAlertEvent(tx.QueryRowContext(ctx, opsAlertEventRead+`WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return created, nil
}

func (r *opsRepository) UpdateAlertEventStatus(ctx context.Context, eventID int64, status string, resolvedAt *time.Time) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("nil ops repository")
	}
	if eventID <= 0 {
		return fmt.Errorf("invalid event id")
	}
	if strings.TrimSpace(status) == "" {
		return fmt.Errorf("invalid status")
	}

	q := `
UPDATE ops_alert_events
SET status = $2,
    resolved_at = $3
WHERE id = $1`

	_, err := r.db.ExecContext(ctx, q, eventID, strings.TrimSpace(status), opsNullTime(resolvedAt))
	return err
}

func (r *opsRepository) UpdateAlertEventEmailSent(ctx context.Context, eventID int64, emailSent bool) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("nil ops repository")
	}
	if eventID <= 0 {
		return fmt.Errorf("invalid event id")
	}

	_, err := r.db.ExecContext(ctx, "UPDATE ops_alert_events SET email_sent = $2 WHERE id = $1", eventID, emailSent)
	return err
}

type opsAlertEventRow interface {
	Scan(dest ...any) error
}

func (r *opsRepository) CreateAlertSilence(ctx context.Context, input *service.OpsAlertSilence) (*service.OpsAlertSilence, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if input == nil {
		return nil, fmt.Errorf("nil input")
	}
	if input.RuleID <= 0 {
		return nil, fmt.Errorf("invalid rule_id")
	}
	platform := strings.TrimSpace(input.Platform)
	if platform == "" {
		return nil, fmt.Errorf("invalid platform")
	}
	if input.Until.IsZero() {
		return nil, fmt.Errorf("invalid until")
	}

	q := `
INSERT INTO ops_alert_silences (
  rule_id,
  platform,
  group_id,
  region,
  until,
  reason,
  created_by,
  created_at
) VALUES (
  $1,$2,$3,$4,$5,$6,$7,NOW()
)
RETURNING id, rule_id, platform, group_id, region, until, COALESCE(reason,''), created_by, created_at`

	row := r.db.QueryRowContext(
		ctx,
		q,
		input.RuleID,
		platform,
		opsNullInt64(input.GroupID),
		opsNullString(input.Region),
		input.Until,
		opsNullString(input.Reason),
		opsNullInt64(input.CreatedBy),
	)

	var out service.OpsAlertSilence
	var groupID sql.NullInt64
	var region sql.NullString
	var createdBy sql.NullInt64
	if err := row.Scan(
		&out.ID,
		&out.RuleID,
		&out.Platform,
		&groupID,
		&region,
		&out.Until,
		&out.Reason,
		&createdBy,
		&out.CreatedAt,
	); err != nil {
		return nil, err
	}
	if groupID.Valid {
		v := groupID.Int64
		out.GroupID = &v
	}
	if region.Valid {
		v := strings.TrimSpace(region.String)
		if v != "" {
			out.Region = &v
		}
	}
	if createdBy.Valid {
		v := createdBy.Int64
		out.CreatedBy = &v
	}
	return &out, nil
}

func (r *opsRepository) IsAlertSilenced(ctx context.Context, ruleID int64, platform string, groupID *int64, region *string, now time.Time) (bool, error) {
	if r == nil || r.db == nil {
		return false, fmt.Errorf("nil ops repository")
	}
	if ruleID <= 0 {
		return false, fmt.Errorf("invalid rule id")
	}
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	q := `
SELECT 1
FROM ops_alert_silences
WHERE rule_id = $1
  AND platform = $2
  AND (group_id IS NOT DISTINCT FROM $3)
  AND (region IS NOT DISTINCT FROM $4)
  AND until > $5
LIMIT 1`

	var dummy int
	err := r.db.QueryRowContext(ctx, q, ruleID, platform, opsNullInt64(groupID), opsNullString(region), now).Scan(&dummy)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func scanOpsAlertEvent(row opsAlertEventRow) (*service.OpsAlertEvent, error) {
	var ev service.OpsAlertEvent
	var metricValue sql.NullFloat64
	var thresholdValue sql.NullFloat64
	var dimensionsRaw []byte
	var resolvedAt sql.NullTime
	var latestAccountName sql.NullString

	if err := row.Scan(
		&ev.ID,
		&ev.RuleID,
		&ev.Severity,
		&ev.Status,
		&ev.Title,
		&ev.Description,
		&metricValue,
		&thresholdValue,
		&dimensionsRaw,
		&ev.FiredAt,
		&resolvedAt,
		&ev.EmailSent,
		&ev.FeishuSent,
		&ev.CreatedAt,
		&latestAccountName,
		&ev.EmailDeliveryStatus,
		&ev.FeishuDeliveryStatus,
	); err != nil {
		return nil, err
	}
	if metricValue.Valid {
		v := metricValue.Float64
		ev.MetricValue = &v
	}
	if thresholdValue.Valid {
		v := thresholdValue.Float64
		ev.ThresholdValue = &v
	}
	if resolvedAt.Valid {
		v := resolvedAt.Time
		ev.ResolvedAt = &v
	}
	if len(dimensionsRaw) > 0 && string(dimensionsRaw) != "null" {
		decoder := json.NewDecoder(bytes.NewReader(dimensionsRaw))
		decoder.UseNumber()
		var decoded map[string]any
		if err := decoder.Decode(&decoded); err == nil {
			ev.Dimensions = decoded
		}
	}
	delete(ev.Dimensions, opsAlertDeliveryPlanKey)
	delete(ev.Dimensions, "latest_account_name")
	if latestAccountName.Valid && ev.Dimensions != nil {
		ev.Dimensions["latest_account_name"] = latestAccountName.String
	}
	return &ev, nil
}

func buildOpsAlertEventsWhere(filter *service.OpsAlertEventFilter) (string, []any) {
	clauses := []string{"1=1"}
	args := []any{}

	if filter == nil {
		return "WHERE " + strings.Join(clauses, " AND "), args
	}

	if status := strings.TrimSpace(filter.Status); status != "" {
		args = append(args, status)
		clauses = append(clauses, "status = $"+itoa(len(args)))
	}
	if severity := strings.TrimSpace(filter.Severity); severity != "" {
		args = append(args, severity)
		clauses = append(clauses, "severity = $"+itoa(len(args)))
	}
	if filter.EmailSent != nil {
		args = append(args, *filter.EmailSent)
		clauses = append(clauses, "email_sent = $"+itoa(len(args)))
	}
	if filter.StartTime != nil && !filter.StartTime.IsZero() {
		args = append(args, *filter.StartTime)
		clauses = append(clauses, "fired_at >= $"+itoa(len(args)))
	}
	if filter.EndTime != nil && !filter.EndTime.IsZero() {
		args = append(args, *filter.EndTime)
		clauses = append(clauses, "fired_at < $"+itoa(len(args)))
	}

	// Cursor pagination (descending by fired_at, then id)
	if filter.BeforeFiredAt != nil && !filter.BeforeFiredAt.IsZero() && filter.BeforeID != nil && *filter.BeforeID > 0 {
		args = append(args, *filter.BeforeFiredAt)
		tsArg := "$" + itoa(len(args))
		args = append(args, *filter.BeforeID)
		idArg := "$" + itoa(len(args))
		clauses = append(clauses, fmt.Sprintf("(fired_at < %s OR (fired_at = %s AND id < %s))", tsArg, tsArg, idArg))
	}
	// Dimensions are stored in JSONB. We filter best-effort without requiring GIN indexes.
	if platform := strings.TrimSpace(filter.Platform); platform != "" {
		args = append(args, platform)
		clauses = append(clauses, "(dimensions->>'platform') = $"+itoa(len(args)))
	}
	if filter.GroupID != nil && *filter.GroupID > 0 {
		args = append(args, fmt.Sprintf("%d", *filter.GroupID))
		clauses = append(clauses, "(dimensions->>'group_id') = $"+itoa(len(args)))
	}

	return "WHERE " + strings.Join(clauses, " AND "), args
}

func opsNullJSONMap(v map[string]any) (any, error) {
	if v == nil {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return sql.NullString{}, nil
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}
