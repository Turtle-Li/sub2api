-- no-transaction
-- Cover the default admin accounts list query (deleted_at IS NULL AND pool_id IS NULL ORDER BY name ASC, id ASC)
-- without scanning pooled or soft-deleted rows and without blocking production gateway traffic.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_accounts_unpooled_name_id
    ON accounts (name, id)
    WHERE deleted_at IS NULL AND pool_id IS NULL;
