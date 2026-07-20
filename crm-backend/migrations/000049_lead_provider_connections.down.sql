ALTER TABLE lead_sources DROP COLUMN IF EXISTS connection_id;
DROP TABLE IF EXISTS integration_pending_connections;
DROP TABLE IF EXISTS integration_oauth_states;
DROP TABLE IF EXISTS integration_connections;
