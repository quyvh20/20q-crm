-- Migration 000039: custom-object ownership (U6.3)
-- ============================================================
-- custom_object_records had no owner, so a custom object could not be private:
-- row scope ('own'/'team') had nothing to filter on, assignment was impossible,
-- and record_shares rows written against a custom slug were never read by any
-- authorization path. This gives custom records the same owner_user_id anchor
-- contacts and deals have had all along.
--
-- KEEP IN SYNC with the boot guard in cmd/server/main.go.

ALTER TABLE custom_object_records ADD COLUMN IF NOT EXISTS owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_custom_object_records_owner ON custom_object_records(owner_user_id);

-- One-time backfill: the creator becomes the owner. Records with no created_by
-- stay unowned, which is reachable only by an 'all'-scoped role — fail closed.
--
-- Release note: this GRANTS visibility to creators who, under a row-scoped role,
-- previously saw every custom record (because nothing was filtered) and would now
-- otherwise see none. It is the intended landing state, not a widening.
UPDATE custom_object_records SET owner_user_id = created_by
 WHERE owner_user_id IS NULL AND created_by IS NOT NULL;
