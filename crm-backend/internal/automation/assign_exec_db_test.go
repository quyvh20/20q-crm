package automation

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// End-to-end assignment tests against real Postgres.
//
// The pure sequence tests in assign_routing_test.go pin the rotation ALGORITHM.
// These pin the two things only a database can show: that the cursor actually
// persists across separate runs, and that membership is read from org_users rather
// than from the legacy users.org_id column.

// Fixed ids so a failure names a member, and so least_loaded's `ou.user_id ASC`
// tie-break is deterministic (alice < bob < carol < dave bytewise).
var (
	aliceID = uuid.MustParse("a0000000-0000-0000-0000-000000000001")
	bobID   = uuid.MustParse("b0000000-0000-0000-0000-000000000002")
	carolID = uuid.MustParse("c0000000-0000-0000-0000-000000000003")
	daveID  = uuid.MustParse("d0000000-0000-0000-0000-000000000004")
)

// setupAssignSchema creates the tables assignment reads that setupTestDB does not:
// users (including the LEGACY org_id column, which the non-member test depends on),
// org_users, and deals.
//
// automation_assign_cursors is deliberately NOT created here — it comes from
// repo.AutoMigrate() inside setupTestDB, so these tests also prove that the model's
// composite primary key really is a usable ON CONFLICT target in a fresh database.
func setupAssignSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id UUID PRIMARY KEY,
		org_id UUID,
		first_name TEXT DEFAULT '',
		last_name TEXT DEFAULT '',
		email TEXT DEFAULT ''
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS org_users (
		user_id UUID NOT NULL,
		org_id UUID NOT NULL,
		role_id UUID,
		status VARCHAR(50) NOT NULL DEFAULT 'active',
		joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ,
		PRIMARY KEY (user_id, org_id)
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS deals (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		title TEXT DEFAULT '',
		owner_user_id UUID,
		is_won BOOLEAN NOT NULL DEFAULT false,
		is_lost BOOLEAN NOT NULL DEFAULT false,
		deleted_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)
}

// addMember creates a user and their membership row. legacyOrgID is what gets
// stamped on users.org_id — the two are passed separately so a test can build the
// exact drift that made the old least_loaded query unsafe.
func addMember(t *testing.T, db *gorm.DB, userID, orgID uuid.UUID, status string, legacyOrgID *uuid.UUID) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO users (id, org_id, email) VALUES (?, ?, ?)`,
		userID, legacyOrgID, userID.String()[:8]+"@test.local",
	).Error)
	if status == "" {
		return // user exists but is not a member of this org at all
	}
	require.NoError(t, db.Exec(
		`INSERT INTO org_users (user_id, org_id, role_id, status) VALUES (?, ?, ?, ?)`,
		userID, orgID, uuid.New(), status,
	).Error)
}

func newContact(t *testing.T, db *gorm.DB, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name) VALUES (?, ?, 'T')`, id, orgID,
	).Error)
	return id
}

func ownerOf(t *testing.T, db *gorm.DB, table string, id uuid.UUID) uuid.UUID {
	t.Helper()
	// Scanned through a struct field, not a bare *uuid.UUID: GORM hands a
	// pointer-to-pointer destination straight to database/sql, which then tries to
	// read the uuid as a []uint8 and fails.
	var row struct {
		OwnerUserID *uuid.UUID `gorm:"column:owner_user_id"`
	}
	require.NoError(t, db.Raw(
		`SELECT owner_user_id FROM `+table+` WHERE id = ?`, id,
	).Scan(&row).Error)
	if row.OwnerUserID == nil {
		return uuid.Nil
	}
	return *row.OwnerUserID
}

// assignN runs the executor n times against n fresh contacts, returning the owner
// each one ended up with. Reusing one workflowID + actionID across the calls is what
// exercises the PERSISTED cursor — each call is an independent run, exactly as the
// engine would issue them.
func assignN(t *testing.T, db *gorm.DB, orgID, workflowID uuid.UUID, actionID string, params map[string]any, n int) []uuid.UUID {
	t.Helper()
	exec := NewAssignUserExecutor(db, nil) // nil authz: P8 enforcement is covered in p8_test.go
	got := make([]uuid.UUID, 0, n)
	for i := 0; i < n; i++ {
		contactID := newContact(t, db, orgID)
		run := &WorkflowRun{ID: uuid.New(), WorkflowID: workflowID, OrgID: orgID}
		action := ActionSpec{Type: ActionAssignUser, ID: actionID, Params: params}
		evalCtx := EvalContext{Contact: map[string]any{"id": contactID.String()}}

		_, err := exec.Execute(context.Background(), run, action, evalCtx)
		require.NoError(t, err, "assignment %d failed", i)

		got = append(got, ownerOf(t, db, "contacts", contactID))
	}
	return got
}

func TestAssignExecute_RoundRobin_ExactSequencePersistsAcrossRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	setupAssignSchema(t, db)

	orgID, workflowID := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{aliceID, bobID, carolID} {
		addMember(t, db, id, orgID, "active", &orgID)
	}

	got := assignN(t, db, orgID, workflowID, "step-1", map[string]any{
		"entity":   "contact",
		"strategy": "round_robin",
		"pool":     []string{aliceID.String(), bobID.String(), carolID.String()},
	}, 6)

	// Each of these six was a SEPARATE run. Only a durable cursor produces this;
	// a stateless picker cannot know whose turn is next.
	assert.Equal(t, []uuid.UUID{aliceID, bobID, carolID, aliceID, bobID, carolID}, got)

	var ticket int64
	require.NoError(t, db.Raw(
		`SELECT ticket FROM automation_assign_cursors WHERE org_id = ? AND workflow_id = ? AND action_id = ?`,
		orgID, workflowID, "step-1",
	).Scan(&ticket).Error)
	assert.Equal(t, int64(5), ticket, "six assignments should have consumed tickets 0..5")
}

func TestAssignExecute_RoundRobin_SuspendedMemberNeverReceives(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	setupAssignSchema(t, db)

	orgID, workflowID := uuid.New(), uuid.New()
	addMember(t, db, aliceID, orgID, "active", &orgID)
	addMember(t, db, bobID, orgID, "suspended", &orgID) // membership row survives suspension
	addMember(t, db, carolID, orgID, "active", &orgID)

	got := assignN(t, db, orgID, workflowID, "step-1", map[string]any{
		"entity":   "contact",
		"strategy": "round_robin",
		"pool":     []string{aliceID.String(), bobID.String(), carolID.String()},
	}, 6)

	for i, owner := range got {
		assert.NotEqual(t, bobID, owner, "assignment %d went to a suspended member", i)
	}
	assert.Equal(t, []uuid.UUID{aliceID, carolID, aliceID, carolID, aliceID, carolID}, got,
		"the suspended member's share splits evenly rather than falling to the next member in the pool")
}

func TestAssignExecute_RoundRobin_RemovedMemberWithFrozenHistoryNeverReceives(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	setupAssignSchema(t, db)

	orgID, workflowID := uuid.New(), uuid.New()
	addMember(t, db, aliceID, orgID, "active", &orgID)
	addMember(t, db, carolID, orgID, "active", &orgID)
	// Dave has LEFT: RemoveMember hard-deletes the row, so he has no membership at
	// all — only the user record and his historical contacts remain.
	addMember(t, db, daveID, orgID, "", &orgID)

	// This is the exact shape of the reported bug. Dave's assignment count is frozen
	// at zero because he receives nothing new, so under a least-loaded picker he is
	// the permanent minimum and every single lead routes to the person who left.
	for _, holder := range []uuid.UUID{aliceID, carolID} {
		for i := 0; i < 25; i++ {
			require.NoError(t, db.Exec(
				`INSERT INTO contacts (id, org_id, first_name, owner_user_id) VALUES (?, ?, 'H', ?)`,
				uuid.New(), orgID, holder,
			).Error)
		}
	}

	got := assignN(t, db, orgID, workflowID, "step-1", map[string]any{
		"entity":   "contact",
		"strategy": "round_robin",
		"pool":     []string{aliceID.String(), daveID.String(), carolID.String()},
	}, 4)

	for i, owner := range got {
		assert.NotEqual(t, daveID, owner,
			"assignment %d routed to a former member — the silent misrouting this fix exists to prevent", i)
	}
	assert.Equal(t, []uuid.UUID{aliceID, carolID, aliceID, carolID}, got,
		"rotation ignores historical load entirely; the 25-contact head start must not change the order")
}

func TestAssignExecute_RoundRobin_SeparateCursorPerStep(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	setupAssignSchema(t, db)

	orgID, workflowID := uuid.New(), uuid.New()
	addMember(t, db, aliceID, orgID, "active", &orgID)
	addMember(t, db, bobID, orgID, "active", &orgID)

	params := map[string]any{
		"entity":   "contact",
		"strategy": "round_robin",
		"pool":     []string{aliceID.String(), bobID.String()},
	}
	first := assignN(t, db, orgID, workflowID, "step-1", params, 2)
	second := assignN(t, db, orgID, workflowID, "step-2", params, 2)

	// Two assign_user steps in one workflow (say, a branch per region) each own an
	// independent rotation. Sharing a cursor would start step-2 mid-cycle.
	assert.Equal(t, []uuid.UUID{aliceID, bobID}, first)
	assert.Equal(t, []uuid.UUID{aliceID, bobID}, second, "step-2 starts its own rotation at pool[0]")
}

func TestAssignExecute_RoundRobin_AcceptsJSONDecodedPool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	setupAssignSchema(t, db)

	orgID, workflowID := uuid.New(), uuid.New()
	addMember(t, db, aliceID, orgID, "active", &orgID)
	addMember(t, db, bobID, orgID, "active", &orgID)

	// A workflow loaded from jsonb yields []any, not []string. Duplicates are
	// collapsed so a hand-edited pool cannot double someone's share.
	got := assignN(t, db, orgID, workflowID, "step-1", map[string]any{
		"entity":   "contact",
		"strategy": "round_robin",
		"pool":     []any{aliceID.String(), bobID.String(), aliceID.String()},
	}, 4)

	assert.Equal(t, []uuid.UUID{aliceID, bobID, aliceID, bobID}, got)
}

func TestAssignExecute_RoundRobin_AllMembersGoneFailsLoudly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	setupAssignSchema(t, db)

	orgID := uuid.New()
	addMember(t, db, aliceID, orgID, "suspended", &orgID)
	addMember(t, db, bobID, orgID, "", &orgID)

	contactID := newContact(t, db, orgID)
	exec := NewAssignUserExecutor(db, nil)
	_, err := exec.Execute(context.Background(),
		&WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID},
		ActionSpec{Type: ActionAssignUser, ID: "step-1", Params: map[string]any{
			"entity":   "contact",
			"strategy": "round_robin",
			"pool":     []string{aliceID.String(), bobID.String()},
		}},
		EvalContext{Contact: map[string]any{"id": contactID.String()}},
	)

	// Failing is correct here: an automation run is retried and surfaced in run
	// history, so refusing beats stamping an owner who cannot open the record.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "active member")
	assert.Equal(t, uuid.Nil, ownerOf(t, db, "contacts", contactID), "a failed assignment must leave the record untouched")
}

func TestAssignExecute_LeastLoaded_ExcludesNonMemberWithStaleLegacyOrgID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	setupAssignSchema(t, db)

	orgID := uuid.New()
	addMember(t, db, aliceID, orgID, "active", &orgID)
	// Dave's users.org_id points here — a stale snapshot of his FIRST org — but he
	// holds no membership. The old query's fallback selected straight from that
	// column and would hand him the record.
	addMember(t, db, daveID, orgID, "", &orgID)

	// Alice already carries load, so any picker that considers Dave will choose him.
	for i := 0; i < 5; i++ {
		require.NoError(t, db.Exec(
			`INSERT INTO contacts (id, org_id, first_name, owner_user_id) VALUES (?, ?, 'H', ?)`,
			uuid.New(), orgID, aliceID,
		).Error)
	}

	got := assignN(t, db, orgID, uuid.New(), "step-1", map[string]any{
		"entity": "contact", "strategy": "least_loaded",
	}, 2)

	for i, owner := range got {
		assert.NotEqual(t, daveID, owner, "assignment %d went to a non-member via the legacy users.org_id column", i)
		assert.Equal(t, aliceID, got[i], "the only live member must receive it despite carrying more load")
	}
}

func TestAssignExecute_LeastLoaded_PicksMemberWithNoRecordsAtAll(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	setupAssignSchema(t, db)

	orgID := uuid.New()
	addMember(t, db, aliceID, orgID, "active", &orgID)
	addMember(t, db, bobID, orgID, "active", &orgID) // brand-new hire, owns nothing

	for i := 0; i < 4; i++ {
		require.NoError(t, db.Exec(
			`INSERT INTO contacts (id, org_id, first_name, owner_user_id) VALUES (?, ?, 'H', ?)`,
			uuid.New(), orgID, aliceID,
		).Error)
	}

	got := assignN(t, db, orgID, uuid.New(), "step-1", map[string]any{
		"entity": "contact", "strategy": "least_loaded",
	}, 1)

	// The previous query grouped the contacts table, so someone owning nothing was
	// invisible to it — inverting the feature for exactly the person it should pick.
	assert.Equal(t, bobID, got[0])
}

func TestAssignExecute_LeastLoaded_IgnoresSoftDeletedRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	setupAssignSchema(t, db)

	orgID := uuid.New()
	addMember(t, db, aliceID, orgID, "active", &orgID)
	addMember(t, db, bobID, orgID, "active", &orgID)

	// Alice holds one live contact; Bob holds five that were all deleted. Counting
	// deleted rows would make Bob look like the busier rep.
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name, owner_user_id) VALUES (?, ?, 'L', ?)`,
		uuid.New(), orgID, aliceID,
	).Error)
	for i := 0; i < 5; i++ {
		require.NoError(t, db.Exec(
			`INSERT INTO contacts (id, org_id, first_name, owner_user_id, deleted_at) VALUES (?, ?, 'D', ?, NOW())`,
			uuid.New(), orgID, bobID,
		).Error)
	}

	got := assignN(t, db, orgID, uuid.New(), "step-1", map[string]any{
		"entity": "contact", "strategy": "least_loaded",
	}, 1)

	assert.Equal(t, bobID, got[0], "soft-deleted records must not count toward a rep's load")
}

func TestAssignExecute_LeastLoaded_IgnoresClosedDeals(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	setupAssignSchema(t, db)

	orgID := uuid.New()
	addMember(t, db, aliceID, orgID, "active", &orgID)
	addMember(t, db, bobID, orgID, "active", &orgID)

	// Bob is the team's best closer: ten deals won, none open. Alice has two open.
	require.NoError(t, db.Exec(
		`INSERT INTO deals (id, org_id, title, owner_user_id) VALUES (?, ?, 'open', ?), (?, ?, 'open', ?)`,
		uuid.New(), orgID, aliceID, uuid.New(), orgID, aliceID,
	).Error)
	for i := 0; i < 10; i++ {
		require.NoError(t, db.Exec(
			`INSERT INTO deals (id, org_id, title, owner_user_id, is_won) VALUES (?, ?, 'won', ?, true)`,
			uuid.New(), orgID, bobID,
		).Error)
	}

	dealID := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO deals (id, org_id, title) VALUES (?, ?, 'new')`, dealID, orgID,
	).Error)

	exec := NewAssignUserExecutor(db, nil)
	_, err := exec.Execute(context.Background(),
		&WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID},
		ActionSpec{Type: ActionAssignUser, ID: "step-1", Params: map[string]any{
			"entity": "deal", "strategy": "least_loaded",
		}},
		EvalContext{Deal: map[string]any{"id": dealID.String()}},
	)
	require.NoError(t, err)

	// Counting closed work permanently routes new deals away from whoever closes
	// the most of it.
	assert.Equal(t, bobID, ownerOf(t, db, "deals", dealID))
}

func TestAssignExecute_LeastLoaded_NoMembersFailsLoudly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	setupAssignSchema(t, db)

	orgID := uuid.New()
	addMember(t, db, daveID, orgID, "", &orgID) // legacy column only, no membership

	contactID := newContact(t, db, orgID)
	exec := NewAssignUserExecutor(db, nil)
	_, err := exec.Execute(context.Background(),
		&WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID},
		ActionSpec{Type: ActionAssignUser, ID: "step-1", Params: map[string]any{
			"entity": "contact", "strategy": "least_loaded",
		}},
		EvalContext{Contact: map[string]any{"id": contactID.String()}},
	)

	require.Error(t, err)
	assert.Equal(t, uuid.Nil, ownerOf(t, db, "contacts", contactID))
}

func TestAssignExecute_Specific_StillAssignsUncheckedByDesign(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	setupAssignSchema(t, db)

	orgID := uuid.New()
	addMember(t, db, bobID, orgID, "suspended", &orgID)

	got := assignN(t, db, orgID, uuid.New(), "step-1", map[string]any{
		"entity": "contact", "strategy": "specific", "user_id": bobID.String(),
	}, 1)

	// Pinning a workflow to one person is an explicit instruction, and suspension is
	// routinely reversible. Only the pooled strategies promise distribution, so only
	// they verify membership. Documented here so the asymmetry is deliberate.
	assert.Equal(t, bobID, got[0])
}
