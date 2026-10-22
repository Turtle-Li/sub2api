CREATE TABLE paid_fallback_incidents (
    id UUID PRIMARY KEY,
    group_id BIGINT NOT NULL,
    public_model TEXT NOT NULL,
    generation BIGINT NOT NULL CHECK (generation > 0),
    opened_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    last_attempt_at TIMESTAMPTZ NOT NULL,
    last_reminder_at TIMESTAMPTZ,
    last_checked_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    UNIQUE (group_id, public_model, generation)
);
CREATE UNIQUE INDEX paid_fallback_incidents_open_scope ON paid_fallback_incidents (group_id, public_model) WHERE resolved_at IS NULL;

CREATE TABLE paid_fallback_attempts (
    id UUID PRIMARY KEY,
    inbound_request_id TEXT NOT NULL,
    api_key_id BIGINT NOT NULL,
    account_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    public_model TEXT NOT NULL,
    forward_model TEXT NOT NULL,
    upstream_model TEXT NOT NULL,
    transport TEXT NOT NULL CHECK (transport IN ('http', 'ws_http_bridge', 'ws_ctx_pool', 'ws_passthrough')),
    turn_index INTEGER NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    finished_at TIMESTAMPTZ,
    outcome TEXT NOT NULL DEFAULT 'in_flight' CHECK (outcome IN ('in_flight', 'http_headers', 'ws_frame_written', 'unknown', 'not_sent')),
    http_status INTEGER NOT NULL DEFAULT 0,
    incident_id UUID REFERENCES paid_fallback_incidents(id)
);
CREATE INDEX paid_fallback_attempts_pending ON paid_fallback_attempts (group_id, public_model, started_at) WHERE incident_id IS NULL AND outcome NOT IN ('in_flight','not_sent');
CREATE INDEX paid_fallback_attempts_unfinished ON paid_fallback_attempts (started_at) WHERE outcome='in_flight';
CREATE INDEX paid_fallback_attempts_recent ON paid_fallback_attempts (started_at DESC);
CREATE INDEX paid_fallback_attempts_incident ON paid_fallback_attempts (incident_id);

CREATE TABLE paid_fallback_deliveries (
    id UUID PRIMARY KEY,
    incident_id UUID NOT NULL REFERENCES paid_fallback_incidents(id),
    event_key TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('open', 'reminder', 'recovered', 'daily_cost')),
    recipient TEXT NOT NULL,
    summary TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    attempts INTEGER NOT NULL DEFAULT 0,
    claim_token UUID,
    lease_expires_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    suppressed_at TIMESTAMPTZ,
    UNIQUE (event_key, recipient)
);
CREATE INDEX paid_fallback_deliveries_pending ON paid_fallback_deliveries (next_attempt_at) WHERE delivered_at IS NULL AND suppressed_at IS NULL;
