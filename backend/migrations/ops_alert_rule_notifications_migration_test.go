package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpsAlertRuleNotificationsMigration291(t *testing.T) {
	contents, err := FS.ReadFile("291_ops_alert_rule_notifications.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(contents)), " ")
	for _, statement := range []string{
		"ALTER TABLE ops_alert_rules ADD COLUMN IF NOT EXISTS notify_feishu BOOLEAN NOT NULL DEFAULT FALSE",
		"ADD COLUMN IF NOT EXISTS notify_emails JSONB", "ADD COLUMN IF NOT EXISTS custom_message TEXT",
		"ALTER TABLE ops_alert_events ADD COLUMN IF NOT EXISTS feishu_sent BOOLEAN NOT NULL DEFAULT FALSE",
		"CREATE TABLE IF NOT EXISTS ops_alert_deliveries", "REFERENCES ops_alert_events (id) ON DELETE CASCADE",
		"UNIQUE (event_id, channel, target)", "CHECK ((claim_token IS NULL) = (claimed_until IS NULL))",
		"CREATE INDEX IF NOT EXISTS ops_alert_deliveries_pending_idx",
	} {
		require.Contains(t, sql, statement)
	}
	for _, destructive := range []string{"DROP ", "UPDATE ", "TRUNCATE ", "DELETE FROM "} {
		require.NotContains(t, strings.ToUpper(sql), destructive)
	}
}
