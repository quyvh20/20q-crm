-- Migration 000038: record-sharing parity (U6.2)
-- ============================================================
-- record_shares (000012) predates report_shares (000032) and never caught up: it
-- had a single grantee_user_id, no org_id, no uniqueness, no CHECKs, and a
-- permission_level of 'read'|'edit' that could never be changed after the first
-- grant. This brings it to the report_shares model — target_type/target_id over
-- user|role|group, level from the shared ladder (view|edit) — so one predicate can
-- serve both and a record share stops being a decorative row.
--
-- KEEP IN SYNC with the boot guard in cmd/server/main.go (golang-migrate is dead
-- on prod; the guard is what actually runs there).

ALTER TABLE record_shares ADD COLUMN IF NOT EXISTS org_id UUID;
ALTER TABLE record_shares ADD COLUMN IF NOT EXISTS target_type VARCHAR(10) NOT NULL DEFAULT 'user';
ALTER TABLE record_shares ADD COLUMN IF NOT EXISTS target_id UUID;

-- Backfill the target from the legacy grantee column (every pre-U6 row is a user
-- grant by construction).
UPDATE record_shares SET target_id = grantee_user_id WHERE target_id IS NULL AND grantee_user_id IS NOT NULL;

-- Backfill org_id from the shared record itself (the table never carried one).
UPDATE record_shares rs SET org_id = c.org_id
  FROM contacts c WHERE rs.org_id IS NULL AND rs.record_type = 'contact' AND rs.record_id = c.id;
UPDATE record_shares rs SET org_id = d.org_id
  FROM deals d WHERE rs.org_id IS NULL AND rs.record_type = 'deal' AND rs.record_id = d.id;
UPDATE record_shares rs SET org_id = cor.org_id
  FROM custom_object_records cor WHERE rs.org_id IS NULL AND rs.record_id = cor.id;

-- Orphans: the shared record was hard-deleted, so the grant names nothing. A NULL
-- org_id would defeat every org-scoped query below, so drop them rather than keep
-- an unfilterable row.
DELETE FROM record_shares WHERE org_id IS NULL OR target_id IS NULL;

-- Level vocabulary: 'read' → 'view' (share.go). Safe: no SQL predicate anywhere
-- compares 'read' — only 'edit' is ever compared — so no query changes meaning.
UPDATE record_shares SET permission_level = 'view' WHERE permission_level = 'read';
UPDATE record_shares SET permission_level = 'view' WHERE permission_level NOT IN ('view', 'edit');

-- The table has never had a uniqueness constraint, so duplicates may exist; they
-- must go before the unique index can be created. Keep the newest row per target.
DELETE FROM record_shares a USING record_shares b
 WHERE a.ctid < b.ctid
   AND a.record_type = b.record_type
   AND a.record_id = b.record_id
   AND a.target_type = b.target_type
   AND a.target_id = b.target_id;

ALTER TABLE record_shares ALTER COLUMN target_id SET NOT NULL;
ALTER TABLE record_shares ALTER COLUMN org_id SET NOT NULL;
-- grantee_user_id becomes a mirrored legacy column (NULL for role/group grants).
-- Kept one release so a rollback still reads correct data; dropped afterwards.
ALTER TABLE record_shares ALTER COLUMN grantee_user_id DROP NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_record_shares_target
  ON record_shares(record_type, record_id, target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_record_shares_record ON record_shares(record_type, record_id);
CREATE INDEX IF NOT EXISTS idx_record_shares_target ON record_shares(target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_record_shares_org ON record_shares(org_id);

DO $$
BEGIN
	IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'record_shares_target_type_check') THEN
		ALTER TABLE record_shares ADD CONSTRAINT record_shares_target_type_check CHECK (target_type IN ('user', 'role', 'group'));
	END IF;
	IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'record_shares_level_check') THEN
		ALTER TABLE record_shares ADD CONSTRAINT record_shares_level_check CHECK (permission_level IN ('view', 'edit'));
	END IF;
END $$;

-- The group-share and team-scope predicates both hit user_group_members by
-- group_id; only the by-user index exists today.
CREATE INDEX IF NOT EXISTS idx_user_group_members_group ON user_group_members(group_id);

ALTER TABLE record_shares ENABLE ROW LEVEL SECURITY;
