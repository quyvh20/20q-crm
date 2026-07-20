-- Reverse of 000050.
--
-- idx_contacts_fulltext is deliberately NOT dropped here: this migration only
-- restated it, it belongs to 000003, and dropping it would leave contact text
-- search seq-scanning after a routine rollback.
DROP INDEX IF EXISTS idx_companies_fulltext;
