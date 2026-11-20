-- Session workspace receipts. Provider ownership and wallet settlement stay in their existing ledgers.
CREATE TABLE IF NOT EXISTS video_studio_jobs (
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  id UUID NOT NULL, api_key_id BIGINT NOT NULL, group_id BIGINT NOT NULL,
  model TEXT NOT NULL, seconds INTEGER NOT NULL CHECK (seconds BETWEEN 4 AND 15), size TEXT NOT NULL,
  fingerprint TEXT NOT NULL, receipt TEXT, state TEXT NOT NULL DEFAULT 'submitting'
    CHECK (state IN ('submitting','uncertain','queued','running','completed','failed','expired')),
  billing_review BOOLEAN NOT NULL DEFAULT FALSE, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY(user_id,id)
);
CREATE INDEX IF NOT EXISTS video_studio_jobs_history_idx ON video_studio_jobs(user_id, created_at DESC, id DESC);
