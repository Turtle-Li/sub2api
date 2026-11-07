-- Operator-only report; run on the documented data host with the existing
-- PostgreSQL runtime identity. psql variables trial_start/trial_end must be
-- explicit ISO8601 timestamps. This never reads customer usage or credentials.
\set ON_ERROR_STOP on
BEGIN READ ONLY;
SET LOCAL statement_timeout = '20s';
SET LOCAL timezone = 'UTC';

WITH bounds AS (
  SELECT :'trial_start'::timestamptz AS starts_at,
         :'trial_end'::timestamptz AS ends_at
), samples AS (
  SELECT p.*
  FROM channel_monitor_v3_group_probes p, bounds b
  WHERE p.slot_start >= b.starts_at AND p.slot_start < b.ends_at
), summary AS (
  SELECT group_id, model,
         count(*) AS checks,
         sum(CASE WHEN attempt_count IS NULL AND finished_at IS NOT NULL THEN 1 ELSE COALESCE(attempt_count,0) END) AS attempts,
         count(*) FILTER (WHERE outcome = 'success') AS successes,
         count(*) FILTER (WHERE outcome = 'failure') AS failures,
         count(*) FILTER (WHERE outcome = 'cancelled') AS cancelled,
         count(*) FILTER (WHERE outcome = 'pending') AS pending,
         count(*) FILTER (WHERE finished_at IS NOT NULL AND input_tokens = 0 AND output_tokens = 0) AS completed_without_reported_usage,
         sum(CASE WHEN attempt_count IS NULL AND finished_at IS NOT NULL AND input_tokens = 0 AND output_tokens = 0 THEN 1 ELSE missing_usage_attempts END) AS attempts_without_reported_usage,
         count(*) FILTER (WHERE status_hidden) AS checks_cleared_from_status,
         sum(input_tokens) AS reported_input_tokens,
         sum(output_tokens) AS reported_output_tokens,
         max(input_tokens) AS maximum_input_per_check,
         max(output_tokens) AS maximum_output_per_check,
         min(slot_start) AS first_slot,
         max(slot_start) AS last_slot
  FROM samples GROUP BY group_id, model
)
SELECT json_build_object(
  'trial_start', b.starts_at,
  'trial_end', b.ends_at,
  'collected_at', now(),
  'window_complete', now() >= b.ends_at,
  'input_definition', 'Provider-reported total input, including cache read/write after the token-accounting release',
  'usage_caveat', 'Counts distinguish scheduled checks from runner-owned attempts. All attempts and cleared history remain included. Forwarder-owned compatibility handling may perform extra upstream work. Missing usage or pending checks are unknown consumption, not proven zero. Tokens are not a monetary bill.',
  'customer_billing', false,
  'groups', COALESCE((SELECT json_agg(s ORDER BY s.group_id, s.model) FROM summary s), '[]'::json)
) FROM bounds b;
ROLLBACK;
