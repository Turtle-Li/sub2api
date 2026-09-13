-- Server-reviewed refund accounting for paid wallet principal and
-- order-scoped subscription terms. Existing balances are deliberately left
-- unattributed (wallet_*_paid = 0) and therefore cannot be auto-refunded.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS wallet_available_paid DECIMAL(20,8) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS wallet_frozen_paid DECIMAL(20,8) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS wallet_component_version BIGINT NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'users'::regclass
          AND conname = 'users_wallet_paid_components_valid'
    ) THEN
        ALTER TABLE users ADD CONSTRAINT users_wallet_paid_components_valid CHECK (
            wallet_available_paid >= 0
            AND wallet_frozen_paid >= 0
            AND wallet_component_version >= 0
            AND wallet_available_paid <= GREATEST(balance, 0)
            AND wallet_frozen_paid <= GREATEST(frozen_balance, 0)
        );
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS wallet_principal_events (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_kind VARCHAR(64) NOT NULL,
    balance_before DECIMAL(20,8) NOT NULL,
    balance_after DECIMAL(20,8) NOT NULL,
    frozen_before DECIMAL(20,8) NOT NULL,
    frozen_after DECIMAL(20,8) NOT NULL,
    available_paid_before DECIMAL(20,8) NOT NULL,
    available_paid_after DECIMAL(20,8) NOT NULL,
    frozen_paid_before DECIMAL(20,8) NOT NULL,
    frozen_paid_after DECIMAL(20,8) NOT NULL,
    component_version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_wallet_principal_events_user_history
    ON wallet_principal_events (user_id, id DESC);

-- The paid component is pooled at account level. Generic debits consume paid
-- principal first. Moves between available/frozen balances carry paid
-- principal with them. Trusted financial code may set the paid columns in the
-- same UPDATE (payment funding and refund reservation); the trigger validates
-- those explicit values instead of reclassifying them.
CREATE OR REPLACE FUNCTION sub2api_sync_wallet_paid_components()
RETURNS TRIGGER AS $$
DECLARE
    old_available_total NUMERIC := GREATEST(COALESCE(OLD.balance, 0), 0);
    new_available_total NUMERIC := GREATEST(COALESCE(NEW.balance, 0), 0);
    old_frozen_total NUMERIC := GREATEST(COALESCE(OLD.frozen_balance, 0), 0);
    new_frozen_total NUMERIC := GREATEST(COALESCE(NEW.frozen_balance, 0), 0);
    available_delta NUMERIC;
    frozen_delta NUMERIC;
    transfer_amount NUMERIC;
    paid_transfer NUMERIC;
    available_paid NUMERIC := COALESCE(OLD.wallet_available_paid, 0);
    frozen_paid NUMERIC := COALESCE(OLD.wallet_frozen_paid, 0);
    explicit_available BOOLEAN;
    explicit_frozen BOOLEAN;
BEGIN
    explicit_available := NEW.wallet_available_paid IS DISTINCT FROM OLD.wallet_available_paid;
    explicit_frozen := NEW.wallet_frozen_paid IS DISTINCT FROM OLD.wallet_frozen_paid;

    IF explicit_available THEN
        available_paid := COALESCE(NEW.wallet_available_paid, 0);
    END IF;
    IF explicit_frozen THEN
        frozen_paid := COALESCE(NEW.wallet_frozen_paid, 0);
    END IF;

    IF NOT explicit_available AND NOT explicit_frozen THEN
        available_delta := new_available_total - old_available_total;
        frozen_delta := new_frozen_total - old_frozen_total;

        IF available_delta < 0 AND frozen_delta > 0 THEN
            transfer_amount := LEAST(-available_delta, frozen_delta);
            paid_transfer := LEAST(available_paid, transfer_amount);
            available_paid := available_paid - paid_transfer;
            frozen_paid := frozen_paid + paid_transfer;
            available_delta := available_delta + transfer_amount;
            frozen_delta := frozen_delta - transfer_amount;
        ELSIF available_delta > 0 AND frozen_delta < 0 THEN
            transfer_amount := LEAST(available_delta, -frozen_delta);
            paid_transfer := LEAST(frozen_paid, transfer_amount);
            frozen_paid := frozen_paid - paid_transfer;
            available_paid := available_paid + paid_transfer;
            available_delta := available_delta - transfer_amount;
            frozen_delta := frozen_delta + transfer_amount;
        END IF;

        IF available_delta < 0 THEN
            available_paid := GREATEST(available_paid + available_delta, 0);
        END IF;
        -- Positive generic credits are gifts/unattributed and do not increase
        -- available_paid.

        IF frozen_delta < 0 THEN
            frozen_paid := GREATEST(frozen_paid + frozen_delta, 0);
        END IF;
        -- Positive generic frozen credit is likewise unattributed.

        NEW.wallet_available_paid := available_paid;
        NEW.wallet_frozen_paid := frozen_paid;
    END IF;

    IF NEW.wallet_available_paid < 0
       OR NEW.wallet_frozen_paid < 0
       OR NEW.wallet_available_paid > new_available_total
       OR NEW.wallet_frozen_paid > new_frozen_total THEN
        RAISE EXCEPTION 'wallet paid component invariant violated for user %', NEW.id;
    END IF;

    IF NEW.balance IS DISTINCT FROM OLD.balance
       OR NEW.frozen_balance IS DISTINCT FROM OLD.frozen_balance
       OR NEW.wallet_available_paid IS DISTINCT FROM OLD.wallet_available_paid
       OR NEW.wallet_frozen_paid IS DISTINCT FROM OLD.wallet_frozen_paid THEN
        NEW.wallet_component_version := OLD.wallet_component_version + 1;
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_users_sync_wallet_paid_components ON users;
CREATE TRIGGER trg_users_sync_wallet_paid_components
    BEFORE UPDATE OF balance, frozen_balance, wallet_available_paid, wallet_frozen_paid
    ON users
    FOR EACH ROW
    EXECUTE FUNCTION sub2api_sync_wallet_paid_components();

CREATE OR REPLACE FUNCTION sub2api_record_wallet_principal_event()
RETURNS TRIGGER AS $$
DECLARE
    event_kind TEXT := NULLIF(
        current_setting('sub2api.wallet_event_kind', TRUE),
        ''
    );
BEGIN
    -- Ordinary usage debits already have request-level billing records and sit
    -- on the hottest write path. Only explicitly named payment/refund/admin
    -- transitions need a second component-ledger event.
    IF event_kind IS NULL THEN
        RETURN NULL;
    END IF;
    INSERT INTO wallet_principal_events (
        user_id, event_kind,
        balance_before, balance_after, frozen_before, frozen_after,
        available_paid_before, available_paid_after,
        frozen_paid_before, frozen_paid_after, component_version
    ) VALUES (
        NEW.id,
        event_kind,
        OLD.balance, NEW.balance, OLD.frozen_balance, NEW.frozen_balance,
        OLD.wallet_available_paid, NEW.wallet_available_paid,
        OLD.wallet_frozen_paid, NEW.wallet_frozen_paid,
        NEW.wallet_component_version
    );
    RETURN NULL;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_users_record_wallet_principal_event ON users;
CREATE TRIGGER trg_users_record_wallet_principal_event
    AFTER UPDATE OF balance, frozen_balance, wallet_available_paid, wallet_frozen_paid
    ON users
    FOR EACH ROW
    WHEN (
        OLD.balance IS DISTINCT FROM NEW.balance
        OR OLD.frozen_balance IS DISTINCT FROM NEW.frozen_balance
        OR OLD.wallet_available_paid IS DISTINCT FROM NEW.wallet_available_paid
        OR OLD.wallet_frozen_paid IS DISTINCT FROM NEW.wallet_frozen_paid
    )
    EXECUTE FUNCTION sub2api_record_wallet_principal_event();

CREATE TABLE IF NOT EXISTS payment_wallet_fundings (
    payment_order_id BIGINT PRIMARY KEY REFERENCES payment_orders(id) ON DELETE RESTRICT,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    paid_credit_amount DECIMAL(20,8) NOT NULL CHECK (paid_credit_amount > 0),
    gift_credit_amount DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (gift_credit_amount >= 0),
    cash_paid_minor BIGINT NOT NULL CHECK (cash_paid_minor > 0),
    currency CHAR(3) NOT NULL,
    refunded_paid_amount DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (refunded_paid_amount >= 0),
    reclaimed_gift_amount DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (reclaimed_gift_amount >= 0),
    refunded_cash_minor BIGINT NOT NULL DEFAULT 0 CHECK (refunded_cash_minor >= 0),
    reserved_paid_amount DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (reserved_paid_amount >= 0),
    reserved_gift_amount DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (reserved_gift_amount >= 0),
    reserved_cash_minor BIGINT NOT NULL DEFAULT 0 CHECK (reserved_cash_minor >= 0),
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (refunded_paid_amount + reserved_paid_amount <= paid_credit_amount),
    CHECK (reclaimed_gift_amount + reserved_gift_amount <= gift_credit_amount),
    CHECK (refunded_cash_minor + reserved_cash_minor <= cash_paid_minor)
);

CREATE INDEX IF NOT EXISTS idx_payment_wallet_fundings_user
    ON payment_wallet_fundings (user_id, payment_order_id);

CREATE TABLE IF NOT EXISTS payment_subscription_grants (
    payment_order_id BIGINT PRIMARY KEY REFERENCES payment_orders(id) ON DELETE RESTRICT,
    subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id) ON DELETE RESTRICT,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
    term_start_at TIMESTAMPTZ NOT NULL,
    original_term_end_at TIMESTAMPTZ NOT NULL,
    current_term_end_at TIMESTAMPTZ NOT NULL,
    refunded_seconds BIGINT NOT NULL DEFAULT 0 CHECK (refunded_seconds >= 0),
    reserved_seconds BIGINT NOT NULL DEFAULT 0 CHECK (reserved_seconds >= 0),
    refunded_cash_minor BIGINT NOT NULL DEFAULT 0 CHECK (refunded_cash_minor >= 0),
    reserved_cash_minor BIGINT NOT NULL DEFAULT 0 CHECK (reserved_cash_minor >= 0),
    balance_bonus DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (balance_bonus >= 0),
    reset_card_count INTEGER NOT NULL DEFAULT 0 CHECK (reset_card_count >= 0),
    concurrency_target INTEGER NOT NULL DEFAULT 0 CHECK (concurrency_target >= 0),
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (term_start_at < original_term_end_at),
    CHECK (current_term_end_at >= term_start_at),
    CHECK (current_term_end_at <= original_term_end_at),
    CHECK (
        refunded_seconds + reserved_seconds
        <= FLOOR(EXTRACT(EPOCH FROM (original_term_end_at - term_start_at)))::BIGINT
    )
);

CREATE INDEX IF NOT EXISTS idx_payment_subscription_grants_tail
    ON payment_subscription_grants (subscription_id, current_term_end_at DESC);

-- A pending subscription refund shortens the live term before the provider
-- request. Keep that expiry immutable until the same refund attempt is either
-- captured or released, otherwise a renewal can make a successful cash refund
-- impossible to reconcile locally. Trusted reserve/release transactions set a
-- transaction-local marker immediately before changing the expiry.
CREATE OR REPLACE FUNCTION sub2api_guard_subscription_refund_hold()
RETURNS TRIGGER AS $$
DECLARE
    mutation_kind TEXT := COALESCE(
        NULLIF(current_setting('sub2api.subscription_refund_mutation', TRUE), ''),
        ''
    );
BEGIN
    IF mutation_kind IN ('reserve', 'release') THEN
        RETURN NEW;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM payment_subscription_grants
        WHERE subscription_id = OLD.id
          AND reserved_seconds > 0
    ) THEN
        RAISE EXCEPTION 'subscription % has a pending refund entitlement hold', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_user_subscriptions_refund_hold ON user_subscriptions;
CREATE TRIGGER trg_user_subscriptions_refund_hold
    BEFORE UPDATE OF expires_at, status, deleted_at, group_id
    ON user_subscriptions
    FOR EACH ROW
    WHEN (
        OLD.expires_at IS DISTINCT FROM NEW.expires_at
        OR OLD.status IS DISTINCT FROM NEW.status
        OR OLD.deleted_at IS DISTINCT FROM NEW.deleted_at
        OR OLD.group_id IS DISTINCT FROM NEW.group_id
    )
    EXECUTE FUNCTION sub2api_guard_subscription_refund_hold();

-- Subscription authorization uses a separate durable outbox. Do not widen the
-- deployed API-key outbox schema: an older worker must remain able to consume
-- its CHAR(64) payload throughout a rolling deploy or rollback.
CREATE TABLE IF NOT EXISTS subscription_cache_invalidation_outbox (
    id             BIGSERIAL PRIMARY KEY,
    user_id        BIGINT NOT NULL CHECK (user_id > 0),
    group_id       BIGINT NOT NULL CHECK (group_id > 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    available_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivery_stage SMALLINT NOT NULL DEFAULT 0 CHECK (delivery_stage IN (0, 1)),
    attempts       INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error     TEXT,
    claimed_at     TIMESTAMPTZ,
    claimed_by     TEXT
);

CREATE INDEX IF NOT EXISTS idx_subscription_cache_invalidation_available
    ON subscription_cache_invalidation_outbox (available_at, id)
    WHERE claimed_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_subscription_cache_invalidation_lease
    ON subscription_cache_invalidation_outbox (claimed_at)
    WHERE claimed_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_subscription_cache_invalidation_target
    ON subscription_cache_invalidation_outbox (user_id, group_id);
CREATE INDEX IF NOT EXISTS idx_subscription_cache_invalidation_created_at
    ON subscription_cache_invalidation_outbox (created_at);

CREATE OR REPLACE FUNCTION enqueue_subscription_authorization_cache_target(
    target_user_id BIGINT,
    target_group_id BIGINT
)
RETURNS VOID AS $$
BEGIN
    IF target_user_id IS NULL OR target_user_id <= 0
       OR target_group_id IS NULL OR target_group_id <= 0 THEN
        RETURN;
    END IF;

    INSERT INTO subscription_cache_invalidation_outbox (user_id, group_id)
    VALUES (target_user_id, target_group_id);

    -- API-key authorization embeds subscription-derived fields as well, so
    -- keep using the established SHA-256-only outbox for those cache entries.
    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')
    FROM api_keys AS k
    WHERE k.user_id = target_user_id
      AND k.deleted_at IS NULL
      AND k.key <> '';
END
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION enqueue_subscription_authorization_cache_invalidation()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        PERFORM enqueue_subscription_authorization_cache_target(NEW.user_id, NEW.group_id);
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        PERFORM enqueue_subscription_authorization_cache_target(OLD.user_id, OLD.group_id);
        RETURN OLD;
    END IF;

    IF OLD.starts_at IS NOT DISTINCT FROM NEW.starts_at
       AND OLD.expires_at IS NOT DISTINCT FROM NEW.expires_at
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at
       AND OLD.user_id IS NOT DISTINCT FROM NEW.user_id
       AND OLD.group_id IS NOT DISTINCT FROM NEW.group_id THEN
        RETURN NEW;
    END IF;

    PERFORM enqueue_subscription_authorization_cache_target(OLD.user_id, OLD.group_id);
    IF OLD.user_id IS DISTINCT FROM NEW.user_id
       OR OLD.group_id IS DISTINCT FROM NEW.group_id THEN
        PERFORM enqueue_subscription_authorization_cache_target(NEW.user_id, NEW.group_id);
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_user_subscriptions_authorization_cache_invalidation ON user_subscriptions;
CREATE TRIGGER trg_user_subscriptions_authorization_cache_invalidation
    AFTER INSERT OR UPDATE OF starts_at, expires_at, status, deleted_at, user_id, group_id OR DELETE
    ON user_subscriptions
    FOR EACH ROW
    EXECUTE FUNCTION enqueue_subscription_authorization_cache_invalidation();

ALTER TABLE unified_payment_refund_attempts
    ADD COLUMN IF NOT EXISTS refund_kind VARCHAR(20) NOT NULL DEFAULT 'legacy_balance',
    ADD COLUMN IF NOT EXISTS quote_revision VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS wallet_paid_amount DECIMAL(20,8) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS wallet_gift_amount DECIMAL(20,8) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS subscription_seconds BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS subscription_grant_order_id BIGINT NULL REFERENCES payment_orders(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS entitlement_reserved BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS valuation_at TIMESTAMPTZ NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'unified_payment_refund_attempts'::regclass
          AND conname = 'unified_refund_entitlement_amounts_nonnegative'
    ) THEN
        ALTER TABLE unified_payment_refund_attempts
            ADD CONSTRAINT unified_refund_entitlement_amounts_nonnegative CHECK (
                wallet_paid_amount >= 0
                AND wallet_gift_amount >= 0
                AND subscription_seconds >= 0
            );
    END IF;
END
$$;

COMMENT ON COLUMN users.wallet_available_paid IS
    'Refundable paid principal currently present in users.balance; all remaining positive balance is gift/unattributed.';
COMMENT ON COLUMN users.wallet_frozen_paid IS
    'Paid principal currently present in users.frozen_balance.';
COMMENT ON TABLE payment_wallet_fundings IS
    'Immutable original paid/gift split and cumulative refund accounting for a fulfilled balance payment order; account paid principal may be lower when the recharge repaid existing debt.';
COMMENT ON TABLE payment_subscription_grants IS
    'Exact order-scoped subscription interval used for tail-only prorated refunds.';
