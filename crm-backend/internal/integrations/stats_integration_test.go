package integrations

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A sparkline that omits quiet days compresses time and makes an outage look like
// normal spacing, so the query fills gaps with zeroes rather than returning fewer rows.
func TestDailyIngestCounts_FillsQuietDaysWithZeroes(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgID := seedOrg(t, db)
	src := seedSource(t, repo, orgID)

	rows, err := repo.DailyIngestCounts(context.Background(), orgID, src.ID, 7)
	require.NoError(t, err)
	require.Len(t, rows, 7, "every day in the window is a point, even with no deliveries")
	for _, r := range rows {
		require.Zero(t, r.Written)
		require.Zero(t, r.Failed)
	}
}

func TestDailyIngestCounts_SplitsOutcomesAndExcludesTestFromWritten(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgID := seedOrg(t, db)
	src := seedSource(t, repo, orgID)

	today := time.Now().UTC()
	mk := func(status, outcome string) {
		e := &IntegrationEvent{
			ID: uuid.New(), OrgID: orgID, SourceID: &src.ID, Status: status,
			Outcome: outcome, RawPayload: []byte(`{}`), Context: []byte(`{}`), CreatedAt: today,
		}
		require.NoError(t, db.Create(e).Error)
	}
	mk(EventStatusProcessed, OutcomeCreated)
	mk(EventStatusProcessed, OutcomeUpdated)
	mk(EventStatusFailed, "")
	mk(EventStatusQuarantined, "")
	// A test delivery carries outcome=created too. It is split OUT of `written` here —
	// a chart gates nothing, so the display can be honest about which leads were real.
	mk(EventStatusTest, OutcomeCreated)

	rows, err := repo.DailyIngestCounts(context.Background(), orgID, src.ID, 3)
	require.NoError(t, err)
	last := rows[len(rows)-1]
	require.Equal(t, int64(2), last.Written, "test deliveries are not real leads")
	require.Equal(t, int64(1), last.Failed)
	require.Equal(t, int64(1), last.Skipped)
}

// Display-only, and it must never become a limit: the daily CAP counts test rows on
// purpose, because google_ads accepts a caller-supplied is_test and excluding it there
// would be cap-free record creation.
func TestDailyIngestCounts_DoesNotChangeTheCapCount(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)
	src := seedSource(t, repo, orgID)

	e := &IntegrationEvent{
		ID: uuid.New(), OrgID: orgID, SourceID: &src.ID, Status: EventStatusTest,
		Outcome: OutcomeCreated, RawPayload: []byte(`{}`), Context: []byte(`{}`), CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, db.Create(e).Error)

	capped, err := repo.CountCreatedToday(ctx, src.ID, time.Now())
	require.NoError(t, err)
	require.Equal(t, int64(1), capped,
		"the cap still counts a test create — excluding it would hand a forged is_test cap-free creation")

	rows, err := repo.DailyIngestCounts(ctx, orgID, src.ID, 1)
	require.NoError(t, err)
	require.Equal(t, int64(0), rows[0].Written, "the chart may split it out; the cap may not")
}

func TestDailyIngestCounts_IsOrgAndSourceScoped(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgA, orgB := seedOrg(t, db), seedOrg(t, db)
	a := seedSource(t, repo, orgA)
	b := seedSource(t, repo, orgB)

	for _, s := range []struct {
		org uuid.UUID
		src uuid.UUID
	}{{orgA, a.ID}, {orgB, b.ID}} {
		e := &IntegrationEvent{
			ID: uuid.New(), OrgID: s.org, SourceID: &s.src, Status: EventStatusProcessed,
			Outcome: OutcomeCreated, RawPayload: []byte(`{}`), Context: []byte(`{}`), CreatedAt: time.Now().UTC(),
		}
		require.NoError(t, db.Create(e).Error)
	}

	rows, err := repo.DailyIngestCounts(context.Background(), orgA, a.ID, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), rows[0].Written, "another workspace's deliveries must never be counted here")
}
