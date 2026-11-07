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
         count(*) AS attempts,
         count(*) FILTER (WHERE outcome = 'success') AS successes,
         count(*) FILTER (WHERE outcome = 'failure') AS failures,
         count(*) FILTER (WHERE outcome = 'cancelled') AS cancelled,
         count(*) FILTER (WHERE outcome = 'pending') AS pending,
         count(*) FILTER (WHERE finished_at IS NOT NULL AND input_tokens = 0 AND output_tokens = 0) AS completed_without_reported_usage,
         sum(input_tokens) AS reported_input_tokens,
         sum(output_tokens) AS reported_output_tokens,
         max(input_tokens) AS maximum_input_per_attempt,
         max(output_tokens) AS maximum_output_per_attempt,
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
  'usage_caveat', 'Missing upstream usage is unknown consumption, not proven zero. Tokens are not a monetary bill.',
  'customer_billing', false,
  'groups', COALESCE((SELECT json_agg(s ORDER BY s.group_id, s.model) FROM summary s), '[]'::json)
) FROM bounds b;
ROLLBACK;
