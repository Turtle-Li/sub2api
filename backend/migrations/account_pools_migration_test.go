package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountPoolsMigration(t *testing.T) {
	content, err := FS.ReadFile("258_account_pools.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS account_pools")
	require.Contains(t, sql, "ALTER TABLE accounts ADD COLUMN IF NOT EXISTS pool_id BIGINT")
	require.Contains(t, sql, "REFERENCES account_pools(id) ON DELETE SET NULL NOT VALID")

	index, err := FS.ReadFile("258a_account_pool_indexes_notx.sql")
	require.NoError(t, err)
	indexSQL := strings.Join(strings.Fields(string(index)), " ")
	require.Contains(t, indexSQL, "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_accounts_pool_id ON accounts (pool_id) WHERE pool_id IS NOT NULL")
}
