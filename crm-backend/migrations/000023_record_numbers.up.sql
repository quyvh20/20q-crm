-- P-relationships: Human-readable record numbers (e.g. DEAL-0001)
--
-- Records are addressed internally by UUID; this adds a friendly, per-object
-- sequential identifier surfaced in the UI. It works identically for typed
-- (contacts/companies/deals) and JSONB (custom) objects because the number lives
-- in a side table keyed by (org, object_slug, record_id) — RecordService stays the
-- single allocation chokepoint and no per-object table is altered.
--
--   object_number_seqs : per-(org, object) monotonic counter
--   record_numbers     : the seq assigned to each record
--   object_defs.number_prefix : admin-editable label prefix (defaults to UPPER(slug))
--
-- The displayed number is COALESCE(number_prefix, UPPER(slug)) || '-' || LPAD(seq,4,'0').

ALTER TABLE object_defs ADD COLUMN IF NOT EXISTS number_prefix VARCHAR(16);

CREATE TABLE IF NOT EXISTS object_number_seqs (
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    object_slug VARCHAR(100) NOT NULL,
    next_seq    BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, object_slug)
);

CREATE TABLE IF NOT EXISTS record_numbers (
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    object_slug VARCHAR(100) NOT NULL,
    record_id   UUID NOT NULL,
    seq         BIGINT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, object_slug, record_id)
);

CREATE INDEX IF NOT EXISTS idx_record_numbers_org_slug ON record_numbers(org_id, object_slug);

ALTER TABLE object_number_seqs ENABLE ROW LEVEL SECURITY;
ALTER TABLE record_numbers ENABLE ROW LEVEL SECURITY;

-- Backfill: assign a seq to every existing record per (org, object) in created_at
-- order, then seed the counters past the current max. Idempotent via NOT EXISTS.
INSERT INTO record_numbers (org_id, object_slug, record_id, seq)
SELECT t.org_id, 'contact', t.id,
       COALESCE(m.maxseq, 0) + ROW_NUMBER() OVER (PARTITION BY t.org_id ORDER BY t.created_at, t.id)
FROM contacts t
LEFT JOIN (SELECT org_id, MAX(seq) AS maxseq FROM record_numbers WHERE object_slug='contact' GROUP BY org_id) m ON m.org_id = t.org_id
WHERE t.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM record_numbers r WHERE r.org_id=t.org_id AND r.object_slug='contact' AND r.record_id=t.id);

INSERT INTO record_numbers (org_id, object_slug, record_id, seq)
SELECT t.org_id, 'company', t.id,
       COALESCE(m.maxseq, 0) + ROW_NUMBER() OVER (PARTITION BY t.org_id ORDER BY t.created_at, t.id)
FROM companies t
LEFT JOIN (SELECT org_id, MAX(seq) AS maxseq FROM record_numbers WHERE object_slug='company' GROUP BY org_id) m ON m.org_id = t.org_id
WHERE t.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM record_numbers r WHERE r.org_id=t.org_id AND r.object_slug='company' AND r.record_id=t.id);

INSERT INTO record_numbers (org_id, object_slug, record_id, seq)
SELECT t.org_id, 'deal', t.id,
       COALESCE(m.maxseq, 0) + ROW_NUMBER() OVER (PARTITION BY t.org_id ORDER BY t.created_at, t.id)
FROM deals t
LEFT JOIN (SELECT org_id, MAX(seq) AS maxseq FROM record_numbers WHERE object_slug='deal' GROUP BY org_id) m ON m.org_id = t.org_id
WHERE t.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM record_numbers r WHERE r.org_id=t.org_id AND r.object_slug='deal' AND r.record_id=t.id);

INSERT INTO record_numbers (org_id, object_slug, record_id, seq)
SELECT r.org_id, d.slug, r.id,
       COALESCE(m.maxseq, 0) + ROW_NUMBER() OVER (PARTITION BY r.org_id, d.slug ORDER BY r.created_at, r.id)
FROM custom_object_records r
JOIN object_defs d ON d.id = r.object_def_id AND d.is_system = false AND d.deleted_at IS NULL
LEFT JOIN (SELECT org_id, object_slug, MAX(seq) AS maxseq FROM record_numbers GROUP BY org_id, object_slug) m
  ON m.org_id=r.org_id AND m.object_slug=d.slug
WHERE r.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM record_numbers x WHERE x.org_id=r.org_id AND x.object_slug=d.slug AND x.record_id=r.id);

INSERT INTO object_number_seqs (org_id, object_slug, next_seq)
SELECT org_id, object_slug, MAX(seq) + 1 FROM record_numbers GROUP BY org_id, object_slug
ON CONFLICT (org_id, object_slug) DO UPDATE SET next_seq = GREATEST(object_number_seqs.next_seq, EXCLUDED.next_seq);
