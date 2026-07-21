DROP INDEX IF EXISTS idx_integration_events_prunable;
ALTER TABLE integration_events DROP COLUMN IF EXISTS redacted_at;
