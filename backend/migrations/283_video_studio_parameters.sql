-- NULL distinguishes earlier receipts from a known text-only submission.
ALTER TABLE video_studio_jobs ADD COLUMN IF NOT EXISTS parameters JSONB
  CHECK (parameters IS NULL OR jsonb_typeof(parameters) = 'object');
