package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBatchImageTerminalItemConsistencyMigration(t *testing.T) {
	sql, err := FS.ReadFile("188_batch_image_terminal_item_consistency.sql")
	require.NoError(t, err)

	content := string(sql)
	require.Contains(t, content, "UPDATE batch_image_items AS item")
	require.Contains(t, content, "item.status = 'pending'")
	require.Contains(t, content, "job.status = 'failed'")
	require.Contains(t, content, "NULLIF(job.last_error_code, '')")
	require.Contains(t, content, "NULLIF(job.last_error_message, '')")
	require.Contains(t, content, "SET success_count = (")
	require.Contains(t, content, "fail_count = (")
	require.Contains(t, content, "item.status = 'failed'")
}
