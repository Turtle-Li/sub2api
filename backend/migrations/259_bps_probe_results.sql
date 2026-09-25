-- 259_bps_probe_results.sql
-- 降智修复面板的手动提问测试结果：仅供管理员对比 BPS 与原生路径，可手动删除，服务端保留 7 天。
CREATE TABLE IF NOT EXISTS bps_probe_results (
    id               BIGSERIAL PRIMARY KEY,
    batch_id         VARCHAR(40)  NOT NULL,
    account_id       BIGINT       NOT NULL,
    account_name     VARCHAR(200) NOT NULL DEFAULT '',
    path             VARCHAR(16)  NOT NULL,
    model            VARCHAR(100) NOT NULL,
    effort           VARCHAR(16)  NOT NULL DEFAULT '',
    applied_effort   VARCHAR(16)  NOT NULL DEFAULT '',
    prompt           TEXT         NOT NULL,
    status           VARCHAR(16)  NOT NULL,
    content          TEXT         NOT NULL DEFAULT '',
    error_message    TEXT         NOT NULL DEFAULT '',
    input_tokens     INTEGER      NOT NULL DEFAULT 0,
    output_tokens    INTEGER      NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER      NOT NULL DEFAULT 0,
    duration_ms      BIGINT       NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    started_at       TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_bps_probe_results_created_at ON bps_probe_results (created_at DESC);
