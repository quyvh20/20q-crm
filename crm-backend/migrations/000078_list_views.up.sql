-- Saved list views: a named, per-user (optionally org-shared) record-list
-- configuration — the same filter AST / q / tags / sort inputs GET
-- /api/registry/objects/:slug/records already accepts, stored verbatim in
-- `definition` and replayed by the frontend. The view grants nothing: every
-- execution re-runs OLS/FLS/row scope as the viewer; the save side is where
-- FLS is enforced (usecase), not here.
--
-- Prod runs the same statements as a boot guard in cmd/server/main.go —
-- golang-migrate is dirty at v2 there and start.sh runs `migrate up || true`,
-- so this file is the local/dev/E2E mirror only. The two must agree statement
-- for statement or CI tests a different schema than production runs.
CREATE TABLE IF NOT EXISTS list_views (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id        UUID NOT NULL,
    owner_user_id UUID NOT NULL,
    object_slug   VARCHAR(100) NOT NULL,
    name          VARCHAR(120) NOT NULL,
    definition    JSONB NOT NULL DEFAULT '{}',
    shared        BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_list_views_org_slug ON list_views(org_id, object_slug) WHERE deleted_at IS NULL;
ALTER TABLE list_views ENABLE ROW LEVEL SECURITY;
