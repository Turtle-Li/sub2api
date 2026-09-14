package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionResetCardTiersMigrationKeepsLegacyGrantsExactOnly(t *testing.T) {
	content, err := FS.ReadFile("248_subscription_reset_card_tiers.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS subscription_reset_card_tiers")
	require.Contains(t, sql, "group_id BIGINT PRIMARY KEY REFERENCES groups(id)")
	require.Contains(t, sql, "family_key VARCHAR(64) NOT NULL")
	require.Contains(t, sql, "tier_rank INTEGER NOT NULL")
	require.Contains(t, sql, "UNIQUE (family_key, tier_rank)")
	require.Contains(t, sql, "CHECK (tier_rank > 0)")
	require.Contains(t, sql, "CHECK (family_key = lower(btrim(family_key)))")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS card_family_key VARCHAR(64)")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS source_tier_rank INTEGER")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS source_plan_id BIGINT")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS tier_snapshot_resolved BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "UPDATE subscription_reset_grants SET tier_snapshot_resolved = TRUE WHERE tier_snapshot_resolved IS DISTINCT FROM TRUE")
	require.Contains(t, sql, "subscription_reset_grants_tier_snapshot_pair")
	require.Contains(t, sql, "CHECK ((card_family_key IS NULL AND source_tier_rank IS NULL) OR (card_family_key IS NOT NULL AND source_tier_rank IS NOT NULL))")
	require.Contains(t, sql, "CHECK (source_tier_rank IS NULL OR source_tier_rank > 0)")
	require.Contains(t, sql, "subscription_reset_grants_tier_snapshot_family_normalized")
	require.Contains(t, sql, "CHECK (source_plan_id IS NULL OR source_plan_id > 0)")
	require.Contains(t, sql, "subscription_reset_grants_tier_snapshot_resolved_true")
	require.Contains(t, sql, "CHECK (tier_snapshot_resolved IS TRUE)")
	require.NotContains(t, sql, "source_plan_id BIGINT REFERENCES subscription_plans")
	require.Contains(t, sql, "not infer or backfill their tier")
	require.Contains(t, sql, "DROP TRIGGER IF EXISTS subscription_reset_grants_enforce_tier_snapshot ON subscription_reset_grants")
	require.Contains(t, sql, "DROP FUNCTION IF EXISTS public.enforce_subscription_reset_grant_tier_snapshot()")
	require.Contains(t, sql, "CREATE FUNCTION public.enforce_subscription_reset_grant_tier_snapshot()")
	require.Contains(t, sql, "PERFORM 1 FROM groups WHERE id = NEW.group_id FOR SHARE")
	require.Contains(t, sql, "FROM subscription_reset_card_tiers AS tier WHERE tier.group_id = NEW.group_id FOR SHARE")
	require.Contains(t, sql, "IF TG_OP = 'UPDATE' THEN")
	require.Contains(t, sql, "subscription reset grant tier provenance is immutable")
	require.Contains(t, sql, "IF NEW.tier_snapshot_resolved IS FALSE THEN")
	require.Contains(t, sql, "NEW.tier_snapshot_resolved := TRUE")
	require.Contains(t, sql, "NEW.card_family_key := policy_family_key")
	require.Contains(t, sql, "NEW.source_tier_rank := policy_tier_rank")
	require.Contains(t, sql, "typed snapshot requires explicit resolution")
	require.Contains(t, sql, "subscription reset grant tier snapshot must match the group policy")
	require.Contains(t, sql, "OR NEW.source_plan_id IS DISTINCT FROM OLD.source_plan_id")
	require.Contains(t, sql, "BEFORE INSERT OR UPDATE OF group_id, card_family_key, source_tier_rank, source_plan_id, tier_snapshot_resolved ON subscription_reset_grants")
	require.NotContains(t, sql, "UPDATE subscription_reset_grants SET card_family_key")
}
