-- Expand only. No automatic product is seeded or enabled by migration.
ALTER TABLE shop_products ADD COLUMN payment_actions JSONB;
CREATE TABLE shop_order_actions (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL UNIQUE REFERENCES payment_orders(id) ON DELETE RESTRICT,
    action_key VARCHAR(48) NOT NULL UNIQUE,
    -- Text preserves the exact immutable bytes used by the snapshot hash.
    snapshot TEXT NOT NULL,
    snapshot_hash VARCHAR(64) NOT NULL,
    deadline_at TIMESTAMPTZ NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','RUNNING','SUCCEEDED','REVIEW_REQUIRED','CANCELLED','COMPENSATING','REVOKED')),
    attempts BIGINT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    claim_revision BIGINT NOT NULL DEFAULT 0 CHECK (claim_revision >= 0),
    lease_until TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    error_kind VARCHAR(48) NOT NULL DEFAULT '',
    compensation_reason VARCHAR(32) NOT NULL DEFAULT '',
    receipt_ref VARCHAR(160) NOT NULL DEFAULT '',
    result_kind VARCHAR(16) NOT NULL DEFAULT 'none' CHECK (result_kind IN ('none','text')),
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX shoporderaction_status_next_attempt_at ON shop_order_actions(status,next_attempt_at);
CREATE INDEX shoporderaction_status_lease_until ON shop_order_actions(status,lease_until);
ALTER TABLE shop_notifications DROP CONSTRAINT shop_notifications_event_check;
ALTER TABLE shop_notifications ADD CONSTRAINT shop_notifications_event_check
    CHECK (event IN ('paid','input_received','request_input','complete','action_review'));
