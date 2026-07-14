-- Migration 000042: personal API tokens (U6.5)
-- ============================================================
-- A token is a second way to BE a user, so it authenticates into exactly the same
-- Caller the JWT path builds — same role, same capabilities, same OLS/FLS, same
-- row scope — narrowed further by the token's own scopes. It can never exceed the
-- person who created it.
--
-- token_hash is SHA-256, not bcrypt: this is looked up on every API request, and a
-- 256-bit random secret has no entropy problem that a slow hash would fix. prefix
-- is a display hint so the UI can tell two tokens apart; it is not a credential.
--
-- KEEP IN SYNC with the boot guard in cmd/server/main.go.

CREATE TABLE IF NOT EXISTS api_tokens (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         VARCHAR(120) NOT NULL,
    token_hash   VARCHAR(64) NOT NULL UNIQUE,
    prefix       VARCHAR(24) NOT NULL,
    scopes       JSONB NOT NULL DEFAULT '[]',
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_id, org_id) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash);
ALTER TABLE api_tokens ENABLE ROW LEVEL SECURITY;
