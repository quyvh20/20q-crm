-- Migration 000032: Report shares (granular sharing)
-- ============================================================
-- Share a report with a specific user, role, or group, at an access level
-- (view/comment/edit). reports.visibility='org' is retained as the coarse
-- "everyone in the workspace (view)" toggle; these rows layer granular grants
-- on top. Report DATA is still re-filtered per viewer by OLS/FLS; this only
-- governs who can open/comment/edit the report definition.
--
-- NOTE: golang-migrate is dead on prod — cmd/server/main.go carries an
-- idempotent boot guard mirroring this file. Keep both in sync.

CREATE TABLE IF NOT EXISTS report_shares (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    report_id   UUID NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    target_type VARCHAR(10) NOT NULL,   -- 'user' | 'role' | 'group'
    target_id   UUID NOT NULL,          -- user_id | role_id | user_group_id
    level       VARCHAR(10) NOT NULL DEFAULT 'view',  -- 'view' | 'comment' | 'edit'
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uix_report_shares_target
    ON report_shares(report_id, target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_report_shares_report ON report_shares(report_id);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'report_shares_target_type_check') THEN
        ALTER TABLE report_shares ADD CONSTRAINT report_shares_target_type_check CHECK (target_type IN ('user','role','group'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'report_shares_level_check') THEN
        ALTER TABLE report_shares ADD CONSTRAINT report_shares_level_check CHECK (level IN ('view','comment','edit'));
    END IF;
END $$;

ALTER TABLE report_shares ENABLE ROW LEVEL SECURITY;
