package automation

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// p8_task_activity_test.go covers the A1 authz closure for the create_task and
// log_activity executors: they now enforce the workflow author's OLS(read) +
// own-scope on the linked contact/deal before inserting, and audit the
// creation — matching assign_user/update_record. Unit tests use fakeAuthz;
// the own-scope SQL is exercised DB-backed (Docker-gated).

// --- authorizeLinkedRecordRead gate (no DB) ---

func TestLinkedRecordRead_OLSDenyPropagates(t *testing.T) {
	az := &fakeAuthz{authorizeErr: domain.NewAppError(403, "no read")}
	cid := uuid.New()
	ctx := p8Ctx(domain.Caller{UserID: uuid.New(), RoleID: uuid.New(), DataScope: domain.DataScopeAll})
	err := authorizeLinkedRecordRead(ctx, nil, az, uuid.New(), &cid, nil)
	require.Error(t, err, "an OLS read denial on the linked contact must fail the create")
}

func TestLinkedRecordRead_NilAuthz_NoEnforcement(t *testing.T) {
	cid := uuid.New()
	did := uuid.New()
	err := authorizeLinkedRecordRead(context.Background(), nil, nil, uuid.New(), &cid, &did)
	require.NoError(t, err, "a nil authorizer must be a no-op (pre-A1 trusted behavior)")
}

func TestLinkedRecordRead_NoLinkedRecords_NoCheck(t *testing.T) {
	az := &fakeAuthz{authorizeErr: domain.NewAppError(403, "no read")}
	ctx := p8Ctx(domain.Caller{UserID: uuid.New(), RoleID: uuid.New(), DataScope: domain.DataScopeAll})
	err := authorizeLinkedRecordRead(ctx, nil, az, uuid.New(), nil, nil)
	require.NoError(t, err, "no linked records means nothing to authorize")
}

func TestLinkedRecordRead_DealDenyPropagates(t *testing.T) {
	az := &fakeAuthz{authorizeErr: domain.NewAppError(403, "no read")}
	did := uuid.New()
	ctx := p8Ctx(domain.Caller{UserID: uuid.New(), RoleID: uuid.New(), DataScope: domain.DataScopeAll})
	err := authorizeLinkedRecordRead(ctx, nil, az, uuid.New(), nil, &did)
	require.Error(t, err, "an OLS read denial on the linked deal must fail the create")
}

// --- own-scope enforcement + audit, DB-backed ---

func TestCreateTask_OwnScope_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`).Error)
	// record_shares (in its real U6 shape) is created by setupTestDB — a local
	// pre-U6 copy here would just lose the race and mislead the next reader.
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS tasks (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		title TEXT NOT NULL,
		contact_id UUID,
		deal_id UUID,
		assigned_to UUID,
		due_at TIMESTAMPTZ,
		priority TEXT DEFAULT 'medium',
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)

	orgID := uuid.New()
	owner := uuid.New()
	stranger := uuid.New()
	cid := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, owner_user_id) VALUES (?, ?, ?)`, cid, orgID, owner).Error)

	az := &fakeAuthz{} // OLS allows; own-scope is the gate under test
	exec := NewTaskExecutor(db, az)
	run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}
	action := ActionSpec{ID: "t1", Type: ActionCreateTask, Params: map[string]any{"title": "follow up"}}
	evalCtx := EvalContext{Contact: map[string]any{"id": cid.String()}}

	// Own-scoped author who is NOT the contact's owner → denied, nothing written.
	strangerCtx := p8Ctx(domain.Caller{UserID: stranger, RoleID: uuid.New(), DataScope: domain.DataScopeOwn})
	_, err := exec.Execute(strangerCtx, run, action, evalCtx)
	require.Error(t, err, "an own-scoped author may not attach a task to a record they can't see")
	var count int64
	require.NoError(t, db.Table("tasks").Where("org_id = ?", orgID).Count(&count).Error)
	assert.Zero(t, count, "the denied create must not leave a row")
	assert.Empty(t, az.audits, "a denied create must not audit")

	// The record's owner → allowed, row written, audited to the author.
	ownerCtx := p8Ctx(domain.Caller{UserID: owner, RoleID: uuid.New(), DataScope: domain.DataScopeOwn})
	out, err := exec.Execute(ownerCtx, run, action, evalCtx)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NoError(t, db.Table("tasks").Where("org_id = ?", orgID).Count(&count).Error)
	assert.Equal(t, int64(1), count)
	require.Len(t, az.audits, 1, "a successful create must emit exactly one audit row")
	got := az.audits[0]
	assert.Equal(t, owner, got.ActorID, "the audit actor is the workflow author")
	assert.Equal(t, "task", got.ObjectSlug)
	assert.Equal(t, domain.ActionCreate, got.Action)
}

func TestLogActivity_OwnScope_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`).Error)
	// record_shares (in its real U6 shape) is created by setupTestDB — a local
	// pre-U6 copy here would just lose the race and mislead the next reader.
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS activities (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		type TEXT NOT NULL,
		contact_id UUID,
		deal_id UUID,
		user_id UUID,
		title TEXT NOT NULL,
		body TEXT,
		occurred_at TIMESTAMPTZ DEFAULT NOW(),
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)

	orgID := uuid.New()
	owner := uuid.New()
	sharedUser := uuid.New()
	stranger := uuid.New()
	cid := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, owner_user_id) VALUES (?, ?, ?)`, cid, orgID, owner).Error)
	// A share now names a TARGET (user|role|group) and carries its org (U6.2).
	require.NoError(t, db.Exec(
		`INSERT INTO record_shares (org_id, record_type, record_id, target_type, target_id, permission_level)
		 VALUES (?, 'contact', ?, 'user', ?, 'view')`, orgID, cid, sharedUser).Error)

	az := &fakeAuthz{}
	exec := NewActivityExecutor(db, az)
	run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}
	action := ActionSpec{ID: "l1", Type: ActionLogActivity, Params: map[string]any{
		"activity_type": "note", "title": "automated note",
	}}
	evalCtx := EvalContext{Contact: map[string]any{"id": cid.String()}}

	// Own-scoped stranger → denied.
	strangerCtx := p8Ctx(domain.Caller{UserID: stranger, RoleID: uuid.New(), DataScope: domain.DataScopeOwn})
	_, err := exec.Execute(strangerCtx, run, action, evalCtx)
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Table("activities").Where("org_id = ?", orgID).Count(&count).Error)
	assert.Zero(t, count)

	// Shared-to user → allowed (owned-OR-shared mirrors the read layer) + audited.
	sharedCtx := p8Ctx(domain.Caller{UserID: sharedUser, RoleID: uuid.New(), DataScope: domain.DataScopeOwn})
	_, err = exec.Execute(sharedCtx, run, action, evalCtx)
	require.NoError(t, err)
	require.NoError(t, db.Table("activities").Where("org_id = ?", orgID).Count(&count).Error)
	assert.Equal(t, int64(1), count)
	require.Len(t, az.audits, 1)
	assert.Equal(t, "activity", az.audits[0].ObjectSlug)
	assert.Equal(t, domain.ActionCreate, az.audits[0].Action)
}
