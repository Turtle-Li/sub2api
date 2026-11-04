-- Manual shop fulfillment is separate from immutable payment facts. No public
-- products or prices are seeded: the owner publishes the catalog in the panel.
CREATE TABLE shop_products (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(160) NOT NULL,
    category VARCHAR(80) NOT NULL DEFAULT '',
    summary VARCHAR(600) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    price_fen BIGINT NOT NULL DEFAULT 0 CHECK (price_fen BETWEEN 0 AND 100000000),
    duration_value BIGINT NOT NULL DEFAULT 1 CHECK (duration_value BETWEEN 1 AND 3650),
    duration_unit VARCHAR(10) NOT NULL DEFAULT 'month' CHECK (duration_unit IN ('month','day','year')),
    fulfillment_mode VARCHAR(24) NOT NULL CHECK (fulfillment_mode IN ('session_topup','account_delivery')),
    input_format VARCHAR(16) NOT NULL DEFAULT 'session_json' CHECK (input_format IN ('session_json','text')),
    input_url VARCHAR(2048) NOT NULL DEFAULT '',
    input_instructions TEXT NOT NULL DEFAULT '',
    guide_key VARCHAR(64) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order BIGINT NOT NULL DEFAULT 0,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (NOT enabled OR price_fen > 0)
);
CREATE INDEX shopproduct_enabled_sort_order ON shop_products(enabled,sort_order);

CREATE TABLE shop_fulfillments (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL UNIQUE REFERENCES payment_orders(id) ON DELETE RESTRICT,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    product_id BIGINT NOT NULL REFERENCES shop_products(id) ON DELETE RESTRICT,
    product_snapshot JSONB NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'WAITING_PAYMENT' CHECK (status IN ('WAITING_PAYMENT','WAITING_INPUT','QUEUED','PROCESSING','NEEDS_INPUT','COMPLETED','CLOSED')),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    input_revision BIGINT NOT NULL DEFAULT 0 CHECK (input_revision >= 0),
    input_ciphertext TEXT,
    input_account VARCHAR(320) NOT NULL DEFAULT '',
    input_submitted_at TIMESTAMPTZ,
    delivery_ciphertext TEXT,
    status_note VARCHAR(2000) NOT NULL DEFAULT '',
    processed_by BIGINT,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (status <> 'COMPLETED' OR (completed_at IS NOT NULL AND input_ciphertext IS NULL))
);
CREATE INDEX shopfulfillment_user_id_created_at ON shop_fulfillments(user_id,created_at);
CREATE INDEX shopfulfillment_status_created_at ON shop_fulfillments(status,created_at);

CREATE TABLE shop_notifications (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES payment_orders(id) ON DELETE RESTRICT,
    event VARCHAR(32) NOT NULL CHECK (event IN ('paid','input_received','request_input','complete')),
    revision BIGINT NOT NULL,
    channel VARCHAR(16) NOT NULL CHECK (channel IN ('email','feishu')),
    status VARCHAR(16) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','SENDING','SENT','FAILED')),
    attempts BIGINT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    claim_token VARCHAR(36) NOT NULL DEFAULT '',
    claimed_at TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    error_kind VARCHAR(48) NOT NULL DEFAULT '',
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (order_id,event,revision,channel)
);
CREATE INDEX shopnotification_status_next_attempt_at ON shop_notifications(status,next_attempt_at);
