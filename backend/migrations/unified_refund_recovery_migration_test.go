package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnifiedRefundRecoveryMigrationStoresOnlySafeAdditiveClassification(t *testing.T) {
	content, err := FS.ReadFile("251_unified_refund_recovery.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS provider_status VARCHAR(80)")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS failure_code VARCHAR(120)")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS provider_updated_at TIMESTAMPTZ")
	require.Contains(t, sql, "provider_status ~ '^[A-Za-z0-9_.:-]{1,80}$'")
	require.Contains(t, sql, "failure_code ~ '^[a-z0-9_.:-]{1,120}$'")
	require.Contains(t, sql, "action = 'UNIFIED_REFUND_RESULT'")
	require.Contains(t, sql, "v_detail := v_event.detail::JSONB")
	require.Contains(t, sql, "EXCEPTION WHEN data_exception THEN CONTINUE")
	require.Contains(t, sql, "v_detail ->> 'product_refund_no' IS DISTINCT FROM v_attempt.product_refund_no")
	require.Contains(t, sql, "v_detail ->> 'refund_request_id' IS DISTINCT FROM CAST(v_attempt.refund_request_id AS TEXT)")
	require.Contains(t, sql, "IF NOT isfinite(v_provider_updated_at)")
	require.Contains(t, sql, "UPDATE unified_payment_refund_attempts SET provider_status")
	require.NotContains(t, sql, "DELETE FROM unified_payment_refund_attempts")
	require.NotContains(t, sql, "DROP COLUMN")
	require.NotContains(t, sql, "provider_message")
	require.NotContains(t, sql, "raw_response")
}
