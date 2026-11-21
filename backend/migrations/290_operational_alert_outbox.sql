CREATE TABLE operational_alert_outbox (
    id BIGSERIAL PRIMARY KEY,
    component VARCHAR(96) NOT NULL,
    stage VARCHAR(96) NOT NULL,
    subject_id VARCHAR(96) NOT NULL,
    code VARCHAR(96) NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    claim VARCHAR(36),
    retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (component, stage, subject_id, code)
);

CREATE INDEX operational_alert_outbox_pending_idx
    ON operational_alert_outbox (retry_at, id) WHERE delivered_at IS NULL;
