-- Migration 000017b: Field-Level Security (P5b, opt-in)
-- ============================================================
-- One table that lets an admin mark individual fields "sensitive" and control
-- which roles can see/edit them. Enforced server-side inside RecordService —
-- the same chokepoint that already carries OLS + audit (P5a) — so a hidden field
-- is stripped from the JSON *response*, not merely from the UI (plan §7.4).
--
-- OPT-IN, OFF BY DEFAULT (plan P5b / §16 D5):
--   A field with NO rows here is fully accessible (level 'edit') to every role —
--   FLS does nothing and costs nothing until an admin restricts a field. Only the
--   rows that actually downgrade a role to 'read'/'hidden' are stored; setting a
--   field back to 'edit' deletes the row, so the table holds genuine restrictions
--   only and "no rows" stays a meaningful fast-path.
--
-- KEYING NOTE (deliberate deviation from the plan's literal DDL, identical to the
-- decision documented in 000017_object_security.up.sql):
--   The plan sketched field_permissions(role_id, field_id) with field_id → an
--   object_fields(id) FK. We key on (org_id, role_id, object_slug, field_key)
--   instead, because:
--     1. Only SYSTEM-object fields live in object_fields. A custom object's fields
--        still live in custom_object_defs.fields (JSONB) until the P7 cutover, so a
--        field_id FK could only ever protect system-object fields — leaving custom
--        objects unprotectable and breaking "all objects equal".
--     2. (object_slug, field_key) is the cross-stack field identifier already used
--        everywhere above storage: it is exactly the key space of UniformRecord.Fields
--        and of object_audit's per-field diff. Keying FLS the same way keeps the whole
--        P3/P4/P5 surface consistent and lets the mask be applied by a simple map
--        lookup on the record's own field keys.
--     3. slug is not org-scoped on its own (the same custom slug can exist in two
--        orgs), so org_id is carried explicitly to scope the row, mirroring 000017.
--
-- Tenant isolation stays app-enforced (WHERE org_id = ?); RLS is enabled to match
-- the external-access posture of 000008/000013/000017.

CREATE TABLE IF NOT EXISTS field_permissions (
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    role_id     UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    object_slug VARCHAR(100) NOT NULL,
    field_key   VARCHAR(100) NOT NULL,
    -- hidden: stripped from read responses and rejected on write.
    -- read:   visible on read, rejected on write.
    -- edit:   full access (the default; such rows are deleted, not stored).
    level       VARCHAR(10) NOT NULL DEFAULT 'edit',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, role_id, object_slug, field_key),
    CONSTRAINT chk_field_permissions_level CHECK (level IN ('hidden', 'read', 'edit'))
);
-- Fast load of an org's whole field-permission set (the FLS cache is populated
-- from one query per org, alongside the OLS load).
CREATE INDEX IF NOT EXISTS idx_field_permissions_org ON field_permissions(org_id);
ALTER TABLE field_permissions ENABLE ROW LEVEL SECURITY;
