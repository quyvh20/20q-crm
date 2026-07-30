-- Task creator (U0.1-ext): row scope for GET /api/tasks / Update / Delete needs a
-- creator signal so a rep's own unlinked/unassigned task stays visible to them
-- (assigned_to alone leaves it reachable to nobody). Prod runs the same statement
-- as a boot guard in cmd/server/main.go (golang-migrate is dead on prod).
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS created_by UUID REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_created_by ON tasks(created_by);
