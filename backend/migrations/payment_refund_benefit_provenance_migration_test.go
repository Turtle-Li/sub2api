package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaymentRefundBenefitProvenanceMigrationKeepsSourceEvidenceAndReplay(t *testing.T) {
	content, err := FS.ReadFile("255_payment_refund_benefit_provenance.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS payment_refund_benefit_sources")
	require.Contains(t, sql, "source_origin IN ('fulfillment', 'legacy_card_fk')")
	require.Contains(t, sql, "state IN ('ACTIVE', 'RESERVED', 'REVOKED')")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS payment_refund_benefit_reset_card_grants")
	require.Contains(t, sql, "UNIQUE (reset_card_grant_id)")
	require.Contains(t, sql, "ord.order_type = 'subscription'")
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION guard_payment_refund_benefit_reset_card_use()")
	require.Contains(t, sql, "FOR SHARE OF src")
	require.Contains(t, sql, "fence_revision BIGINT NOT NULL DEFAULT 0")
	require.Contains(t, sql, "fence_revision = fence_revision + 1")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS payment_refund_concurrency_events")
	require.Contains(t, sql, "UNIQUE (user_id, event_seq)")
	require.Contains(t, sql, "event_kind IN ('SET', 'DELTA', 'PAYMENT_MAX', 'UNATTRIBUTED')")
	require.Contains(t, sql, "event_kind = 'UNATTRIBUTED'")
	require.Contains(t, sql, "A raw writer has no trusted operation intent")
	require.Contains(t, sql, "v_kind := 'UNATTRIBUTED'")
	require.Contains(t, sql, "v_kind = 'RECOMPUTE'")
	require.Contains(t, sql, "sub2api_apply_user_concurrency_delta")
	require.Contains(t, sql, "sub2api_set_user_concurrency_batch")
	require.Contains(t, sql, "ORDER BY id FOR UPDATE")
	require.Contains(t, sql, "sub2api_recompute_user_concurrency")
	require.Contains(t, sql, "v_event.requested_delta")
	require.Contains(t, sql, "state = 'ACTIVE'")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS benefit_proof_digest")
}
