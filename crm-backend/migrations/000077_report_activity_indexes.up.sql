-- R9.5: tasks and activities become reportable.
--
-- Read-side only — no new tables, no new columns. Mirrors the boot guard in
-- cmd/server/main.go statement for statement (prod runs the guard; this file
-- covers a fresh install and the E2E job).
--
-- Tasks already carry every index the report predicate needs
-- (idx_tasks_assigned_to from 000002; idx_tasks_created_by, idx_tasks_contact_id
-- and idx_tasks_deal_id from 000069), so only activities need anything.

-- The first arm of ActivityAccessPredicate, and the GROUP BY of the
-- "Activities per rep" template.
CREATE INDEX IF NOT EXISTS idx_activities_user_id ON activities(user_id);

-- Every activity report is org-scoped and date-bounded. idx_activities_org_id
-- alone turns that into a full org scan plus a sort.
CREATE INDEX IF NOT EXISTS idx_activities_org_occurred ON activities(org_id, occurred_at);
