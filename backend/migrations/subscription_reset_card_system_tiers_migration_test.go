package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionResetCardSystemTiersMigrationBindsOnlyReviewedProducts(t *testing.T) {
	content, err := FS.ReadFile("250_subscription_reset_card_system_tiers.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS subscription_product_code VARCHAR(64)")
	require.Contains(t, sql, "groups_subscription_product_code_unique")
	require.Contains(t, sql, "groups_subscription_product_code_valid")
	require.Contains(t, sql, "conrelid = 'groups'::regclass")
	require.Contains(t, sql, "subscription_product_code IN ('openai_plus', 'openai_5x')")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS subscription_reset_card_schedules_occurrence_count_valid")
	require.Contains(t, sql, "CHECK (occurrence_count BETWEEN 1 AND 120)")
	require.Contains(t, sql, "WHERE id = 4")
	require.Contains(t, sql, "name = 'GPT PLUS'")
	require.Contains(t, sql, "platform = 'openai'")
	require.Contains(t, sql, "subscription_type = 'subscription'")
	require.Contains(t, sql, "subscription_product_code = 'openai_plus'")
	require.Contains(t, sql, "SELECT 4, 'openai_subscription', 1")
	require.Contains(t, sql, "WHERE id = 12")
	require.Contains(t, sql, "name = 'GPT 5X PRO'")
	require.Contains(t, sql, "subscription_product_code = 'openai_5x'")
	require.Contains(t, sql, "SELECT 12, 'openai_subscription', 2")
	require.Contains(t, sql, "system tier conflict")
	require.Contains(t, sql, "prevent_subscription_product_code_mutation")
	require.Contains(t, sql, "groups_prevent_subscription_product_code_mutation")
	require.Contains(t, sql, "prevent_system_subscription_reset_card_tier_mutation")
	require.Contains(t, sql, "subscription_reset_card_tiers_prevent_system_mutation")
	require.Contains(t, sql, "managed by versioned migrations")
	require.Contains(t, sql, "Historical grants remain intentionally untouched")
	require.NotContains(t, sql, "UPDATE subscription_reset_grants")
}
