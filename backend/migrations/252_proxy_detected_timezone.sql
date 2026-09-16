-- Persist the IANA timezone observed for each proxy exit. OpenAI/Codex request
-- rewriting reads this metadata only after account selection, so proxy-bound
-- accounts keep their fixed egress identity and matching local-time context.
ALTER TABLE proxies
    ADD COLUMN IF NOT EXISTS detected_timezone VARCHAR(64),
    ADD COLUMN IF NOT EXISTS timezone_detected_at TIMESTAMPTZ;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'proxies'::regclass
          AND conname = 'proxies_detected_timezone_nonempty'
    ) THEN
        ALTER TABLE proxies
            ADD CONSTRAINT proxies_detected_timezone_nonempty CHECK (
                detected_timezone IS NULL OR (
                    detected_timezone = BTRIM(detected_timezone)
                    AND LENGTH(detected_timezone) BETWEEN 1 AND 64
                    AND detected_timezone !~ '[[:space:]]'
                )
            );
    END IF;
END
$$;

COMMENT ON COLUMN proxies.detected_timezone IS
    'Probe-derived IANA timezone for the current proxy exit IP; NULL until a geo-capable probe succeeds.';
COMMENT ON COLUMN proxies.timezone_detected_at IS
    'Timestamp of the successful probe that supplied detected_timezone.';
