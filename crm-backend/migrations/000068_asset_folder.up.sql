-- Flat folders for media-library assets ("" = unfiled), matching template
-- folders: no folder table, folders exist implicitly as the distinct values.
ALTER TABLE marketing_assets
    ADD COLUMN IF NOT EXISTS folder VARCHAR(80) NOT NULL DEFAULT '';
