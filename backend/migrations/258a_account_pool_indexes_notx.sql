CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_accounts_pool_id
    ON accounts (pool_id) WHERE pool_id IS NOT NULL;
