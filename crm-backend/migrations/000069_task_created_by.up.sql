-- Task creator (U0.1-ext): row scope for GET /api/tasks / Update / Delete needs a
-- creator signal so a rep's own unlinked/unassigned task stays visible to them
-- (assigned_to alone leaves it reachable to nobody). Prod runs the same statements
-- as a boot guard in cmd/server/main.go (golang-migrate is dead on prod).
--
-- Numbered 000069, not 000043 as originally written: this migration was authored
-- on a branch cut before the lead-integration arc, which has since taken 43.
-- golang-migrate rejects a duplicate version while enumerating the directory,
-- which aborts the whole `up` run rather than just the offending pair.
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS created_by UUID REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_created_by ON tasks(created_by);

-- taskScope reaches a caller's tasks through correlated EXISTS subqueries on the
-- linked contact/deal; tasks only had (org_id, assigned_to) indexed.
CREATE INDEX IF NOT EXISTS idx_tasks_contact_id ON tasks(contact_id);
CREATE INDEX IF NOT EXISTS idx_tasks_deal_id ON tasks(deal_id);

-- Backfill: without it every pre-existing row is created_by = NULL, and a task
-- with no assignee and no reachable contact/deal goes dark (404 on read AND
-- write) for every own/team-scoped user the instant the scope lands. assigned_to
-- is the only creator evidence the old schema kept.
UPDATE tasks SET created_by = assigned_to WHERE created_by IS NULL AND assigned_to IS NOT NULL;
