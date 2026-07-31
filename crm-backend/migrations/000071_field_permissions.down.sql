-- Down migration for 000071 (renumbered from 000017b): drop Field-Level Security.
DROP TABLE IF EXISTS field_permissions;
