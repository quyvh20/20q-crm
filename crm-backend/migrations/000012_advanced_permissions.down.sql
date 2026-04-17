DROP TABLE IF EXISTS record_shares;
DROP TABLE IF EXISTS org_invitations;

ALTER TABLE org_users DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE org_users DROP COLUMN IF EXISTS role_id;

DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS roles;
