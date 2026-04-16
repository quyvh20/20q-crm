-- Enable RLS on previously created tables to resolve Supabase security alerts
ALTER TABLE IF EXISTS knowledge_base ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS ai_token_usages ENABLE ROW LEVEL SECURITY;

-- Note: Because all API access routes through a trusted backend
-- using the superuser/service-role connection string (or unrestricted auth), 
-- these tables do not need anonymous RLS policies (e.g. `CREATE POLICY ...`). 
-- Enabling RLS merely closes external direct PostgREST vulnerability windows.
