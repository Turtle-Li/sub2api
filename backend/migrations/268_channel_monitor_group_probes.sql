-- Synthetic group checks are opt-in and never enter real-request aggregates.
ALTER TABLE channel_monitor_v3_config
    ADD COLUMN IF NOT EXISTS group_probe JSONB NOT NULL DEFAULT '{"enabled":false,"groups":[]}';

CREATE TABLE IF NOT EXISTS channel_monitor_v3_group_probes (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    model TEXT NOT NULL,
    slot_start TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    outcome TEXT NOT NULL DEFAULT 'pending' CHECK (outcome IN ('pending','success','failure','cancelled')),
    category TEXT NOT NULL DEFAULT '',
    latency_ms BIGINT NOT NULL DEFAULT 0,
    account_id BIGINT,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    UNIQUE (group_id, slot_start)
);
CREATE INDEX IF NOT EXISTS channel_monitor_v3_group_probes_time_idx
    ON channel_monitor_v3_group_probes (slot_start);
