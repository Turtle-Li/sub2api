-- Reset-card compatibility is driven only by this explicit group policy.  A
-- subscription group represents one comparable tier; names, prices, and IDs
-- are intentionally not part of the eligibility contract.
CREATE TABLE IF NOT EXISTS subscription_reset_card_tiers (
    group_id BIGINT PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    family_key VARCHAR(64) NOT NULL,
    tier_rank INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT subscription_reset_card_tiers_family_rank_unique UNIQUE (family_key, tier_rank),
    CONSTRAINT subscription_reset_card_tiers_rank_positive CHECK (tier_rank > 0),
    CONSTRAINT subscription_reset_card_tiers_family_key_nonempty CHECK (btrim(family_key) <> ''),
    CONSTRAINT subscription_reset_card_tiers_family_key_normalized CHECK (family_key = lower(btrim(family_key)))
);

-- Existing grants predate the policy and stay exact-subscription grants.  Do
-- not infer or backfill their tier from mutable catalogue data.
ALTER TABLE subscription_reset_grants
    ADD COLUMN IF NOT EXISTS card_family_key VARCHAR(64),
    ADD COLUMN IF NOT EXISTS source_tier_rank INTEGER,
    ADD COLUMN IF NOT EXISTS source_plan_id BIGINT,
    -- FALSE is only the rolling-deployment sentinel for an old binary that
    -- omitted every tier column. The BEFORE INSERT trigger resolves it to
    -- TRUE before a row can pass the constraint below.
    ADD COLUMN IF NOT EXISTS tier_snapshot_resolved BOOLEAN NOT NULL DEFAULT FALSE;

-- The new marker distinguishes a row inserted before this migration from an
-- old binary that writes after it. Preserve every historical NULL/NULL grant
-- exactly as it was: record that its snapshot is already resolved, without
-- inferring a family or rank from the mutable current policy.
UPDATE subscription_reset_grants
SET tier_snapshot_resolved = TRUE
WHERE tier_snapshot_resolved IS DISTINCT FROM TRUE;

-- source_plan_id is immutable provenance, not a tier discriminator.  A
-- grant is exact-only when the family/rank pair is both NULL, including
-- monthly schedule grants that still retain their source plan.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'subscription_reset_grants_tier_snapshot_pair') THEN
        ALTER TABLE subscription_reset_grants
            ADD CONSTRAINT subscription_reset_grants_tier_snapshot_pair
            CHECK ((card_family_key IS NULL AND source_tier_rank IS NULL) OR (card_family_key IS NOT NULL AND source_tier_rank IS NOT NULL));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'subscription_reset_grants_tier_snapshot_rank_positive') THEN
        ALTER TABLE subscription_reset_grants
            ADD CONSTRAINT subscription_reset_grants_tier_snapshot_rank_positive
            CHECK (source_tier_rank IS NULL OR source_tier_rank > 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'subscription_reset_grants_tier_snapshot_family_normalized') THEN
        ALTER TABLE subscription_reset_grants
            ADD CONSTRAINT subscription_reset_grants_tier_snapshot_family_normalized
            CHECK (card_family_key IS NULL OR (btrim(card_family_key) <> '' AND card_family_key = lower(btrim(card_family_key))));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'subscription_reset_grants_source_plan_positive') THEN
        ALTER TABLE subscription_reset_grants
            ADD CONSTRAINT subscription_reset_grants_source_plan_positive
            CHECK (source_plan_id IS NULL OR source_plan_id > 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'subscription_reset_grants_tier_snapshot_resolved_true') THEN
        ALTER TABLE subscription_reset_grants
            ADD CONSTRAINT subscription_reset_grants_tier_snapshot_resolved_true
            CHECK (tier_snapshot_resolved IS TRUE);
    END IF;
END $$;

COMMENT ON COLUMN subscription_reset_grants.card_family_key IS
    'Immutable reset-card family snapshot. NULL with source_tier_rank NULL denotes an exact-subscription grant.';
COMMENT ON COLUMN subscription_reset_grants.source_tier_rank IS
    'Immutable reset-card source rank snapshot. NULL with card_family_key NULL denotes an exact-subscription grant.';
COMMENT ON COLUMN subscription_reset_grants.source_plan_id IS
    'Historical source subscription plan ID snapshot; deliberately not a foreign key and not a tier discriminator so plans remain deletable.';
COMMENT ON COLUMN subscription_reset_grants.tier_snapshot_resolved IS
    'TRUE means family/rank are an intentionally frozen snapshot, including an intentional NULL/NULL exact-only grant. FALSE is accepted only transiently from pre-248 INSERT SQL and is resolved by the grant trigger.';

-- Older rolling-deployment binaries still insert grants without the typed
-- snapshot columns and do not hold the parent group lock.  Enforce the same
-- group -> tier lock order in PostgreSQL so they cannot commit an exact-only
-- row after a policy writer has made a policy visible. The explicit resolved
-- marker prevents this compatibility path from changing a new code path's
-- frozen NULL/NULL order or monthly-schedule snapshot.
DROP TRIGGER IF EXISTS subscription_reset_grants_enforce_tier_snapshot ON subscription_reset_grants;
DROP FUNCTION IF EXISTS public.enforce_subscription_reset_grant_tier_snapshot();

CREATE FUNCTION public.enforce_subscription_reset_grant_tier_snapshot()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    policy_family_key VARCHAR(64);
    policy_tier_rank INTEGER;
    has_policy BOOLEAN;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        -- Grant provenance is immutable after INSERT. In particular, never
        -- turn an intentionally exact-only frozen card into a typed card on a
        -- later UPDATE. Updates that only consume a card do not fire this
        -- trigger because none of these columns appears in their SET clause.
        IF NEW.group_id IS DISTINCT FROM OLD.group_id
            OR NEW.card_family_key IS DISTINCT FROM OLD.card_family_key
            OR NEW.source_tier_rank IS DISTINCT FROM OLD.source_tier_rank
            OR NEW.source_plan_id IS DISTINCT FROM OLD.source_plan_id
            OR NEW.tier_snapshot_resolved IS DISTINCT FROM OLD.tier_snapshot_resolved THEN
            RAISE EXCEPTION 'subscription reset grant tier provenance is immutable'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.tier_snapshot_resolved IS NULL THEN
        RAISE EXCEPTION 'subscription reset grant tier snapshot resolution is required'
            USING ERRCODE = '23514';
    END IF;

    -- A tier child can be absent on the first policy write, so lock its stable
    -- parent first. Policy writers take this row FOR UPDATE before reading or
    -- writing the child; FOR SHARE therefore serializes both old and new grant
    -- writers with policy creation and changes.
    PERFORM 1
    FROM groups
    WHERE id = NEW.group_id
    FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'subscription reset grant group % does not exist', NEW.group_id
            USING ERRCODE = '23503';
    END IF;

    SELECT tier.family_key, tier.tier_rank
    INTO policy_family_key, policy_tier_rank
    FROM subscription_reset_card_tiers AS tier
    WHERE tier.group_id = NEW.group_id
    FOR SHARE;
    has_policy := FOUND;

    IF NEW.tier_snapshot_resolved IS FALSE THEN
        -- A FALSE marker is the only shape emitted by old INSERT SQL, which
        -- omitted both tier columns. A typed value with this marker is neither
        -- an old writer nor an explicit new frozen snapshot, so fail closed.
        IF NEW.card_family_key IS NOT NULL OR NEW.source_tier_rank IS NOT NULL THEN
            RAISE EXCEPTION 'subscription reset grant typed snapshot requires explicit resolution'
                USING ERRCODE = '23514';
        END IF;
        IF has_policy THEN
            NEW.card_family_key := policy_family_key;
            NEW.source_tier_rank := policy_tier_rank;
        END IF;
        NEW.tier_snapshot_resolved := TRUE;
        RETURN NEW;
    END IF;

    -- A new writer explicitly marks its tier snapshot as resolved. NULL/NULL
    -- is valid frozen exact-only evidence even when a policy now exists.
    IF NEW.card_family_key IS NULL AND NEW.source_tier_rank IS NULL THEN
        RETURN NEW;
    END IF;

    -- The pair constraint also rejects this shape, but fail before returning
    -- so an incorrect writer cannot rely on a partially typed snapshot.
    IF NEW.card_family_key IS NULL OR NEW.source_tier_rank IS NULL THEN
        RAISE EXCEPTION 'subscription reset grant tier snapshot must include both family and rank'
            USING ERRCODE = '23514';
    END IF;

    -- Typed snapshots are a copy of policy, never caller-selected metadata.
    IF NOT has_policy
        OR NEW.card_family_key IS DISTINCT FROM policy_family_key
        OR NEW.source_tier_rank IS DISTINCT FROM policy_tier_rank THEN
        RAISE EXCEPTION 'subscription reset grant tier snapshot must match the group policy'
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER subscription_reset_grants_enforce_tier_snapshot
BEFORE INSERT OR UPDATE OF group_id, card_family_key, source_tier_rank, source_plan_id, tier_snapshot_resolved
ON subscription_reset_grants
FOR EACH ROW
EXECUTE FUNCTION public.enforce_subscription_reset_grant_tier_snapshot();

CREATE INDEX IF NOT EXISTS idx_subscription_reset_grants_owner_tier_available
    ON subscription_reset_grants (user_id, card_family_key, source_tier_rank, expires_at, id)
    WHERE used_count < quantity AND card_family_key IS NOT NULL AND source_tier_rank IS NOT NULL;
