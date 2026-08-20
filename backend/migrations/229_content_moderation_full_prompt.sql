-- Keep the complete textual prompt for blocked moderation/cyber-policy hits.
-- The admin list deliberately does not select this large sensitive field;
-- detail reads are permission-gated at the existing admin route.
ALTER TABLE content_moderation_logs
    ADD COLUMN IF NOT EXISTS full_prompt TEXT NOT NULL DEFAULT '';
