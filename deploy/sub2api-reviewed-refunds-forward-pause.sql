\set ON_ERROR_STOP on
-- Only run while the target-specific --forward-pause-guard owns the app
-- host's shared maintenance lock. This is a restrictive true -> false CAS;
-- ordinary request admission and every financial record remain unchanged.
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';
SELECT pg_advisory_xact_lock(91625647919686);
DO $pause$
DECLARE changed INTEGER; benefit_pending BOOLEAN;
BEGIN
  IF EXISTS (
    SELECT 1 FROM unified_payment_refund_attempts
    WHERE entitlement_reserved = TRUE
      AND quote_revision <> ''
      AND refund_kind IN ('balance', 'subscription')
  ) THEN
    RAISE EXCEPTION 'reviewed refund reservations remain';
  END IF;
  IF to_regclass('public.payment_refund_benefit_sources') IS NOT NULL THEN
    EXECUTE 'SELECT EXISTS(SELECT 1 FROM public.payment_refund_benefit_sources WHERE state=''RESERVED'')'
      INTO benefit_pending;
    IF benefit_pending THEN
      RAISE EXCEPTION 'refund benefit reservations remain';
    END IF;
  END IF;
  UPDATE settings SET value='false', updated_at=NOW()
  WHERE key='PAYMENT_REVIEWED_REFUNDS_ENABLED' AND value='true';
  GET DIAGNOSTICS changed = ROW_COUNT;
  IF changed <> 1 THEN
    RAISE EXCEPTION 'reviewed refund gate did not match expected true';
  END IF;
END $pause$;
COMMIT;
SELECT 'DB_CAS_FALSE_CONFIRMED';
