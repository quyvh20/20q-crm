-- P0: append-only auth + admin event log (plan §4.1). One table answers both
-- "who logged in?" and "who changed my access?"; `category` filters between them.
-- Writes are best-effort — a failed log never fails the user action (mirrors
-- object_audit). RLS is enabled with NO policy: tenant isolation stays
-- application-enforced (WHERE org_id = ?), matching every other table here.
CREATE TABLE IF NOT EXISTS auth_events (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id      UUID REFERENCES organizations(id) ON DELETE CASCADE,   -- NULL for pre-org events (e.g. login)
    actor_id    UUID REFERENCES users(id) ON DELETE SET NULL,           -- who did it
    target_id   UUID,                                                   -- who/what it affected
    category    VARCHAR(20)  NOT NULL,   -- 'auth' | 'admin' | 'security'
    event_type  VARCHAR(60)  NOT NULL,   -- 'login.success','password.reset','email.verified', …
    ip          INET,
    user_agent  TEXT,
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_auth_events_org   ON auth_events(org_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_auth_events_actor ON auth_events(actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_auth_events_type  ON auth_events(org_id, event_type, created_at DESC);
ALTER TABLE auth_events ENABLE ROW LEVEL SECURITY;
