package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBatchImageIdempotencyUniqueMigration(t *testing.T) {
	content, err := FS.ReadFile("187_batch_image_idempotency_unique_notx.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_batch_image_jobs_owner_idempotency")
	require.Contains(t, sql, "ON batch_image_jobs (user_id, api_key_id, idempotency_key)")
	require.Contains(t, sql, "WHERE api_key_id IS NOT NULL")
	require.Contains(t, sql, "idempotency_key IS NOT NULL")
	require.Contains(t, sql, "idempotency_key <> ''")
}
