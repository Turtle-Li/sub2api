-- Separate transaction from the metadata-only ALTER. Only the visible 72 slots
-- are needed; never rebuild usage, V2 diagnostics, rollups or billing here.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '2min';

WITH bounds AS MATERIALIZED (
  SELECT date_bin(interval '10 minutes', now(), timestamptz '1970-01-01 00:00:00+00') - interval '710 minutes' AS lo,
         date_trunc('minute',now()) AS hi
), targets AS MATERIALIZED (
  SELECT m.bucket_start, m.group_id, m.platform, m.model
  FROM channel_monitor_v2_metrics_1m m JOIN groups g ON g.id=m.group_id CROSS JOIN bounds b
  WHERE g.platform='openai' AND m.bucket_start >= b.lo AND m.bucket_start < b.hi
    AND m.error_requests > 0 AND m.openai_fault_requests IS NULL
), candidates AS MATERIALIZED (
  SELECT e.id, NULLIF(e.request_id,'') AS request_id FROM ops_error_logs e
  JOIN (SELECT DISTINCT group_id,bucket_start FROM targets) t ON e.group_id=t.group_id
    AND e.created_at >= t.bucket_start AND e.created_at < t.bucket_start + interval '1 minute'
  CROSS JOIN bounds b
  WHERE e.created_at >= b.lo AND e.created_at < b.hi
    AND NOT e.is_count_tokens AND (e.status_code >= 400 OR e.error_type='cyber_policy')
), final_ids AS (
  SELECT id FROM candidates WHERE request_id IS NULL
  UNION ALL
  SELECT latest.id FROM (SELECT DISTINCT request_id FROM candidates WHERE request_id IS NOT NULL) c
  CROSS JOIN bounds b CROSS JOIN LATERAL (
    SELECT e.id FROM ops_error_logs e WHERE e.request_id=c.request_id AND NOT e.is_count_tokens
      AND (e.status_code >= 400 OR e.error_type='cyber_policy')
      AND e.created_at >= b.lo - interval '90 minutes' AND e.created_at < b.hi
    ORDER BY e.created_at DESC, e.id DESC LIMIT 1
  ) latest
), restored AS (
  SELECT date_trunc('minute',e.created_at) AS bucket_start, e.group_id,
         lower(COALESCE(NULLIF(trim(e.platform),''),'unknown')) AS platform,
         COALESCE(NULLIF(trim(e.requested_model),''),NULLIF(trim(e.model),''),'unknown') AS model,
         count(*) AS total, count(*) FILTER (WHERE channel_monitor_openai_fault_v1(e)) AS faults
  FROM final_ids f JOIN ops_error_logs e ON e.id=f.id GROUP BY 1,2,3,4
)
UPDATE channel_monitor_v2_metrics_1m m SET openai_fault_requests=r.faults
FROM restored r JOIN targets t USING(bucket_start,group_id,platform,model)
WHERE (m.bucket_start,m.group_id,m.platform,m.model)=(r.bucket_start,r.group_id,r.platform,r.model)
  AND m.openai_fault_requests IS NULL AND m.error_requests=r.total;
