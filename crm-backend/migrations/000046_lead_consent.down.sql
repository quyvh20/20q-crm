DROP INDEX IF EXISTS idx_integration_events_result;
ALTER TABLE integration_events DROP COLUMN IF EXISTS consent;
