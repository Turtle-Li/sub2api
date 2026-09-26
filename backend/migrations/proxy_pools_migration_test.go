package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProxyPoolsMigration(t *testing.T) {
	content, err := FS.ReadFile("260_proxy_pools.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS proxy_pools")
	require.Contains(t, sql, "ALTER TABLE proxies ADD COLUMN IF NOT EXISTS pool_id BIGINT")
	require.Contains(t, sql, "REFERENCES proxy_pools(id) ON DELETE SET NULL NOT VALID")

	index, err := FS.ReadFile("260a_proxy_pool_indexes_notx.sql")
	require.NoError(t, err)
	indexSQL := strings.Join(strings.Fields(string(index)), " ")
	require.Contains(t, indexSQL, "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_proxies_pool_id")
}
