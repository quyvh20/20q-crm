-- 000021 (P7) down: repoint the records FK back to custom_object_defs.
--
-- The converged object_defs/object_fields rows are NOT deleted: ids are reused, so a
-- custom object's row is the same identity in both tables, and the app may already be
-- treating object_defs as authoritative. custom_object_defs was never dropped by the
-- up migration, so repointing the FK back fully restores the prior referential shape;
-- rolling back the application code restores the prior read/write path.
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c FROM pg_constraint
   WHERE conrelid = 'custom_object_records'::regclass AND contype = 'f'
     AND confrelid = 'object_defs'::regclass
   LIMIT 1;
  IF c IS NOT NULL THEN
    EXECUTE 'ALTER TABLE custom_object_records DROP CONSTRAINT ' || quote_ident(c);
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conrelid = 'custom_object_records'::regclass AND contype = 'f'
       AND confrelid = 'custom_object_defs'::regclass
  ) THEN
    ALTER TABLE custom_object_records
      ADD CONSTRAINT custom_object_records_object_def_id_fkey
      FOREIGN KEY (object_def_id) REFERENCES custom_object_defs(id) ON DELETE CASCADE;
  END IF;
END $$;
