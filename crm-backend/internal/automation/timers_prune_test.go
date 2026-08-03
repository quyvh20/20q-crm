package automation

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// timers_prune_test.go covers Repository.PruneFiredTimers (timers.go:143), the hourly
// destructive job wired at scheduler.go:287 with a bare `7*24*time.Hour` literal. It
// HARD-DELETEs rows from automation_timers across the whole fleet — no org scope, no
// dry run, no env knob — and until this file it had zero tests.
//
// What these tests pin, in order of what would hurt most if it broke:
//   - only status='fired' rows are eligible (a pending occurrence is work still owed);
//   - the pruner is fleet-wide and uniform (both a stray org filter and a missing one
//     would change which rows die);
//   - the delete is HARD, not soft — see TestPruneFiredTimers_IsAHardDelete.

// mkTimer inserts one automation_timers row with an explicit status / fire time and,
// for fired rows, an explicit fired_at. Each row gets its own workflow_id so the unique
// (workflow_id, dedupe_key) index can never collide between fixtures.
func mkTimer(t *testing.T, db *gorm.DB, orgID uuid.UUID, status string, fireAt time.Time, firedAt *time.Time) uuid.UUID {
	t.Helper()
	tm := &AutomationTimer{
		ID:         uuid.New(),
		WorkflowID: uuid.New(),
		OrgID:      orgID,
		Kind:       TimerKindSchedule,
		Status:     status,
		FireAt:     fireAt,
		DedupeKey:  scheduleDedupeKey(fireAt),
		FiredAt:    firedAt,
	}
	require.NoError(t, db.Create(tm).Error)
	return tm.ID
}

// timerExists asks the table directly, with raw SQL, whether a row is still there.
// Raw SQL (not a GORM model query) on purpose — see TestPruneFiredTimers_IsAHardDelete.
func timerExists(t *testing.T, db *gorm.DB, id uuid.UUID) bool {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM automation_timers WHERE id = ?`, id).Scan(&n).Error)
	return n > 0
}

func ptime(v time.Time) *time.Time { return &v }

// TestPruneFiredTimers_DeletesOnlyLongFiredTimers is the core contract: fired rows past
// the window go, everything else stays — and it happens FLEET-WIDE.
//
// The two-org fixture is the point of this test, not decoration. PruneFiredTimers takes
// no orgID and is called once per scheduler tick for the whole deployment, so orgB's
// long-fired timer must die on the same call as orgA's. A stray `org_id = ?` added to
// the predicate would leave orgB's row behind (the table quietly resumes growing for
// every org but one), and a predicate that dropped the status check would eat the
// pending occurrence. Both regressions fail here.
func TestPruneFiredTimers_DeletesOnlyLongFiredTimers(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgA, orgB := uuid.New(), uuid.New()
	now := time.Now()

	firedOldA := mkTimer(t, db, orgA, timerStatusFired, now.Add(-10*24*time.Hour), ptime(now.Add(-10*24*time.Hour)))
	firedRecentA := mkTimer(t, db, orgA, timerStatusFired, now.Add(-24*time.Hour), ptime(now.Add(-24*time.Hour)))
	pendingA := mkTimer(t, db, orgA, timerStatusPending, now.Add(-400*24*time.Hour), nil)
	// A cancelled row carrying an ancient fired_at is a deliberately hostile fixture: it
	// satisfies the timestamp half of the predicate and is excluded ONLY by the status
	// check, so a pruner that forgot `status = 'fired'` deletes it and fails here.
	cancelledA := mkTimer(t, db, orgA, "cancelled", now.Add(-400*24*time.Hour), ptime(now.Add(-400*24*time.Hour)))
	firedOldB := mkTimer(t, db, orgB, timerStatusFired, now.Add(-10*24*time.Hour), ptime(now.Add(-10*24*time.Hour)))

	require.NoError(t, repo.PruneFiredTimers(ctx, 7*24*time.Hour))

	assert.False(t, timerExists(t, db, firedOldA), "orgA's 10-day-old fired timer must be pruned")
	assert.False(t, timerExists(t, db, firedOldB), "orgB's 10-day-old fired timer must be pruned by the SAME fleet-wide call")
	assert.True(t, timerExists(t, db, firedRecentA), "a timer fired 1 day ago is inside the 7-day window")
	assert.True(t, timerExists(t, db, pendingA), "a pending occurrence is work still owed — never pruned, at any age")
	assert.True(t, timerExists(t, db, cancelledA), "a cancelled row is not 'fired' — the status check must exclude it")

	var total int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM automation_timers`).Scan(&total).Error)
	assert.Equal(t, int64(3), total, "exactly the two long-fired rows were removed")
}

// TestPruneFiredTimers_BoundaryRowSurvives pins the direction of the comparison at
// `fired_at < cutoff`: a row sitting just inside the window survives, one just outside
// dies.
//
// Why a ±1s band rather than an exact tie: PruneFiredTimers derives its cutoff from its
// OWN time.Now() (timers.go:144) with no injectable clock, so the instant the fixture
// reads and the instant the pruner reads differ by however long the two UPDATEs below
// take. Stamping fired_at from a reference captured immediately before the call keeps
// that drift in the low milliseconds; 1 second of guard band on either side makes the
// assertion deterministic while still resolving the boundary to a second. A `>` or a
// day/hour unit slip fails this test; a `<=`-vs-`<` tie is not observable from here.
func TestPruneFiredTimers_BoundaryRowSurvives(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	org := uuid.New()
	const retention = 7 * 24 * time.Hour

	edge := mkTimer(t, db, org, timerStatusFired, time.Now().Add(-retention), ptime(time.Now()))
	older := mkTimer(t, db, org, timerStatusFired, time.Now().Add(-retention-time.Hour), ptime(time.Now()))

	ref := time.Now()
	require.NoError(t, db.Exec(`UPDATE automation_timers SET fired_at = ? WHERE id = ?`,
		ref.Add(-retention).Add(time.Second), edge).Error)
	require.NoError(t, db.Exec(`UPDATE automation_timers SET fired_at = ? WHERE id = ?`,
		ref.Add(-retention).Add(-time.Second), older).Error)

	require.NoError(t, repo.PruneFiredTimers(ctx, retention))

	assert.True(t, timerExists(t, db, edge), "a timer fired just INSIDE the retention window must survive")
	assert.False(t, timerExists(t, db, older), "a timer fired just outside it must be pruned")
}

// TestPruneFiredTimers_NeverDeletesAPendingOccurrence isolates the failure mode with the
// worst blast radius. A pending timer whose fire_at is 400 days in the past is not
// garbage — it is a scheduled automation that is overdue (a long outage, a paused
// scanner, a workflow armed and then never scanned). DueTimers (timers.go:123) will still
// claim and fire it. If the pruner ever keyed off fire_at instead of fired_at, or dropped
// the status predicate, the fleet would silently lose overdue work with nothing logged.
func TestPruneFiredTimers_NeverDeletesAPendingOccurrence(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	org := uuid.New()

	overdue := mkTimer(t, db, org, timerStatusPending, time.Now().Add(-400*24*time.Hour), nil)

	require.NoError(t, repo.PruneFiredTimers(ctx, 7*24*time.Hour))
	require.True(t, timerExists(t, db, overdue), "a due-but-unfired timer is owed work, not garbage")

	// And it is still claimable afterwards — the point of keeping it.
	due, err := repo.DueTimers(ctx, 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, overdue, due[0].ID)
}

// TestPruneFiredTimers_IsAHardDelete is a tripwire, not a behaviour test.
//
// AutomationTimer (models.go:124) has NO gorm.DeletedAt field today, so
// `Delete(&AutomationTimer{})` emits a real DELETE. The moment anyone adds a DeletedAt to
// that struct, AutoMigrate adds the column and the exact same pruner line silently becomes
// an UPDATE ... SET deleted_at — the table keeps growing forever, every existing
// GORM-based assertion in this file still passes (GORM hides soft-deleted rows), and
// nothing anywhere fails. So this test asserts against the raw table on both counts:
// the row is physically gone, and the column that would flip the semantics does not exist.
func TestPruneFiredTimers_IsAHardDelete(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()

	id := mkTimer(t, db, uuid.New(), timerStatusFired,
		time.Now().Add(-30*24*time.Hour), ptime(time.Now().Add(-30*24*time.Hour)))

	require.NoError(t, repo.PruneFiredTimers(ctx, 7*24*time.Hour))

	var raw int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM automation_timers WHERE id = ?`, id).Scan(&raw).Error)
	assert.Equal(t, int64(0), raw, "the row must be physically gone from the table, not hidden by a GORM scope")

	var softCol int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'automation_timers' AND column_name = 'deleted_at'`).Scan(&softCol).Error)
	assert.Equal(t, int64(0), softCol,
		"automation_timers gained a deleted_at column: PruneFiredTimers is now a SOFT delete and the table "+
			"will grow without bound. Either drop the column or rewrite the pruner to Unscoped().")
}

// TestPruneFiredTimers_ZeroRetentionDeletesEverythingFired pins what a zero window means:
// cutoff == now, so a timer fired one second ago is deleted.
//
// This is documentation of a sharp edge, not an endorsement. The only caller passes a bare
// `7*24*time.Hour` literal at scheduler.go:287 — no constant, no env var, no floor guard —
// so a fat-fingered edit to 0 (or to a value Go parses as a nanosecond count) wipes the
// fired history fleet-wide on the next hourly tick with nothing to stop it. The status
// check is the only thing that still holds: pending work survives even at zero.
func TestPruneFiredTimers_ZeroRetentionDeletesEverythingFired(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	org := uuid.New()

	justFired := mkTimer(t, db, org, timerStatusFired, time.Now().Add(-time.Second), ptime(time.Now().Add(-time.Second)))
	pending := mkTimer(t, db, org, timerStatusPending, time.Now().Add(time.Hour), nil)

	require.NoError(t, repo.PruneFiredTimers(ctx, 0))

	assert.False(t, timerExists(t, db, justFired), "olderThan=0 means cutoff=now: anything already fired is eligible")
	assert.True(t, timerExists(t, db, pending), "even at zero retention, pending work is never touched")
}
