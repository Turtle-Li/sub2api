package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaymentRefundReconciliationMigrationScopesAutomaticClaimsToReviewedReservations(t *testing.T) {
	content, err := FS.ReadFile("246_payment_refund_reconciliation.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS reconciliation_available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS reconciliation_claimed_by TEXT")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS reconciliation_attempts INTEGER NOT NULL DEFAULT 0")
	require.Contains(t, sql, "WHERE status = 'PENDING' AND entitlement_reserved = TRUE AND needs_manual_review = FALSE AND quote_revision <> '' AND refund_kind IN ('balance', 'subscription')")
	require.NotContains(t, sql, "DELETE FROM unified_payment_refund_attempts")
}
