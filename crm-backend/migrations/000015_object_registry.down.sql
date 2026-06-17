-- Rollback Migration 000015: Object Registry
-- ============================================================
-- Drop the deferred FK first to break the object_defs <-> object_fields cycle,
-- then drop the tables (object_fields references object_defs).

ALTER TABLE IF EXISTS object_defs DROP CONSTRAINT IF EXISTS fk_object_defs_display_field;
DROP TABLE IF EXISTS object_fields;
DROP TABLE IF EXISTS object_defs;
