-- 000019 (P7): converge system-object custom field defs onto object_fields.
--
-- Admin-defined ("custom") fields on the system objects (contact/company/deal)
-- previously lived in the org_settings.custom_field_defs JSONB blob. P7 makes
-- object_fields the single field-def store (fixing the lost-update race that
-- rewrote the whole blob array on every edit — symptom #3 / R6). This copies each
-- such field into object_fields with is_system=false, storage_kind='jsonb'.
--
-- Idempotent: the NOT EXISTS guard skips any field already present on the def
-- (previously backfilled, or a native column sharing the key).
--
-- On prod this same backfill runs as a boot guard in main.go
-- (repository.BackfillObjectFieldsFromBlob), because golang-migrate is dead there
-- and the system object_defs are seeded lazily on first read. This file mirrors it
-- for fresh DBs / CI, where it is a harmless no-op until orgs and fields exist.

INSERT INTO object_fields
    (id, org_id, object_def_id, key, label, type, options, target_slug,
     is_required, is_unique, is_system, storage_kind, maps_to_column, position,
     created_at, updated_at)
SELECT uuid_generate_v4(), s.org_id, d.id,
       e.key,
       COALESCE(NULLIF(e.label, ''), e.key),
       COALESCE(NULLIF(e.type, ''), 'text'),
       e.options,
       NULL,
       e.required,
       false, false, 'jsonb', NULL,
       e.position,
       NOW(), NOW()
FROM org_settings s
CROSS JOIN LATERAL jsonb_array_elements(
       CASE WHEN jsonb_typeof(s.custom_field_defs) = 'array'
            THEN s.custom_field_defs ELSE '[]'::jsonb END) AS elem
CROSS JOIN LATERAL (
       SELECT elem->>'key'         AS key,
              elem->>'label'       AS label,
              elem->>'type'        AS type,
              elem->>'entity_type' AS entity_type,
              CASE WHEN jsonb_typeof(elem->'options') = 'array'
                   THEN elem->'options' ELSE '[]'::jsonb END   AS options,
              COALESCE((elem->>'required')::boolean, false)    AS required,
              COALESCE(NULLIF(elem->>'position', '')::int, 0)  AS position
) AS e
JOIN object_defs d
       ON d.org_id = s.org_id
      AND d.slug = e.entity_type
      AND d.is_system = true
      AND d.deleted_at IS NULL
WHERE e.key IS NOT NULL AND e.key <> ''
  AND e.entity_type IS NOT NULL
  AND NOT EXISTS (
        SELECT 1 FROM object_fields f
        WHERE f.object_def_id = d.id
          AND f.key = e.key
          AND f.deleted_at IS NULL);
