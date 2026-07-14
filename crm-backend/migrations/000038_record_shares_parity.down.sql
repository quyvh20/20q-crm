-- Down: back to the pre-U6 user-only record share.
-- grantee_user_id was kept mirrored for exactly this reason. Role- and
-- group-targeted grants have no pre-U6 representation and are dropped.

DELETE FROM record_shares WHERE target_type <> 'user';
UPDATE record_shares SET grantee_user_id = target_id WHERE grantee_user_id IS NULL;
UPDATE record_shares SET permission_level = 'read' WHERE permission_level = 'view';

ALTER TABLE record_shares DROP CONSTRAINT IF EXISTS record_shares_target_type_check;
ALTER TABLE record_shares DROP CONSTRAINT IF EXISTS record_shares_level_check;

DROP INDEX IF EXISTS uq_record_shares_target;
DROP INDEX IF EXISTS idx_record_shares_record;
DROP INDEX IF EXISTS idx_record_shares_target;
DROP INDEX IF EXISTS idx_record_shares_org;

ALTER TABLE record_shares ALTER COLUMN grantee_user_id SET NOT NULL;
ALTER TABLE record_shares DROP COLUMN IF EXISTS target_type;
ALTER TABLE record_shares DROP COLUMN IF EXISTS target_id;
ALTER TABLE record_shares DROP COLUMN IF EXISTS org_id;
