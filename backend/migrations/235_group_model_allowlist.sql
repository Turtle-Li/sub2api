-- Upstream v0.2.2 changes display-only model lists into admission allowlists.
-- This fork shares one database across blue-green generations and a rollback
-- origin. Keep the physical storage column so existing binaries can still
-- read and write groups; the new Ent model_allowlist field uses StorageKey.
-- Preserve every stored enabled flag and model entry without alteration.
COMMENT ON COLUMN groups.models_list_config IS
    'Group model allowlist; legacy storage name retained for rolling-upgrade compatibility';
