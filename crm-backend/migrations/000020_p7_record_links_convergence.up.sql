-- 000020 (P7): converge custom-record relationships onto object_links.
--
-- Custom records related to a contact/deal via two hardcoded FK columns
-- (custom_object_records.contact_id / deal_id). P7 makes object_links the single
-- relationship store, so we copy those FKs into 'contact'/'deal' edges and then
-- drop the columns. The edge relation_key matches the target slug, mirroring how
-- the registry models a relation field.
--
-- Idempotent backfill (NOT EXISTS guard). On prod this runs as a boot guard in
-- main.go (repository.BackfillObjectLinksFromRecordFKs), since golang-migrate is
-- dead there; this file mirrors it for fresh DBs / CI. The backfill runs BEFORE the
-- DROP so the SELECTs can still read the columns.

INSERT INTO object_links (id, org_id, from_slug, from_id, to_slug, to_id, relation_key, created_at)
SELECT uuid_generate_v4(), r.org_id, d.slug, r.id, 'contact', r.contact_id, 'contact', NOW()
FROM custom_object_records r
JOIN custom_object_defs d ON d.id = r.object_def_id
WHERE r.contact_id IS NOT NULL AND r.deleted_at IS NULL
  AND NOT EXISTS (
        SELECT 1 FROM object_links l
        WHERE l.org_id = r.org_id AND l.from_slug = d.slug AND l.from_id = r.id
          AND l.relation_key = 'contact' AND l.to_slug = 'contact' AND l.to_id = r.contact_id
          AND l.deleted_at IS NULL);

INSERT INTO object_links (id, org_id, from_slug, from_id, to_slug, to_id, relation_key, created_at)
SELECT uuid_generate_v4(), r.org_id, d.slug, r.id, 'deal', r.deal_id, 'deal', NOW()
FROM custom_object_records r
JOIN custom_object_defs d ON d.id = r.object_def_id
WHERE r.deal_id IS NOT NULL AND r.deleted_at IS NULL
  AND NOT EXISTS (
        SELECT 1 FROM object_links l
        WHERE l.org_id = r.org_id AND l.from_slug = d.slug AND l.from_id = r.id
          AND l.relation_key = 'deal' AND l.to_slug = 'deal' AND l.to_id = r.deal_id
          AND l.deleted_at IS NULL);

ALTER TABLE custom_object_records DROP COLUMN IF EXISTS contact_id;
ALTER TABLE custom_object_records DROP COLUMN IF EXISTS deal_id;
