-- idx_tasks_contact_id / idx_tasks_deal_id are deliberately NOT dropped here.
-- They are plain read-path indexes that predate nothing and break nothing if
-- they outlive this migration, whereas dropping them would silently regress
-- every other query that filters tasks by their linked record.
DROP INDEX IF EXISTS idx_tasks_created_by;
ALTER TABLE tasks DROP COLUMN IF EXISTS created_by;
