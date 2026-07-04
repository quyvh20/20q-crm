-- Migration 000029: Reports (P9)
-- ============================================================
-- Saved report definitions. The report engine itself is stateless — a report is
-- an object slug + a config JSONB (filters / group_by / aggregate / chart), run
-- fresh on every view so OLS/FLS and data scope always apply to the VIEWER, not
-- the author. visibility: 'private' (creator only) | 'org' (whole workspace).
--
-- NOTE: golang-migrate is dead on prod (dirty at v2), so cmd/server/main.go
-- carries an idempotent boot guard mirroring this file exactly. Keep both in sync.

CREATE TABLE IF NOT EXISTS reports (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name        VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    object_slug VARCHAR(100) NOT NULL,
    config      JSONB NOT NULL DEFAULT '{}',
    visibility  VARCHAR(10) NOT NULL DEFAULT 'private',
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_reports_org ON reports(org_id) WHERE deleted_at IS NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'reports_visibility_check'
    ) THEN
        ALTER TABLE reports ADD CONSTRAINT reports_visibility_check CHECK (visibility IN ('private', 'org'));
    END IF;
END $$;

ALTER TABLE reports ENABLE ROW LEVEL SECURITY;
