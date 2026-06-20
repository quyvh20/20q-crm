-- Down migration 000017: drop Object-Level Security + audit (P5a)
DROP TABLE IF EXISTS object_audit;
DROP TABLE IF EXISTS object_permissions;
