package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelModelPricingCurrencyMigration(t *testing.T) {
	content, err := FS.ReadFile("242_channel_model_pricing_currency.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE channel_model_pricing ADD COLUMN IF NOT EXISTS currency VARCHAR(3) NOT NULL DEFAULT 'USD'")
	require.Contains(t, sql, "ALTER TABLE channel_account_stats_model_pricing ADD COLUMN IF NOT EXISTS currency VARCHAR(3) NOT NULL DEFAULT 'USD'")
	require.Contains(t, sql, "chk_channel_model_pricing_currency")
	require.Contains(t, sql, "chk_channel_account_stats_model_pricing_currency")
	require.Contains(t, sql, "CHECK (currency IN ('USD', 'CNY')) NOT VALID")
	require.NotContains(t, strings.ToUpper(sql), "UPDATE ")
}
