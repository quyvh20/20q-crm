package automation

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// p8_ownscope_db_test.go exercises the own-scope SQL (ownScopeAllows +
// UpdateRecordExecutor.enforceOwnScope) against a real Postgres, since that logic
// — owned OR shared-to-me, mirroring repository/scopes.go — can't be unit-tested
// without the contacts/record_shares tables. Docker-gated (skips in short mode).

func TestOwnScopeAllows_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS record_shares (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		record_type TEXT NOT NULL,
		record_id UUID NOT NULL,
		grantee_user_id UUID,
		created_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)

	orgID := uuid.New()
	owner := uuid.New()
	shared := uuid.New()
	stranger := uuid.New()
	cid := uuid.New()

	require.NoError(t, db.Exec(`INSERT INTO contacts (id, org_id, owner_user_id) VALUES (?, ?, ?)`, cid, orgID, owner).Error)
	require.NoError(t, db.Exec(`INSERT INTO record_shares (record_type, record_id, grantee_user_id) VALUES ('contact', ?, ?)`, cid, shared).Error)

	ctx := context.Background()

	got, err := ownScopeAllows(ctx, db, orgID, "contacts", "contact", cid, owner)
	require.NoError(t, err)
	assert.True(t, got, "the record owner may act on it")

	got, err = ownScopeAllows(ctx, db, orgID, "contacts", "contact", cid, shared)
	require.NoError(t, err)
	assert.True(t, got, "a user the record is shared to may act on it")

	got, err = ownScopeAllows(ctx, db, orgID, "contacts", "contact", cid, stranger)
	require.NoError(t, err)
	assert.False(t, got, "a stranger may not act on it")

	got, err = ownScopeAllows(ctx, db, uuid.New(), "contacts", "contact", cid, owner)
	require.NoError(t, err)
	assert.False(t, got, "cross-org access is denied even for the owner id")
}

func TestEnforceOwnScope_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS record_shares (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		record_type TEXT NOT NULL,
		record_id UUID NOT NULL,
		grantee_user_id UUID,
		created_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)

	orgID := uuid.New()
	owner := uuid.New()
	stranger := uuid.New()
	cid := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO contacts (id, org_id, owner_user_id) VALUES (?, ?, ?)`, cid, orgID, owner).Error)

	// A non-nil authz is required for enforceOwnScope to run (nil short-circuits).
	e := NewUpdateRecordExecutor(db, &fakeAuthz{})
	run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}

	// Own-scoped stranger → denied.
	strangerCtx := domain.WithCallerIdentity(context.Background(),
		domain.Caller{UserID: stranger, RoleID: uuid.New(), DataScope: domain.DataScopeOwn})
	require.Error(t, e.enforceOwnScope(strangerCtx, run, "contacts", "contact", cid),
		"an own-scoped author who does not own the record must be denied")

	// Own-scoped owner → allowed.
	ownerCtx := domain.WithCallerIdentity(context.Background(),
		domain.Caller{UserID: owner, RoleID: uuid.New(), DataScope: domain.DataScopeOwn})
	require.NoError(t, e.enforceOwnScope(ownerCtx, run, "contacts", "contact", cid),
		"an own-scoped author who owns the record may act")

	// All-scoped caller → own-scope check does not apply, allowed regardless.
	allCtx := domain.WithCallerIdentity(context.Background(),
		domain.Caller{UserID: stranger, RoleID: uuid.New(), DataScope: domain.DataScopeAll})
	require.NoError(t, e.enforceOwnScope(allCtx, run, "contacts", "contact", cid),
		"an all-scoped author is never restricted by own-scope")

	// Owner-role caller (IsOwner) → bypasses own-scope even if not the row owner.
	ownerRoleCtx := domain.WithCallerIdentity(context.Background(),
		domain.Caller{UserID: stranger, RoleID: uuid.New(), IsOwner: true, DataScope: domain.DataScopeOwn})
	require.NoError(t, e.enforceOwnScope(ownerRoleCtx, run, "contacts", "contact", cid),
		"the owner role bypasses own-scope")
}
