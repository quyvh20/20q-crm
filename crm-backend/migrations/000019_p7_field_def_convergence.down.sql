-- 000019 (P7) down: the blob → object_fields backfill is a one-way data migration.
--
-- Backfilled rows are indistinguishable from custom fields created directly via the
-- unified field editor, so deleting them here would risk dropping legitimately
-- created fields. The up migration leaves org_settings.custom_field_defs intact, so
-- rolling back the *application code* restores the previous read/write path without
-- data loss. There is no schema change to revert.
SELECT 1;
