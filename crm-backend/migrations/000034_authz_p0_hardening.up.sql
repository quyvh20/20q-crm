-- Authorization hardening (P10 P0). NOTE: prod schema is maintained by the boot
-- guards in cmd/server/main.go (golang-migrate is dead-dirty on prod); this file
-- exists for fresh-DB / dev parity only and must mirror those guards.

-- roles: is_owner is the authoritative god-mode flag (enforcement reads it via
-- domain.IsOwnerRole, never the name string). description / template_key /
-- seeded_from_role_id are P6 admin-UX metadata whose DDL lands early.
ALTER TABLE roles ADD COLUMN IF NOT EXISTS is_owner BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE roles ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
ALTER TABLE roles ADD COLUMN IF NOT EXISTS template_key VARCHAR(40);
ALTER TABLE roles ADD COLUMN IF NOT EXISTS seeded_from_role_id UUID REFERENCES roles(id) ON DELETE SET NULL;
UPDATE roles SET is_owner = TRUE WHERE name = 'owner' AND is_system = TRUE AND is_owner = FALSE;
UPDATE roles SET template_key = name WHERE is_system = TRUE AND template_key IS NULL;

-- Uniqueness: permission caches are keyed by role name until the P5 id re-key,
-- so duplicate names would silently merge grants.
CREATE UNIQUE INDEX IF NOT EXISTS uq_roles_global_name ON roles(name) WHERE org_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_roles_org_name ON roles(org_id, name) WHERE org_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_roles_one_owner ON roles(org_id) WHERE is_owner AND org_id IS NOT NULL;

DO $$
BEGIN
	IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'roles_no_owner_shadow') THEN
		ALTER TABLE roles ADD CONSTRAINT roles_no_owner_shadow CHECK (is_system OR name <> 'owner');
	END IF;
	IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'roles_owner_lineage') THEN
		ALTER TABLE roles ADD CONSTRAINT roles_owner_lineage CHECK (NOT is_owner OR template_key = 'owner');
	END IF;
END $$;

-- users: server-side default workspace (R2) + case-insensitive email lookup
-- support (P2 cutover).
ALTER TABLE users ADD COLUMN IF NOT EXISTS default_org_id UUID REFERENCES organizations(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_users_email_lower ON users(LOWER(email));

-- password_reset_tokens: who triggered an admin-sent reset link (P2).
ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS initiated_by UUID REFERENCES users(id) ON DELETE SET NULL;

-- org_invitations: resend / revoke lifecycle stamps (P2).
ALTER TABLE org_invitations ADD COLUMN IF NOT EXISTS resent_at TIMESTAMPTZ;
ALTER TABLE org_invitations ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;
