-- M3: optional per-org marketing topics. Dev/Docker/fresh-install mirror of the
-- cmd/server/main.go boot guard (prod runs the boot guard — golang-migrate is dead).
-- opt_in_default is immutable after creation (app-enforced). No soft-delete, so the
-- inline UNIQUE(org_id, name) is a correct table constraint.
CREATE TABLE IF NOT EXISTS marketing_topics (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id         UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name           VARCHAR(160) NOT NULL,
    description    VARCHAR(500) NOT NULL DEFAULT '',
    opt_in_default BOOLEAN NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, name)
);

CREATE INDEX IF NOT EXISTS idx_marketing_topics_org ON marketing_topics(org_id);

ALTER TABLE marketing_topics ENABLE ROW LEVEL SECURITY;
