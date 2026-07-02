ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS rotated_from;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS last_used_at;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS user_agent;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS ip;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS device_label;
ALTER TABLE users DROP COLUMN IF EXISTS token_version;
