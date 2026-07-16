-- Drop integration_events FIRST: it references lead_sources.
DROP TABLE IF EXISTS integration_events;
DROP TABLE IF EXISTS lead_sources;

-- The contacts index lives on a table that survives, so a table DROP won't take it.
DROP INDEX IF EXISTS idx_contacts_org_lower_email;
