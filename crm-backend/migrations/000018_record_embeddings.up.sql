-- Migration 000018: Generic search index for every object (P6)
-- ============================================================
-- Today only contacts are searchable: they carry a vector(768) embedding column
-- and a fulltext GIN index on the contacts table (000002/000003). Custom objects
-- are invisible to AI and global search. This migration adds ONE generic index
-- table so any object — starting with custom objects — can opt into semantic +
-- fulltext search without a per-object column.
--
-- DESIGN (plan §4.5, §3 "all objects equal above storage"):
--   record_embeddings is keyed by (org_id, object_slug, record_id) — the same
--   cross-stack identifier object_links / object_permissions / field_permissions
--   already use. It stores BOTH the vector (for semantic search) and the text that
--   was embedded (content, which also powers the fulltext GIN index). One row per
--   searchable record; the application maintains it from RecordService's write path
--   via the embedding worker (best-effort, async — mirrors how contacts embed).
--
--   Typed objects keep their own fast path: contacts stay on their native
--   contacts.embedding column + fulltext index (R1 — never touch typed storage
--   before P7). record_embeddings is purely additive and, in P6, holds custom
--   objects. System objects (deal/company) join at the P7 cutover.
--
-- searchable FLAG (deliberate, mirrors the P5a/P5b slug-keying rationale):
--   object_defs already has a `searchable` column (000015) but in P2–P6 that table
--   holds only the three SYSTEM objects. A custom object's definition still lives in
--   custom_object_defs until the P7 backfill, so the opt-in flag for the objects P6
--   actually targets must live there. We add custom_object_defs.searchable here so
--   an admin can mark a custom object searchable today; it converges onto
--   object_defs.searchable at P7 along with the rest of the registry.
--
-- Tenant isolation stays application-enforced (WHERE org_id = ?); RLS is enabled to
-- match the external-access posture of 000008/000013/000015/000016/000017.
--
-- The `vector` extension is created in 000001; this migration assumes it exists.

-- 4.5 record_embeddings (generic semantic + fulltext index) --------------------
CREATE TABLE IF NOT EXISTS record_embeddings (
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    object_slug VARCHAR(100) NOT NULL,
    record_id   UUID NOT NULL,
    embedding   vector(768),            -- NULL until the async embed succeeds; fulltext still works
    content     TEXT,                   -- the text that was embedded (also the fulltext source)
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, object_slug, record_id)
);

-- Fulltext over the embedded content. 'simple' matches the contacts fulltext
-- config (000003) so behaviour is consistent across the two indexes.
CREATE INDEX IF NOT EXISTS idx_record_embeddings_fts
    ON record_embeddings USING GIN (to_tsvector('simple', coalesce(content, '')));

-- An ivfflat/hnsw ANN index on embedding is intentionally deferred until row counts
-- make a sequential scan slow (plan §11) — avoids premature index-build cost.

ALTER TABLE record_embeddings ENABLE ROW LEVEL SECURITY;

-- Opt-in searchability flag for custom objects (see header). Idempotent so the
-- boot guard in main.go and this migration agree.
ALTER TABLE custom_object_defs ADD COLUMN IF NOT EXISTS searchable BOOLEAN NOT NULL DEFAULT FALSE;
