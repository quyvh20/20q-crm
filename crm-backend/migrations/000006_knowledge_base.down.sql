ALTER TABLE system_templates DROP COLUMN IF EXISTS kb_templates;
DROP INDEX IF EXISTS idx_kb_org;
DROP TABLE IF EXISTS knowledge_base;
