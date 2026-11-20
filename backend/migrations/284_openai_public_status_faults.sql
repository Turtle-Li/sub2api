SET LOCAL lock_timeout = '5s';

-- NULL means an older collector has not attributed this minute; zero is a verified result.
ALTER TABLE channel_monitor_v2_metrics_1m ADD COLUMN IF NOT EXISTS openai_fault_requests BIGINT;

-- Only for the public OpenAI group scorer. Use final Ops fields, not raw
-- bodies, the intermediate failover array, diagnostic categories or billing.
CREATE OR REPLACE FUNCTION channel_monitor_openai_fault_v1(e ops_error_logs)
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT CASE
    WHEN COALESCE(e.is_count_tokens, false) OR COALESCE(e.status_code, 0) < 400
      OR e.status_code = 499 OR e.error_type IN ('cyber_policy','client_cancelled','client_canceled','client_disconnected')
      OR code IN ('client_closed_request','client_disconnected','client_cancelled','client_canceled','request_body_timeout')
      OR final_msg IN ('timed out reading request body','request body read timed out','client closed the connection before the request body was received')
      OR final_msg ~ '^client (disconnected|cancell?ed|closed request)($|[.]$| (during|before) )' THEN false
    -- Fetching our credential is not fetching a caller's asset.
    WHEN e.error_phase = 'account_auth' AND e.error_owner = 'provider' THEN true
    WHEN code IN ('download_timeout','url_download_timeout','upstream_download_timeout',
                  'file_download_timeout','image_download_timeout','asset_download_timeout')
      OR EXISTS (
        SELECT 1 FROM unnest(summaries) m WHERE
          m ~ '(^|: )(unable|failed) to (download\M|fetch (the |a |an )?(provided |remote |input )?((file|image|asset|url|attachment)\M|https?://)).*(timeout|timed out|deadline exceeded)'
          OR m ~ '(^|: )(upstream[ _-])?(((file|image|asset|url|attachment)[ _-])?download|(file|image|asset|url|attachment)[ _-]fetch)[ _:-]+(timeout|timed out|deadline exceeded)'
          OR m ~ '(timeout|timed out|deadline exceeded) (while |when )?(downloading|fetching) (the |a |an )?(file|image|asset|url|attachment|https?://)'
          OR m ~ '(上游)?(下载|拉取)(文件|图片|素材|附件|结果)?(请求)?超时'
      ) THEN false
    WHEN code IN ('content_policy_violation','content_filter','moderation_blocked','safety_violation')
      OR e.error_type IN ('content_policy','content_policy_violation')
      OR EXISTS (SELECT 1 FROM unnest(summaries) m WHERE m ~ '^(request (was )?(blocked|rejected) (by|due to) (the )?(content|safety) policy|content policy violation)') THEN false
    -- The scheduler marks exhaustion as business-limited in Ops, but the pool is ours.
    WHEN e.error_phase = 'routing' AND e.error_owner = 'platform' AND e.status_code IN (429,503)
      AND (final_msg IN ('service temporarily unavailable','all available accounts are currently rate-limited. please retry later.')
        OR final_msg ~ '^(no (available|healthy) (upstream |openai |compatible )?accounts?|account pool (exhausted|unavailable))([.,:]|$)') THEN true
    WHEN e.error_owner = 'client' OR e.error_phase IN ('request','auth','billing','concurrency') THEN false
    WHEN e.error_owner = 'provider' AND e.error_phase IN ('upstream','network') THEN CASE
      -- A caller's final error can follow a failed model attempt, or be wrapped as a 502.
      WHEN e.status_code IN (400,404,413,415,422) AND e.upstream_status_code >= 500
        AND e.error_type IN ('invalid_request_error','context_length_exceeded','model_not_found') THEN false
      WHEN code IN ('invalid_api_key','account_deactivated','account_disabled','account_suspended','invalid_token','token_invalidated',
                    'insufficient_quota','billing_hard_limit_reached','usage_limit_reached','account_forbidden','invalid_grant','token_revoked')
        OR (e.account_id IS NOT NULL AND EXISTS (SELECT 1 FROM unnest(summaries) m WHERE
          m ~ '^(you exceeded your current quota|billing hard limit (has been )?reached|(your |the )?account (has been |is )?(deactivated|suspended)|upstream authentication failed|upstream payment required)\M')) THEN true
      -- OpenAI sometimes reports model overload as 400, including in SSE. Do not
      -- assume every provider 400 is caller input or search echoed prompt text.
      WHEN e.account_id IS NOT NULL AND (code IN ('server_is_overloaded','slow_down') OR e.error_type = 'overloaded_error'
        OR EXISTS (SELECT 1 FROM unnest(summaries) m WHERE m ~ '^((the )?selected model is at capacity|(our |the )?servers? (is|are) (currently )?overloaded)\M')) THEN true
      WHEN e.upstream_status_code IN (401,402,429) THEN true
      WHEN e.upstream_status_code = 403 OR (e.status_code = 403 AND COALESCE(e.upstream_status_code,0) < 400) THEN
        code IN ('account_forbidden','insufficient_scopes') OR EXISTS (SELECT 1 FROM unnest(summaries) m WHERE
          m ~ '^((upstream account|api key|access token).{0,48}(forbidden|disabled|revoked|permission)|missing scopes:|you have insufficient permissions for this operation[.] missing scopes:)')
      WHEN e.upstream_status_code BETWEEN 400 AND 499 THEN e.upstream_status_code = 408
      WHEN code IN ('unsupported_parameter','previous_response_not_found','invalid_image_url','invalid_file_id','context_length_exceeded') THEN false
      WHEN e.upstream_status_code >= 500 THEN true
      WHEN e.status_code = 429 THEN e.account_id IS NOT NULL AND (final_msg LIKE 'upstream rate limit exceeded%'
        OR upstream_msg LIKE 'rate limit reached for %' OR upstream_msg = 'the usage limit has been reached')
      WHEN COALESCE(e.is_business_limited,false) OR e.error_type IN ('invalid_request_error','context_length_exceeded','model_not_found')
        OR code IN ('invalid_request','invalid_value') THEN false
      WHEN e.status_code IN (401,402) THEN e.account_id IS NOT NULL
      ELSE e.status_code >= 500 AND (e.account_id IS NOT NULL OR e.error_phase = 'network' OR e.error_source = 'upstream_http') END
    ELSE NOT COALESCE(e.is_business_limited,false) AND e.error_owner = 'platform' AND e.error_phase IN ('routing','internal') AND e.status_code >= 500
  END IS TRUE
  FROM (SELECT lower(COALESCE(e.provider_error_code,'')) AS code,
               lower(trim(left(e.error_message,1024))) AS final_msg,
               lower(trim(left(e.upstream_error_message,1024))) AS upstream_msg) signals
  CROSS JOIN LATERAL (SELECT ARRAY[final_msg,upstream_msg] AS summaries) summary
$$;
