-- Keep the submitted quote intact and record the independently verified meter.
ALTER TABLE goodput_video_jobs
    ADD COLUMN billed_seconds INT CHECK (billed_seconds BETWEEN 4 AND seconds),
    ADD COLUMN review_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN content_failure_at TIMESTAMPTZ;

CREATE INDEX goodput_video_jobs_review_due_idx ON goodput_video_jobs(next_poll_at)
    WHERE state = 'review' AND native_id IS NOT NULL
      AND review_reason IN ('', 'completed_usage_unverified');

ALTER TABLE ops_alert_events ADD COLUMN goodput_video_job_id UUID, ADD COLUMN goodput_video_incident TEXT;
CREATE UNIQUE INDEX ops_alert_events_goodput_job_idx ON ops_alert_events(rule_id, goodput_video_job_id, goodput_video_incident)
    WHERE goodput_video_job_id IS NOT NULL;

INSERT INTO ops_alert_rules (name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes)
VALUES ('GoodPut H3 生成或结算异常', '逐任务告警：回执丢失、计费秒数无法核实、长期未结算，或成片下载持续失败。',
    true, 'goodput_video_incident', '>', 0, 15, 1, 'P1', true, 0)
ON CONFLICT (name) DO NOTHING;
