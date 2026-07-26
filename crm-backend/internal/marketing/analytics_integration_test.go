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

// createEventsTable adds a minimal marketing_email_events table to the shared campaign
// test DB (the harness doesn't create it).
func createEventsTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS marketing_email_events (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id UUID NOT NULL,
		svix_id VARCHAR(80) NOT NULL,
		event_type VARCHAR(48) NOT NULL,
		email_normalized VARCHAR(320) NOT NULL DEFAULT '',
		campaign_id UUID,
		bounce_type VARCHAR(24) NOT NULL DEFAULT '',
		status VARCHAR(16) NOT NULL DEFAULT 'pending',
		raw_payload JSONB NOT NULL DEFAULT '{}',
		occurred_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`).Error)
}

// TestCampaignAnalytics_UniqueTotalAndMPP proves the rollup: unique vs total opens,
// per-campaign isolation, and the MPP heuristic (an open within the window of delivery is
// machine; a recipient with a later open is "engaged").
func TestCampaignAnalytics_UniqueTotalAndMPP(t *testing.T) {
	db := newCampaignTestDB(t)
	createEventsTable(t, db)
	repo := marketing.NewRepository(db)
	ctx := context.Background()
	org := uuid.New()
	camp := uuid.New()
	other := uuid.New()

	ins := func(campaignID uuid.UUID, etype, email string, offsetSec int) {
		require.NoError(t, db.Exec(`INSERT INTO marketing_email_events
			(org_id, svix_id, event_type, email_normalized, campaign_id, occurred_at)
			VALUES (?, ?, ?, ?, ?, NOW() + make_interval(secs => ?))`,
			org, uuid.NewString(), etype, email, campaignID, offsetSec).Error)
	}

	// Campaign under test.
	ins(camp, marketing.ResendTypeDelivered, "a@x.com", 0)
	ins(camp, marketing.ResendTypeDelivered, "b@x.com", 0)
	ins(camp, marketing.ResendTypeOpened, "a@x.com", 2)   // within 10s → machine
	ins(camp, marketing.ResendTypeOpened, "a@x.com", 100) // later → a is engaged
	ins(camp, marketing.ResendTypeOpened, "b@x.com", 3)   // within 10s → b only machine
	ins(camp, marketing.ResendTypeClicked, "a@x.com", 120)
	ins(camp, marketing.ResendTypeBounced, "c@x.com", 0)
	// A different campaign — must never bleed into camp's rollup.
	ins(other, marketing.ResendTypeOpened, "z@x.com", 0)

	a, err := repo.CampaignAnalytics(ctx, org, camp)
	require.NoError(t, err)

	assert.Equal(t, 2, a.Delivered, "unique delivered = a,b")
	assert.Equal(t, 2, a.Opened, "unique openers = a,b")
	assert.Equal(t, 3, a.OpenedTotal, "total opens = a(2)+b(1)")
	assert.Equal(t, 1, a.OpenedEngaged, "only a has a non-machine open")
	assert.Equal(t, 1, a.Clicked)
	assert.Equal(t, 1, a.Bounced)
	assert.Equal(t, 0, a.Complained)

	// The other campaign is isolated.
	o, err := repo.CampaignAnalytics(ctx, org, other)
	require.NoError(t, err)
	assert.Equal(t, 1, o.Opened)
	assert.Equal(t, 0, o.Delivered)

	// A different org sees nothing.
	none, err := repo.CampaignAnalytics(ctx, uuid.New(), camp)
	require.NoError(t, err)
	assert.Equal(t, 0, none.Opened)
	assert.Equal(t, 0, none.Delivered)
}
