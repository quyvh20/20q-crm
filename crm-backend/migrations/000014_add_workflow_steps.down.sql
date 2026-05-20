DROP INDEX IF EXISTS idx_wf_action_logs_run_path;
ALTER TABLE automation_workflows DROP COLUMN IF EXISTS steps;
ALTER TABLE automation_workflow_versions DROP COLUMN IF EXISTS steps;
ALTER TABLE automation_workflow_action_logs DROP COLUMN IF EXISTS action_path;
