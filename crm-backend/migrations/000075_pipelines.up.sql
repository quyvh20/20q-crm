-- Multiple pipelines (R9.3): a pipelines table, plus pipeline_id on stages and deals.
--
-- Before this an org had exactly ONE pipeline and it was implicit — the org's
-- entire pipeline_stages row set, ordered by position. This makes the board
-- explicit so an org can run more than one sales motion.
--
-- Prod runs the same statements as boot guards in cmd/server/main.go —
-- golang-migrate is dirty at v2 there and start.sh runs `migrate up || true`, so
-- this file is the local/dev/E2E mirror only. The two must agree statement for
-- statement or CI tests a different schema than production runs.
--
-- pipeline_id is NULLABLE here and stays that way for this deploy: making it NOT
-- NULL while old pods are still inserting stages without the column would break
-- org registration mid-rollout. Tightening is a separate, count-gated deploy.
CREATE TABLE IF NOT EXISTS pipelines (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name       VARCHAR(100) NOT NULL,
    position   INT NOT NULL DEFAULT 0,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_pipelines_org ON pipelines(org_id) WHERE deleted_at IS NULL;

-- Exactly one default pipeline per org, enforced at the database rather than in
-- application code: two instances booting together would both pass an app-level
-- check and both insert.
CREATE UNIQUE INDEX IF NOT EXISTS uq_pipelines_org_default
    ON pipelines(org_id) WHERE is_default AND deleted_at IS NULL;

ALTER TABLE pipeline_stages ADD COLUMN IF NOT EXISTS pipeline_id UUID REFERENCES pipelines(id);
CREATE INDEX IF NOT EXISTS idx_pipeline_stages_pipeline ON pipeline_stages(pipeline_id);

ALTER TABLE deals ADD COLUMN IF NOT EXISTS pipeline_id UUID REFERENCES pipelines(id);
CREATE INDEX IF NOT EXISTS idx_deals_pipeline ON deals(pipeline_id);

ALTER TABLE pipelines ENABLE ROW LEVEL SECURITY;
