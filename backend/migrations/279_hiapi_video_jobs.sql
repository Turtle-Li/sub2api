-- Older runtimes continue to understand GoodPut's rows and never claim HiAPI jobs.
ALTER TABLE goodput_video_jobs ADD COLUMN IF NOT EXISTS resolution TEXT NOT NULL DEFAULT '720p';
CREATE TABLE IF NOT EXISTS hiapi_video_jobs (LIKE goodput_video_jobs INCLUDING ALL);
