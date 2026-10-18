-- Provider that actually served the request, when the upstream response declares
-- one: the Cline Pass planner pipeline reports
-- provider_metadata.gateway.routing.finalProvider, the direct/OpenRouter pipeline
-- reports a top-level provider. NULL means the upstream declared none.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS upstream_provider VARCHAR(64);

COMMENT ON COLUMN usage_logs.upstream_provider IS
    'Actual upstream provider observed in the response (Cline Pass planner finalProvider / OpenRouter provider); NULL when not declared.';
