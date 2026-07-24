-- L5.4 Facebook Data Deletion Callback: the app-scoped provider user id captured at
-- connect, so a signed_request can find and tear down the connections that user made.
-- Mirrored by an idempotent boot guard in cmd/server/main.go (prod runs the guard,
-- since golang-migrate is dead there; a fresh install and the Docker harness run this
-- file). Both must agree. The column is UNMAPPED on the Go model, so nothing on the
-- connect or webhook path SELECTs it.
ALTER TABLE integration_connections
    ADD COLUMN IF NOT EXISTS external_user_id VARCHAR(64);

CREATE INDEX IF NOT EXISTS idx_integration_connections_ext_user
    ON integration_connections(provider, external_user_id)
    WHERE external_user_id IS NOT NULL;
