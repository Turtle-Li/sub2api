ALTER TABLE ops_alert_rules
    ADD COLUMN IF NOT EXISTS notify_feishu BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS notify_emails JSONB,
    ADD COLUMN IF NOT EXISTS custom_message TEXT;

ALTER TABLE ops_alert_events
    ADD COLUMN IF NOT EXISTS feishu_sent BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS ops_alert_deliveries (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES ops_alert_events (id) ON DELETE CASCADE,
    channel TEXT NOT NULL CHECK (channel IN ('email', 'feishu')),
    target TEXT NOT NULL CHECK (target <> '' AND (channel <> 'feishu' OR target = 'payment_feishu')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed', 'skipped')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 5),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claim_token TEXT,
    claimed_until TIMESTAMPTZ,
    last_error TEXT,
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (event_id, channel, target),
    CHECK ((claim_token IS NULL) = (claimed_until IS NULL))
);

CREATE INDEX IF NOT EXISTS ops_alert_deliveries_pending_idx
    ON ops_alert_deliveries (next_attempt_at, id) WHERE status = 'pending';
