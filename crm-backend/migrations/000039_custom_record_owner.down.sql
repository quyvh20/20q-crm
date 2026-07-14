DROP INDEX IF EXISTS idx_custom_object_records_owner;
ALTER TABLE custom_object_records DROP COLUMN IF EXISTS owner_user_id;
