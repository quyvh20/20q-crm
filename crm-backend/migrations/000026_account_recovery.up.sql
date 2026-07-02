-- P1: account recovery + email verification (plan §4.2). Both token tables
-- mirror the proven org_invitations pattern: only a SHA-256 hash of the URL
-- token is stored, tokens are single-use (used_at) and short-TTL (expires_at).
-- RLS enabled with no policy (user-scoped tables; isolation is app-enforced).

-- Add email_verified_at AND grandfather every existing user as verified in ONE
-- guarded step. Gating on the column's absence makes the backfill run EXACTLY
-- once — when the column is first created — so accounts created afterwards keep
-- their unverified state and the soft gate/banner only ever affect new signups.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'users' AND column_name = 'email_verified_at'
    ) THEN
        ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;
        UPDATE users SET email_verified_at = NOW();
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  VARCHAR(255) NOT NULL,   -- SHA-256 of the URL token; raw token never stored
    expires_at  TIMESTAMPTZ NOT NULL,    -- short TTL (~1h)
    used_at     TIMESTAMPTZ,             -- single-use
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_password_reset_token ON password_reset_tokens(token_hash);
CREATE INDEX IF NOT EXISTS idx_password_reset_user  ON password_reset_tokens(user_id);
ALTER TABLE password_reset_tokens ENABLE ROW LEVEL SECURITY;

CREATE TABLE IF NOT EXISTS email_verification_tokens (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  VARCHAR(255) NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,    -- longer TTL (~24h)
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_email_verification_token ON email_verification_tokens(token_hash);
CREATE INDEX IF NOT EXISTS idx_email_verification_user  ON email_verification_tokens(user_id);
ALTER TABLE email_verification_tokens ENABLE ROW LEVEL SECURITY;
