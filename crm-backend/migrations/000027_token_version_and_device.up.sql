-- P2 attack hardening: instant global session invalidation + device-aware,
-- rotation-tracked refresh tokens.
--
--   users.token_version — bumped to invalidate EVERY outstanding access token
--   for a user at once (password reset, "sign out everywhere", refresh-token
--   theft). The middleware compares the JWT's `tv` claim to this column. A JWT
--   minted before this column existed carries no `tv` (decodes to 0), which
--   matches the default, so deploying forces no logouts.
--
--   refresh_tokens.rotated_from + device/ip/ua/last_used — each refresh mints a
--   new row pointing at its predecessor (the rotation chain). Presenting an
--   already-rotated/revoked token is a theft signal. The device columns back the
--   sessions/devices UI in P4.
--
-- Idempotent DO-block guard mirrors 000026; RLS already enabled on both tables.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'users' AND column_name = 'token_version'
    ) THEN
        ALTER TABLE users ADD COLUMN token_version INTEGER NOT NULL DEFAULT 0;
    END IF;
END $$;

ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS device_label VARCHAR(255);
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS ip           INET;
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS user_agent   TEXT;
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ;
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS rotated_from UUID REFERENCES refresh_tokens(id) ON DELETE SET NULL;
