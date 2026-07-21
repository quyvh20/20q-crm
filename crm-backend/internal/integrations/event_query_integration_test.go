package integrations

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The filtered ledger query and the requeue CAS only mean anything against real
// Postgres: the keyset page uses a row-value comparison, and the requeue's whole
// safety argument is that its guards ride in the WHERE rather than in Go.

func seedEvent(t *testing.T, db *gorm.DB, orgID uuid.UUID, mut func(*IntegrationEvent)) *IntegrationEvent {
	t.Helper()
	e := &IntegrationEvent{
		ID:         uuid.New(),
		OrgID:      orgID,
		Status:     EventStatusProcessed,
		RawPayload: []byte(`{}`),
		Context:    []byte(`{}`),
		CreatedAt:  time.Now(),
	}
	mut(e)
	require.NoError(t, db.Create(e).Error)
	return e
}

func TestListEventsFiltered_ScopesAndFilters(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()

	orgA, orgB := seedOrg(t, db), seedOrg(t, db)
	srcA := seedSource(t, repo, orgA)
	connID := uuid.New()

	failedWithSource := seedEvent(t, db, orgA, func(e *IntegrationEvent) {
		e.Status = EventStatusFailed
		e.SourceID = &srcA.ID
	})
	// A provider delivery that failed BEFORE ingest could stamp a source. This is the
	// row the per-source route structurally cannot show, and the reason this query
	// exists at all.
	preIngest := seedEvent(t, db, orgA, func(e *IntegrationEvent) {
		e.Status = EventStatusQuarantined
		e.ConnectionID = &connID
	})
	rec := uuid.New()
	written := seedEvent(t, db, orgA, func(e *IntegrationEvent) {
		e.Status = EventStatusProcessed
		e.SourceID = &srcA.ID
		e.ResultRecordID = &rec
	})
	// Another tenant's row, same shape.
	seedEvent(t, db, orgB, func(e *IntegrationEvent) { e.Status = EventStatusFailed })

	all, err := repo.ListEventsFiltered(ctx, orgA, EventFilter{Limit: 50})
	require.NoError(t, err)
	require.Len(t, all, 3, "org scope: another tenant's deliveries are never returned")

	byStatus, err := repo.ListEventsFiltered(ctx, orgA, EventFilter{
		Limit: 50, Statuses: []string{EventStatusFailed, EventStatusQuarantined},
	})
	require.NoError(t, err)
	require.Len(t, byStatus, 2)

	byConn, err := repo.ListEventsFiltered(ctx, orgA, EventFilter{Limit: 50, ConnectionID: &connID})
	require.NoError(t, err)
	require.Len(t, byConn, 1)
	require.Equal(t, preIngest.ID, byConn[0].ID,
		"a connection filter is the only way to reach a delivery that never got a source")

	bySource, err := repo.ListEventsFiltered(ctx, orgA, EventFilter{Limit: 50, SourceID: &srcA.ID})
	require.NoError(t, err)
	require.Len(t, bySource, 2)

	unresolved, err := repo.ListEventsFiltered(ctx, orgA, EventFilter{Limit: 50, Unresolved: true})
	require.NoError(t, err)
	require.Len(t, unresolved, 2, "'made no record' is the question the log exists to answer")
	for _, e := range unresolved {
		require.NotEqual(t, written.ID, e.ID)
	}
	_ = failedWithSource
}

// A soft-deleted source's ledger must stay readable. The soft delete exists so the
// history survives, but the per-source route resolves through loadSource and 404s it,
// which left the rows alive and unreachable.
func TestListEventsFiltered_ReachesASoftDeletedSourcesLedger(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()

	orgID := seedOrg(t, db)
	src := seedSource(t, repo, orgID)
	seedEvent(t, db, orgID, func(e *IntegrationEvent) { e.SourceID = &src.ID })
	require.NoError(t, repo.SoftDeleteSource(ctx, orgID, src.ID))

	rows, err := repo.ListEventsFiltered(ctx, orgID, EventFilter{Limit: 50, SourceID: &src.ID})
	require.NoError(t, err)
	require.Len(t, rows, 1, "deleting a source must not hide the history the soft delete preserves")

	labels, err := repo.SourceLabels(ctx, orgID, []uuid.UUID{src.ID})
	require.NoError(t, err)
	require.True(t, labels[src.ID].Deleted, "the row must be labelled, not shown as a bare uuid")
	require.Equal(t, src.Name, labels[src.ID].Name)
}

// Keyset paging must not skip or repeat a row when timestamps tie — deliveries
// arrive in bursts, so ties are routine rather than theoretical.
func TestListEventsFiltered_KeysetPagesWithoutGapsOnTiedTimestamps(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)

	shared := time.Now().Truncate(time.Millisecond)
	want := map[uuid.UUID]bool{}
	for i := 0; i < 7; i++ {
		e := seedEvent(t, db, orgID, func(e *IntegrationEvent) { e.CreatedAt = shared })
		want[e.ID] = true
	}

	seen := map[uuid.UUID]bool{}
	var cursorAt *time.Time
	var cursorID *uuid.UUID
	for page := 0; page < 10; page++ {
		f := EventFilter{Limit: 3, CursorAt: cursorAt, CursorID: cursorID}
		rows, err := repo.ListEventsFiltered(ctx, orgID, f)
		require.NoError(t, err)
		if len(rows) == 0 {
			break
		}
		keep := rows
		if len(rows) > f.Limit {
			keep = rows[:f.Limit]
		}
		for _, r := range keep {
			require.False(t, seen[r.ID], "a row was returned on two pages")
			seen[r.ID] = true
		}
		if len(rows) <= f.Limit {
			break
		}
		last := keep[len(keep)-1]
		at, id := last.CreatedAt, last.ID
		cursorAt, cursorID = &at, &id
	}
	require.Equal(t, len(want), len(seen), "every row must appear on exactly one page")
}

// The requeue CAS is the whole safety story for retry: every guard rides in the
// WHERE, so a row that changed underneath simply matches nothing.
func TestRequeueEventForRetry_GuardsRideInTheStatement(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()

	orgA, orgB := seedOrg(t, db), seedOrg(t, db)
	conn := uuid.New()
	rec := uuid.New()

	newRow := func(mut func(*IntegrationEvent)) *IntegrationEvent {
		return seedEvent(t, db, orgA, func(e *IntegrationEvent) {
			e.Status = EventStatusQuarantined
			e.ConnectionID = &conn
			mut(e)
		})
	}

	t.Run("a retryable row is requeued exactly once", func(t *testing.T) {
		ev := newRow(func(*IntegrationEvent) {})
		ok, err := repo.RequeueEventForRetry(ctx, orgA, ev.ID)
		require.NoError(t, err)
		require.True(t, ok)

		var got IntegrationEvent
		require.NoError(t, db.First(&got, "id = ?", ev.ID).Error)
		require.Equal(t, EventStatusPending, got.Status)
		require.Nil(t, got.ClaimedAt)
		// The reason the row was quarantined is the only record of WHY, and an admin
		// who retries and fails again needs it more than they need a blank field.
		require.Equal(t, ev.Error, got.Error)

		// Second call finds it `pending`, which the guard excludes.
		ok, err = repo.RequeueEventForRetry(ctx, orgA, ev.ID)
		require.NoError(t, err)
		require.False(t, ok, "a double click must not re-queue an already-queued delivery")
	})

	t.Run("a row that already wrote a record is refused", func(t *testing.T) {
		ev := newRow(func(e *IntegrationEvent) { e.ResultRecordID = &rec })
		ok, err := repo.RequeueEventForRetry(ctx, orgA, ev.ID)
		require.NoError(t, err)
		require.False(t, ok, "re-running a delivery that produced a record writes the lead twice")
	})

	t.Run("a sync row is refused", func(t *testing.T) {
		ev := newRow(func(e *IntegrationEvent) { e.ConnectionID = nil })
		ok, err := repo.RequeueEventForRetry(ctx, orgA, ev.ID)
		require.NoError(t, err)
		require.False(t, ok, "a pending row with no connection is failed by the worker on its next tick")
	})

	t.Run("an in-flight row is refused", func(t *testing.T) {
		for _, st := range []string{EventStatusProcessing, EventStatusPending, EventStatusProcessed} {
			ev := newRow(func(e *IntegrationEvent) { e.Status = st })
			ok, err := repo.RequeueEventForRetry(ctx, orgA, ev.ID)
			require.NoError(t, err)
			require.False(t, ok, "status %s", st)
		}
	})

	t.Run("another org cannot requeue this row", func(t *testing.T) {
		ev := newRow(func(*IntegrationEvent) {})
		ok, err := repo.RequeueEventForRetry(ctx, orgB, ev.ID)
		require.NoError(t, err)
		require.False(t, ok, "without the org predicate this is a cross-tenant write primitive")

		var got IntegrationEvent
		require.NoError(t, db.First(&got, "id = ?", ev.ID).Error)
		require.Equal(t, EventStatusQuarantined, got.Status)
	})
}

// RependEvent gained an org predicate because L6.2 made it reachable from a request.
func TestRependEvent_IsOrgScoped(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()

	orgA, orgB := seedOrg(t, db), seedOrg(t, db)
	ev := seedEvent(t, db, orgA, func(e *IntegrationEvent) { e.Status = EventStatusProcessing })

	require.NoError(t, repo.RependEvent(ctx, orgB, ev.ID, "nope"))
	var got IntegrationEvent
	require.NoError(t, db.First(&got, "id = ?", ev.ID).Error)
	require.Equal(t, EventStatusProcessing, got.Status, "another org must not move this row")

	require.NoError(t, repo.RependEvent(ctx, orgA, ev.ID, "will retry"))
	require.NoError(t, db.First(&got, "id = ?", ev.ID).Error)
	require.Equal(t, EventStatusPending, got.Status)
}
