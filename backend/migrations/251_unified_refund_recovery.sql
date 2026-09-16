-- Project the central service's safe refund failure classification so the
-- admin UI can explain a paused refund and offer only the recovery actions
-- authorized by that exact evidence. Raw provider bodies/messages stay in the
-- provider boundary and are never stored here.
ALTER TABLE unified_payment_refund_attempts
    ADD COLUMN IF NOT EXISTS provider_status VARCHAR(80),
    ADD COLUMN IF NOT EXISTS failure_code VARCHAR(120),
    ADD COLUMN IF NOT EXISTS provider_updated_at TIMESTAMPTZ;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'unified_payment_refund_attempts'::regclass
          AND conname = 'unified_refund_provider_status_safe'
    ) THEN
        ALTER TABLE unified_payment_refund_attempts
            ADD CONSTRAINT unified_refund_provider_status_safe CHECK (
                provider_status IS NULL OR provider_status ~ '^[A-Za-z0-9_.:-]{1,80}$'
            );
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'unified_payment_refund_attempts'::regclass
          AND conname = 'unified_refund_failure_code_safe'
    ) THEN
        ALTER TABLE unified_payment_refund_attempts
            ADD CONSTRAINT unified_refund_failure_code_safe CHECK (
                failure_code IS NULL OR failure_code ~ '^[a-z0-9_.:-]{1,120}$'
            );
    END IF;
END
$$;

COMMENT ON COLUMN unified_payment_refund_attempts.provider_status IS
    'Safe central/provider classification only; never a raw provider response or message.';
COMMENT ON COLUMN unified_payment_refund_attempts.failure_code IS
    'Stable central refund lifecycle code used for audited recovery eligibility.';
COMMENT ON COLUMN unified_payment_refund_attempts.provider_updated_at IS
    'Timestamp of the central refund resource that supplied the projected classification.';

-- Existing attempts already have append-only, normalized central result
-- evidence. Hydrate only a strictly correlated latest valid event so a
-- pre-migration balance shortage becomes visible without querying or
-- resubmitting money. This migration must also survive malformed historical
-- TEXT rows: parse JSON and timestamps inside exception blocks rather than
-- casting every audit row in one set expression.
DO $$
DECLARE
    v_attempt RECORD;
    v_event RECORD;
    v_detail JSONB;
    v_provider_updated_at TIMESTAMPTZ;
BEGIN
    FOR v_attempt IN
        SELECT product_refund_no, order_id, refund_request_id
        FROM unified_payment_refund_attempts
        WHERE provider_status IS NULL
    LOOP
        FOR v_event IN
            SELECT id, detail, created_at
            FROM unified_payment_refund_events
            WHERE order_id = v_attempt.order_id
              AND action = 'UNIFIED_REFUND_RESULT'
            ORDER BY created_at DESC, id DESC
        LOOP
            BEGIN
                v_detail := v_event.detail::JSONB;
            EXCEPTION WHEN data_exception THEN
                CONTINUE;
            END;

            IF v_detail ->> 'product_refund_no' IS DISTINCT FROM v_attempt.product_refund_no OR
               v_detail ->> 'refund_request_id' IS DISTINCT FROM CAST(v_attempt.refund_request_id AS TEXT) OR
               COALESCE(v_detail ->> 'provider_status', '') !~ '^[A-Za-z0-9_.:-]{1,80}$' OR
               (v_detail ->> 'failure_code' IS NOT NULL AND
                v_detail ->> 'failure_code' !~ '^[a-z0-9_.:-]{1,120}$') THEN
                CONTINUE;
            END IF;

            v_provider_updated_at := v_event.created_at;
            IF COALESCE(v_detail ->> 'updated_at', '') <> '' THEN
                BEGIN
                    v_provider_updated_at := (v_detail ->> 'updated_at')::TIMESTAMPTZ;
                EXCEPTION WHEN data_exception THEN
                    v_provider_updated_at := v_event.created_at;
                END;
                IF NOT isfinite(v_provider_updated_at) THEN
                    v_provider_updated_at := v_event.created_at;
                END IF;
            END IF;

            UPDATE unified_payment_refund_attempts
            SET provider_status = v_detail ->> 'provider_status',
                failure_code = NULLIF(v_detail ->> 'failure_code', ''),
                provider_updated_at = v_provider_updated_at
            WHERE product_refund_no = v_attempt.product_refund_no
              AND provider_status IS NULL;
            EXIT;
        END LOOP;
    END LOOP;
END
$$;
