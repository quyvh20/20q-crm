-- Rollback Migration 000003
DROP INDEX IF EXISTS idx_contacts_org_email;
DROP INDEX IF EXISTS idx_contacts_fulltext;
DROP INDEX IF EXISTS idx_contacts_owner;
ALTER TABLE contacts DROP COLUMN IF EXISTS owner_user_id;
