package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageLogUpstreamProviderMigration(t *testing.T) {
	content, err := FS.ReadFile("262_usage_log_upstream_provider.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS upstream_provider VARCHAR(64)")
	require.Contains(t, sql, "COMMENT ON COLUMN usage_logs.upstream_provider IS")
	// Additive only: the migration must not rewrite or backfill existing rows.
	require.NotContains(t, strings.ToUpper(sql), "UPDATE ")
	require.NotContains(t, strings.ToUpper(sql), "NOT NULL")
}
