package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResetCardPaymentGrantUniquenessMigration(t *testing.T) {
	content, err := FS.ReadFile("244_reset_card_payment_grant_uniqueness.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_reset_grants_payment_order_unique")
	require.Contains(t, sql, "ON subscription_reset_grants (payment_order_id)")
	require.Contains(t, sql, "WHERE payment_order_id IS NOT NULL")
	require.Contains(t, sql, "HAVING COUNT(*) > 1")
	require.Contains(t, sql, "duplicate payment_order_id rows require manual review")
}
