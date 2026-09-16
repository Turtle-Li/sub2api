package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProxyDetectedTimezoneMigration(t *testing.T) {
	content, err := FS.ReadFile("252_proxy_detected_timezone.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS detected_timezone VARCHAR(64)")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS timezone_detected_at TIMESTAMPTZ")
	require.Contains(t, sql, "proxies_detected_timezone_nonempty")
}
