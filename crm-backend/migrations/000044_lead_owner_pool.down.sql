ALTER TABLE integration_events DROP COLUMN IF EXISTS assigned_owner_id;
ALTER TABLE lead_sources       DROP COLUMN IF EXISTS owner_cursor;
ALTER TABLE lead_sources       DROP COLUMN IF EXISTS owner_pool;
ALTER TABLE lead_sources       RESET (fillfactor);
