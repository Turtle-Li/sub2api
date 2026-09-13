package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaymentRefundAccountingMigrationKeepsPaidAndGiftSeparate(t *testing.T) {
	content, err := FS.ReadFile("245_payment_refund_accounting.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS wallet_available_paid DECIMAL(20,8) NOT NULL DEFAULT 0")
	require.Contains(t, sql, "wallet_available_paid <= GREATEST(balance, 0)")
	require.Contains(t, sql, "IF available_delta < 0 THEN available_paid := GREATEST(available_paid + available_delta, 0)")
	require.Contains(t, sql, "Positive generic credits are gifts/unattributed")
	require.Contains(t, sql, "IF event_kind IS NULL THEN RETURN NULL")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS payment_wallet_fundings")
	require.Contains(t, sql, "CHECK (refunded_paid_amount + reserved_paid_amount <= paid_credit_amount)")
	require.Contains(t, sql, "CHECK (reclaimed_gift_amount + reserved_gift_amount <= gift_credit_amount)")
}

func TestPaymentRefundAccountingMigrationGuardsSubscriptionHolds(t *testing.T) {
	content, err := FS.ReadFile("245_payment_refund_accounting.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS payment_subscription_grants")
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION sub2api_guard_subscription_refund_hold()")
	require.Contains(t, sql, "reserved_seconds > 0")
	require.Contains(t, sql, "BEFORE UPDATE OF expires_at, status, deleted_at, group_id")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS subscription_cache_invalidation_outbox")
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION enqueue_subscription_authorization_cache_target")
	require.Contains(t, sql, "INSERT INTO auth_cache_invalidation_outbox (cache_key)")
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION enqueue_subscription_authorization_cache_invalidation()")
	require.Contains(t, sql, "trg_user_subscriptions_authorization_cache_invalidation")
	require.NotContains(t, sql, "ALTER TABLE auth_cache_invalidation_outbox")
	require.NotContains(t, sql, "ALTER COLUMN cache_key DROP NOT NULL")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS entitlement_reserved BOOLEAN NOT NULL DEFAULT FALSE")
}
