package repository

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// task_activity_scope_db_test.go exercises the U0.1-extension row scoping on
// tasks and activities against a real Postgres — the SQL (assigned/created/
// self-logged OR a reachable linked contact/deal, via RecordAccessPredicate)
// can't be unit-tested without the contacts / deals / record_shares tables.
// Docker-gated (skips in short mode).

// setupTaskActivitySchema stands up the minimal shape the two scope helpers touch.
func setupTaskActivitySchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE contacts (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, owner_user_id UUID, deleted_at TIMESTAMPTZ
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE deals (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, owner_user_id UUID, deleted_at TIMESTAMPTZ
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE tasks (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, title VARCHAR(255) NOT NULL DEFAULT '',
		deal_id UUID, contact_id UUID, assigned_to UUID, created_by UUID,
		due_at TIMESTAMPTZ, completed_at TIMESTAMPTZ, priority VARCHAR(20) NOT NULL DEFAULT 'medium',
		-- Added by migration 000074 (R8.1). Its absence here is why
		-- TestTaskCreate_StampsCreatedByFromCaller_DB has been failing since:
		-- gorm INSERTs every column on domain.Task, so Create died with
		-- column "last_reminded_at" of relation "tasks" does not exist.
		last_reminded_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`).Error)
	// activity_type is a real ENUM in production (migrations/000002). Declaring it
	// as a varchar here would make this schema silently more permissive than the
	// one the code runs against, and the R9.5 report catalog compares this column
	// against bound strings and the literal '' — both of which a varchar accepts
	// and an enum does not.
	require.NoError(t, db.Exec(`DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'activity_type') THEN
			CREATE TYPE activity_type AS ENUM ('call', 'email', 'meeting', 'note', 'stage_change');
		END IF;
	END $$`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE activities (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, type activity_type NOT NULL DEFAULT 'note',
		deal_id UUID, contact_id UUID, user_id UUID, title VARCHAR(255), body TEXT,
		duration_minutes INT, occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), sentiment TEXT,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE record_shares (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(), org_id UUID NOT NULL,
		record_type TEXT NOT NULL, record_id UUID NOT NULL,
		target_type TEXT NOT NULL DEFAULT 'user', target_id UUID NOT NULL,
		permission_level TEXT NOT NULL DEFAULT 'view', created_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE user_groups (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, name TEXT NOT NULL DEFAULT '', deleted_at TIMESTAMPTZ
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE user_group_members (
		group_id UUID NOT NULL, user_id UUID NOT NULL, org_id UUID NOT NULL, created_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)
}

func insContact(t *testing.T, db *gorm.DB, org, id, owner uuid.UUID) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO contacts (id, org_id, owner_user_id) VALUES (?, ?, ?)`, id, org, owner).Error)
}

func insDeal(t *testing.T, db *gorm.DB, org, id, owner uuid.UUID) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO deals (id, org_id, owner_user_id) VALUES (?, ?, ?)`, id, org, owner).Error)
}

func shareToUser(t *testing.T, db *gorm.DB, org uuid.UUID, recordType string, recordID, target uuid.UUID) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO record_shares (org_id, record_type, record_id, target_type, target_id, permission_level)
		VALUES (?, ?, ?, 'user', ?, 'view')`, org, recordType, recordID, target).Error)
}

// ownCtx builds an 'own'-scoped caller context (what the auth middleware sets).
func ownCtx(userID uuid.UUID) context.Context {
	return WithCallerScope(context.Background(), domain.DataScopeOwn, userID, uuid.New())
}

func allCtx(userID uuid.UUID) context.Context {
	return WithCallerScope(context.Background(), domain.DataScopeAll, userID, uuid.New())
}

func TestTaskScope_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupTaskActivitySchema(t, db)
	repo := NewTaskRepository(db)

	org := uuid.New()
	me := uuid.New()
	other := uuid.New()

	myContact := uuid.New()
	otherContact := uuid.New()
	sharedContact := uuid.New() // owned by other, but shared to me
	myDeal := uuid.New()
	otherDeal := uuid.New()
	insContact(t, db, org, myContact, me)
	insContact(t, db, org, otherContact, other)
	insContact(t, db, org, sharedContact, other)
	insDeal(t, db, org, myDeal, me)
	insDeal(t, db, org, otherDeal, other)
	shareToUser(t, db, org, "contact", sharedContact, me)

	// task id → (assigned_to, created_by, contact_id, deal_id), and whether I may see it.
	assignedToMe := uuid.New()
	createdByMe := uuid.New()
	onMyContact := uuid.New()
	onMyDeal := uuid.New()
	onSharedContact := uuid.New()
	onOtherContact := uuid.New()
	unlinkedOther := uuid.New()

	insTask := func(id uuid.UUID, assigned, createdBy, contact, deal *uuid.UUID) {
		require.NoError(t, db.Exec(`INSERT INTO tasks (id, org_id, title, assigned_to, created_by, contact_id, deal_id, priority)
			VALUES (?, ?, 'T', ?, ?, ?, ?, 'medium')`, id, org, assigned, createdBy, contact, deal).Error)
	}
	insTask(assignedToMe, &me, &other, nil, nil)
	insTask(createdByMe, &other, &me, nil, nil)
	insTask(onMyContact, &other, &other, &myContact, nil)
	insTask(onMyDeal, &other, &other, nil, &myDeal)
	insTask(onSharedContact, &other, &other, &sharedContact, nil)
	insTask(onOtherContact, &other, &other, &otherContact, nil)
	insTask(unlinkedOther, &other, &other, nil, nil)

	reachable := map[uuid.UUID]bool{
		assignedToMe: true, createdByMe: true, onMyContact: true,
		onMyDeal: true, onSharedContact: true,
	}
	unreachable := []uuid.UUID{onOtherContact, unlinkedOther}

	// List, own scope: exactly the reachable set.
	gotResult, err := repo.List(ownCtx(me), org, domain.TaskFilter{})
	require.NoError(t, err)
	got := gotResult.Tasks
	gotIDs := map[uuid.UUID]bool{}
	for _, tk := range got {
		gotIDs[tk.ID] = true
	}
	assert.Len(t, got, len(reachable), "own-scoped list should return only reachable tasks")
	for id := range reachable {
		assert.True(t, gotIDs[id], "expected reachable task %s in the list", id)
	}
	for _, id := range unreachable {
		assert.False(t, gotIDs[id], "task %s must NOT leak to an own-scoped caller", id)
	}

	// List, all scope: every task in the org.
	allResult, err := repo.List(allCtx(other), org, domain.TaskFilter{})
	require.NoError(t, err)
	assert.Len(t, allResult.Tasks, 7, "an all-scoped caller sees every task")

	// GetByID: reachable found, unreachable is (nil, nil) — indistinguishable from
	// a non-existent id, so a row-scoped user can't probe for it.
	found, err := repo.GetByID(ownCtx(me), org, assignedToMe)
	require.NoError(t, err)
	require.NotNil(t, found)
	hidden, err := repo.GetByID(ownCtx(me), org, onOtherContact)
	require.NoError(t, err)
	assert.Nil(t, hidden, "an unreachable task must not be fetchable by id")

	// SoftDelete: unreachable → 404 (not a silent success); reachable → deletes.
	err = repo.SoftDelete(ownCtx(me), org, onOtherContact)
	assert.ErrorIs(t, err, domain.ErrTaskNotFound, "deleting another rep's task must 404, not succeed")
	require.NoError(t, repo.SoftDelete(ownCtx(me), org, assignedToMe), "deleting my own task should succeed")

	// The delete actually took effect and stays scoped afterwards.
	after, err := repo.GetByID(ownCtx(me), org, assignedToMe)
	require.NoError(t, err)
	assert.Nil(t, after, "the deleted task is gone")
}

func TestTaskCreate_StampsCreatedByFromCaller_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupTaskActivitySchema(t, db)
	repo := NewTaskRepository(db)

	org := uuid.New()
	me := uuid.New()

	// A task I create with no link and no assignee is invisible unless created_by
	// is stamped — which the repository does from the caller on the context.
	tk := &domain.Task{OrgID: org, Title: "solo"}
	require.NoError(t, repo.Create(ownCtx(me), tk))

	require.NotNil(t, tk.CreatedBy, "Create should stamp created_by from the caller")
	assert.Equal(t, me, *tk.CreatedBy)

	gotResult, err := repo.List(ownCtx(me), org, domain.TaskFilter{})
	require.NoError(t, err)
	require.Len(t, gotResult.Tasks, 1, "my own unlinked/unassigned task must be visible to me")
	assert.Equal(t, tk.ID, gotResult.Tasks[0].ID)
}

func TestActivityScope_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupTaskActivitySchema(t, db)
	repo := NewActivityRepository(db)

	org := uuid.New()
	me := uuid.New()
	other := uuid.New()

	myContact := uuid.New()
	otherContact := uuid.New()
	sharedContact := uuid.New()
	myDeal := uuid.New()
	otherDeal := uuid.New()
	insContact(t, db, org, myContact, me)
	insContact(t, db, org, otherContact, other)
	insContact(t, db, org, sharedContact, other)
	insDeal(t, db, org, myDeal, me)
	insDeal(t, db, org, otherDeal, other)
	shareToUser(t, db, org, "contact", sharedContact, me)

	loggedByMe := uuid.New()
	onMyContact := uuid.New()
	onMyDeal := uuid.New()
	onSharedContact := uuid.New()
	onOtherContact := uuid.New()
	onOtherDeal := uuid.New()

	insAct := func(id uuid.UUID, user, contact, deal *uuid.UUID) {
		require.NoError(t, db.Exec(`INSERT INTO activities (id, org_id, type, user_id, contact_id, deal_id, body)
			VALUES (?, ?, 'note', ?, ?, ?, 'secret notes')`, id, org, user, contact, deal).Error)
	}
	insAct(loggedByMe, &me, nil, nil)
	insAct(onMyContact, &other, &myContact, nil)
	insAct(onMyDeal, &other, nil, &myDeal)
	insAct(onSharedContact, &other, &sharedContact, nil)
	insAct(onOtherContact, &other, &otherContact, nil)
	insAct(onOtherDeal, &other, nil, &otherDeal)

	reachable := map[uuid.UUID]bool{loggedByMe: true, onMyContact: true, onMyDeal: true, onSharedContact: true}
	leaks := []uuid.UUID{onOtherContact, onOtherDeal}

	got, err := repo.List(ownCtx(me), org, domain.ActivityFilter{})
	require.NoError(t, err)
	gotIDs := map[uuid.UUID]bool{}
	for _, a := range got {
		gotIDs[a.ID] = true
	}
	assert.Len(t, got, len(reachable), "own-scoped activity list should return only reachable rows")
	for id := range reachable {
		assert.True(t, gotIDs[id], "expected reachable activity %s", id)
	}
	for _, id := range leaks {
		assert.False(t, gotIDs[id], "activity %s (another rep's contact/deal notes) must NOT leak", id)
	}

	all, err := repo.List(allCtx(other), org, domain.ActivityFilter{})
	require.NoError(t, err)
	assert.Len(t, all, 6, "an all-scoped caller sees every activity")

	// A contact-scoped filter still applies row scope: filtering by a contact I
	// can't see returns nothing, not that contact's notes.
	filtered, err := repo.List(ownCtx(me), org, domain.ActivityFilter{ContactID: &otherContact})
	require.NoError(t, err)
	assert.Empty(t, filtered, "filtering by an unreachable contact must not bypass row scope")
}
