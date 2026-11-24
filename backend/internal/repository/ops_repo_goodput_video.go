package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// SyncGoodPutVideoAlerts materializes incidents from the durable ledger, not
// request traffic. An open task cannot suppress notifications for other tasks.
func (r *opsRepository) SyncGoodPutVideoAlerts(ctx context.Context, rule *service.OpsAlertRule, groupID *int64) ([]*service.OpsAlertEvent, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
INSERT INTO ops_alert_events (rule_id,severity,status,title,description,metric_value,threshold_value,
	dimensions,fired_at,resolved_at,goodput_video_job_id,goodput_video_incident)
SELECT $1,$2,CASE WHEN incident.recovered THEN 'resolved' ELSE 'firing' END,$3,
	concat(incident.reason, ' 模型 ', job.model, '，分组 #', job.group_id, '，任务 ', COALESCE(job.public_id, job.id::text), '。'),
	1,0,jsonb_build_object('platform','openai','group_id',job.group_id,'task_id',COALESCE(job.public_id,job.id::text)),
	NOW(),CASE WHEN incident.recovered THEN NOW() END,job.id,incident.kind
FROM goodput_video_jobs job
CROSS JOIN LATERAL (
	SELECT 'settlement' AS kind, job.state IN ('completed','failed') AS recovered,
		CASE
			WHEN job.native_id IS NULL THEN '提交回执缺失，不要重发。'
			WHEN job.review_reason='completed_usage_unverified' THEN '上游已完成，计费秒数无法核实。'
			WHEN job.review_reason='receipt_expired' THEN '回执查询窗口已结束，结算尚未确认。'
			WHEN job.state='review' OR job.review_reason='legacy_review' THEN '历史任务进入人工复核。'
			ELSE '超过等待窗口，结算仍未确认。'
		END AS reason
	WHERE job.state='review'
		OR (job.state IN ('submitting','pending') AND job.created_at < NOW()-make_interval(mins => $5))
		OR (job.state IN ('completed','failed') AND job.review_reason <> '' AND job.created_at > NOW()-INTERVAL '24 hours')
	UNION ALL
	SELECT 'content',false,'上游已完成，成片下载仍未恢复。'
	WHERE job.content_failure_at <= NOW()-INTERVAL '1 minute'
) incident
WHERE ($4::bigint IS NULL OR job.group_id=$4)
  AND NOT EXISTS (SELECT 1 FROM ops_alert_events e
	WHERE e.rule_id=$1 AND e.goodput_video_job_id=job.id AND e.goodput_video_incident=incident.kind)
ORDER BY job.created_at,job.id LIMIT 32
ON CONFLICT (rule_id,goodput_video_job_id,goodput_video_incident) WHERE goodput_video_job_id IS NOT NULL DO NOTHING`,
		rule.ID, rule.Severity, rule.Name, opsNullInt64(groupID), max(1, rule.WindowMinutes))
	if err != nil {
		return nil, err
	}

	_, err = tx.ExecContext(ctx, `
UPDATE ops_alert_events e SET status='resolved',resolved_at=NOW()
FROM goodput_video_jobs job
WHERE e.rule_id=$1 AND e.status='firing' AND e.goodput_video_job_id=job.id
  AND ((e.goodput_video_incident='settlement' AND job.state IN ('completed','failed'))
	OR (e.goodput_video_incident='content' AND job.content_failure_at IS NULL))`, rule.ID)
	if err != nil {
		return nil, err
	}

	// A real incident may recover before its first notification. Give those
	// events a short delivery window without sending a second recovery email.
	rows, err := tx.QueryContext(ctx, opsAlertEventRead+`
WHERE rule_id=$1 AND goodput_video_job_id IS NOT NULL AND NOT email_sent
  AND (status='firing' OR (status='resolved' AND fired_at > NOW()-INTERVAL '30 minutes'))
ORDER BY fired_at DESC,id DESC LIMIT 16`, rule.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var events []*service.OpsAlertEvent
	for rows.Next() {
		event, err := scanOpsAlertEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return events, nil
}
