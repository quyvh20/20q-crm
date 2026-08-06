ALTER TABLE deals DROP COLUMN IF EXISTS pipeline_id;
ALTER TABLE pipeline_stages DROP COLUMN IF EXISTS pipeline_id;
DROP TABLE IF EXISTS pipelines;
