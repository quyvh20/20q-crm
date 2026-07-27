package marketing_test

import (
	"context"
	"testing"

	"crm-backend/internal/marketing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// addABColumns brings the shared harness marketing_campaigns table up to the A/B schema.
func addABColumns(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`ALTER TABLE marketing_campaigns
		ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ,
		ADD COLUMN IF NOT EXISTS ab_test_pct INT NOT NULL DEFAULT 0,
		ADD COLUMN IF NOT EXISTS ab_subject_b VARCHAR(998) NOT NULL DEFAULT '',
		ADD COLUMN IF NOT EXISTS ab_test_window_hours INT NOT NULL DEFAULT 4,
		ADD COLUMN IF NOT EXISTS ab_winner_variant VARCHAR(8) NOT NULL DEFAULT '',
		ADD COLUMN IF NOT EXISTS ab_decided_at TIMESTAMPTZ`).Error)
}

// TestAB_AssignMetricsDecideRelease exercises the whole A/B SQL loop against real
// Postgres: split test cells + hold remainder, per-variant metrics, decider claim, the
// single-winner guard, and winner release.
func TestAB_AssignMetricsDecideRelease(t *testing.T) {
	db := newCampaignTestDB(t)
	addABColumns(t, db)
	createEventsTable(t, db)
	repo := marketing.NewRepository(db)
	ctx := context.Background()
	org := uuid.New()
	camp := uuid.New()

	// A/B campaign, sending, window already elapsed.
	require.NoError(t, db.Exec(`INSERT INTO marketing_campaigns (id, org_id, name, status, ab_test_pct, ab_subject_b, ab_test_window_hours, started_at)
		VALUES (?, ?, 'AB', 'sending', 20, 'Subject B', 1, NOW() - INTERVAL '2 hours')`, camp, org).Error)

	// 100 pending recipients.
	for i := 0; i < 100; i++ {
		require.NoError(t, db.Exec(`INSERT INTO marketing_campaign_recipients (campaign_id, org_id, email_normalized, status)
			VALUES (?, ?, ?, 'pending')`, camp, org, uuid.NewString()+"@x.com").Error)
	}

	// Split ~20% into A/B cells; hold the rest.
	assigned, err := repo.AssignABTestCells(ctx, camp, 20)
	require.NoError(t, err)
	assert.Equal(t, 20, assigned, "20% of 100 assigned to test cells")

	count := func(sql string, args ...any) int64 {
		var n int64
		require.NoError(t, db.Raw(sql, args...).Scan(&n).Error)
		return n
	}
	assert.Equal(t, int64(10), count(`SELECT count(*) FROM marketing_campaign_recipients WHERE campaign_id = ? AND variant = 'A'`, camp))
	assert.Equal(t, int64(10), count(`SELECT count(*) FROM marketing_campaign_recipients WHERE campaign_id = ? AND variant = 'B'`, camp))
	assert.Equal(t, int64(80), count(`SELECT count(*) FROM marketing_campaign_recipients WHERE campaign_id = ? AND variant IS NULL AND scheduled_for > NOW()`, camp),
		"the remainder is held far in the future")

	// Mark the test cells sent, then give variant A more opens than B.
	require.NoError(t, db.Exec(`UPDATE marketing_campaign_recipients SET status='sent' WHERE campaign_id = ? AND variant IS NOT NULL`, camp).Error)
	openEvents := func(variant string, n int) {
		var emails []string
		require.NoError(t, db.Raw(`SELECT email_normalized FROM marketing_campaign_recipients WHERE campaign_id = ? AND variant = ? ORDER BY id LIMIT ?`, camp, variant, n).Scan(&emails).Error)
		for _, e := range emails {
			require.NoError(t, db.Exec(`INSERT INTO marketing_email_events (org_id, svix_id, event_type, email_normalized, campaign_id, occurred_at)
				VALUES (?, ?, ?, ?, ?, NOW())`, org, uuid.NewString(), marketing.ResendTypeOpened, e, camp).Error)
		}
	}
	openEvents("A", 8) // A: 8/10 opened
	openEvents("B", 3) // B: 3/10 opened

	metrics, err := repo.ABVariantMetrics(ctx, camp)
	require.NoError(t, err)
	assert.Equal(t, 10, metrics["A"].Sent)
	assert.Equal(t, 8, metrics["A"].Opened)
	assert.Equal(t, 10, metrics["B"].Sent)
	assert.Equal(t, 3, metrics["B"].Opened)

	// The decider claims this campaign (window elapsed, undecided).
	claimed, err := repo.ClaimUndecidedABCampaigns(ctx)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	assert.Equal(t, camp, claimed[0].ID)

	// Atomic decide+release: first call marks the winner AND releases the held remainder;
	// a second call is a no-op (single-winner guard).
	marked, released, err := repo.DecideABWinner(ctx, camp, "A")
	require.NoError(t, err)
	assert.True(t, marked)
	assert.Equal(t, int64(80), released, "the held remainder is released to the winner in the same tx")
	assert.Equal(t, int64(80), count(`SELECT count(*) FROM marketing_campaign_recipients WHERE campaign_id = ? AND variant = 'A' AND status = 'pending' AND scheduled_for <= NOW()`, camp),
		"held remainder now carries the winner and is due to send")

	markedAgain, releasedAgain, err := repo.DecideABWinner(ctx, camp, "B")
	require.NoError(t, err)
	assert.False(t, markedAgain, "a second decide is a no-op (winner already set)")
	assert.Equal(t, int64(0), releasedAgain)

	// A decided campaign no longer appears in the claim.
	claimed2, err := repo.ClaimUndecidedABCampaigns(ctx)
	require.NoError(t, err)
	assert.Len(t, claimed2, 0)
}
