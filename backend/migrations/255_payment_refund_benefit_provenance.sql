-- P19 refund-benefit provenance.
--
-- A payment benefit source is immutable economic evidence captured in the
-- fulfillment transaction. Mutable refund lifecycle fields deliberately live
-- beside (but are guarded separately from) that evidence so a retry cannot
-- invent a different card set or concurrency baseline.

CREATE TABLE IF NOT EXISTS payment_refund_benefit_sources (
    id BIGSERIAL PRIMARY KEY,
    payment_order_id BIGINT NOT NULL UNIQUE REFERENCES payment_orders(id) ON DELETE RESTRICT,
    subscription_grant_order_id BIGINT NULL REFERENCES payment_orders(id) ON DELETE RESTRICT,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    subscription_id BIGINT NULL REFERENCES user_subscriptions(id) ON DELETE RESTRICT,
    group_id BIGINT NULL REFERENCES groups(id) ON DELETE RESTRICT,
    source_origin VARCHAR(24) NOT NULL DEFAULT 'fulfillment',
    reset_cards_committed INTEGER NOT NULL DEFAULT 0,
    reset_card_delivery_mode VARCHAR(16) NOT NULL DEFAULT 'none',
    concurrency_before INTEGER NULL,
    concurrency_target INTEGER NOT NULL DEFAULT 0,
    concurrency_after_grant INTEGER NULL,
    state VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    reserved_product_refund_no VARCHAR(64) NULL
        REFERENCES unified_payment_refund_attempts(product_refund_no) ON DELETE RESTRICT,
    reservation_proof_digest VARCHAR(64) NULL,
    reserved_at TIMESTAMPTZ NULL,
    revoked_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT payment_refund_benefit_sources_state_valid
        CHECK (state IN ('ACTIVE', 'RESERVED', 'REVOKED')),
    CONSTRAINT payment_refund_benefit_sources_origin_valid
        CHECK (source_origin IN ('fulfillment', 'legacy_card_fk')),
    CONSTRAINT payment_refund_benefit_sources_reset_cards_valid
        CHECK (reset_cards_committed >= 0),
    CONSTRAINT payment_refund_benefit_sources_delivery_mode_valid
        CHECK (reset_card_delivery_mode IN ('none', 'immediate', 'monthly')),
    CONSTRAINT payment_refund_benefit_sources_delivery_mode_matches_cards
        CHECK ((reset_cards_committed = 0 AND reset_card_delivery_mode = 'none')
            OR (reset_cards_committed > 0 AND reset_card_delivery_mode IN ('immediate', 'monthly'))),
    CONSTRAINT payment_refund_benefit_sources_concurrency_valid
        CHECK (concurrency_target >= 0
            AND (concurrency_target = 0 AND concurrency_before IS NULL AND concurrency_after_grant IS NULL
                OR concurrency_target > 0 AND concurrency_before IS NOT NULL
                    AND concurrency_before >= 0
                    AND concurrency_after_grant = GREATEST(concurrency_before, concurrency_target))),
    CONSTRAINT payment_refund_benefit_sources_reservation_matches_state
        CHECK ((state = 'ACTIVE' AND reserved_product_refund_no IS NULL
                    AND reservation_proof_digest IS NULL AND reserved_at IS NULL AND revoked_at IS NULL)
            OR (state = 'RESERVED' AND reserved_product_refund_no IS NOT NULL
                    AND reservation_proof_digest IS NOT NULL AND reserved_at IS NOT NULL AND revoked_at IS NULL)
            OR (state = 'REVOKED' AND reserved_product_refund_no IS NOT NULL
                    AND reservation_proof_digest IS NOT NULL AND reserved_at IS NOT NULL AND revoked_at IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_payment_refund_benefit_sources_user_state
    ON payment_refund_benefit_sources (user_id, state, id);
CREATE INDEX IF NOT EXISTS idx_payment_refund_benefit_sources_subscription_state
    ON payment_refund_benefit_sources (subscription_id, state, id)
    WHERE subscription_id IS NOT NULL;

-- The one-row cutover is evidence, not a feature flag. Orders created after
-- this migration must carry a source snapshot; only older finite orders may
-- take the deliberately narrow exact-card legacy fallback.
CREATE TABLE IF NOT EXISTS payment_refund_benefit_rollout (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    cutover_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO payment_refund_benefit_rollout (singleton)
VALUES (TRUE)
ON CONFLICT (singleton) DO NOTHING;

CREATE TABLE IF NOT EXISTS payment_refund_benefit_reset_card_grants (
    source_id BIGINT NOT NULL REFERENCES payment_refund_benefit_sources(id) ON DELETE RESTRICT,
    reset_card_grant_id BIGINT NOT NULL REFERENCES subscription_reset_grants(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (source_id, reset_card_grant_id),
    CONSTRAINT payment_refund_benefit_reset_card_grants_unique_grant UNIQUE (reset_card_grant_id)
);

CREATE INDEX IF NOT EXISTS idx_payment_refund_benefit_reset_cards_source
    ON payment_refund_benefit_reset_card_grants (source_id, reset_card_grant_id);

-- Existing users are intentionally a baseline only. No historical source is
-- fabricated: an old order can use its exact card FK as legacy evidence, but
-- has no trusted concurrency_before until a new capture creates a source.
CREATE TABLE IF NOT EXISTS payment_refund_concurrency_baselines (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    baseline_concurrency INTEGER NOT NULL CHECK (baseline_concurrency >= 0),
    next_event_seq BIGINT NOT NULL DEFAULT 0 CHECK (next_event_seq >= 0),
    -- Monotonic durable projection version for the Redis admission fence. It
    -- changes for every recorded user-concurrency write and every source
    -- lifecycle transition; a stale cache writer can therefore never widen a
    -- newer temporary cap.
    fence_revision BIGINT NOT NULL DEFAULT 0 CHECK (fence_revision >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO payment_refund_concurrency_baselines (user_id, baseline_concurrency)
SELECT id, GREATEST(concurrency, 0)
FROM users
ON CONFLICT (user_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS payment_refund_concurrency_events (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_seq BIGINT NOT NULL,
    event_kind VARCHAR(16) NOT NULL,
    requested_delta INTEGER NULL,
    target_concurrency INTEGER NULL,
    benefit_source_id BIGINT NULL REFERENCES payment_refund_benefit_sources(id) ON DELETE RESTRICT,
    before_concurrency INTEGER NOT NULL CHECK (before_concurrency >= 0),
    after_concurrency INTEGER NOT NULL CHECK (after_concurrency >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT payment_refund_concurrency_events_kind_valid
        CHECK (event_kind IN ('SET', 'DELTA', 'PAYMENT_MAX', 'UNATTRIBUTED')),
    CONSTRAINT payment_refund_concurrency_events_shape_valid
        CHECK ((event_kind = 'SET' AND requested_delta IS NULL AND target_concurrency IS NOT NULL
                AND benefit_source_id IS NULL)
            OR (event_kind = 'DELTA' AND requested_delta IS NOT NULL AND target_concurrency IS NULL
                AND benefit_source_id IS NULL)
            OR (event_kind = 'PAYMENT_MAX' AND requested_delta IS NULL AND target_concurrency IS NOT NULL
                AND target_concurrency >= 0 AND benefit_source_id IS NOT NULL)
            OR (event_kind = 'UNATTRIBUTED' AND requested_delta IS NULL AND target_concurrency IS NOT NULL
                AND target_concurrency >= 0 AND benefit_source_id IS NULL)),
    CONSTRAINT payment_refund_concurrency_events_user_seq_unique UNIQUE (user_id, event_seq)
);

CREATE INDEX IF NOT EXISTS idx_payment_refund_concurrency_events_replay
    ON payment_refund_concurrency_events (user_id, event_seq, id);

ALTER TABLE unified_payment_refund_attempts
    ADD COLUMN IF NOT EXISTS benefit_proof_digest VARCHAR(64) NOT NULL DEFAULT '';

CREATE OR REPLACE FUNCTION payment_refund_benefit_source_guard()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.payment_order_id IS DISTINCT FROM OLD.payment_order_id
       OR NEW.subscription_grant_order_id IS DISTINCT FROM OLD.subscription_grant_order_id
       OR NEW.user_id IS DISTINCT FROM OLD.user_id
       OR NEW.subscription_id IS DISTINCT FROM OLD.subscription_id
       OR NEW.group_id IS DISTINCT FROM OLD.group_id
       OR NEW.source_origin IS DISTINCT FROM OLD.source_origin
       OR NEW.reset_cards_committed IS DISTINCT FROM OLD.reset_cards_committed
       OR NEW.reset_card_delivery_mode IS DISTINCT FROM OLD.reset_card_delivery_mode
       OR NEW.concurrency_before IS DISTINCT FROM OLD.concurrency_before
       OR NEW.concurrency_target IS DISTINCT FROM OLD.concurrency_target
       OR NEW.concurrency_after_grant IS DISTINCT FROM OLD.concurrency_after_grant
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'payment refund benefit source snapshot is immutable';
    END IF;

    IF NEW.state = OLD.state THEN
        IF NEW.reserved_product_refund_no IS NOT DISTINCT FROM OLD.reserved_product_refund_no
           AND NEW.reservation_proof_digest IS NOT DISTINCT FROM OLD.reservation_proof_digest
           AND NEW.reserved_at IS NOT DISTINCT FROM OLD.reserved_at
           AND NEW.revoked_at IS NOT DISTINCT FROM OLD.revoked_at THEN
            RETURN NEW;
        END IF;
        RAISE EXCEPTION 'payment refund benefit source lifecycle is immutable without a state transition';
    END IF;

    IF NOT (
        (OLD.state = 'ACTIVE' AND NEW.state = 'RESERVED'
            AND NEW.reserved_product_refund_no IS NOT NULL
            AND NEW.reservation_proof_digest IS NOT NULL
            AND NEW.reserved_at IS NOT NULL
            AND NEW.revoked_at IS NULL)
        OR (OLD.state = 'RESERVED' AND NEW.state = 'ACTIVE'
            AND NEW.reserved_product_refund_no IS NULL
            AND NEW.reservation_proof_digest IS NULL
            AND NEW.reserved_at IS NULL
            AND NEW.revoked_at IS NULL)
        OR (OLD.state = 'RESERVED' AND NEW.state = 'REVOKED'
            AND NEW.reserved_product_refund_no IS NOT DISTINCT FROM OLD.reserved_product_refund_no
            AND NEW.reservation_proof_digest IS NOT DISTINCT FROM OLD.reservation_proof_digest
            AND NEW.reserved_at IS NOT DISTINCT FROM OLD.reserved_at
            AND NEW.revoked_at IS NOT NULL)
    ) THEN
        RAISE EXCEPTION 'invalid payment refund benefit source state transition % -> %', OLD.state, NEW.state;
    END IF;

    -- Refund code takes the user row before this source row. The bump occurs
    -- in the same transaction, so the committed source lifecycle and cache
    -- projection revision are inseparable. A raw legacy writer that reaches
    -- this guarded transition also cannot leave a reusable old projection.
    UPDATE payment_refund_concurrency_baselines
    SET fence_revision = fence_revision + 1, updated_at = CURRENT_TIMESTAMP
    WHERE user_id = NEW.user_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'concurrency baseline is missing for benefit source user %', NEW.user_id;
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_payment_refund_benefit_source_guard ON payment_refund_benefit_sources;
CREATE TRIGGER trg_payment_refund_benefit_source_guard
    BEFORE UPDATE ON payment_refund_benefit_sources
    FOR EACH ROW EXECUTE FUNCTION payment_refund_benefit_source_guard();

-- New source-backed grant rows are linked automatically. payment_order_id
-- covers immediate delivery; schedule_issuance_id resolves monthly delivery
-- through its immutable schedule. NULL source remains the explicit manual or
-- historical path and is never captured by a refund source.
CREATE OR REPLACE FUNCTION link_payment_refund_benefit_reset_card_grant()
RETURNS TRIGGER AS $$
DECLARE
    v_order_id BIGINT;
    v_source_id BIGINT;
    v_state TEXT;
BEGIN
    v_order_id := NEW.payment_order_id;
    IF v_order_id IS NULL AND NEW.schedule_issuance_id IS NOT NULL THEN
        SELECT sch.payment_order_id INTO v_order_id
        FROM subscription_reset_card_issuances issue
        JOIN subscription_reset_card_schedules sch ON sch.id = issue.schedule_id
        WHERE issue.id = NEW.schedule_issuance_id;
    END IF;
    IF v_order_id IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT src.id, src.state INTO v_source_id, v_state
    FROM payment_refund_benefit_sources src
    JOIN payment_orders ord ON ord.id = src.payment_order_id
    WHERE src.payment_order_id = v_order_id
      AND src.reset_cards_committed > 0
      AND ord.order_type = 'subscription'
    FOR SHARE;
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;
    IF v_state <> 'ACTIVE' THEN
        RAISE EXCEPTION 'reset-card source is held or revoked';
    END IF;

    INSERT INTO payment_refund_benefit_reset_card_grants (source_id, reset_card_grant_id)
    VALUES (v_source_id, NEW.id)
    ON CONFLICT (reset_card_grant_id) DO NOTHING;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_link_payment_refund_benefit_reset_card_grant ON subscription_reset_grants;
CREATE TRIGGER trg_link_payment_refund_benefit_reset_card_grant
    AFTER INSERT ON subscription_reset_grants
    FOR EACH ROW EXECUTE FUNCTION link_payment_refund_benefit_reset_card_grant();

-- Card consumers already lock subscription -> grant. This final FOR SHARE
-- source lock aligns with the refund order (cards first, source last) and
-- rejects a use racing a held/revoked source even from an older writer.
CREATE OR REPLACE FUNCTION guard_payment_refund_benefit_reset_card_use()
RETURNS TRIGGER AS $$
DECLARE
    v_state TEXT;
BEGIN
    IF NEW.used_count <= OLD.used_count THEN
        RETURN NEW;
    END IF;
    SELECT src.state INTO v_state
    FROM payment_refund_benefit_reset_card_grants link
    JOIN payment_refund_benefit_sources src ON src.id = link.source_id
    WHERE link.reset_card_grant_id = NEW.id
    FOR SHARE OF src;
    IF FOUND AND v_state <> 'ACTIVE' THEN
        RAISE EXCEPTION 'reset-card benefit source is held or revoked';
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_payment_refund_benefit_reset_card_use ON subscription_reset_grants;
CREATE TRIGGER trg_guard_payment_refund_benefit_reset_card_use
    BEFORE UPDATE OF used_count ON subscription_reset_grants
    FOR EACH ROW EXECUTE FUNCTION guard_payment_refund_benefit_reset_card_use();

CREATE OR REPLACE FUNCTION ensure_payment_refund_concurrency_baseline()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO payment_refund_concurrency_baselines (user_id, baseline_concurrency)
    VALUES (NEW.id, GREATEST(NEW.concurrency, 0))
    ON CONFLICT (user_id) DO NOTHING;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_users_payment_refund_concurrency_baseline ON users;
CREATE TRIGGER trg_users_payment_refund_concurrency_baseline
    AFTER INSERT ON users
    FOR EACH ROW EXECUTE FUNCTION ensure_payment_refund_concurrency_baseline();

CREATE OR REPLACE FUNCTION capture_payment_refund_concurrency_event()
RETURNS TRIGGER AS $$
DECLARE
    v_kind TEXT := NULLIF(current_setting('sub2api.concurrency_event_kind', TRUE), '');
    v_delta_raw TEXT := NULLIF(current_setting('sub2api.concurrency_event_requested_delta', TRUE), '');
    v_target_raw TEXT := NULLIF(current_setting('sub2api.concurrency_event_target', TRUE), '');
    v_source_raw TEXT := NULLIF(current_setting('sub2api.concurrency_event_benefit_source_id', TRUE), '');
    v_delta INTEGER;
    v_target INTEGER;
    v_source BIGINT;
    v_seq BIGINT;
BEGIN
    IF v_kind = 'RECOMPUTE' THEN
        RETURN NEW;
    END IF;
    IF v_kind IS NULL AND NEW.concurrency IS NOT DISTINCT FROM OLD.concurrency THEN
        RETURN NEW;
    END IF;

    IF v_kind IS NULL THEN
        -- A raw writer has no trusted operation intent. Preserve the observed
        -- result as a barrier instead of treating it as an administrator SET.
        -- A later payment source may use this fact as its new baseline, while
        -- a source created before the barrier remains non-refundable.
        v_kind := 'UNATTRIBUTED';
        v_target := GREATEST(NEW.concurrency, 0);
    ELSIF v_kind = 'SET' THEN
        IF v_target_raw IS NULL OR v_target_raw !~ '^-?[0-9]+$'
           OR v_target_raw::NUMERIC < -2147483648 OR v_target_raw::NUMERIC > 2147483647 THEN
            RAISE EXCEPTION 'SET concurrency event target is out of range';
        END IF;
        v_target := GREATEST(v_target_raw::INTEGER, 0);
    ELSIF v_kind = 'DELTA' THEN
        IF v_delta_raw IS NULL OR v_delta_raw !~ '^-?[0-9]+$'
           OR v_delta_raw::NUMERIC < -2147483648 OR v_delta_raw::NUMERIC > 2147483647 THEN
            RAISE EXCEPTION 'DELTA concurrency event requested delta is out of range';
        END IF;
        v_delta := v_delta_raw::INTEGER;
    ELSIF v_kind = 'PAYMENT_MAX' THEN
        IF v_target_raw IS NULL OR v_target_raw !~ '^-?[0-9]+$'
           OR v_target_raw::NUMERIC < -2147483648 OR v_target_raw::NUMERIC > 2147483647
           OR v_source_raw IS NULL OR v_source_raw !~ '^[0-9]+$' THEN
            RAISE EXCEPTION 'PAYMENT_MAX concurrency event is incomplete';
        END IF;
        v_target := GREATEST(v_target_raw::INTEGER, 0);
        v_source := v_source_raw::BIGINT;
        PERFORM 1 FROM payment_refund_benefit_sources
        WHERE id = v_source AND user_id = NEW.id;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'PAYMENT_MAX concurrency event source does not belong to user';
        END IF;
    ELSIF v_kind = 'UNATTRIBUTED' THEN
        -- The trigger itself supplies the observed target. This branch is
        -- explicit so an accidental marker cannot silently become trusted.
        v_target := GREATEST(NEW.concurrency, 0);
    ELSE
        RAISE EXCEPTION 'unsupported concurrency event kind %', v_kind;
    END IF;

    INSERT INTO payment_refund_concurrency_baselines (user_id, baseline_concurrency)
    VALUES (NEW.id, GREATEST(OLD.concurrency, 0))
    ON CONFLICT (user_id) DO NOTHING;
    UPDATE payment_refund_concurrency_baselines
    SET next_event_seq = next_event_seq + 1,
        fence_revision = fence_revision + 1,
        updated_at = CURRENT_TIMESTAMP
    WHERE user_id = NEW.id
    RETURNING next_event_seq INTO v_seq;

    INSERT INTO payment_refund_concurrency_events (
        user_id, event_seq, event_kind, requested_delta, target_concurrency,
        benefit_source_id, before_concurrency, after_concurrency
    ) VALUES (
        NEW.id, v_seq, v_kind,
        CASE WHEN v_kind = 'DELTA' THEN v_delta ELSE NULL END,
        CASE WHEN v_kind IN ('SET', 'PAYMENT_MAX', 'UNATTRIBUTED') THEN v_target ELSE NULL END,
        CASE WHEN v_kind = 'PAYMENT_MAX' THEN v_source ELSE NULL END,
        GREATEST(OLD.concurrency, 0), GREATEST(NEW.concurrency, 0)
    );
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_users_capture_payment_refund_concurrency_event ON users;
CREATE TRIGGER trg_users_capture_payment_refund_concurrency_event
    AFTER UPDATE OF concurrency ON users
    FOR EACH ROW EXECUTE FUNCTION capture_payment_refund_concurrency_event();

CREATE OR REPLACE FUNCTION sub2api_apply_user_concurrency_delta(p_user_id BIGINT, p_delta INTEGER)
RETURNS TABLE(before_concurrency INTEGER, after_concurrency INTEGER) AS $$
DECLARE
    v_before INTEGER;
    v_after INTEGER;
BEGIN
    SELECT concurrency INTO v_before FROM users
    WHERE id = p_user_id AND deleted_at IS NULL FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'user % not found while adjusting concurrency', p_user_id;
    END IF;
    PERFORM set_config('sub2api.concurrency_event_kind', 'DELTA', TRUE);
    PERFORM set_config('sub2api.concurrency_event_requested_delta', p_delta::TEXT, TRUE);
    UPDATE users
    SET concurrency = LEAST(2147483647, GREATEST(0, concurrency::BIGINT + p_delta::BIGINT))::INTEGER,
        updated_at = CURRENT_TIMESTAMP
    WHERE id = p_user_id
    RETURNING concurrency INTO v_after;
    PERFORM set_config('sub2api.concurrency_event_kind', '', TRUE);
    PERFORM set_config('sub2api.concurrency_event_requested_delta', '', TRUE);
    RETURN QUERY SELECT GREATEST(v_before, 0), GREATEST(v_after, 0);
END
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION sub2api_set_user_concurrency(p_user_id BIGINT, p_target INTEGER)
RETURNS TABLE(before_concurrency INTEGER, after_concurrency INTEGER) AS $$
DECLARE
    v_before INTEGER;
    v_after INTEGER;
    v_target INTEGER := GREATEST(p_target, 0);
BEGIN
    SELECT concurrency INTO v_before FROM users
    WHERE id = p_user_id AND deleted_at IS NULL FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'user % not found while setting concurrency', p_user_id;
    END IF;
    PERFORM set_config('sub2api.concurrency_event_kind', 'SET', TRUE);
    PERFORM set_config('sub2api.concurrency_event_target', v_target::TEXT, TRUE);
    UPDATE users SET concurrency = v_target, updated_at = CURRENT_TIMESTAMP
    WHERE id = p_user_id
    RETURNING concurrency INTO v_after;
    PERFORM set_config('sub2api.concurrency_event_kind', '', TRUE);
    PERFORM set_config('sub2api.concurrency_event_target', '', TRUE);
    RETURN QUERY SELECT GREATEST(v_before, 0), GREATEST(v_after, 0);
END
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION sub2api_apply_payment_concurrency_max(
    p_user_id BIGINT, p_target INTEGER, p_benefit_source_id BIGINT
)
RETURNS TABLE(before_concurrency INTEGER, after_concurrency INTEGER) AS $$
DECLARE
    v_before INTEGER;
    v_after INTEGER;
    v_target INTEGER := GREATEST(p_target, 0);
BEGIN
    SELECT concurrency INTO v_before FROM users
    WHERE id = p_user_id AND deleted_at IS NULL FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'user % not found while granting payment concurrency', p_user_id;
    END IF;
    PERFORM 1 FROM payment_refund_benefit_sources
    WHERE id = p_benefit_source_id AND user_id = p_user_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'payment concurrency source is invalid';
    END IF;
    PERFORM set_config('sub2api.concurrency_event_kind', 'PAYMENT_MAX', TRUE);
    PERFORM set_config('sub2api.concurrency_event_target', v_target::TEXT, TRUE);
    PERFORM set_config('sub2api.concurrency_event_benefit_source_id', p_benefit_source_id::TEXT, TRUE);
    UPDATE users
    SET concurrency = GREATEST(concurrency, v_target), updated_at = CURRENT_TIMESTAMP
    WHERE id = p_user_id
    RETURNING concurrency INTO v_after;
    PERFORM set_config('sub2api.concurrency_event_kind', '', TRUE);
    PERFORM set_config('sub2api.concurrency_event_target', '', TRUE);
    PERFORM set_config('sub2api.concurrency_event_benefit_source_id', '', TRUE);
    RETURN QUERY SELECT GREATEST(v_before, 0), GREATEST(v_after, 0);
END
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION sub2api_set_user_concurrency_batch(p_user_ids BIGINT[], p_target INTEGER)
RETURNS INTEGER AS $$
DECLARE
    v_user_id BIGINT;
    v_count INTEGER := 0;
BEGIN
    FOR v_user_id IN
        SELECT id FROM users WHERE id = ANY(p_user_ids) AND deleted_at IS NULL ORDER BY id FOR UPDATE
    LOOP
        PERFORM sub2api_set_user_concurrency(v_user_id, p_target);
        v_count := v_count + 1;
    END LOOP;
    RETURN v_count;
END
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION sub2api_apply_user_concurrency_delta_batch(p_user_ids BIGINT[], p_delta INTEGER)
RETURNS INTEGER AS $$
DECLARE
    v_user_id BIGINT;
    v_count INTEGER := 0;
BEGIN
    FOR v_user_id IN
        SELECT id FROM users WHERE id = ANY(p_user_ids) AND deleted_at IS NULL ORDER BY id FOR UPDATE
    LOOP
        PERFORM sub2api_apply_user_concurrency_delta(v_user_id, p_delta);
        v_count := v_count + 1;
    END LOOP;
    RETURN v_count;
END
$$ LANGUAGE plpgsql;

-- The replay intentionally uses requested DELTA rather than NEW-OLD. A
-- clipped negative redeem at zero must still replay as that negative request
-- after a failed refund restores an earlier PAYMENT_MAX source.
CREATE OR REPLACE FUNCTION sub2api_recompute_user_concurrency(p_user_id BIGINT)
RETURNS INTEGER AS $$
DECLARE
    v_current INTEGER;
    v_event RECORD;
BEGIN
    PERFORM 1 FROM users WHERE id = p_user_id AND deleted_at IS NULL FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'user % not found while replaying concurrency', p_user_id;
    END IF;
    SELECT baseline_concurrency INTO v_current
    FROM payment_refund_concurrency_baselines
    WHERE user_id = p_user_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'concurrency baseline is missing for user %', p_user_id;
    END IF;
    FOR v_event IN
        SELECT event_kind, requested_delta, target_concurrency, benefit_source_id
        FROM payment_refund_concurrency_events
        WHERE user_id = p_user_id
        ORDER BY event_seq ASC, id ASC
    LOOP
        IF v_event.event_kind = 'SET' THEN
            v_current := GREATEST(v_event.target_concurrency, 0);
        ELSIF v_event.event_kind = 'DELTA' THEN
            v_current := LEAST(2147483647,
                GREATEST(0, v_current::BIGINT + v_event.requested_delta::BIGINT))::INTEGER;
        ELSIF v_event.event_kind = 'PAYMENT_MAX' THEN
            IF EXISTS (
                SELECT 1 FROM payment_refund_benefit_sources
                WHERE id = v_event.benefit_source_id AND state = 'ACTIVE'
            ) THEN
                v_current := GREATEST(v_current, v_event.target_concurrency);
            END IF;
        ELSIF v_event.event_kind = 'UNATTRIBUTED' THEN
            -- This is a durable observed value, not a trusted absolute
            -- override. It establishes a new replay frontier for later
            -- source-backed payments.
            v_current := GREATEST(v_event.target_concurrency, 0);
        END IF;
    END LOOP;
    PERFORM set_config('sub2api.concurrency_event_kind', 'RECOMPUTE', TRUE);
    UPDATE users SET concurrency = v_current, updated_at = CURRENT_TIMESTAMP WHERE id = p_user_id;
    PERFORM set_config('sub2api.concurrency_event_kind', '', TRUE);
    RETURN v_current;
END
$$ LANGUAGE plpgsql;

COMMENT ON TABLE payment_refund_benefit_sources IS
    'Immutable payment benefit evidence plus ACTIVE/RESERVED/REVOKED refund lifecycle for post-P19 fulfillment.';
COMMENT ON TABLE payment_refund_benefit_reset_card_grants IS
    'Exact reset-card grant IDs issued from a payment refund benefit source, including monthly append-only issuances.';
COMMENT ON TABLE payment_refund_concurrency_events IS
    'Append-only user concurrency history. UNATTRIBUTED is a raw-writer barrier; PAYMENT_MAX is included only while its benefit source is ACTIVE.';
COMMENT ON COLUMN payment_refund_concurrency_baselines.fence_revision IS
    'Monotonic durable version for Redis authorization-ceiling projection CAS.';
