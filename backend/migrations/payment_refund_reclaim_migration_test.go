package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaymentRefundReclaimMigrationSeparatesPendingMoneyAndCorrelatesGrants(t *testing.T) {
	content, err := FS.ReadFile("240_payment_refund_reclaim.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS refund_requested_amount DECIMAL(20,2) NOT NULL DEFAULT 0")
	require.Contains(t, sql, "CHECK (refund_requested_amount >= 0)")
	require.Contains(t, sql, "SET refund_requested_amount = refund_amount, refund_amount = 0")
	require.Contains(t, sql, "status IN ('REFUND_REQUESTED', 'REFUNDING', 'REFUND_PENDING')")
	require.Contains(t, sql, "status = 'REFUND_FAILED' AND refund_at IS NULL AND refund_requested_at IS NOT NULL")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS payment_order_id BIGINT NULL REFERENCES payment_orders(id) ON DELETE RESTRICT")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_subscription_reset_grants_payment_order")
}
