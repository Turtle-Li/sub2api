-- Reset-card cadence and compatibility are now product-internal contracts.
-- Do not infer identities from mutable names at runtime: the migration binds
-- the two reviewed production groups once, then the resolver keeps using the
-- existing immutable snapshot strategy table.

-- A code is nullable for unrelated and newly installed groups. Once present,
-- it is immutable in Ent and unique across all groups, including soft-deleted
-- rows, so a product identity cannot be silently rebound.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS subscription_product_code VARCHAR(64);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'groups'::regclass
          AND conname = 'groups_subscription_product_code_unique'
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_subscription_product_code_unique
            UNIQUE (subscription_product_code);
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'groups'::regclass
          AND conname = 'groups_subscription_product_code_valid'
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_subscription_product_code_valid
            CHECK (
                subscription_product_code IS NULL
                OR subscription_product_code IN ('openai_plus', 'openai_5x')
            );
    END IF;
END $$;

-- Migration 249 originally required at least two calendar occurrences. A
-- one-month term is a valid monthly plan: it creates one indexed, ledgered
-- occurrence rather than falling back to an immediate grant.
ALTER TABLE subscription_reset_card_schedules
    DROP CONSTRAINT IF EXISTS subscription_reset_card_schedules_occurrence_count_valid;

ALTER TABLE subscription_reset_card_schedules
    ADD CONSTRAINT subscription_reset_card_schedules_occurrence_count_valid
    CHECK (occurrence_count BETWEEN 1 AND 120);

-- Only the reviewed production IDs are bound. New installs and non-production
-- databases without either row remain empty. If an ID exists with a different
-- product shape, stop rather than attaching a stable identity to the wrong
-- group. Existing matching codes and tiers are idempotent; every conflicting
-- value fails before any rewrite.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM groups WHERE id = 4) THEN
        IF NOT EXISTS (
            SELECT 1
            FROM groups
            WHERE id = 4
              AND name = 'GPT PLUS'
              AND platform = 'openai'
              AND subscription_type = 'subscription'
        ) THEN
            RAISE EXCEPTION 'subscription reset-card system tier group 4 does not match GPT PLUS/openai/subscription'
                USING ERRCODE = '23514';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM groups
            WHERE id = 4
              AND subscription_product_code IS NOT NULL
              AND subscription_product_code <> 'openai_plus'
        ) OR EXISTS (
            SELECT 1
            FROM groups
            WHERE id <> 4
              AND subscription_product_code = 'openai_plus'
        ) THEN
            RAISE EXCEPTION 'subscription reset-card system product code conflict for group 4'
                USING ERRCODE = '23505';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM subscription_reset_card_tiers
            WHERE group_id = 4
              AND (family_key <> 'openai_subscription' OR tier_rank <> 1)
        ) OR EXISTS (
            SELECT 1
            FROM subscription_reset_card_tiers
            WHERE group_id <> 4
              AND family_key = 'openai_subscription'
              AND tier_rank = 1
        ) THEN
            RAISE EXCEPTION 'subscription reset-card system tier conflict for group 4'
                USING ERRCODE = '23505';
        END IF;

        UPDATE groups
        SET subscription_product_code = 'openai_plus'
        WHERE id = 4
          AND subscription_product_code IS NULL;

        INSERT INTO subscription_reset_card_tiers (
            group_id, family_key, tier_rank, created_at, updated_at
        )
        SELECT 4, 'openai_subscription', 1, NOW(), NOW()
        WHERE NOT EXISTS (
            SELECT 1
            FROM subscription_reset_card_tiers
            WHERE group_id = 4
        );
    END IF;

    IF EXISTS (SELECT 1 FROM groups WHERE id = 12) THEN
        IF NOT EXISTS (
            SELECT 1
            FROM groups
            WHERE id = 12
              AND name = 'GPT 5X PRO'
              AND platform = 'openai'
              AND subscription_type = 'subscription'
        ) THEN
            RAISE EXCEPTION 'subscription reset-card system tier group 12 does not match GPT 5X PRO/openai/subscription'
                USING ERRCODE = '23514';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM groups
            WHERE id = 12
              AND subscription_product_code IS NOT NULL
              AND subscription_product_code <> 'openai_5x'
        ) OR EXISTS (
            SELECT 1
            FROM groups
            WHERE id <> 12
              AND subscription_product_code = 'openai_5x'
        ) THEN
            RAISE EXCEPTION 'subscription reset-card system product code conflict for group 12'
                USING ERRCODE = '23505';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM subscription_reset_card_tiers
            WHERE group_id = 12
              AND (family_key <> 'openai_subscription' OR tier_rank <> 2)
        ) OR EXISTS (
            SELECT 1
            FROM subscription_reset_card_tiers
            WHERE group_id <> 12
              AND family_key = 'openai_subscription'
              AND tier_rank = 2
        ) THEN
            RAISE EXCEPTION 'subscription reset-card system tier conflict for group 12'
                USING ERRCODE = '23505';
        END IF;

        UPDATE groups
        SET subscription_product_code = 'openai_5x'
        WHERE id = 12
          AND subscription_product_code IS NULL;

        INSERT INTO subscription_reset_card_tiers (
            group_id, family_key, tier_rank, created_at, updated_at
        )
        SELECT 12, 'openai_subscription', 2, NOW(), NOW()
        WHERE NOT EXISTS (
            SELECT 1
            FROM subscription_reset_card_tiers
            WHERE group_id = 12
        );
    END IF;
END $$;

-- Historical grants remain intentionally untouched. NULL family/rank
-- snapshots retain their exact-subscription-only behavior.
