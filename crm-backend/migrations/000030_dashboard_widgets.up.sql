-- Migration 000030: Dashboard widgets (P9, Phase B)
-- ============================================================
-- Each user pins saved reports to their own dashboard (the app's home page).
-- Per-user rows — no dashboards table; a shared/team dashboard can layer on
-- later without conflicting. The unique index makes pinning idempotent, and
-- report deletion cascades the widget away.
--
-- NOTE: golang-migrate is dead on prod, so cmd/server/main.go carries an
-- idempotent boot guard mirroring this file exactly. Keep both in sync.

CREATE TABLE IF NOT EXISTS dashboard_widgets (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    report_id  UUID NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    position   INT NOT NULL DEFAULT 0,
    size       VARCHAR(10) NOT NULL DEFAULT 'half',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uix_dashboard_widgets_user_report
    ON dashboard_widgets(org_id, user_id, report_id);
CREATE INDEX IF NOT EXISTS idx_dashboard_widgets_user ON dashboard_widgets(org_id, user_id);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'dashboard_widgets_size_check'
    ) THEN
        ALTER TABLE dashboard_widgets ADD CONSTRAINT dashboard_widgets_size_check CHECK (size IN ('half', 'full'));
    END IF;
END $$;

ALTER TABLE dashboard_widgets ENABLE ROW LEVEL SECURITY;
