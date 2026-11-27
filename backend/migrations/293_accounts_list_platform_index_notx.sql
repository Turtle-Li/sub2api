-- no-transaction
-- Speed up default admin accounts listing filtered by platform
-- (WHERE deleted_at IS NULL AND pool_id IS NULL AND platform = ? ORDER BY name ASC, id ASC).
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_accounts_unpooled_platform_name_id
ON accounts (platform, name, id)
WHERE deleted_at IS NULL AND pool_id IS NULL;
