-- Notification preferences (U5): per-member, per-workspace control over how
-- notifications are delivered — a mute-all switch, an email-digest mode, and a
-- sparse jsonb of per-event-type {in_app,email} channel overrides (absent keys use
-- the built-in defaults: in-app on, email off).
--
-- NOTE: golang-migrate is dead on prod, so cmd/server/main.go carries an idempotent
-- boot guard mirroring this file exactly. Keep both in sync.
CREATE TABLE IF NOT EXISTS notification_preferences (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id               UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id              UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mute_all             BOOLEAN NOT NULL DEFAULT FALSE,
    email_digest         VARCHAR(16) NOT NULL DEFAULT 'off',
    overrides            JSONB NOT NULL DEFAULT '{}',
    last_digest_sent_at  TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, user_id)
);

-- Partial index for the daily-digest job's work list (only 'daily' rows are scanned).
CREATE INDEX IF NOT EXISTS idx_notif_prefs_digest ON notification_preferences(email_digest) WHERE email_digest = 'daily';

ALTER TABLE notification_preferences ENABLE ROW LEVEL SECURITY;

-- U5 also extends the A6 notifications table:
--   digest_only = true when a row is stored ONLY to feed the daily digest (the
--                 recipient turned the bell channel off for that type) — the bell
--                 (List/UnreadCount) filters these out.
--   digested_at = set once the digest job has processed the row, so each notification
--                 reaches exactly one digest regardless of the per-run cap or bursts.
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS digest_only BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS digested_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_notifications_digest_pending ON notifications(user_id, org_id) WHERE read_at IS NULL AND digested_at IS NULL;
