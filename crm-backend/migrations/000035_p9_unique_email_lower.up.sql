-- Authorization cleanup (P10 P9). NOTE: prod schema is maintained by the boot
-- guards in cmd/server/main.go (golang-migrate is dead-dirty on prod); this file
-- exists for fresh-DB / dev parity only and must mirror those guards.

-- Promote the LOWER(email) index (created non-unique in 000034) to UNIQUE, so the
-- DB — not just normalizeEmail — forbids casing-forked accounts. Dup-tolerant:
-- skips the promotion when case-insensitive duplicate emails exist (matching the
-- boot guard's log-and-skip), so this migration never goes dirty on tenant data.
-- The non-unique index is dropped only after the UNIQUE one exists.
DO $$
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = 'uq_users_email_lower' AND i.indisunique
	) AND NOT EXISTS (
		SELECT 1 FROM (SELECT LOWER(email) FROM users GROUP BY LOWER(email) HAVING COUNT(*) > 1) d
	) THEN
		CREATE UNIQUE INDEX IF NOT EXISTS uq_users_email_lower ON users(LOWER(email));
		DROP INDEX IF EXISTS idx_users_email_lower;
	END IF;
END $$;
