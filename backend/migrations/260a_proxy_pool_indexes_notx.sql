-- no-transaction
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_proxies_pool_id
  ON proxies (pool_id) WHERE pool_id IS NOT NULL;
