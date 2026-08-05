-- Due-task reminder scanner (R8.1): dedupes a reminder to once per calendar
-- day, compared against this column rather than a separate ledger table.
--
-- Prod runs the same statements as boot guards in cmd/server/main.go —
-- golang-migrate is dead there, so this file is the local/dev mirror only.
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS last_reminded_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_tasks_due_reminder ON tasks(due_at) WHERE completed_at IS NULL;
