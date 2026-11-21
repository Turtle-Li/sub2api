-- Temporary provider result lines and final image bytes, independent of COS.
CREATE TABLE batch_image_result_checkpoints (
    job_id VARCHAR(64) NOT NULL REFERENCES batch_image_jobs(batch_id) ON DELETE CASCADE,
    custom_id VARCHAR(255) NOT NULL,
    stage VARCHAR(32) NOT NULL CHECK (stage IN ('provider_result', 'final_image')),
    payload BYTEA NOT NULL,
    mime_type VARCHAR(128) NOT NULL,
    width INTEGER NOT NULL,
    height INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (job_id, stage, custom_id),
    CHECK (
        (stage = 'provider_result' AND octet_length(payload) BETWEEN 1 AND 33554432
            AND mime_type = 'application/x-ndjson' AND width = 0 AND height = 0)
        OR (stage = 'final_image' AND octet_length(payload) BETWEEN 1 AND 536870912
            AND mime_type IN ('image/png', 'image/jpeg', 'image/webp') AND width > 0 AND height > 0)
    )
);

CREATE TABLE batch_image_delivery_retries (
    job_id VARCHAR(64) PRIMARY KEY REFERENCES batch_image_jobs(batch_id) ON DELETE CASCADE,
    stage VARCHAR(32) NOT NULL,
    code VARCHAR(96) NOT NULL,
    failure_count INTEGER NOT NULL CHECK (failure_count > 0),
    retry_at TIMESTAMPTZ NOT NULL
);

-- A new archive delivery run never submits another Vertex generation.
CREATE TABLE batch_image_pump_attempts (
    job_id VARCHAR(64) PRIMARY KEY REFERENCES batch_image_jobs(batch_id) ON DELETE CASCADE,
    attempt INTEGER NOT NULL CHECK (attempt > 0)
);

-- Settlement commits before the controller can discard its last staging copy.
-- A lost ACK or DB write leaves this row discoverable after worker restart.
CREATE INDEX batch_image_shanghai_pending_ack_idx ON batch_image_jobs (id)
    WHERE provider_output_ref = 'studio-local-checkpoint:shanghai:v1'
      AND status IN ('completed', 'output_deleted');
