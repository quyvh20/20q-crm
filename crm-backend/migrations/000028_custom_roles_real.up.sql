-- Migration 000028: Make custom roles real (P3)
-- ============================================================
-- Everything authorization-related is keyed by role_id. Three schema changes
-- support that:
--
--   roles.data_scope — generalizes the hardcoded sales_rep='own' row-scope check
--   into data. 'own' = owned + shared records; 'all' = the whole org. Any role
--   (system or custom) can now be own-scoped from a column instead of a string
--   comparison in scopes.go.
--
--   role_permissions.org_id — repurposes the dormant role_permissions table as the
--   SYSTEM CAPABILITY store (plan D5). System-role capability rows have org_id
--   NULL and are seeded idempotently; custom-role rows are org-scoped so they
--   cascade with the org. The legacy per-user capability vocabulary
--   ('deal:read:team', 'all:all:all') was never read — it is deleted here so the
--   seeder can repopulate the table with the real capability codes (§3.4).
--
--   users.role — the last of the three drifting role representations (I4). It was
--   already vestigial: the JWT's active role comes from org_users.role_id → roles,
--   and the SPA reads activeWorkspace.role. Dropped here; the user_role ENUM type
--   is intentionally kept in case another object still references it.
--
-- Idempotent DO-block guards mirror 000027; roles/role_permissions already have
-- RLS posture from 000012.

-- roles.data_scope + its CHECK, added guardedly so a re-run is a no-op.
ALTER TABLE roles ADD COLUMN IF NOT EXISTS data_scope VARCHAR(10) NOT NULL DEFAULT 'all';
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'roles_data_scope_check'
    ) THEN
        ALTER TABLE roles ADD CONSTRAINT roles_data_scope_check CHECK (data_scope IN ('own', 'all'));
    END IF;
END $$;

-- The one system role that was hardcoded to 'own' in scopes.go.
UPDATE roles SET data_scope = 'own' WHERE name = 'sales_rep' AND is_system = TRUE;

-- Capability rows for custom roles are org-scoped (system-role rows stay NULL).
ALTER TABLE role_permissions ADD COLUMN IF NOT EXISTS org_id UUID REFERENCES organizations(id) ON DELETE CASCADE;

-- Purge the dead legacy vocabulary so SeedSystemRoles can repopulate with the
-- real capability codes. The legacy codes are all colon-format ('deal:read:team',
-- 'all:all:all'); real capability codes use dots ('members.manage'), so this
-- targets only the dead rows and never a real capability grant.
DELETE FROM role_permissions WHERE permission_code LIKE '%:%';

-- Retire the vestigial per-user role enum column (keep the user_role type).
ALTER TABLE users DROP COLUMN IF EXISTS role;
