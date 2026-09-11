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
