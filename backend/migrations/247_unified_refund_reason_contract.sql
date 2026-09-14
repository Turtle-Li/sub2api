-- Persist the exact normalized reason contract before any provider call so a
-- lost response or reconciler retry replays the original gateway request.
ALTER TABLE unified_payment_refund_attempts
    ADD COLUMN IF NOT EXISTS reason_code VARCHAR(32) NOT NULL DEFAULT 'other';

-- The unified gateway accepts up to 240 Unicode code points. Widening the
-- existing additive summary field keeps old attempts readable while allowing
-- new normalized summaries to use the full contract-safe boundary.
ALTER TABLE unified_payment_refund_attempts
    ALTER COLUMN reason_summary TYPE VARCHAR(240);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'unified_payment_refund_attempts'::regclass
          AND conname = 'unified_refund_reason_code_valid'
    ) THEN
        ALTER TABLE unified_payment_refund_attempts
            ADD CONSTRAINT unified_refund_reason_code_valid CHECK (
                reason_code IN (
                    'customer_request',
                    'duplicate_charge',
                    'service_not_delivered',
                    'service_error',
                    'other'
                )
            );
    END IF;
END
$$;

COMMENT ON COLUMN unified_payment_refund_attempts.reason_code IS
    'Normalized unified-payment refund reason code; old attempts default to other.';
COMMENT ON COLUMN unified_payment_refund_attempts.reason_summary IS
    'Normalized, whitespace-collapsed gateway reason summary replayed with the persisted reason code.';
