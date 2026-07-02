-- Down for 000028. Re-adds users.role nullable (data is not restored — the column
-- was vestigial), drops the added columns and constraint. Capability rows seeded
-- by the app are left in place; they are harmless and re-seed idempotently.

ALTER TABLE users ADD COLUMN IF NOT EXISTS role user_role;

ALTER TABLE role_permissions DROP COLUMN IF EXISTS org_id;

ALTER TABLE roles DROP CONSTRAINT IF EXISTS roles_data_scope_check;
ALTER TABLE roles DROP COLUMN IF EXISTS data_scope;
