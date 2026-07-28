-- Image library for the marketing email builder. Bytes live in Postgres: the
-- public serve route (/api/marketing/asset/:id) feeds recipients' mail clients,
-- so storage must be durable and shared across backend instances.
CREATE TABLE IF NOT EXISTS marketing_assets (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    filename     VARCHAR(255) NOT NULL,
    content_type VARCHAR(100) NOT NULL,
    size_bytes   INT NOT NULL,
    data         BYTEA NOT NULL,
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_marketing_assets_org
    ON marketing_assets(org_id, created_at DESC);

ALTER TABLE marketing_assets ENABLE ROW LEVEL SECURITY;
