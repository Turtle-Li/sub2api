-- Monthly reset-card delivery is a new, opt-in entitlement mode. Existing
-- orders and grants are deliberately not backfilled: their original immutable
-- product snapshots do not prove a calendar cadence or anchor.
CREATE TABLE IF NOT EXISTS subscription_reset_card_schedules (
    id BIGSERIAL PRIMARY KEY,
    payment_order_id BIGINT NOT NULL UNIQUE REFERENCES payment_orders(id) ON DELETE RESTRICT,
    subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id) ON DELETE RESTRICT,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
    source_plan_id BIGINT NOT NULL,
    card_family_key VARCHAR(64),
    source_tier_rank INTEGER,
    anchor_at TIMESTAMPTZ NOT NULL,
    anchor_timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Shanghai',
    anchor_day SMALLINT NOT NULL,
    term_end_at TIMESTAMPTZ NOT NULL,
    occurrence_count INTEGER NOT NULL,
    cards_per_occurrence INTEGER NOT NULL,
    card_validity_days INTEGER NOT NULL,
    next_occurrence INTEGER NOT NULL DEFAULT 0,
    next_due_at TIMESTAMPTZ,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT subscription_reset_card_schedules_term_valid CHECK (term_end_at > anchor_at),
    CONSTRAINT subscription_reset_card_schedules_anchor_day_valid CHECK (anchor_day BETWEEN 1 AND 31),
    CONSTRAINT subscription_reset_card_schedules_occurrence_count_valid CHECK (occurrence_count BETWEEN 2 AND 120),
    CONSTRAINT subscription_reset_card_schedules_cards_per_occurrence_valid CHECK (cards_per_occurrence BETWEEN 1 AND 1000),
    CONSTRAINT subscription_reset_card_schedules_card_validity_valid CHECK (card_validity_days BETWEEN 1 AND 3650),
    CONSTRAINT subscription_reset_card_schedules_next_occurrence_valid CHECK (next_occurrence BETWEEN 0 AND occurrence_count),
    CONSTRAINT subscription_reset_card_schedules_status_valid CHECK (status IN ('active', 'completed', 'cancelled')),
    CONSTRAINT subscription_reset_card_schedules_tier_snapshot_complete CHECK (
        (card_family_key IS NULL AND source_tier_rank IS NULL)
        OR (
            card_family_key IS NOT NULL
            AND btrim(card_family_key) <> ''
            AND card_family_key = lower(card_family_key)
            AND source_tier_rank IS NOT NULL
            AND source_tier_rank > 0
        )
    ),
    CONSTRAINT subscription_reset_card_schedules_active_due_valid CHECK (
        (status = 'active' AND next_occurrence < occurrence_count AND next_due_at IS NOT NULL)
        OR (status <> 'active' AND next_due_at IS NULL)
    ),
    CONSTRAINT subscription_reset_card_schedules_timezone_nonempty CHECK (btrim(anchor_timezone) <> '')
);

CREATE INDEX IF NOT EXISTS idx_subscription_reset_card_schedules_due
    ON subscription_reset_card_schedules (next_due_at, id)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_subscription_reset_card_schedules_subscription
    ON subscription_reset_card_schedules (subscription_id, created_at DESC);

-- Every scheduled month becomes one immutable ledger row, including windows
-- skipped after downtime. This prevents later runs from stockpiling missed
-- cards while preserving why a period did not issue a grant.
CREATE TABLE IF NOT EXISTS subscription_reset_card_issuances (
    id BIGSERIAL PRIMARY KEY,
    schedule_id BIGINT NOT NULL REFERENCES subscription_reset_card_schedules(id) ON DELETE RESTRICT,
    occurrence_index INTEGER NOT NULL,
    due_at TIMESTAMPTZ NOT NULL,
    window_end_at TIMESTAMPTZ NOT NULL,
    status VARCHAR(16) NOT NULL,
    skip_reason VARCHAR(64),
    issued_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT subscription_reset_card_issuances_occurrence_nonnegative CHECK (occurrence_index >= 0),
    CONSTRAINT subscription_reset_card_issuances_window_valid CHECK (window_end_at > due_at),
    CONSTRAINT subscription_reset_card_issuances_status_valid CHECK (status IN ('issued', 'skipped')),
    CONSTRAINT subscription_reset_card_issuances_skip_reason_valid CHECK (
        (status = 'issued' AND skip_reason IS NULL)
        OR (status = 'skipped' AND skip_reason IS NOT NULL AND btrim(skip_reason) <> '')
    ),
    CONSTRAINT subscription_reset_card_issuances_schedule_occurrence_unique UNIQUE (schedule_id, occurrence_index)
);

ALTER TABLE subscription_reset_grants
    ADD COLUMN IF NOT EXISTS schedule_issuance_id BIGINT
        REFERENCES subscription_reset_card_issuances(id) ON DELETE RESTRICT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_reset_grants_schedule_issuance_unique
    ON subscription_reset_grants (schedule_issuance_id)
    WHERE schedule_issuance_id IS NOT NULL;

COMMENT ON COLUMN subscription_reset_grants.schedule_issuance_id IS
    'Monthly issuance ledger source. Scheduled grants leave payment_order_id NULL to preserve one paid order to one direct grant uniqueness.';
