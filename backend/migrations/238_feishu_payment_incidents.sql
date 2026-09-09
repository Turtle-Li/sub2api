-- Durable operator incidents for the independent Feishu payment-notification
-- worker. These records deliberately keep only a stable incident key, payment
-- order id, and delivery state; they do not copy payment callback bodies,
-- customer identifiers, or webhook credentials.
CREATE TABLE IF NOT EXISTS feishu_payment_incidents (
    id UUID PRIMARY KEY,
    incident_key VARCHAR(160) NOT NULL,
    incident_type VARCHAR(40) NOT NULL CHECK (incident_type IN (
        'REFUND_REVIEW', 'PAID_INCOMPLETE', 'TEST_NOTIFICATION'
    )),
    subject_order_id BIGINT NULL REFERENCES payment_orders(id) ON DELETE RESTRICT,
    generation INTEGER NOT NULL CHECK (generation > 0),
    status VARCHAR(16) NOT NULL CHECK (status IN ('OPEN', 'RESOLVED', 'CLOSED')),
    opened_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMPTZ NULL,
    last_observed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_checked_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_reminder_enqueued_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (incident_key, generation)
);

-- A closed generation remains as audit evidence. A new occurrence gets a new
-- generation while this partial index prevents two simultaneously OPEN rows.
CREATE UNIQUE INDEX IF NOT EXISTS feishu_payment_incidents_one_open_key
    ON feishu_payment_incidents (incident_key)
    WHERE status = 'OPEN';

-- Fair rechecks use the oldest unchecked open incident first, rather than a
-- permanently old payment order monopolising every bounded worker pass.
CREATE INDEX IF NOT EXISTS feishu_payment_incidents_open_recheck
    ON feishu_payment_incidents (last_checked_at ASC, opened_at ASC, id ASC)
    WHERE status = 'OPEN';

CREATE TABLE IF NOT EXISTS feishu_payment_incident_deliveries (
    id UUID PRIMARY KEY,
    incident_id UUID NOT NULL REFERENCES feishu_payment_incidents(id) ON DELETE RESTRICT,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    delivery_kind VARCHAR(16) NOT NULL CHECK (delivery_kind IN ('OPEN', 'REMINDER', 'RESOLVED', 'TEST')),
    status VARCHAR(16) NOT NULL CHECK (status IN ('PENDING', 'CLAIMED', 'DELIVERED', 'SUPPRESSED')),
    not_before TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    claim_token UUID NULL,
    claimed_at TIMESTAMPTZ NULL,
    lease_expires_at TIMESTAMPTZ NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error_code VARCHAR(80) NULL,
    delivered_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (incident_id, sequence)
);

CREATE INDEX IF NOT EXISTS feishu_payment_incident_deliveries_due
    ON feishu_payment_incident_deliveries (status, not_before ASC, created_at ASC, id ASC)
    WHERE status IN ('PENDING', 'CLAIMED');

CREATE INDEX IF NOT EXISTS feishu_payment_incident_deliveries_incident_sequence
    ON feishu_payment_incident_deliveries (incident_id, sequence ASC);

COMMENT ON TABLE feishu_payment_incidents IS
    'Durable Sub2 payment operational incidents; contains no payment callback bodies, customer data, or Feishu credential.';
COMMENT ON TABLE feishu_payment_incident_deliveries IS
    'Ordered at-least-once Feishu notification attempts; generic error codes only, never webhook URLs or response bodies.';
