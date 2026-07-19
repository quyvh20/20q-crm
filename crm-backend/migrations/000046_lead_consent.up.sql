-- L2 item 7: the consent envelope — the verbatim record of what a data subject was
-- shown, stored on the delivery that carried it.
--
-- Fresh installs only; prod gets this through the boot guard in cmd/server/main.go
-- (golang-migrate is dirty at v2). The two are twins and must agree.

-- Its OWN column, not folded into `context`. Two reasons:
--   1. `context` is caller-supplied and already mutated by the batch path, so a
--      caller posting context:{consent:{…}} would be byte-indistinguishable from a
--      stamped record — destroying the provenance that is this column's only value.
--   2. Erasure redacts raw_payload, context and consent separately.
--
-- NULLABLE with NO default, unlike its NOT NULL DEFAULT '{}' siblings (whose
-- defaults exist so GORM cannot write a nil datatypes.JSON — nothing maps this one).
-- That keeps three states distinguishable: NULL = none was ever sent, an object =
-- one was, and a tombstone = one was sent and has since been erased. Writing '{}' or
-- NULL on erasure would make the ledger falsely assert no consent was ever obtained.
--
-- No index on this column, ever: an index is a second copy of the PII to chase.
ALTER TABLE integration_events ADD COLUMN IF NOT EXISTS consent JSONB;

-- Erasure is contact-keyed, and nothing indexed result_record_id (000043 covers
-- source/created, org/created and the pending poll only) — so the redaction someone
-- runs against a legal deadline would sequential-scan the whole ledger.
CREATE INDEX IF NOT EXISTS idx_integration_events_result
    ON integration_events(org_id, result_record_id)
    WHERE result_record_id IS NOT NULL;
