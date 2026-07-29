-- Reusable builder blocks (Klaviyo-style "saved blocks"): one wire-shape Block
-- per row (columns blocks carry their nested content), org-wide.
CREATE TABLE IF NOT EXISTS marketing_saved_blocks (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name       VARCHAR(120) NOT NULL,
    block      JSONB NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_marketing_saved_blocks_org
    ON marketing_saved_blocks(org_id, created_at DESC);

ALTER TABLE marketing_saved_blocks ENABLE ROW LEVEL SECURITY;
