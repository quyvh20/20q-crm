package automation

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// p8_ownscope_db_test.go exercises the row-scope SQL (rowScopeAllows +
// UpdateRecordExecutor.enforceRowScope) against a real Postgres, since that logic
// — owned OR teammate-owned OR shared-to-me, via repository.RecordAccessPredicate
// — can't be unit-tested without the contacts / record_shares / user_group tables.
// Docker-gated (skips in short mode).
//
// U6 widened the rule in three directions, and each one is asserted below: a share
// can now name a ROLE or a GROUP (not just a user); a 'team'-scoped author reaches
// their teammates' records; and a WRITE demands an 'edit' share, so a view-shared
// record is no longer silently writable by a workflow.

// setupRowScopeSchema creates the U6 shape of the tables the predicate touches.
func setupRowScopeSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS record_shares (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		org_id UUID NOT NULL,
		record_type TEXT NOT NULL,
		record_id UUID NOT NULL,
		target_type TEXT NOT NULL DEFAULT 'user',
		target_id UUID NOT NULL,
		grantee_user_id UUID,
		permission_level TEXT NOT NULL DEFAULT 'view',
		created_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS user_groups (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		org_id UUID NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		deleted_at TIMESTAMPTZ
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS user_group_members (
		group_id UUID NOT NULL,
		user_id UUID NOT NULL,
		org_id UUID NOT NULL,
		created_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)
}

func ownCaller(userID uuid.UUID) domain.Caller {
	return domain.Caller{UserID: userID, RoleID: uuid.New(), DataScope: domain.DataScopeOwn}
}

func TestRowScopeAllows_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	setupRowScopeSchema(t, db)

	orgID := uuid.New()
	owner := uuid.New()
	viewShared := uuid.New()
	editShared := uuid.New()
	roleShared := uuid.New()
	groupShared := uuid.New()
	teammate := uuid.New()
	stranger := uuid.New()
	cid := uuid.New()

	sharedRoleID := uuid.New()
	groupID := uuid.New()
	teamID := uuid.New()

	require.NoError(t, db.Exec(`INSERT INTO contacts (id, org_id, owner_user_id) VALUES (?, ?, ?)`, cid, orgID, owner).Error)

	// One share per target kind.
	require.NoError(t, db.Exec(`INSERT INTO record_shares (org_id, record_type, record_id, target_type, target_id, permission_level)
		VALUES (?, 'contact', ?, 'user', ?, 'view')`, orgID, cid, viewShared).Error)
	require.NoError(t, db.Exec(`INSERT INTO record_shares (org_id, record_type, record_id, target_type, target_id, permission_level)
		VALUES (?, 'contact', ?, 'user', ?, 'edit')`, orgID, cid, editShared).Error)
	require.NoError(t, db.Exec(`INSERT INTO record_shares (org_id, record_type, record_id, target_type, target_id, permission_level)
		VALUES (?, 'contact', ?, 'role', ?, 'view')`, orgID, cid, sharedRoleID).Error)
	require.NoError(t, db.Exec(`INSERT INTO record_shares (org_id, record_type, record_id, target_type, target_id, permission_level)
		VALUES (?, 'contact', ?, 'group', ?, 'view')`, orgID, cid, groupID).Error)

	// groupShared belongs to the group the record is shared with.
	require.NoError(t, db.Exec(`INSERT INTO user_groups (id, org_id, name) VALUES (?, ?, 'Shared')`, groupID, orgID).Error)
	require.NoError(t, db.Exec(`INSERT INTO user_group_members (group_id, user_id, org_id) VALUES (?, ?, ?)`, groupID, groupShared, orgID).Error)

	// The record's owner and `teammate` share a team — the basis of 'team' scope.
	require.NoError(t, db.Exec(`INSERT INTO user_groups (id, org_id, name) VALUES (?, ?, 'Team')`, teamID, orgID).Error)
	require.NoError(t, db.Exec(`INSERT INTO user_group_members (group_id, user_id, org_id) VALUES (?, ?, ?)`, teamID, owner, orgID).Error)
	require.NoError(t, db.Exec(`INSERT INTO user_group_members (group_id, user_id, org_id) VALUES (?, ?, ?)`, teamID, teammate, orgID).Error)

	ctx := context.Background()
	allows := func(c domain.Caller, requireEdit bool) bool {
		got, err := rowScopeAllows(ctx, db, orgID, "contacts", "contact", cid, c, requireEdit)
		require.NoError(t, err)
		return got
	}

	// Reads.
	assert.True(t, allows(ownCaller(owner), false), "the record owner may act on it")
	assert.True(t, allows(ownCaller(viewShared), false), "a user the record is view-shared to may read it")
	assert.True(t, allows(domain.Caller{UserID: roleShared, RoleID: sharedRoleID, DataScope: domain.DataScopeOwn}, false),
		"a share to the caller's ROLE grants access")
	assert.True(t, allows(ownCaller(groupShared), false), "a share to a GROUP the caller belongs to grants access")
	assert.False(t, allows(ownCaller(stranger), false), "a stranger may not act on it")

	// Team scope: a teammate of the owner reaches the record; a stranger still does not.
	assert.True(t, allows(domain.Caller{UserID: teammate, RoleID: uuid.New(), DataScope: domain.DataScopeTeam}, false),
		"a team-scoped caller reaches a teammate's record")
	assert.False(t, allows(domain.Caller{UserID: stranger, RoleID: uuid.New(), DataScope: domain.DataScopeTeam}, false),
		"team scope does not reach a non-teammate's record")
	assert.False(t, allows(ownCaller(teammate), false),
		"an OWN-scoped caller does not reach a teammate's record")

	// Writes demand an 'edit' share — a 'view' share is not silently writable.
	assert.True(t, allows(ownCaller(owner), true), "the owner may write")
	assert.True(t, allows(ownCaller(editShared), true), "an edit share grants writes")
	assert.False(t, allows(ownCaller(viewShared), true), "a VIEW share must not grant writes")
	assert.False(t, allows(ownCaller(groupShared), true), "a view-level group share must not grant writes")
	assert.False(t, allows(domain.Caller{UserID: teammate, RoleID: uuid.New(), DataScope: domain.DataScopeTeam}, true),
		"team scope grants visibility, not write access to a teammate's record")

	// An 'all'-scoped caller is never row-restricted.
	assert.True(t, allows(domain.Caller{UserID: stranger, RoleID: uuid.New(), DataScope: domain.DataScopeAll}, true),
		"an all-scoped caller is unrestricted")

	// Cross-org: the same ids in another org resolve to nothing.
	other, err := rowScopeAllows(ctx, db, uuid.New(), "contacts", "contact", cid, ownCaller(owner), false)
	require.NoError(t, err)
	assert.False(t, other, "cross-org access is denied even for the owner id")
}

func TestEnforceRowScope_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	setupRowScopeSchema(t, db)

	orgID := uuid.New()
	owner := uuid.New()
	stranger := uuid.New()
	viewShared := uuid.New()
	cid := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO contacts (id, org_id, owner_user_id) VALUES (?, ?, ?)`, cid, orgID, owner).Error)
	require.NoError(t, db.Exec(`INSERT INTO record_shares (org_id, record_type, record_id, target_type, target_id, permission_level)
		VALUES (?, 'contact', ?, 'user', ?, 'view')`, orgID, cid, viewShared).Error)

	// A non-nil authz is required for enforceRowScope to run (nil short-circuits).
	e := NewUpdateRecordExecutor(db, &fakeAuthz{})
	run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}
	withCaller := func(c domain.Caller) context.Context {
		return domain.WithCallerIdentity(context.Background(), c)
	}

	require.Error(t, e.enforceRowScope(withCaller(ownCaller(stranger)), run, "contacts", "contact", cid),
		"an own-scoped author who does not own the record must be denied")

	require.NoError(t, e.enforceRowScope(withCaller(ownCaller(owner)), run, "contacts", "contact", cid),
		"an own-scoped author who owns the record may write it")

	require.Error(t, e.enforceRowScope(withCaller(ownCaller(viewShared)), run, "contacts", "contact", cid),
		"a VIEW share must not let a workflow write the record")

	require.NoError(t, e.enforceRowScope(withCaller(domain.Caller{UserID: stranger, RoleID: uuid.New(), DataScope: domain.DataScopeAll}), run, "contacts", "contact", cid),
		"an all-scoped author is never restricted by row scope")

	require.NoError(t, e.enforceRowScope(withCaller(domain.Caller{UserID: stranger, RoleID: uuid.New(), IsOwner: true, DataScope: domain.DataScopeOwn}), run, "contacts", "contact", cid),
		"the owner role bypasses row scope")
}
