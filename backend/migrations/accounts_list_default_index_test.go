package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountsListDefaultIndexMigration(t *testing.T) {
	content, err := FS.ReadFile("292_accounts_list_default_index_notx.sql")
	require.NoError(t, err)
	raw := string(content)
	require.Contains(t, raw, "-- no-transaction")
	normalized := strings.Join(strings.Fields(raw), " ")
	require.Contains(
		t,
		normalized,
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_accounts_unpooled_name_id ON accounts (name, id) WHERE deleted_at IS NULL AND pool_id IS NULL",
	)
}
