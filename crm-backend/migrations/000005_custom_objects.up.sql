-- Migration 000005: Custom Objects
-- ============================================================

-- Custom Object Definitions (schema per org)
CREATE TABLE custom_object_defs (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    slug          VARCHAR(100) NOT NULL,
    label         VARCHAR(255) NOT NULL,
    label_plural  VARCHAR(255) NOT NULL,
    icon          VARCHAR(50) DEFAULT '📦',
    fields        JSONB DEFAULT '[]',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uix_custom_object_defs_org_slug
ON custom_object_defs(org_id, slug) WHERE deleted_at IS NULL;
CREATE INDEX idx_custom_object_defs_org ON custom_object_defs(org_id);

-- Custom Object Records (data rows)
CREATE TABLE custom_object_records (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    object_def_id UUID NOT NULL REFERENCES custom_object_defs(id) ON DELETE CASCADE,
    display_name  VARCHAR(500) NOT NULL DEFAULT '',
    data          JSONB DEFAULT '{}',
    contact_id    UUID REFERENCES contacts(id) ON DELETE SET NULL,
    deal_id       UUID REFERENCES deals(id) ON DELETE SET NULL,
    created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);
CREATE INDEX idx_cor_org_def ON custom_object_records(org_id, object_def_id);
CREATE INDEX idx_cor_contact ON custom_object_records(contact_id) WHERE contact_id IS NOT NULL;
CREATE INDEX idx_cor_deal    ON custom_object_records(deal_id) WHERE deal_id IS NOT NULL;
