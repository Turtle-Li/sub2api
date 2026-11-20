-- Separate table so the other providers' reconcilers can never poll a Muse task.
CREATE TABLE IF NOT EXISTS muse_studio_video_jobs (LIKE hiapi_video_jobs INCLUDING ALL);
ALTER TABLE muse_studio_video_jobs ADD CONSTRAINT muse_studio_video_owner CHECK (api_key_id=0 AND model='muse-video');
CREATE INDEX IF NOT EXISTS muse_studio_video_owner_idx ON muse_studio_video_jobs(user_id,group_id,created_at DESC);
ALTER TABLE muse_studio_video_jobs ADD COLUMN studio_request_id UUID;
ALTER TABLE goodput_video_jobs ADD COLUMN studio_request_id UUID;
CREATE UNIQUE INDEX muse_studio_request_idx ON muse_studio_video_jobs(user_id,studio_request_id) WHERE studio_request_id IS NOT NULL;
CREATE UNIQUE INDEX goodput_studio_request_idx ON goodput_video_jobs(user_id,studio_request_id) WHERE studio_request_id IS NOT NULL;
ALTER TABLE video_studio_jobs ADD COLUMN charged_amount NUMERIC(20,8), ADD COLUMN charged_currency TEXT,
 ADD CONSTRAINT video_studio_charge_pair CHECK ((charged_amount IS NULL AND charged_currency IS NULL) OR (charged_amount IS NOT NULL AND charged_amount > 0 AND charged_currency IS NOT NULL));
