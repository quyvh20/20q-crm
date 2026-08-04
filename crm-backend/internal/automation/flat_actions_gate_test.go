package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// flat_actions_gate_test.go covers the R5 teardown gate: the `actions` column that now
// outlives its Go field, and the log line that reports whether the column itself can be
// dropped (deploy 2) without losing behaviour.
//
// WHAT DEPLOY 1 CHANGED HERE. The actions→steps BACKFILL is gone, and with it the two
// tests that covered it (TestStepsFromFlatActions, TestMigrateFlatActionsToSteps). There
// is nothing left to back-fill: production's gate was verified CLEAR before the code was
// removed, no writer produces a flat action list any more, and the Go code can no longer
// read the column through the model at all. The GATE stays, precisely because the column
// stays — it is how the deploy-1 soak confirms nothing regressed, and it reads the
// column through raw SQL rather than a struct field, which is why it still works.
//
// THE TWO THINGS HERE THAT ARE LOAD-BEARING RATHER THAN DESCRIPTIVE:
//
//  1. TestFlatActionsColumnDefault — the only assertion anywhere that the column and its
//     DEFAULT '[]'::jsonb survive. Deploy 0 made that a struct tag; deploy 1 deleted the
//     field, so it is now Repository.ensureLegacyActionsColumn that has to keep it, and
//     this test is what proves that swap did not drop it on the floor.
//  2. The blocking-vs-inert split (TestFlatActionsGate_BlockingVsInert) — the gate must
//     be clearable, and it is only clearable if "steps are empty" and "the teardown is
//     unsafe" are answered separately.
//
// Everything here asserts on the STORED jsonb text, not on a Go struct round-trip. The
// distinctions that matter — SQL NULL vs jsonb 'null' vs '[]' — are invisible once a
// value has been through datatypes.JSON, and they are exactly what the gate counts.

const (
	flatGateWorkflowsTable = "automation_workflows"
	flatGateVersionsTable  = "automation_workflow_versions"
	// flatGateSQLNull is what the read helpers report for a column that is SQL NULL,
	// so a test can tell it apart from the jsonb scalar 'null'.
	flatGateSQLNull = "<SQL NULL>"
)

// ── Seeding ───────────────────────────────────────────────────────────────────

// flatGateWorkflow inserts one automation_workflows row with EXACT control over the
// `actions` and `steps` jsonb.
//
// Raw SQL rather than the GORM model, for two reasons that now stack. It was always
// deliberate: the states this file is about (steps SQL-NULL vs jsonb 'null'; actions
// '[]' vs 'null' vs a non-array object) are states datatypes.JSON cannot express
// unambiguously, and seeding through the model is how a test ends up asserting against
// a value it never actually created. Since R5 deploy 1 it is also the ONLY option: no
// Go struct declares `actions` any more, so there is no model field to set.
func flatGateWorkflow(t *testing.T, db *gorm.DB, orgID uuid.UUID, actions string, steps *string, softDeleted bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var deletedAt *time.Time
	if softDeleted {
		now := time.Now()
		deletedAt = &now
	}
	require.NoError(t, db.Exec(`
		INSERT INTO automation_workflows
			(id, org_id, name, description, is_active, trigger, conditions, actions, steps,
			 version, created_by, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, '', false, '{"type":"webhook_inbound"}'::jsonb, NULL, ?::jsonb, ?::jsonb,
			 1, ?, NOW(), NOW(), ?)`,
		id, orgID, "flatgate-"+id.String()[:8], actions, steps, uuid.New(), deletedAt).Error)
	return id
}

// flatGateVersion inserts one automation_workflow_versions row. That table has no
// deleted_at and no updated_at — see TestFlatActionsGate_VersionsTableHasNoDeletedAt.
func flatGateVersion(t *testing.T, db *gorm.DB, workflowID uuid.UUID, version int, actions string, steps *string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(`
		INSERT INTO automation_workflow_versions
			(id, workflow_id, version, trigger, conditions, actions, steps, created_at)
		VALUES (?, ?, ?, '{"type":"webhook_inbound"}'::jsonb, NULL, ?::jsonb, ?::jsonb, NOW())`,
		id, workflowID, version, actions, steps).Error)
	return id
}

// flatGateJSONPtr is the pointer form the seeders take for a jsonb column that is
// present but whose value is a literal (nil ⇒ SQL NULL).
func flatGateJSONPtr(v string) *string { return &v }

// flatGateActions renders a flat actions list the way the pre-Steps frontend stored it.
// Nothing WRITES this shape any more; the seeders use it to plant the legacy rows the
// gate exists to score.
func flatGateActions(t *testing.T, actions ...ActionSpec) string {
	t.Helper()
	raw, err := json.Marshal(actions)
	require.NoError(t, err)
	return string(raw)
}

// flatGateStepsText renders the steps tree equivalent to a flat action list — what a
// row looks like once someone has given it a real steps tree.
func flatGateStepsText(t *testing.T, actions ...ActionSpec) string {
	t.Helper()
	raw, err := json.Marshal(actionSteps(actions...))
	require.NoError(t, err)
	return string(raw)
}

// ── Reading ───────────────────────────────────────────────────────────────────

// flatGateSteps returns `steps::text` for a row, or flatGateSQLNull when the column is
// SQL NULL. The ::text cast is what makes jsonb 'null' readable as the four characters
// "null" instead of collapsing into the SQL-NULL case.
func flatGateSteps(t *testing.T, db *gorm.DB, table string, id uuid.UUID) string {
	t.Helper()
	var row struct {
		Steps *string `gorm:"column:steps"`
		Found bool    `gorm:"column:found"`
	}
	require.NoError(t, db.Raw(
		`SELECT steps::text AS steps, true AS found FROM `+table+` WHERE id = ?`, id).
		Scan(&row).Error)
	require.True(t, row.Found, "row %s not found in %s", id, table)
	if row.Steps == nil {
		return flatGateSQLNull
	}
	return *row.Steps
}

func flatGateUpdatedAt(t *testing.T, db *gorm.DB, id uuid.UUID) time.Time {
	t.Helper()
	var row struct {
		UpdatedAt time.Time `gorm:"column:updated_at"`
	}
	require.NoError(t, db.Raw(
		`SELECT updated_at FROM automation_workflows WHERE id = ?`, id).Scan(&row).Error)
	require.False(t, row.UpdatedAt.IsZero(), "workflow %s not found", id)
	return row.UpdatedAt
}

func flatGateParseSteps(t *testing.T, raw string) []StepSpec {
	t.Helper()
	var steps []StepSpec
	require.NoError(t, json.Unmarshal([]byte(raw), &steps), "stored steps is not a steps array: %s", raw)
	return steps
}

// flatGateStillSelected reports whether a row would be picked up by the migration's own
// WHERE clause on the NEXT boot — the predicate is spelled out here rather than reusing
// the constant, so a change to the production predicate has to be re-stated deliberately
// instead of tautologically agreeing with itself.
//
// Selection is NOT the same thing as churn. Since '[]' joined the predicate, an
// already-converged empty-actions row is selected on every boot and correctly rewritten
// zero times — convergence for those rows is proven by updated_at (see the no-churn
// subtest), not by this helper.
func flatGateStillSelected(t *testing.T, db *gorm.DB, table string, id uuid.UUID) bool {
	t.Helper()
	var row struct {
		N int64 `gorm:"column:n"`
	}
	require.NoError(t, db.Raw(
		`SELECT count(*) AS n FROM `+table+
			` WHERE id = ? AND (steps IS NULL OR steps = 'null'::jsonb OR steps = '[]'::jsonb)`, id).
		Scan(&row).Error)
	return row.N > 0
}

// ── DB-backed: the column default the whole teardown ordering rests on ────────

// TestFlatActionsColumnDefault is the ONLY thing in the repo that asserts the `actions`
// column and its default are still there — and deploy 1 makes it matter MORE, not less.
//
// Deploy 0 put `default:'[]'::jsonb` on both Actions struct tags and got it past the
// point of rollback. Deploy 1 then deleted the fields, which means gorm can no longer
// see the column at all: it does not create it, does not maintain its default, and (as
// always) does not drop it. On every existing database the column and default simply
// persist, and they are the only reason writes still work — GORM has stopped naming
// `actions` in its INSERTs, so without the default every workflow write dies on
//
//	ERROR: null value in column "actions" … violates not-null constraint (SQLSTATE 23502)
//
// killing CreateWorkflow on its first statement and UpdateWorkflow on its version
// snapshot (which rolls the whole update back).
//
// On a FRESH database — which is exactly what this test runs against — nothing would
// create the column at all, and the gate below would read UNKNOWN forever while a
// rollback to a deploy-0 binary would reject every write. Repository.ensureLegacyActionsColumn
// is what closes that, and this test is what proves it ran. Nothing else in the tree
// reads column_default.
func TestFlatActionsColumnDefault(t *testing.T) {
	db, cleanup := setupTestDB(t) // runs Repository.AutoMigrate
	defer cleanup()

	const want = `'[]'::jsonb`
	for _, table := range []string{flatGateWorkflowsTable, flatGateVersionsTable} {
		var row struct {
			Default *string `gorm:"column:column_default"`
			Nullable string `gorm:"column:is_nullable"`
			Found    bool   `gorm:"column:found"`
		}
		// table_schema = current_schema(), exactly like the guard's own probe:
		// information_schema.columns spans EVERY schema, so an unqualified table_name
		// match would let a stray copy of the table in another namespace answer for the
		// one we write to — and this assertion would then agree with the bug.
		require.NoError(t, db.Raw(`
			SELECT column_default, is_nullable, true AS found
			FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = ? AND column_name = 'actions'`, table).Scan(&row).Error)
		require.True(t, row.Found, "%s has no `actions` column at all", table)

		// NOT NULL is the other half of the trap: a nullable column would survive the
		// field's removal without any default. If this ever flips to YES the default
		// stops being load-bearing — but so does the 23502, so re-read both.
		assert.Equal(t, "NO", row.Nullable,
			"%s.actions is expected to be NOT NULL — that is WHY the default is required", table)

		// assert, not require: BOTH tables are reported in one run, because both tags
		// are equally deletable and the versions table is where the missing default
		// does the most damage (its 23502 rolls the workflow UPDATE back with it).
		if !assert.NotNil(t, row.Default,
			"%s.actions has NO column default.\n"+
				"No Go struct declares this column any more, so gorm did not set it — the only thing\n"+
				"that does is Repository.ensureLegacyActionsColumn, called from AutoMigrate.\n"+
				"That default is not cosmetic: it is the single thing that lets this COLUMN outlive\n"+
				"the Go FIELD during the R5 teardown. Without it every workflow write returns 500\n"+
				"with SQLSTATE 23502 (null value in column \"actions\"), with every workflow UPDATE\n"+
				"rolled back by its version-snapshot INSERT.", table) {
			continue
		}
		assert.Equal(t, want, *row.Default,
			"%s.actions default must be exactly %s so the column fills itself with an empty\n"+
				"action list once the Go field is gone", table, want)
	}
}

// flatGateColumnDefault reads `actions`' column_default IN THE SCHEMA WRITES GO TO,
// returning found=false when the column is absent there. Schema-qualified for the same
// reason the guard's probe is: information_schema.columns spans every schema on the
// database.
func flatGateColumnDefault(t *testing.T, db *gorm.DB, table string) (def *string, found bool) {
	t.Helper()
	var row struct {
		Default *string `gorm:"column:column_default"`
		Found   bool    `gorm:"column:found"`
	}
	require.NoError(t, db.Raw(`
		SELECT column_default, true AS found
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = ? AND column_name = 'actions'`, table).Scan(&row).Error)
	return row.Default, row.Found
}

// TestEnsureLegacyActionsColumn_RestoresALostDefault is SCENARIO A: the column exists,
// is NOT NULL, and carries NO default.
//
// TestFlatActionsColumnDefault cannot reach this state — it runs against a fresh
// container, where the guard takes its ADD COLUMN … DEFAULT path and the default is
// there by construction. Scenario A is the state PRODUCTION can be in, and the one an
// existence-only guard (`if n > 0 { continue }`) leaves broken forever:
//
//   - deploy 0 applied the default under migrationLockTimeout ("3s"), so a boot that
//     lost its lock wait abandoned the ALTER and logged 55P03;
//   - deploy 1 removed the struct tag, so gorm can no longer see the column and the
//     "the next uncontended boot applies it" self-healing that used to make a lock-timeout
//     miss survivable no longer exists for THIS column;
//   - nothing else in the tree sets it.
//
// Left unrepaired, every POST /api/workflows dies on its first INSERT and every PUT dies
// on its version snapshot (rolling the workflow UPDATE back with it) — SQLSTATE 23502,
// permanently. So the assertion is not only "the default came back" but "a write that
// never names `actions`, which is all GORM emits now, succeeds on BOTH tables".
func TestEnsureLegacyActionsColumn_RestoresALostDefault(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	tables := []string{flatGateWorkflowsTable, flatGateVersionsTable}

	// Put the database INTO scenario A. DROP DEFAULT only: the column stays, and stays
	// NOT NULL — that combination is the whole point.
	for _, table := range tables {
		require.NoError(t, db.Exec(`ALTER TABLE `+table+` ALTER COLUMN actions DROP DEFAULT`).Error)
		def, found := flatGateColumnDefault(t, db, table)
		require.True(t, found, "precondition: %s must still HAVE the column", table)
		require.Nil(t, def, "precondition: %s.actions must have NO default", table)
	}

	// Prove the state is genuinely broken before the guard runs, so the repair below is
	// not asserting against a database that was fine all along.
	brokenWF := &Workflow{
		OrgID: uuid.New(), Name: "pre-repair",
		Trigger:   datatypes.JSON(`{"type":"contact_created"}`),
		Steps:     actionStepsJSON(t, ActionSpec{ID: "a1", Type: "send_email", Params: map[string]any{"to": "x@test.com"}}),
		CreatedBy: uuid.New(),
	}
	require.Error(t, repo.CreateWorkflow(context.Background(), brokenWF),
		"a column that is NOT NULL with no default must reject an INSERT that omits it — "+
			"if this ever passes, re-read whether the column is still NOT NULL")

	repo.ensureLegacyActionsColumn()

	for _, table := range tables {
		def, found := flatGateColumnDefault(t, db, table)
		require.True(t, found, "%s lost its column entirely", table)
		// assert, not require: both tables are reported in one run, because the versions
		// table is where a missing default does the most damage.
		if !assert.NotNil(t, def,
			"%s.actions still has NO default after the guard ran.\n"+
				"An existence-only check (`if n > 0 { continue }`) is what leaves this state\n"+
				"permanent: nothing else in the tree can set this default now that no struct\n"+
				"tag declares the column, so every workflow write 500s with SQLSTATE 23502.", table) {
			continue
		}
		assert.Equal(t, `'[]'::jsonb`, *def, "%s.actions default", table)
	}

	// The behaviour the default exists for, on BOTH tables in one statement pair:
	// CreateWorkflow INSERTs the workflow and then its version snapshot, and neither
	// INSERT names `actions`.
	wf := &Workflow{
		OrgID: uuid.New(), Name: "post-repair",
		Trigger:   datatypes.JSON(`{"type":"contact_created"}`),
		Steps:     actionStepsJSON(t, ActionSpec{ID: "a1", Type: "send_email", Params: map[string]any{"to": "x@test.com"}}),
		CreatedBy: uuid.New(),
	}
	require.NoError(t, repo.CreateWorkflow(context.Background(), wf),
		"after the repair a workflow write must succeed — this is the 500 the guard prevents")

	for _, table := range tables {
		var stored struct {
			Actions string `gorm:"column:actions"`
		}
		require.NoError(t, db.Raw(
			`SELECT actions::text AS actions FROM `+table+` ORDER BY created_at DESC LIMIT 1`).
			Scan(&stored).Error)
		assert.Equal(t, "[]", stored.Actions,
			"%s.actions must fill itself from the DEFAULT, since nothing writes it", table)
	}

	t.Run("running again is a no-op that issues no DDL at all", func(t *testing.T) {
		// The steady-state property stated in the guard's comment, asserted rather than
		// assumed: `ALTER TABLE` takes ACCESS EXCLUSIVE even when it changes nothing, so
		// a guard that re-alters on every boot is the per-boot lock deploy 0 removed.
		// An event trigger is the only way to see DDL that a catalog read cannot.
		require.NoError(t, db.Exec(`CREATE TABLE flatgate_ddl_log (tag text)`).Error)
		require.NoError(t, db.Exec(`
			CREATE FUNCTION flatgate_record_ddl() RETURNS event_trigger AS $$
			BEGIN INSERT INTO flatgate_ddl_log(tag) VALUES (tg_tag); END $$ LANGUAGE plpgsql`).Error)
		require.NoError(t, db.Exec(
			`CREATE EVENT TRIGGER flatgate_ddl ON ddl_command_end EXECUTE FUNCTION flatgate_record_ddl()`).Error)
		t.Cleanup(func() {
			_ = db.Exec(`DROP EVENT TRIGGER IF EXISTS flatgate_ddl`).Error
			_ = db.Exec(`DROP FUNCTION IF EXISTS flatgate_record_ddl()`).Error
			_ = db.Exec(`DROP TABLE IF EXISTS flatgate_ddl_log`).Error
		})

		ddlCount := func() int64 {
			var n int64
			require.NoError(t, db.Raw(`SELECT count(*) FROM flatgate_ddl_log`).Scan(&n).Error)
			return n
		}

		repo.ensureLegacyActionsColumn()
		assert.Zero(t, ddlCount(),
			"a healthy database must issue ZERO DDL: the check-then-ALTER shape exists "+
				"precisely so a boot does not take ACCESS EXCLUSIVE on these tables")

		// …and the trigger is genuinely watching: break one table again and the guard
		// does exactly one repair.
		require.NoError(t, db.Exec(
			`ALTER TABLE `+flatGateWorkflowsTable+` ALTER COLUMN actions DROP DEFAULT`).Error)
		before := ddlCount()
		repo.ensureLegacyActionsColumn()
		assert.Equal(t, before+1, ddlCount(), "exactly one ALTER, for the one broken table")
		def, _ := flatGateColumnDefault(t, db, flatGateWorkflowsTable)
		require.NotNil(t, def)
		assert.Equal(t, `'[]'::jsonb`, *def)
	})
}

// TestEnsureLegacyActionsColumn_ProbesOnlyTheCurrentSchema pins the schema qualification.
//
// information_schema.columns spans EVERY schema on the database. With an unqualified
// `table_name = ?`, a copy of the table in any other namespace — an old restore, a
// per-tenant schema, a staging clone — answers the question, and the guard skips the
// schema we actually write to. The observable failure is the worst-case one: no column
// at all in `public`, so the teardown gate reads UNKNOWN forever and a rollback rejects
// every write, while the probe cheerfully reports the column exists.
func TestEnsureLegacyActionsColumn_ProbesOnlyTheCurrentSchema(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)

	// A decoy in another schema that DOES have the column…
	require.NoError(t, db.Exec(`CREATE SCHEMA flatgate_other`).Error)
	require.NoError(t, db.Exec(
		`CREATE TABLE flatgate_other.`+flatGateWorkflowsTable+` (id uuid, actions jsonb NOT NULL DEFAULT '[]'::jsonb)`).Error)

	// …while the schema we write to has lost it.
	require.NoError(t, db.Exec(`ALTER TABLE `+flatGateWorkflowsTable+` DROP COLUMN actions`).Error)
	_, found := flatGateColumnDefault(t, db, flatGateWorkflowsTable)
	require.False(t, found, "precondition: the current schema must have no `actions` column")

	repo.ensureLegacyActionsColumn()

	def, found := flatGateColumnDefault(t, db, flatGateWorkflowsTable)
	require.True(t, found,
		"the guard skipped a missing column because ANOTHER schema had one — its probe must "+
			"be scoped with `table_schema = current_schema()`")
	require.NotNil(t, def)
	assert.Equal(t, `'[]'::jsonb`, *def)
}

// ── Pure logic (no DB — runs in CI's -short unit job too) ─────────────────────

// TestFlatActionsGateCounts_ClearedAndInert pins the separation the gate is built on:
// Cleared() answers "may the column be dropped", Inert() answers "does anything execute
// nothing". Only the first decides the verdict.
func TestFlatActionsGateCounts_ClearedAndInert(t *testing.T) {
	assert.True(t, FlatActionsGateCounts{}.Cleared())
	assert.Zero(t, FlatActionsGateCounts{}.Inert())

	assert.False(t, FlatActionsGateCounts{WorkflowsBlocking: 1}.Cleared(),
		"a live workflow whose behaviour exists only in `actions` blocks the teardown")
	assert.False(t, FlatActionsGateCounts{VersionsBlocking: 1}.Cleared(),
		"runs execute the PINNED version, so a blocking snapshot blocks too")

	// The whole point of finding 2's second half. These rows are broken at runtime and
	// they are reported — but they lose NOTHING when the column is dropped, so treating
	// them as blockers is what wedged the gate at BLOCKED with no automated remedy.
	inertOnly := FlatActionsGateCounts{
		WorkflowsMissingSteps: 3, WorkflowsEmptySteps: 2,
		VersionsMissingSteps: 1, VersionsEmptySteps: 4,
	}
	assert.True(t, inertOnly.Cleared(),
		"no blocking rows ⇒ the teardown is safe, however many rows have empty steps")
	assert.Equal(t, int64(6), inertOnly.Inert(),
		"Inert counts the EMPTY-steps rows (2+4) — the ones that run and do nothing")
}

// TestFlatActionsGate_VersionsTableHasNoDeletedAt pins the schema asymmetry the gate's
// two halves are built on: automation_workflows is soft-deleted, automation_workflow_versions
// is not. Adding `deleted_at IS NULL` to the versions query would be a SQL error, not a
// stricter filter.
func TestFlatActionsGate_VersionsTableHasNoDeletedAt(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := uuid.New()

	var cols struct {
		Workflows int64 `gorm:"column:workflows"`
		Versions  int64 `gorm:"column:versions"`
	}
	require.NoError(t, db.Raw(`
		SELECT
			count(*) FILTER (WHERE table_name = 'automation_workflows')          AS workflows,
			count(*) FILTER (WHERE table_name = 'automation_workflow_versions')  AS versions
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name IN ('automation_workflows','automation_workflow_versions')
		  AND column_name = 'deleted_at'`).Scan(&cols).Error)
	assert.Equal(t, int64(1), cols.Workflows, "automation_workflows is soft-deleted")
	assert.Equal(t, int64(0), cols.Versions,
		"automation_workflow_versions has NO deleted_at; the gate must not predicate on one")

	// And the gate applies that asymmetry: a soft-deleted workflow whose behaviour lives
	// only in `actions` does NOT block (it can never run again), while its version
	// snapshot DOES (an in-flight run pins the snapshot, not the live workflow).
	actions := flatGateActions(t, ActionSpec{ID: "a1", Type: "send_email"})
	deletedWF := flatGateWorkflow(t, db, orgID, actions, nil, true)
	flatGateVersion(t, db, deletedWF, 1, actions, nil)

	c, err := repo.CountFlatActionsGate(ctx)
	require.NoError(t, err)
	assert.Zero(t, c.WorkflowsBlocking,
		"a soft-deleted workflow can never run, so it must not wedge the gate")
	assert.Equal(t, int64(1), c.VersionsBlocking,
		"its version snapshot still counts — that table has no deleted_at to scope by")
}

// ── DB-backed: the boot diagnostic ────────────────────────────────────────────

// TestFlatActionsGate_CountsAndVerdict is the test that actually unblocks the rest of
// R5: it pins the counts and the log line someone will read off Railway instead of
// opening a production SQL console.
func TestFlatActionsGate_CountsAndVerdict(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := uuid.New()

	// A fresh container: AutoMigrate has run over empty tables, so the gate starts CLEAR.
	start, err := repo.CountFlatActionsGate(ctx)
	require.NoError(t, err)
	require.True(t, start.Cleared(), "an empty database clears the gate: %+v", start)

	good := flatGateActions(t, ActionSpec{ID: "a1", Type: "send_email"})
	goodStepsText := flatGateStepsText(t, ActionSpec{ID: "a1", Type: "send_email"})

	// The seeded mix. Every row below is here to move exactly one number.
	missingA := flatGateWorkflow(t, db, orgID, good, nil, false)                     // steps SQL NULL
	flatGateWorkflow(t, db, orgID, good, flatGateJSONPtr(`null`), false)             // steps jsonb 'null'
	emptyWF := flatGateWorkflow(t, db, orgID, `[]`, flatGateJSONPtr(`[]`), false)    // executes NOTHING
	healthy := flatGateWorkflow(t, db, orgID, good, &goodStepsText, false)           // fine
	softDeleted := flatGateWorkflow(t, db, orgID, good, nil, true)                   // excluded
	flatGateVersion(t, db, missingA, 1, good, nil)                                   // versions_missing
	flatGateVersion(t, db, softDeleted, 1, good, nil)                                // versions_missing (counted!)
	flatGateVersion(t, db, emptyWF, 1, `[]`, flatGateJSONPtr(`[]`))                  // versions_empty
	flatGateVersion(t, db, healthy, 1, good, &goodStepsText)                         // fine

	got, err := repo.CountFlatActionsGate(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), got.WorkflowsMissingSteps,
		"the two steps-less LIVE workflows; the soft-deleted one must NOT be counted")
	assert.Equal(t, int64(1), got.WorkflowsEmptySteps,
		"steps='[]' takes the steps path in the engine and executes nothing — it must be counted")
	assert.Equal(t, int64(2), got.VersionsMissingSteps,
		"including the soft-deleted workflow's version row: that table has no deleted_at")
	assert.Equal(t, int64(1), got.VersionsEmptySteps)
	assert.Equal(t, int64(2), got.WorkflowsBlocking,
		"the two steps-less LIVE workflows carry real flat actions; emptyWF does not")
	assert.Equal(t, int64(2), got.VersionsBlocking)
	assert.Equal(t, int64(2), got.Inert(), "one empty-steps workflow + one empty-steps version")
	assert.False(t, got.Cleared())

	t.Run("a blocked gate logs one greppable BLOCKED line at a visible level", func(t *testing.T) {
		var buf bytes.Buffer
		repo.LogFlatActionsGate(ctx, slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
		out := buf.String()

		assert.Contains(t, out, FlatActionsGateLogPrefix, "the prefix is the whole point — it is what gets grepped")
		assert.Contains(t, out, `"verdict":"BLOCKED"`)
		assert.Contains(t, out, `"level":"WARN"`, "a blocked gate must not hide at DEBUG")
		assert.Contains(t, out, `"workflows_blocking":2`)
		assert.Contains(t, out, `"versions_blocking":2`)
		assert.Contains(t, out, `"inert_rows":2`)
		assert.Contains(t, out, `"workflows_missing_steps":2`)
		assert.Contains(t, out, `"workflows_empty_steps":1`)
		assert.Contains(t, out, `"versions_missing_steps":2`)
		assert.Contains(t, out, `"versions_empty_steps":1`)
		assert.Equal(t, 1, strings.Count(out, FlatActionsGateLogPrefix), "exactly ONE line per boot")
	})

	t.Run("clearing every count flips the verdict to an unambiguous CLEAR", func(t *testing.T) {
		// Fix the live rows. The soft-deleted workflow is deliberately LEFT steps-less:
		// the gate must clear anyway, which is the asymmetry stated out loud.
		require.NoError(t, db.Exec(
			`UPDATE automation_workflows SET steps = ?::jsonb WHERE deleted_at IS NULL`, goodStepsText).Error)
		require.NoError(t, db.Exec(
			`UPDATE automation_workflow_versions SET steps = ?::jsonb`, goodStepsText).Error)

		cleared, err := repo.CountFlatActionsGate(ctx)
		require.NoError(t, err)
		assert.True(t, cleared.Cleared(),
			"a soft-deleted workflow with no steps must not wedge the gate forever: %+v", cleared)
		assert.Equal(t, flatGateSQLNull, flatGateSteps(t, db, flatGateWorkflowsTable, softDeleted),
			"…and it is still steps-less, so the clear is genuinely the deleted_at scoping")

		var buf bytes.Buffer
		repo.LogFlatActionsGate(ctx, slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
		out := buf.String()
		assert.Contains(t, out, FlatActionsGateLogPrefix)
		assert.Contains(t, out, `"verdict":"CLEAR"`)
		assert.Contains(t, out, `"level":"INFO"`)
		assert.Contains(t, out, "ZERO", "the cleared case must say so in words, not only in numbers")
		assert.Contains(t, out, `"workflows_blocking":0`)
		assert.Contains(t, out, `"versions_blocking":0`)
		assert.Contains(t, out, `"inert_rows":0`)
		assert.Contains(t, out, `"workflows_missing_steps":0`)
		assert.Contains(t, out, `"versions_empty_steps":0`)
	})

	t.Run("a nil logger falls back to slog.Default rather than panicking", func(t *testing.T) {
		assert.NotPanics(t, func() { repo.LogFlatActionsGate(ctx, nil) })
	})

	t.Run("a gate query that fails degrades to UNKNOWN and never blocks boot", func(t *testing.T) {
		// The diagnostic is the LAST thing AutoMigrate does; if it could return an error
		// up the stack, a broken read here would refuse the deploy. Prove it cannot, by
		// taking the versions table away underneath it.
		require.NoError(t, db.Exec(`ALTER TABLE automation_workflow_versions RENAME TO flatgate_versions_stash`).Error)
		defer func() {
			require.NoError(t, db.Exec(`ALTER TABLE flatgate_versions_stash RENAME TO automation_workflow_versions`).Error)
		}()

		_, err := repo.CountFlatActionsGate(ctx)
		require.Error(t, err, "the count itself does surface the error")

		var buf bytes.Buffer
		assert.NotPanics(t, func() {
			repo.LogFlatActionsGate(ctx, slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
		})
		out := buf.String()
		assert.Contains(t, out, FlatActionsGateLogPrefix)
		assert.Contains(t, out, `"verdict":"UNKNOWN"`)
		assert.NotContains(t, out, `"verdict":"CLEAR"`,
			"a gate that could not run is NOT a cleared gate")
	})
}

// TestFlatActionsGate_BlockingVsInert separates the two classes of row the gate reports,
// because conflating them is what made the gate un-clearable.
//
// BLOCKING: `actions` holds behaviour that `steps` does not. Dropping the column loses
// it. Only these decide the verdict.
//
// INERT: steps = '[]', so the row runs the steps interpreter over zero steps and
// completes having done nothing. A real defect — reported, at Warn — but it loses
// nothing when the column is dropped, so it must NOT read as BLOCKED. The first cut of
// this gate never looked at `actions` at all and so reported these as blockers: the
// backfill could not reach them either, which is a verdict=BLOCKED on every boot with no
// automated remedy and a 2026-09-01 deadline sailing past.
func TestFlatActionsGate_BlockingVsInert(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := uuid.New()

	logLine := func(t *testing.T) string {
		t.Helper()
		var buf bytes.Buffer
		repo.LogFlatActionsGate(ctx, slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
		return buf.String()
	}

	real := flatGateActions(t, ActionSpec{ID: "a1", Type: "send_email"})

	t.Run("INERT: empty actions AND empty steps is CLEAR, but still reported", func(t *testing.T) {
		wf := flatGateWorkflow(t, db, orgID, `[]`, flatGateJSONPtr(`[]`), false)
		ver := flatGateVersion(t, db, wf, 1, `[]`, flatGateJSONPtr(`[]`))
		t.Cleanup(func() {
			require.NoError(t, db.Exec(`DELETE FROM automation_workflow_versions WHERE id = ?`, ver).Error)
			require.NoError(t, db.Exec(`DELETE FROM automation_workflows WHERE id = ?`, wf).Error)
		})

		c, err := repo.CountFlatActionsGate(ctx)
		require.NoError(t, err)
		assert.Zero(t, c.WorkflowsBlocking, "nothing to lose: `actions` is empty too")
		assert.Zero(t, c.VersionsBlocking)
		assert.True(t, c.Cleared(), "%+v", c)
		assert.Equal(t, int64(2), c.Inert(), "…and both rows are still reported as executing nothing")

		out := logLine(t)
		assert.Contains(t, out, `"verdict":"CLEAR"`, "safe to tear down")
		assert.Contains(t, out, `"inert_rows":2`, "…and separately, two rows do nothing")
		assert.Contains(t, out, `"level":"WARN"`,
			"a workflow that runs and executes nothing must not be buried at INFO")
		assert.Contains(t, out, "SEPARATELY",
			"the line has to say IN WORDS that the inert rows are not the teardown's problem")
	})

	t.Run("BLOCKING: real actions with steps missing", func(t *testing.T) {
		wf := flatGateWorkflow(t, db, orgID, real, nil, false)
		t.Cleanup(func() { require.NoError(t, db.Exec(`DELETE FROM automation_workflows WHERE id = ?`, wf).Error) })

		c, err := repo.CountFlatActionsGate(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(1), c.WorkflowsBlocking)
		assert.False(t, c.Cleared())
		assert.Contains(t, logLine(t), `"verdict":"BLOCKED"`)
	})

	// This shape is no longer CREATABLE — ValidateWorkflowPayload rejects steps='[]'
	// outright now that there is no flat fallback to fall through to — but rows created
	// before that landed still exist, and the gate still has to score them correctly.
	t.Run("BLOCKING: real actions with steps='[]' (a legacy row)", func(t *testing.T) {
		wf := flatGateWorkflow(t, db, orgID, real, flatGateJSONPtr(`[]`), false)
		t.Cleanup(func() { require.NoError(t, db.Exec(`DELETE FROM automation_workflows WHERE id = ?`, wf).Error) })

		c, err := repo.CountFlatActionsGate(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(1), c.WorkflowsBlocking,
			"empty steps + real actions loses behaviour on drop, exactly like missing steps")
		assert.Equal(t, int64(1), c.Inert(), "it is BOTH: it executes nothing today AND blocks the teardown")
		assert.False(t, c.Cleared())

		// The remedy is now a human writing a steps tree, not a boot-time backfill.
		goodStepsText := flatGateStepsText(t, ActionSpec{ID: "a1", Type: "send_email"})
		require.NoError(t, db.Exec(
			`UPDATE automation_workflows SET steps = ?::jsonb WHERE id = ?`, goodStepsText, wf).Error)
		after, err := repo.CountFlatActionsGate(ctx)
		require.NoError(t, err)
		assert.True(t, after.Cleared(), "%+v", after)
		assert.Zero(t, after.Inert())
	})

	t.Run("BLOCKING: an unconvertible actions blob nobody can convert automatically", func(t *testing.T) {
		wf := flatGateWorkflow(t, db, orgID, `{"type":"send_email"}`, nil, false)
		t.Cleanup(func() { require.NoError(t, db.Exec(`DELETE FROM automation_workflows WHERE id = ?`, wf).Error) })

		c, err := repo.CountFlatActionsGate(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(1), c.WorkflowsBlocking,
			"a non-array `actions` is not empty; nobody may drop the column until a human has looked")
		assert.False(t, c.Cleared())
	})

	t.Run("a version snapshot blocks even when its workflow is fine", func(t *testing.T) {
		goodStepsText := flatGateStepsText(t, ActionSpec{ID: "a1", Type: "send_email"})
		wf := flatGateWorkflow(t, db, orgID, real, &goodStepsText, false)
		ver := flatGateVersion(t, db, wf, 1, real, nil)
		t.Cleanup(func() {
			require.NoError(t, db.Exec(`DELETE FROM automation_workflow_versions WHERE id = ?`, ver).Error)
			require.NoError(t, db.Exec(`DELETE FROM automation_workflows WHERE id = ?`, wf).Error)
		})

		c, err := repo.CountFlatActionsGate(ctx)
		require.NoError(t, err)
		assert.Zero(t, c.WorkflowsBlocking)
		assert.Equal(t, int64(1), c.VersionsBlocking, "runs execute the PINNED version, not the live workflow")
		assert.False(t, c.Cleared())
	})
}
