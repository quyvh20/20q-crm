package repository

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

// task_reminder_claim_db_test.go exercises ClaimDueForReminder against a real
// Postgres. The three properties it pins cannot be unit-tested: FOR UPDATE SKIP
// LOCKED handing a row to exactly one claimant, the rolling dedupe window, and
// the predicates (soft-deleted / completed / no due date) that must never be
// claimed. Docker-gated (skips in short mode).

func setupReminderTasksTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE tasks (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(), org_id UUID NOT NULL,
		title VARCHAR(255) NOT NULL DEFAULT '',
		deal_id UUID, contact_id UUID, assigned_to UUID, created_by UUID,
		due_at TIMESTAMPTZ, completed_at TIMESTAMPTZ,
		priority VARCHAR(20) NOT NULL DEFAULT 'medium',
		last_reminded_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`).Error)
}

func insertReminderTask(t *testing.T, db *gorm.DB, title string, due *time.Time, mutate map[string]any) uuid.UUID {
	t.Helper()
	id := uuid.New()
	row := map[string]any{
		"id": id, "org_id": uuid.New(), "title": title, "due_at": due,
		"assigned_to": uuid.New(),
	}
	for k, v := range mutate {
		row[k] = v
	}
	require.NoError(t, db.Table("tasks").Create(row).Error)
	return id
}

func TestClaimDueForReminder_SelectsOnlyRemindableTasks_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping Postgres-backed test")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupReminderTasksTable(t, db)

	now := time.Now()
	due := now.Add(-time.Hour)
	future := now.Add(48 * time.Hour)
	longAgo := now.Add(-30 * time.Hour)
	recently := now.Add(-2 * time.Hour)

	wantDue := insertReminderTask(t, db, "overdue", &due, nil)
	wantStale := insertReminderTask(t, db, "reminded long ago", &due, map[string]any{"last_reminded_at": longAgo})
	insertReminderTask(t, db, "reminded recently", &due, map[string]any{"last_reminded_at": recently})
	insertReminderTask(t, db, "not due yet", &future, nil)
	insertReminderTask(t, db, "no due date", nil, nil)
	insertReminderTask(t, db, "already completed", &due, map[string]any{"completed_at": now})
	insertReminderTask(t, db, "soft deleted", &due, map[string]any{"deleted_at": now})

	repo := NewTaskRepository(db)
	got, err := repo.ClaimDueForReminder(context.Background(), now, 15*time.Minute, 100)
	require.NoError(t, err)

	ids := map[uuid.UUID]bool{}
	for _, task := range got {
		ids[task.ID] = true
	}
	assert.True(t, ids[wantDue], "an overdue task must be claimed")
	assert.True(t, ids[wantStale], "a task last reminded outside the window must be claimed again")
	assert.Len(t, got, 2, "completed, soft-deleted, undated, not-yet-due and recently-reminded tasks must all be left alone: %+v", got)

	// The claim must stamp as it returns, so an immediate second pass is empty.
	again, err := repo.ClaimDueForReminder(context.Background(), now, 15*time.Minute, 100)
	require.NoError(t, err)
	assert.Empty(t, again, "claimed tasks are stamped, so the next pass sees nothing")
}

func TestClaimDueForReminder_ConcurrentClaimsNeverOverlap_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping Postgres-backed test")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupReminderTasksTable(t, db)

	now := time.Now()
	due := now.Add(-time.Hour)
	const total = 40
	for i := 0; i < total; i++ {
		insertReminderTask(t, db, "due", &due, nil)
	}

	// Two scanners racing, exactly as two replicas would on the same tick.
	repo := NewTaskRepository(db)
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[uuid.UUID]int{}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := repo.ClaimDueForReminder(context.Background(), now, 15*time.Minute, total)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, task := range claimed {
				seen[task.ID]++
			}
		}()
	}
	wg.Wait()

	for id, n := range seen {
		assert.Equal(t, 1, n, "task %s was claimed by both scanners — that is a duplicate reminder", id)
	}
	assert.Len(t, seen, total, "every due task should be claimed exactly once across the two scanners")
}

func TestClaimDueForReminder_RespectsTheRollingWindow_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping Postgres-backed test")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupReminderTasksTable(t, db)

	now := time.Now()
	due := now.Add(-time.Hour)
	id := insertReminderTask(t, db, "recurring nag", &due, nil)

	repo := NewTaskRepository(db)
	first, err := repo.ClaimDueForReminder(context.Background(), now, time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Equal(t, id, first[0].ID)

	// 23h later the task is still inside the window: no second reminder. This is
	// what guarantees at most one per local day in every timezone.
	none, err := repo.ClaimDueForReminder(context.Background(), now.Add(23*time.Hour), time.Minute, 10)
	require.NoError(t, err)
	assert.Empty(t, none, "a task must not be reminded twice within the dedupe window")

	// Past the window it becomes claimable again.
	later, err := repo.ClaimDueForReminder(context.Background(), now.Add(reminderDedupeWindow+time.Minute), time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, later, 1)
	assert.Equal(t, id, later[0].ID)
}

func TestClaimDueForReminder_HonoursTheLookaheadWindow_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping Postgres-backed test")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupReminderTasksTable(t, db)

	now := time.Now()
	soon := now.Add(10 * time.Minute)
	insertReminderTask(t, db, "due inside the lookahead", &soon, nil)

	repo := NewTaskRepository(db)
	none, err := repo.ClaimDueForReminder(context.Background(), now, 5*time.Minute, 10)
	require.NoError(t, err)
	assert.Empty(t, none, "a 5-minute lookahead must not reach a task due in 10")

	got, err := repo.ClaimDueForReminder(context.Background(), now, 15*time.Minute, 10)
	require.NoError(t, err)
	assert.Len(t, got, 1, "a 15-minute lookahead reaches it")
}
