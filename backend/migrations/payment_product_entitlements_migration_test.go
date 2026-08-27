package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaymentProductEntitlementsMigration(t *testing.T) {
	content, err := FS.ReadFile("194_payment_product_entitlements.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS entitlements JSONB NOT NULL DEFAULT '{}'::jsonb")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS product_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_subscription_plans_entitlements")
}

func TestUserConcurrencyAuthCacheInvalidationMigration(t *testing.T) {
	content, err := FS.ReadFile("195_user_concurrency_auth_cache_invalidation.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "OLD.concurrency IS NOT DISTINCT FROM NEW.concurrency")
	require.Contains(t, sql, "OLD.balance IS NOT DISTINCT FROM NEW.balance")
	require.Contains(t, sql, "OLD.total_recharged IS NOT DISTINCT FROM NEW.total_recharged")
	require.Contains(t, sql, "OLD.rpm_limit IS NOT DISTINCT FROM NEW.rpm_limit")
}

// 196 supersedes 195. Balance moves on every billed request, so keeping it in
// the predicate enqueued an outbox row per API key per request and collapsed
// the auth cache for active users. Concurrency must stay — payment fulfillment
// raises it and the cap lives in the auth snapshot.
func TestAuthCacheInvalidationDropsBalancePredicateMigration(t *testing.T) {
	content, err := FS.ReadFile("196_auth_cache_invalidation_drop_balance_predicate.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION enqueue_user_auth_cache_invalidation()")
	require.Contains(t, sql, "OLD.concurrency IS NOT DISTINCT FROM NEW.concurrency")
	require.Contains(t, sql, "OLD.total_recharged IS NOT DISTINCT FROM NEW.total_recharged")
	require.Contains(t, sql, "OLD.rpm_limit IS NOT DISTINCT FROM NEW.rpm_limit")
	require.Contains(t, sql, "OLD.status IS NOT DISTINCT FROM NEW.status")
	require.Contains(t, sql, "OLD.role IS NOT DISTINCT FROM NEW.role")
	require.Contains(t, sql, "OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at")

	// The whole point of this migration: balance must no longer gate the trigger.
	predicate := sql[strings.Index(sql, "IF TG_OP = 'UPDATE'"):strings.Index(sql, "THEN RETURN NEW;")]
	require.NotContains(t, predicate, "OLD.balance")
}
