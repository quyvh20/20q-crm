-- P8: Per-role detail layouts
--
-- object_layouts holds named, admin-authored detail-page layouts for any object.
-- Multiple layouts may exist per (org, object); the unique partial index ensures
-- at most one is_default per object. The JSONB `layout` column stores an ordered
-- section array:
--   [{ id, label, columns: 1|2, fields: [{ key, width? }] }]
-- Layout is presentation only — FLS (field_permissions) remains the security
-- boundary. Deals are excluded from the admin UI but not enforced here.

CREATE TABLE object_layouts (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    object_slug  VARCHAR(100) NOT NULL,
    name         VARCHAR(255) NOT NULL,
    layout       JSONB NOT NULL DEFAULT '[]',
    is_default   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ
);

-- At most one is_default layout per (org, object) at any time.
CREATE UNIQUE INDEX uix_object_layouts_default
    ON object_layouts(org_id, object_slug)
    WHERE is_default AND deleted_at IS NULL;

CREATE INDEX idx_object_layouts_org_slug
    ON object_layouts(org_id, object_slug)
    WHERE deleted_at IS NULL;

ALTER TABLE object_layouts ENABLE ROW LEVEL SECURITY;

-- object_layout_roles routes one role to exactly one layout per (org, object).
-- object_slug is denormalized from the parent layout so the one-role-one-layout
-- uniqueness constraint is a single index (no cross-table trigger needed).

CREATE TABLE object_layout_roles (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    layout_id    UUID NOT NULL REFERENCES object_layouts(id) ON DELETE CASCADE,
    object_slug  VARCHAR(100) NOT NULL,
    role_id      UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE
);

-- A role resolves to exactly ONE layout per object per org — no ambiguity, no merge.
CREATE UNIQUE INDEX uix_object_layout_roles_one_per_role
    ON object_layout_roles(org_id, object_slug, role_id);

ALTER TABLE object_layout_roles ENABLE ROW LEVEL SECURITY;
