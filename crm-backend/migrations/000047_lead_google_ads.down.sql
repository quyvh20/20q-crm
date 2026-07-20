DROP INDEX IF EXISTS idx_lead_sources_public_token;
ALTER TABLE lead_sources DROP COLUMN IF EXISTS public_token;
ALTER TABLE lead_sources DROP COLUMN IF EXISTS google_key_hash;
