-- Migration 000041: two-factor authentication (U6.4)
-- ============================================================
-- TOTP + single-use backup codes, plus a workspace policy that can require it.
--
-- totp_secret holds the AES-GCM-ENCRYPTED seed, not the raw one. A password hash
-- still costs an attacker a crack; a leaked TOTP seed is a working second factor
-- forever, silently. The key lives in the process environment (TOTP_ENC_KEY, or
-- derived from JWT_SECRET), so a stolen database dump alone is not enough.
--
-- totp_enabled_at is what makes 2FA ACTIVE. A secret can exist without it (setup
-- started, never confirmed) — enrollment only completes when a valid code proves
-- the authenticator actually works, so a scan that never registered can't lock
-- anyone out.
--
-- KEEP IN SYNC with the boot guard in cmd/server/main.go.

ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled_at TIMESTAMPTZ;
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS require_two_factor BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS two_factor_backup_codes (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  VARCHAR(255) NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_2fa_backup_user ON two_factor_backup_codes(user_id) WHERE used_at IS NULL;
ALTER TABLE two_factor_backup_codes ENABLE ROW LEVEL SECURITY;

-- The half-authenticated state between "password correct" and "code correct".
-- Opaque token stored hashed (never a JWT — a JWT would sail through the auth
-- middleware and BE the session it exists to gate), short TTL, single use, and an
-- attempt counter so a 6-digit code has a bound that holds even when the per-IP
-- rate limiter can't (it fails open with no Redis).
CREATE TABLE IF NOT EXISTS two_factor_challenges (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash VARCHAR(255) NOT NULL UNIQUE,
    attempts   INT NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_2fa_challenge_expiry ON two_factor_challenges(expires_at);
ALTER TABLE two_factor_challenges ENABLE ROW LEVEL SECURITY;
