-- 000021 (P7): converge custom objects onto the registry.
--
-- Custom objects' defs + fields move into object_defs/object_fields so the registry
-- tables are the single store for EVERY object (system and custom). Ids are reused
-- so custom_object_records.object_def_id still resolves; the records FK is repointed
-- to object_defs. custom_object_defs is kept (unused) for rollback safety, like the
-- org_settings blob column. Records themselves stay in custom_object_records.
--
-- On prod this runs as an idempotent boot guard in main.go
-- (repository.ConvergeCustomObjectsToRegistry), since golang-migrate is dead there.
-- All three steps are idempotent; safe to re-run.

-- 1) defs → object_defs (reuse id; skip system-slug collisions + already-converged)
INSERT INTO object_defs
    (id, org_id, slug, label, label_plural, icon, color, is_system, storage,
     record_table, searchable, created_at, updated_at, deleted_at)
SELECT d.id, d.org_id, d.slug, d.label, d.label_plural,
       COALESCE(NULLIF(d.icon, ''), '📦'), '#6B7280', false, 'jsonb', NULL,
       COALESCE(d.searchable, false), d.created_at, d.updated_at, d.deleted_at
FROM custom_object_defs d
WHERE NOT EXISTS (SELECT 1 FROM object_defs o WHERE o.id = d.id)
  AND NOT EXISTS (
        SELECT 1 FROM object_defs o2
        WHERE o2.org_id = d.org_id AND o2.slug = d.slug AND o2.deleted_at IS NULL);

-- 2) fields blob → object_fields
INSERT INTO object_fields
    (id, org_id, object_def_id, key, label, type, options, target_slug,
     is_required, is_unique, is_system, storage_kind, maps_to_column, position,
     created_at, updated_at)
SELECT uuid_generate_v4(), d.org_id, d.id,
       e.key, COALESCE(NULLIF(e.label, ''), e.key), COALESCE(NULLIF(e.type, ''), 'text'),
       e.options, NULL, e.required, false, false, 'jsonb', NULL, e.position, NOW(), NOW()
FROM custom_object_defs d
JOIN object_defs o ON o.id = d.id
CROSS JOIN LATERAL jsonb_array_elements(
       CASE WHEN jsonb_typeof(d.fields) = 'array' THEN d.fields ELSE '[]'::jsonb END) AS elem
CROSS JOIN LATERAL (
       SELECT elem->>'key'   AS key,
              elem->>'label' AS label,
              elem->>'type'  AS type,
              CASE WHEN jsonb_typeof(elem->'options') = 'array'
                   THEN elem->'options' ELSE '[]'::jsonb END   AS options,
              COALESCE((elem->>'required')::boolean, false)    AS required,
              COALESCE(NULLIF(elem->>'position', '')::int, 0)  AS position
) AS e
WHERE e.key IS NOT NULL AND e.key <> ''
  AND NOT EXISTS (
        SELECT 1 FROM object_fields f
        WHERE f.object_def_id = d.id AND f.key = e.key AND f.deleted_at IS NULL);

-- 3) repoint custom_object_records.object_def_id FK → object_defs (dynamic name)
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c FROM pg_constraint
   WHERE conrelid = 'custom_object_records'::regclass AND contype = 'f'
     AND confrelid = 'custom_object_defs'::regclass
   LIMIT 1;
  IF c IS NOT NULL THEN
    EXECUTE 'ALTER TABLE custom_object_records DROP CONSTRAINT ' || quote_ident(c);
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conrelid = 'custom_object_records'::regclass AND contype = 'f'
       AND confrelid = 'object_defs'::regclass
  ) THEN
    ALTER TABLE custom_object_records
      ADD CONSTRAINT custom_object_records_object_def_id_fkey
      FOREIGN KEY (object_def_id) REFERENCES object_defs(id) ON DELETE CASCADE;
  END IF;
END $$;
