-- Manual invoice workflow. This is additive and intentionally leaves payment,
-- refund, and fulfillment facts untouched. PDF bytes remain private in the
-- document table and are never selected by ordinary order-list queries.
CREATE TABLE IF NOT EXISTS payment_invoice_requests (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL UNIQUE REFERENCES payment_orders(id) ON DELETE RESTRICT,
    user_id BIGINT NOT NULL,
    title_type VARCHAR(20) NOT NULL,
    title VARCHAR(200) NOT NULL,
    tax_identifier VARCHAR(64),
    recipient_email VARCHAR(255) NOT NULL,
    recipient_phone VARCHAR(32),
    remark TEXT,
    amount DECIMAL(20,2) NOT NULL,
    currency VARCHAR(12) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    revision INT NOT NULL DEFAULT 1,
    provider VARCHAR(50) NOT NULL DEFAULT 'manual',
    provider_invoice_id VARCHAR(128),
    invoice_item_name VARCHAR(200),
    invoice_code VARCHAR(64),
    invoice_number VARCHAR(64),
    document_filename VARCHAR(255),
    document_size_bytes BIGINT,
    document_sha256 CHAR(64),

    email_delivery_status VARCHAR(20) NOT NULL DEFAULT 'NOT_SENT',
    email_delivery_attempts INT NOT NULL DEFAULT 0,
    email_delivery_error_kind VARCHAR(32),
    email_delivery_attempted_at TIMESTAMPTZ,
    email_delivered_at TIMESTAMPTZ,
    email_delivery_claim_token VARCHAR(36),
    email_delivery_claimed_at TIMESTAMPTZ,
    email_delivery_next_attempt_at TIMESTAMPTZ,

    feishu_notification_status VARCHAR(20) NOT NULL DEFAULT 'NOT_SENT',
    feishu_notification_revision INT NOT NULL DEFAULT 0,
    feishu_notification_attempts INT NOT NULL DEFAULT 0,
    feishu_notification_error_kind VARCHAR(32),
    feishu_notification_attempted_at TIMESTAMPTZ,
    feishu_notified_at TIMESTAMPTZ,
    feishu_notification_claim_token VARCHAR(36),
    feishu_notification_claimed_at TIMESTAMPTZ,
    feishu_notification_next_attempt_at TIMESTAMPTZ,

    rejection_reason TEXT,
    processed_by BIGINT,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    processed_at TIMESTAMPTZ,
    issued_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT payment_invoice_requests_title_type_check
        CHECK (title_type IN ('personal', 'enterprise')),
    CONSTRAINT payment_invoice_requests_status_check
        CHECK (status IN ('PENDING', 'PROCESSING', 'ISSUED', 'REJECTED')),
    CONSTRAINT payment_invoice_requests_amount_check CHECK (amount > 0),
    CONSTRAINT payment_invoice_requests_revision_check CHECK (revision > 0),
    CONSTRAINT payment_invoice_requests_email_delivery_status_check
        CHECK (email_delivery_status IN ('NOT_SENT', 'PENDING', 'SENDING', 'SENT', 'FAILED')),
    CONSTRAINT payment_invoice_requests_email_delivery_attempts_check CHECK (email_delivery_attempts >= 0),
    CONSTRAINT payment_invoice_requests_feishu_notification_status_check
        CHECK (feishu_notification_status IN ('NOT_SENT', 'PENDING', 'SENDING', 'SENT', 'FAILED')),
    CONSTRAINT payment_invoice_requests_feishu_notification_revision_check CHECK (feishu_notification_revision >= 0),
    CONSTRAINT payment_invoice_requests_feishu_notification_attempts_check CHECK (feishu_notification_attempts >= 0),
    CONSTRAINT payment_invoice_requests_enterprise_tax_id_check
        CHECK (title_type <> 'enterprise' OR NULLIF(BTRIM(tax_identifier), '') IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_payment_invoice_requests_user_id
    ON payment_invoice_requests(user_id);
CREATE INDEX IF NOT EXISTS idx_payment_invoice_requests_status_requested_at
    ON payment_invoice_requests(status, requested_at DESC);
CREATE INDEX IF NOT EXISTS idx_payment_invoice_requests_email_delivery_due
    ON payment_invoice_requests(email_delivery_status, email_delivery_next_attempt_at, updated_at)
    WHERE email_delivery_status IN ('PENDING', 'SENDING', 'FAILED');
CREATE INDEX IF NOT EXISTS idx_payment_invoice_requests_feishu_notification_due
    ON payment_invoice_requests(feishu_notification_status, feishu_notification_next_attempt_at, updated_at)
    WHERE feishu_notification_status IN ('PENDING', 'SENDING', 'FAILED');

CREATE TABLE IF NOT EXISTS payment_invoice_documents (
    id BIGSERIAL PRIMARY KEY,
    invoice_request_id BIGINT NOT NULL UNIQUE REFERENCES payment_invoice_requests(id) ON DELETE CASCADE,
    filename VARCHAR(255) NOT NULL,
    content_type VARCHAR(64) NOT NULL DEFAULT 'application/pdf',
    size_bytes BIGINT NOT NULL,
    sha256 CHAR(64) NOT NULL,
    data BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT payment_invoice_documents_size_check CHECK (size_bytes > 0),
    CONSTRAINT payment_invoice_documents_sha256_check CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT payment_invoice_documents_content_type_check CHECK (content_type = 'application/pdf')
);

COMMENT ON TABLE payment_invoice_requests IS
    'Manual invoice request workflow. Buyer tax identifiers and email stay in this restricted table; delivery errors are generic only.';
COMMENT ON TABLE payment_invoice_documents IS
    'Private official invoice PDF bytes. Normal order-list queries do not load this table.';
