-- Down migration for 000017b: drop Field-Level Security.
DROP TABLE IF EXISTS field_permissions;
