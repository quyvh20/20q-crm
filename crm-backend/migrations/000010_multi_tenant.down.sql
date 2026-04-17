DROP INDEX IF EXISTS idx_contacts_owner;
ALTER TABLE contacts DROP COLUMN IF EXISTS owner_user_id;
DROP TABLE IF EXISTS org_users;
ALTER TABLE organizations DROP COLUMN IF EXISTS type;
