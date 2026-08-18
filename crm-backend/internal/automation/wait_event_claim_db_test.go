package automation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// wait_event_claim_db_test.go exercises ClaimEventWaits against a real Postgres.
// Three properties only SQL can prove: the campaign matching rules, that expired
// waits are left to the clock path, and — the important one — that two
// concurrent webhook drains never claim the same wait, which is what stops one
// engagement event resuming a run twice.

func newWaitRow(t *testing.T, db *gorm.DB, orgID uuid.UUID, contactID uuid.UUID, event string, campaign *uuid.UUID, expires time.Time) EventWait {
	t.Helper()
	w := EventWait{
		OrgID: orgID, RunID: uuid.New(), WorkflowID: uuid.New(),
		StepPath: "0", EventType: event, ContactID: contactID,
		CampaignID: campaign, ExpiresAt: expires,
	}
	require.NoError(t, db.Create(&w).Error)
	return w
}

func TestClaimEventWaits_MatchingRules_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()

	org, contact := uuid.New(), uuid.New()
	campaign, otherCampaign := uuid.New(), uuid.New()
	future := time.Now().Add(time.Hour)

	anyCampaign := newWaitRow(t, db, org, contact, TriggerEmailClicked, nil, future)
	thisCampaign := newWaitRow(t, db, org, contact, TriggerEmailClicked, &campaign, future)
	newWaitRow(t, db, org, contact, TriggerEmailClicked, &otherCampaign, future)            // different campaign
	newWaitRow(t, db, org, contact, TriggerEmailOpened, nil, future)                        // different event
	newWaitRow(t, db, org, uuid.New(), TriggerEmailClicked, nil, future)                    // different contact
	newWaitRow(t, db, uuid.New(), contact, TriggerEmailClicked, nil, future)                // different org
	newWaitRow(t, db, org, contact, TriggerEmailClicked, nil, time.Now().Add(-time.Minute)) // expired

	claimed, err := repo.ClaimEventWaits(ctx, org, TriggerEmailClicked, contact, &campaign)
	require.NoError(t, err)

	ids := map[uuid.UUID]bool{}
	for _, w := range claimed {
		ids[w.ID] = true
	}
	assert.True(t, ids[anyCampaign.ID], "a wait with no campaign matches any campaign")
	assert.True(t, ids[thisCampaign.ID], "a wait pinned to this campaign matches")
	assert.Len(t, claimed, 2, "other campaigns, events, contacts, orgs and expired waits must all be left alone: %+v", claimed)

	// Claiming marks them satisfied, so an immediate replay claims nothing —
	// this is what stops a redelivered webhook resuming the same run twice.
	again, err := repo.ClaimEventWaits(ctx, org, TriggerEmailClicked, contact, &campaign)
	require.NoError(t, err)
	assert.Empty(t, again)
}

func TestClaimEventWaits_ExpiredWaitsBelongToTheClock_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)

	org, contact := uuid.New(), uuid.New()
	newWaitRow(t, db, org, contact, TriggerEmailOpened, nil, time.Now().Add(-time.Second))

	claimed, err := repo.ClaimEventWaits(context.Background(), org, TriggerEmailOpened, contact, nil)
	require.NoError(t, err)
	assert.Empty(t, claimed, "an expired wait is the timeout path's to finish — claiming it here would race that")
}

func TestClaimEventWaits_ConcurrentDrainsNeverDoubleClaim_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)

	org := uuid.New()
	contact := uuid.New()
	const total = 25
	for i := 0; i < total; i++ {
		newWaitRow(t, db, org, contact, TriggerEmailClicked, nil, time.Now().Add(time.Hour))
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[uuid.UUID]int{}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := repo.ClaimEventWaits(context.Background(), org, TriggerEmailClicked, contact, nil)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, w := range claimed {
				seen[w.ID]++
			}
		}()
	}
	wg.Wait()

	for id, n := range seen {
		assert.Equal(t, 1, n, "wait %s was claimed twice — that would resume one run twice for one event", id)
	}
	assert.Len(t, seen, total, "every open wait should be claimed exactly once across both drains")
}

func TestSatisfyWaitingRun_OnlyWakesAParkedRun_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()

	orgID := uuid.New()
	wf := createTestWorkflow(t, db, orgID, 1)

	parked := &WorkflowRun{
		ID: uuid.New(), WorkflowID: wf.ID, WorkflowVersion: 1, OrgID: orgID,
		Status: StatusWaiting, TriggerContext: []byte(`{}`), IdempotencyKey: uuid.NewString(),
	}
	require.NoError(t, db.Create(parked).Error)
	log := &WorkflowActionLog{
		ID: uuid.New(), RunID: parked.ID, ActionPath: "0", ActionType: ActionDelay,
		Status: LogStatusWaiting, Output: []byte(`{"wake_at":"2030-01-01T00:00:00Z"}`), CreatedAt: time.Now(),
	}
	require.NoError(t, db.Create(log).Error)

	woken, err := repo.SatisfyWaitingRun(ctx, parked.ID, "0", time.Now(), map[string]any{"event_satisfied": true})
	require.NoError(t, err)
	assert.True(t, woken)

	var got WorkflowRun
	require.NoError(t, db.First(&got, "id = ?", parked.ID).Error)
	assert.Equal(t, StatusPending, got.Status)
	assert.Nil(t, got.WakeAt, "the deadline is cleared — the event, not the clock, resumed it")

	var gotLog WorkflowActionLog
	require.NoError(t, db.First(&gotLog, "id = ?", log.ID).Error)
	assert.Contains(t, string(gotLog.Output), "event_satisfied",
		"the flag must be durable: the resumed walk reads it to know the event arrived")
	assert.Contains(t, string(gotLog.Output), "wake_at", "merging must not drop the existing output")

	// A run that already left `waiting` (timeout swept it) must not be touched.
	woken, err = repo.SatisfyWaitingRun(ctx, parked.ID, "0", time.Now(), map[string]any{"event_satisfied": true})
	require.NoError(t, err)
	assert.False(t, woken, "a run already woken by its timeout owns its own outcome")
}

func TestPruneCompletedRuns_ReclaimsSpentEventWaits_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)

	org, contact := uuid.New(), uuid.New()
	stale := newWaitRow(t, db, org, contact, TriggerEmailOpened, nil, time.Now().Add(-30*24*time.Hour))
	live := newWaitRow(t, db, org, contact, TriggerEmailClicked, nil, time.Now().Add(time.Hour))
	recentlyExpired := newWaitRow(t, db, org, contact, TriggerEmailOpened, nil, time.Now().Add(-time.Hour))

	spent := newWaitRow(t, db, org, contact, TriggerEmailClicked, nil, time.Now().Add(30*24*time.Hour))
	long := time.Now().Add(-30 * 24 * time.Hour)
	require.NoError(t, db.Model(&EventWait{}).Where("id = ?", spent.ID).Update("satisfied_at", long).Error)

	_, err := repo.PruneCompletedRuns(context.Background(), runPruneRetention)
	require.NoError(t, err)

	alive := func(id uuid.UUID) bool {
		var n int64
		require.NoError(t, db.Model(&EventWait{}).Where("id = ?", id).Count(&n).Error)
		return n == 1
	}
	assert.False(t, alive(stale.ID), "a long-expired wait is dead weight — nothing reads it")
	assert.False(t, alive(spent.ID), "a wait satisfied long ago is equally spent")
	assert.True(t, alive(live.ID), "an open wait must survive — its run is still parked on it")
	assert.True(t, alive(recentlyExpired.ID),
		"just past its deadline is inside the grace window: the run's timeout path may not have swept it yet")
}
