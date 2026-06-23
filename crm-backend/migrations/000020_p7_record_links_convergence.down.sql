-- 000020 (P7) down: re-add the dropped FK columns (schema only).
--
-- The contact/deal edges copied into object_links are not removed here — they are
-- indistinguishable from relationships created directly via the …/links API, so
-- deleting them would risk dropping legitimate edges. The original FK *values* are
-- not restored (the up migration is a one-way data move); this just restores the
-- columns so the schema round-trips for tests/rollback.
ALTER TABLE custom_object_records ADD COLUMN IF NOT EXISTS contact_id UUID;
ALTER TABLE custom_object_records ADD COLUMN IF NOT EXISTS deal_id UUID;
