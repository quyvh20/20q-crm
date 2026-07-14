DROP INDEX IF EXISTS idx_notifications_digest_pending;
ALTER TABLE notifications DROP COLUMN IF EXISTS digested_at;
ALTER TABLE notifications DROP COLUMN IF EXISTS digest_only;
DROP TABLE IF EXISTS notification_preferences;
