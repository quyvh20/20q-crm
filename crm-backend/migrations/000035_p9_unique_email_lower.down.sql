-- Revert the UNIQUE promotion: restore the non-unique LOWER(email) index (as
-- 000034 left it) and drop the unique one.
CREATE INDEX IF NOT EXISTS idx_users_email_lower ON users(LOWER(email));
DROP INDEX IF EXISTS uq_users_email_lower;
