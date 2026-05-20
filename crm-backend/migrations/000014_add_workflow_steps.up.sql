ALTER TABLE automation_workflows ADD COLUMN steps JSONB;
ALTER TABLE automation_workflow_versions ADD COLUMN steps JSONB;
ALTER TABLE automation_workflow_action_logs ADD COLUMN action_path VARCHAR(255);
CREATE INDEX IF NOT EXISTS idx_wf_action_logs_run_path ON automation_workflow_action_logs (run_id, action_path);
