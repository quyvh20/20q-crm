-- L7.2b: the legacy automation webhook (POST /api/webhooks/inbound/:org_token) gets a
-- lead_sources row so its deliveries reach the ledger, its leads can be routed to an
-- owner, and it raises a health alert like every other channel. Its WRITE and its
-- automation trigger payload are deliberately unchanged.
--
-- The twin of this file is the boot-guard block in cmd/server/main.go; golang-migrate
-- does not run on prod, so the guard is the only thing that reaches it and the two
-- must agree. This file must also be added to newIntegrationsTestDB's list in
-- internal/integrations/repository_integration_test.go, or every integration test runs
-- against a schema without the index and passes vacuously.
--
-- No new columns: `kind` is a plain VARCHAR with no CHECK constraint (a deliberate
-- decision recorded in internal/integrations/models.go), so a new kind is a new VALUE
-- rather than a schema change. The only artifact is this uniqueness guarantee.
--
-- A DISTINCT NAME is load-bearing -- CREATE INDEX IF NOT EXISTS matches on name only
-- and never compares columns.
CREATE UNIQUE INDEX IF NOT EXISTS uix_lead_sources_org_webhook_inbound
    ON lead_sources (org_id) WHERE kind = 'webhook_inbound' AND deleted_at IS NULL;
