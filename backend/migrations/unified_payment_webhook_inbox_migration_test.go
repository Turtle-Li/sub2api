package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnifiedPaymentWebhookInboxStoresOnlyDedupeMetadata(t *testing.T) {
	content, err := FS.ReadFile("232_unified_payment_webhook_inbox.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "event_id UUID PRIMARY KEY")
	require.Contains(t, sql, "UNIQUE (payment_order_id, sequence)")
	require.Contains(t, sql, "max_processed_sequence BIGINT NOT NULL DEFAULT 0")
	require.Contains(t, sql, "active_event_id UUID")
	require.Contains(t, sql, "body_sha256 CHAR(64)")
	require.Contains(t, sql, "RETRYABLE_FAILED")
	require.NotContains(t, strings.ToLower(sql), "raw_body")
	require.NotContains(t, strings.ToLower(sql), "authorization")
}
