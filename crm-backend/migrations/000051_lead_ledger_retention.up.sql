-- L6.5b: retention for ledger rows no data-subject request can reach.
--
-- Erasure elsewhere in the lead pipeline is CONTACT-KEYED: deleting a contact
-- redacts the rows whose result_record_id points at it. That covers every delivery
-- that became a record and none of the rest -- a failed or quarantined delivery
-- stored the person's payload verbatim and has no contact to key an erasure off.
--
-- redacted_at is the marker for "the personal data on this row has been removed".
-- It exists as a COLUMN rather than being inferred from an empty payload for two
-- reasons: the sweep retains a few non-personal routing keys (so payload shape
-- cannot answer "already done", and a batching loop keyed on shape would never
-- terminate), and an operator has to be able to tell a redacted delivery from one
-- that never stored anything -- otherwise retention is indistinguishable from a bug.
--
-- The twin of this file is the boot-guard block in cmd/server/main.go; golang-migrate
-- does not run on prod, so the guard is the only thing that reaches it and the two
-- must agree. This file must also be added to newIntegrationsTestDB's list in
-- internal/integrations/repository_integration_test.go, or every integration test
-- runs against a schema without the column and passes vacuously.
ALTER TABLE integration_events
    ADD COLUMN IF NOT EXISTS redacted_at TIMESTAMPTZ;

-- Serves the retention sweep, and empties itself as the backlog drains: rows drop
-- out of the index the moment they are redacted. A DISTINCT NAME is load-bearing --
-- CREATE INDEX IF NOT EXISTS matches on name only and never compares columns, so
-- reusing an existing index name is a silent no-op that leaves prod seq-scanning the
-- whole cross-org ledger every sweep.
CREATE INDEX IF NOT EXISTS idx_integration_events_prunable
    ON integration_events(created_at)
    WHERE redacted_at IS NULL;
