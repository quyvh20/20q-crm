DROP INDEX IF EXISTS idx_tasks_due_reminder;
ALTER TABLE tasks DROP COLUMN IF EXISTS last_reminded_at;
