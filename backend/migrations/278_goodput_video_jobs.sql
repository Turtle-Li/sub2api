-- Receipt, wallet reservation and settlement fence for asynchronous GoodPut jobs.
CREATE TABLE IF NOT EXISTS goodput_video_jobs (
    id UUID PRIMARY KEY,
    public_id TEXT UNIQUE,
    native_id TEXT,
    user_id BIGINT NOT NULL,
    api_key_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    account_id BIGINT NOT NULL,
    model TEXT NOT NULL,
    seconds INT NOT NULL CHECK (seconds BETWEEN 4 AND 15),
    hold_amount NUMERIC(20,8) NOT NULL CHECK (hold_amount > 0),
    reference_amount NUMERIC(20,8) NOT NULL CHECK (reference_amount > 0),
    rate_multiplier NUMERIC(20,8) NOT NULL,
    currency TEXT NOT NULL,
    account_rate NUMERIC(20,8) NOT NULL,
    account_quota_cost NUMERIC(20,8) NOT NULL,
    account_quota_daily BOOL NOT NULL,
    account_quota_weekly BOOL NOT NULL,
    account_request_count INT NOT NULL,
    state TEXT NOT NULL DEFAULT 'submitting' CHECK (state IN ('submitting', 'pending', 'completed', 'failed', 'review')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    next_poll_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((public_id IS NULL) = (native_id IS NULL))
);
CREATE INDEX IF NOT EXISTS goodput_video_jobs_due_idx ON goodput_video_jobs(next_poll_at) WHERE state IN ('submitting', 'pending');
CREATE INDEX IF NOT EXISTS goodput_video_jobs_key_pending_idx ON goodput_video_jobs(api_key_id) WHERE state IN ('submitting', 'pending', 'review');
