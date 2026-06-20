-- Migration 000017: Object-Level Security + audit (P5a)
-- ============================================================
-- Two tables that turn RecordService into the security chokepoint the plan
-- promises (§7.2): per-role access to each object, and an append-only audit of
-- every write.
--
-- KEYING NOTE (deliberate deviation from the plan's literal DDL):
-- The plan sketched object_permissions(role_id, object_def_id). We key on
-- (org_id, role_id, object_slug) instead, because:
--   1. Custom objects are NOT in object_defs yet — they live in
--      custom_object_defs until the P7 cutover (see object_registry_usecase.go).
--      An object_def_id FK could therefore only cover system objects, leaving
--      custom objects unprotectable in P5a — breaking "all objects equal".
--   2. slug is already the cross-stack record identifier used by object_links
--      (from_slug/to_slug) and by the plan's own object_audit (object_slug).
--      Keying permissions by slug keeps the whole P3/P4/P5 surface consistent.
--   3. object_def_id for system objects is org-scoped, so org_id was implied;
--      slug is not, so org_id is carried explicitly to scope the row.
--
-- Tenant isolation stays app-enforced (WHERE org_id = ?); RLS is enabled to
-- match the external-access posture of 000008/000013.

-- Per (role, object) access bits. Absence of a row = no access (default-deny),
-- enforced in RecordService. A row with all-false bits is an explicit denial
-- that survives the idempotent default seed (which only seeds objects that have
-- ZERO rows), so an admin can fully lock an object down.
CREATE TABLE IF NOT EXISTS object_permissions (
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    role_id     UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    object_slug VARCHAR(100) NOT NULL,
    can_read    BOOLEAN NOT NULL DEFAULT FALSE,
    can_create  BOOLEAN NOT NULL DEFAULT FALSE,
    can_edit    BOOLEAN NOT NULL DEFAULT FALSE,
    can_delete  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, role_id, object_slug)
);
-- Fast load of an org's whole permission set (the OLS cache is populated from one
-- query per org).
CREATE INDEX IF NOT EXISTS idx_object_permissions_org ON object_permissions(org_id);
ALTER TABLE object_permissions ENABLE ROW LEVEL SECURITY;

-- Append-only audit of every create/update/delete routed through RecordService.
-- Slug-keyed (matches the plan §4.4) so system and custom objects share one trail.
CREATE TABLE IF NOT EXISTS object_audit (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    object_slug VARCHAR(100) NOT NULL,
    record_id   UUID NOT NULL,
    actor_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    action      VARCHAR(20) NOT NULL,   -- create | update | delete
    changes     JSONB NOT NULL DEFAULT '{}',  -- { field: {old, new} }
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- Per-record history (the audit-view endpoint reads newest-first by this).
CREATE INDEX IF NOT EXISTS idx_object_audit_record
    ON object_audit(org_id, object_slug, record_id, created_at DESC);
ALTER TABLE object_audit ENABLE ROW LEVEL SECURITY;
