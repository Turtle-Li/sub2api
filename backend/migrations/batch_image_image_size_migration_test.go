package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBatchImageImageSizeMigration(t *testing.T) {
	content, err := FS.ReadFile("261_batch_image_image_size.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS image_size VARCHAR(2) NOT NULL DEFAULT '1K'")
	require.Contains(t, sql, "CHECK (image_size IN ('1K', '2K', '4K'))")
}
