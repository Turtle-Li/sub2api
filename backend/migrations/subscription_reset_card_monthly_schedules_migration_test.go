package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionResetCardMonthlySchedulesMigrationKeepsAnIdempotentIssueLedger(t *testing.T) {
	content, err := FS.ReadFile("249_subscription_reset_card_monthly_schedules.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS subscription_reset_card_schedules")
	require.Contains(t, sql, "payment_order_id BIGINT NOT NULL UNIQUE REFERENCES payment_orders(id) ON DELETE RESTRICT")
	require.Contains(t, sql, "anchor_timezone VARCHAR(64) NOT NULL")
	require.Contains(t, sql, "occurrence_count INTEGER NOT NULL")
	require.Contains(t, sql, "cards_per_occurrence INTEGER NOT NULL")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS subscription_reset_card_issuances")
	require.Contains(t, sql, "UNIQUE (schedule_id, occurrence_index)")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS schedule_issuance_id BIGINT")
	require.Contains(t, sql, "ON subscription_reset_grants (schedule_issuance_id)")
	require.Contains(t, sql, "Scheduled grants leave payment_order_id NULL")
	require.Contains(t, sql, "not backfilled")
}
