-- Flat folders for email templates ("" = unfiled; folders exist implicitly as
-- the distinct set of values).
ALTER TABLE marketing_campaign_content
    ADD COLUMN IF NOT EXISTS folder VARCHAR(80) NOT NULL DEFAULT '';
