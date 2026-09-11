package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageLogSettlementCurrencyMigration(t *testing.T) {
	content, err := FS.ReadFile("243_usage_log_settlement_currency.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS currency VARCHAR(3) NOT NULL DEFAULT 'USD'")
	require.Contains(t, sql, "chk_usage_logs_currency")
	require.Contains(t, sql, "CHECK (currency IN ('USD', 'CNY')) NOT VALID")
	require.NotContains(t, strings.ToUpper(sql), "UPDATE ")
}
