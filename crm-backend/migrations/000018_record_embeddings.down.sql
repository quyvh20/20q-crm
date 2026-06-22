-- Rollback Migration 000018: Generic search index for every object (P6)
-- ============================================================
-- Drop the generic index table and the custom-object searchability flag. The
-- contacts native embedding/fulltext path is untouched (it predates this
-- migration), so rolling back only removes P6's additive search surface.

DROP INDEX IF EXISTS idx_record_embeddings_fts;
DROP TABLE IF EXISTS record_embeddings;
ALTER TABLE custom_object_defs DROP COLUMN IF EXISTS searchable;
