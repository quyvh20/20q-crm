-- Reverse migration 000002: Drop all CRM tables in correct FK order

DROP TABLE IF EXISTS workflow_runs;
DROP TABLE IF EXISTS workflows;
DROP TABLE IF EXISTS org_settings;
DROP TABLE IF EXISTS system_templates;
DROP TABLE IF EXISTS voice_notes;
DROP TABLE IF EXISTS contact_tags;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS activities;
DROP TABLE IF EXISTS deals;
DROP TABLE IF EXISTS pipeline_stages;
DROP TABLE IF EXISTS contacts;
DROP TABLE IF EXISTS companies;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS organizations;

DROP TYPE IF EXISTS activity_type;
DROP TYPE IF EXISTS user_role;
