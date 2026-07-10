-- Migration 000036: In-app notifications (A6)
-- ============================================================
-- A platform table (not automation-owned): produced by automation `notify_user`
-- actions today and consumed app-wide by the header bell / inbox. Addressed to a
-- specific member (user_id). No soft-delete — a stale notification carries no
-- audit value, so a 90-day sweep hard-deletes old rows and the partial unread
-- index stays tight. Delivery is two-legged: the usecase inserts the row AND
-- publishes it on the recipient's per-user SSE channel (sse:<org>:<user>).
--
-- NOTE: golang-migrate is dead on prod, so cmd/server/main.go carries an
-- idempotent boot guard mirroring this file exactly. Keep both in sync.

CREATE TABLE IF NOT EXISTS notifications (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type        VARCHAR(50) NOT NULL DEFAULT 'automation',
    title       VARCHAR(255) NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    link        VARCHAR(1024) NOT NULL DEFAULT '',
    entity_type VARCHAR(64) NOT NULL DEFAULT '',
    entity_id   UUID,
    read_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_notifications_inbox  ON notifications(user_id, org_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_unread ON notifications(user_id, org_id) WHERE read_at IS NULL;

ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
