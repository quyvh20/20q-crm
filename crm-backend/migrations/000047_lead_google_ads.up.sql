-- L3: Google Ads lead-form webhooks (plan lead_integration_plan.md).
--
-- NOTE: prod never runs this file — golang-migrate is dirty at v2 there, so the
-- boot guards in cmd/server/main.go are the only path that executes on prod.
-- This file exists so a FRESH install and the Docker test harness converge on
-- the same schema. Keep the two in lockstep.

-- public_token: the webhook URL's source identifier. Not a secret (google_key is
-- the credential) but random, and unique so a URL resolves to at most one source.
-- Partial index: every non-google_ads row stays NULL.
ALTER TABLE lead_sources ADD COLUMN IF NOT EXISTS public_token VARCHAR(64);
CREATE UNIQUE INDEX IF NOT EXISTS idx_lead_sources_public_token
    ON lead_sources(public_token) WHERE public_token IS NOT NULL;

-- google_key_hash: SHA-256 (hex) of the key Google posts inside the webhook body.
-- Plaintext is never stored; shown once at mint, rotatable.
ALTER TABLE lead_sources ADD COLUMN IF NOT EXISTS google_key_hash VARCHAR(64);
