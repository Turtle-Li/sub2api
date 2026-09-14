package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnifiedRefundReasonContractMigrationPersistsOnlyGatewayReasonCodes(t *testing.T) {
	content, err := FS.ReadFile("247_unified_refund_reason_contract.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS reason_code VARCHAR(32) NOT NULL DEFAULT 'other'")
	require.Contains(t, sql, "ALTER COLUMN reason_summary TYPE VARCHAR(240)")
	require.Contains(t, sql, "reason_code IN ( 'customer_request', 'duplicate_charge', 'service_not_delivered', 'service_error', 'other' )")
	require.NotContains(t, sql, "DELETE FROM unified_payment_refund_attempts")
}
