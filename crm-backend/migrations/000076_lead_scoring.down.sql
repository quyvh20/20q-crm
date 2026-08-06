DROP INDEX IF EXISTS idx_marketing_email_events_recipient;
ALTER TABLE organizations DROP COLUMN IF EXISTS lead_score_cursor;
ALTER TABLE organizations DROP COLUMN IF EXISTS lead_score_run_at;
DROP INDEX IF EXISTS idx_contacts_org_lead_score;
ALTER TABLE contacts DROP COLUMN IF EXISTS lead_score_at;
ALTER TABLE contacts DROP COLUMN IF EXISTS lead_score;
DROP TABLE IF EXISTS lead_scoring_rules;
