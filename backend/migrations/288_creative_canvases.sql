-- Creative canvases store private layout and lineage, never media or a second generation ledger.
CREATE TABLE IF NOT EXISTS creative_canvases (
  id UUID PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title TEXT NOT NULL, revision BIGINT NOT NULL DEFAULT 1 CHECK(revision > 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), document JSONB NOT NULL,
  UNIQUE(user_id,id)
);
CREATE INDEX IF NOT EXISTS creative_canvases_owner_idx ON creative_canvases(user_id,updated_at DESC,id);
CREATE TABLE IF NOT EXISTS creative_canvas_attempts (
  user_id BIGINT NOT NULL, canvas_id UUID NOT NULL, node_id UUID NOT NULL, request_id UUID NOT NULL,
  source_image JSONB, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY(user_id,request_id), FOREIGN KEY(user_id,canvas_id) REFERENCES creative_canvases(user_id,id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS creative_canvas_attempts_node_idx ON creative_canvas_attempts(user_id,canvas_id,node_id,created_at);
