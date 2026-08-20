package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContentModerationFullPromptMigrationAddsIndependentColumn(t *testing.T) {
	sql, err := FS.ReadFile("229_content_moderation_full_prompt.sql")
	require.NoError(t, err)
	require.Contains(t, string(sql), "ALTER TABLE content_moderation_logs")
	require.Contains(t, string(sql), "ADD COLUMN IF NOT EXISTS full_prompt TEXT NOT NULL DEFAULT ''")
}
