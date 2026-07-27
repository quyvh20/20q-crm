ALTER TABLE marketing_campaigns
    DROP COLUMN IF EXISTS ab_test_pct,
    DROP COLUMN IF EXISTS ab_subject_b,
    DROP COLUMN IF EXISTS ab_test_window_hours,
    DROP COLUMN IF EXISTS ab_winner_variant,
    DROP COLUMN IF EXISTS ab_decided_at;
