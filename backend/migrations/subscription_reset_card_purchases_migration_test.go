package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionResetCardPurchasesMigration(t *testing.T) {
	content, err := FS.ReadFile("241_subscription_reset_card_purchases.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS subscription_reset_card_purchases")
	require.Contains(t, sql, "purchase_key UUID NOT NULL")
	require.Contains(t, sql, "UNIQUE (user_id, purchase_key)")
	require.Contains(t, sql, "UNIQUE (grant_id)")
	require.Contains(t, sql, "grant_id BIGINT NOT NULL REFERENCES subscription_reset_grants(id) ON DELETE RESTRICT")
	require.Contains(t, sql, "CHECK (price > 0)")
}
