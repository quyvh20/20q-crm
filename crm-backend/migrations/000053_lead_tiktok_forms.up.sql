-- L7.5: TikTok Lead Generation. The tiktok_form twin of uix_lead_sources_conn_form.
--
-- No new columns and no new tables: a source kind is a VALUE in lead_sources.kind,
-- which is a plain VARCHAR with no CHECK constraint by an explicit decision recorded
-- in internal/integrations/models.go. The only artifact a second provider needs is
-- its own partial unique index, because a partial index cannot be parameterised by
-- the kind it filters on.
--
-- The twin of this file is the boot-guard block in cmd/server/main.go; golang-migrate
-- does not run on prod, so the guard is the only thing that reaches it and the two
-- must agree. This file must also be added to newIntegrationsTestDB's list in
-- internal/integrations/repository_integration_test.go, or every integration test runs
-- against a schema without the index and passes vacuously.
--
-- A DISTINCT NAME is load-bearing -- CREATE INDEX IF NOT EXISTS matches on name only
-- and never compares columns.
CREATE UNIQUE INDEX IF NOT EXISTS uix_lead_sources_conn_form_tiktok
    ON lead_sources (connection_id, (config->'tiktok'->>'form_id'))
    WHERE kind = 'tiktok_form' AND deleted_at IS NULL;
