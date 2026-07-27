-- M9 Part B: subject-line A/B test config on campaigns. Dev/Docker/fresh-install mirror
-- of the cmd/server/main.go boot guard (prod runs the boot guard — golang-migrate is
-- dead). Every column carries a DDL DEFAULT (GORM omits zero-values on insert).
ALTER TABLE marketing_campaigns
    ADD COLUMN IF NOT EXISTS ab_test_pct          INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS ab_subject_b         VARCHAR(998) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS ab_test_window_hours INT NOT NULL DEFAULT 4,
    ADD COLUMN IF NOT EXISTS ab_winner_variant    VARCHAR(8) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS ab_decided_at        TIMESTAMPTZ;
