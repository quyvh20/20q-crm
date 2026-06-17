-- Migration 000015: Object Registry (read-only foundation, P2)
-- ============================================================
-- Introduces the registry tables that describe every object the same way above
-- storage. In P2 these hold ONLY the three system objects (contact/deal/company),
-- seeded per org by the application's idempotent EnsureSystemObjects routine.
-- Custom objects continue to live in custom_object_defs and are merged into the
-- registry view at read time; they are not duplicated here until P7.
--
-- Tenant isolation stays application-enforced (WHERE org_id = ?); RLS is enabled
-- on every new table to match the external-access posture of 000008/000013.

-- 4.1 object_defs (registry) ---------------------------------------------------
CREATE TABLE IF NOT EXISTS object_defs (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    slug             VARCHAR(100) NOT NULL,
    label            VARCHAR(255) NOT NULL,
    label_plural     VARCHAR(255) NOT NULL,
    icon             VARCHAR(50)  DEFAULT '📦',
    color            VARCHAR(20)  DEFAULT '#6B7280',
    is_system        BOOLEAN NOT NULL DEFAULT FALSE,   -- contact/deal/company
    storage          VARCHAR(10) NOT NULL DEFAULT 'jsonb', -- 'table' | 'jsonb'
    record_table     VARCHAR(63),                      -- 'contacts'… for system, else NULL
    display_field_id UUID,                             -- FK added below (deferred)
    searchable       BOOLEAN NOT NULL DEFAULT FALSE,   -- opt-in embeddings/fulltext (P6)
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at       TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uix_object_defs_org_slug
    ON object_defs(org_id, slug) WHERE deleted_at IS NULL;
ALTER TABLE object_defs ENABLE ROW LEVEL SECURITY;

-- 4.2 object_fields ------------------------------------------------------------
CREATE TABLE IF NOT EXISTS object_fields (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id         UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    object_def_id  UUID NOT NULL REFERENCES object_defs(id) ON DELETE CASCADE,
    key            VARCHAR(100) NOT NULL,
    label          VARCHAR(255) NOT NULL,
    type           VARCHAR(30)  NOT NULL,   -- text|number|date|select|boolean|url|relation
    options        JSONB DEFAULT '[]',      -- for select
    target_slug    VARCHAR(100),            -- for type='relation' → object_defs.slug
    is_required    BOOLEAN NOT NULL DEFAULT FALSE,
    is_unique      BOOLEAN NOT NULL DEFAULT FALSE,
    is_system      BOOLEAN NOT NULL DEFAULT FALSE,  -- native column, label-editable only
    storage_kind   VARCHAR(10) NOT NULL DEFAULT 'jsonb', -- 'column' | 'jsonb'
    maps_to_column VARCHAR(63),             -- when storage_kind='column'
    position       INT NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at     TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uix_object_fields_def_key
    ON object_fields(object_def_id, key) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_object_fields_def
    ON object_fields(object_def_id) WHERE deleted_at IS NULL;
ALTER TABLE object_fields ENABLE ROW LEVEL SECURITY;

-- Deferred FK now that object_fields exists. Guarded so a re-run is a no-op
-- (Postgres has no ADD CONSTRAINT IF NOT EXISTS).
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'fk_object_defs_display_field'
    ) THEN
        ALTER TABLE object_defs
            ADD CONSTRAINT fk_object_defs_display_field
            FOREIGN KEY (display_field_id) REFERENCES object_fields(id) ON DELETE SET NULL;
    END IF;
END $$;
