package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionResetCardsMigration(t *testing.T) {
	content, err := FS.ReadFile("180_subscription_reset_cards.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS subscription_reset_grants")
	require.Contains(t, sql, "subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id) ON DELETE CASCADE")
	require.Contains(t, sql, "CHECK (quantity BETWEEN 1 AND 1000)")
	require.Contains(t, sql, "CHECK (used_count >= 0 AND used_count <= quantity)")
	require.Contains(t, sql, "CHECK (expires_at > created_at)")
	require.Contains(t, sql, "WHERE used_count < quantity")
}
