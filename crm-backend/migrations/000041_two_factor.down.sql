DROP TABLE IF EXISTS two_factor_challenges;
DROP TABLE IF EXISTS two_factor_backup_codes;
ALTER TABLE organizations DROP COLUMN IF EXISTS require_two_factor;
ALTER TABLE users DROP COLUMN IF EXISTS totp_enabled_at;
ALTER TABLE users DROP COLUMN IF EXISTS totp_secret;
