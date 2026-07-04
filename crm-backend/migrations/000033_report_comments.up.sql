-- Migration 000033: Report comments
-- ============================================================
-- A comment thread on a saved report. Anyone who can SEE a report may read its
-- comments; posting requires effective level >= 'comment'; deleting a comment
-- requires being its author or holding 'manage' on the report. Authorization is
-- resolved by the report usecase (ResolveAccess) — this table only stores the
-- thread. author_id is nullable (ON DELETE SET NULL) so a removed user's
-- comments survive as authored by "(removed)".
--
-- NOTE: golang-migrate is dead on prod — cmd/server/main.go carries an
-- idempotent boot guard mirroring this file. Keep both in sync.

CREATE TABLE IF NOT EXISTS report_comments (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    report_id  UUID NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    author_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    body       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_report_comments_report ON report_comments(report_id) WHERE deleted_at IS NULL;
ALTER TABLE report_comments ENABLE ROW LEVEL SECURITY;
