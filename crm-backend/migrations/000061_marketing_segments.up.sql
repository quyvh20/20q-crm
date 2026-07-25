-- M5: audiences — saved segments (dynamic AST or static list) + membership, plus the
-- reverse tag index and a custom_fields GIN for segment filtering. Dev/Docker/fresh-
-- install mirror of the cmd/server/main.go boot guard (prod runs the boot guard —
-- golang-migrate is dead). Every non-zero-default column carries a DDL DEFAULT.
CREATE TABLE IF NOT EXISTS marketing_segments (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name             VARCHAR(160) NOT NULL,
    type             VARCHAR(16) NOT NULL DEFAULT 'dynamic',
    definition       JSONB NOT NULL DEFAULT '{}',
    materialized     BOOLEAN NOT NULL DEFAULT false,
    count_cached     INT NOT NULL DEFAULT 0,
    count_cached_at  TIMESTAMPTZ,
    refreshed_at     TIMESTAMPTZ,
    created_by       UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at       TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_marketing_segments_org
    ON marketing_segments(org_id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS marketing_segment_static_members (
    segment_id UUID NOT NULL REFERENCES marketing_segments(id) ON DELETE CASCADE,
    contact_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    source     VARCHAR(32) NOT NULL DEFAULT '',
    PRIMARY KEY (segment_id, contact_id)
);

CREATE TABLE IF NOT EXISTS marketing_segment_members (
    segment_id UUID NOT NULL REFERENCES marketing_segments(id) ON DELETE CASCADE,
    contact_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    matched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (segment_id, contact_id)
);

-- Reverse tag index (PK is (contact_id, tag_id) — not sargable for a tag->contacts
-- EXISTS) + a GIN on contacts.custom_fields for segment filtering.
CREATE INDEX IF NOT EXISTS idx_contact_tags_tag_contact ON contact_tags(tag_id, contact_id);
CREATE INDEX IF NOT EXISTS idx_contacts_custom_fields_gin ON contacts USING GIN (custom_fields);

ALTER TABLE marketing_segments ENABLE ROW LEVEL SECURITY;
ALTER TABLE marketing_segment_static_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE marketing_segment_members ENABLE ROW LEVEL SECURITY;
