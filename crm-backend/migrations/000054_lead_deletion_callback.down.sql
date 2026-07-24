DROP INDEX IF EXISTS idx_integration_connections_ext_user;
ALTER TABLE integration_connections DROP COLUMN IF EXISTS external_user_id;
