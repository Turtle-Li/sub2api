-- no-transaction
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_paid_fallback_created
    ON usage_logs (created_at) WHERE paid_fallback = TRUE;
