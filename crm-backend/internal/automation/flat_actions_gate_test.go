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

// flat_actions_gate_test.go covers R5 DEPLOY 0: the `actions` column default that lets
// the column outlive the Go field, the actions→steps backfill that runs on every boot,
// and the log line that reports whether the deprecated Actions field can be removed yet.
//
// MigrateFlatActionsToSteps had ZERO tests before this file, which is how it shipped a
// defect that made some rows permanently un-gateable: a row with actions = '[]' was
// rewritten to steps = 'null'::jsonb (json.Marshal of a nil slice), which still matches
// the migration's own WHERE clause — so the row was re-written on every boot, forever,
// and could never satisfy the gate.
//
// THE THREE THINGS HERE THAT ARE LOAD-BEARING RATHER THAN DESCRIPTIVE:
//
//  1. TestFlatActionsColumnDefault — the only assertion anywhere that deploy 0's actual
//     change (a struct tag) reached the database. Every other test in this file passes
//     with the tag deleted.
//  2. The blocking-vs-inert split (TestFlatActionsGate_BlockingVsInert) — the gate must
//     be clearable, and it is only clearable if "steps are empty" and "the teardown is
//     unsafe" are answered separately.
//  3. The updated_at assertions — they are what keeps the backfill converging now that
//     its predicate deliberately re-selects already-converged rows.
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
// `actions` and `steps` jsonb. Raw SQL rather than the GORM model on purpose: the
// states this file is about (steps SQL-NULL vs jsonb 'null'; actions '[]' vs 'null' vs
// a non-array object) are states datatypes.JSON cannot express unambiguously, and
// seeding through the model is how a test ends up asserting against a value it never
// actually created.
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
func flatGateActions(t *testing.T, actions ...ActionSpec) string {
	t.Helper()
	raw, err := json.Marshal(actions)
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

// TestFlatActionsColumnDefault is the ONLY thing in the repo that asserts R5 deploy 0's
// one load-bearing change actually reached the database.
//
// Deploy 0 exists to put `default:'[]'::jsonb` on both Actions tags and get it past the
// point of rollback BEFORE deploy 1 deletes the Go field. AutoMigrate never DROPs a
// column, so after deploy 1 the column is still `actions jsonb NOT NULL` while GORM has
// stopped naming it in INSERTs — and the column default is the only thing that fills it.
// Without it every workflow write dies on
//
//	ERROR: null value in column "actions" … violates not-null constraint (SQLSTATE 23502)
//
// killing CreateWorkflow on its first statement and UpdateWorkflow on its version
// snapshot (which rolls the whole update back).
//
// A struct tag is deletable by a merge conflict or by anyone "tidying" the model, and
// every other test in this file passes with the tag gone — verified by overlaying a
// models.go without it. Nothing else in the tree reads column_default. This does.
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
		require.NoError(t, db.Raw(`
			SELECT column_default, is_nullable, true AS found
			FROM information_schema.columns
			WHERE table_name = ? AND column_name = 'actions'`, table).Scan(&row).Error)
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
				"AutoMigrate did not set it, which means `default:'[]'::jsonb` is missing from the\n"+
				"Actions struct tag in models.go (or a boot ALTER timed out before applying it).\n"+
				"That default is not cosmetic: it is the single thing that lets this COLUMN outlive\n"+
				"the Go FIELD during the R5 teardown. Ship deploy 1 without it and every workflow\n"+
				"write returns 500 with SQLSTATE 23502 (null value in column \"actions\"), with every\n"+
				"workflow UPDATE rolled back by its version-snapshot INSERT. Restore the tag.", table) {
			continue
		}
		assert.Equal(t, want, *row.Default,
			"%s.actions default must be exactly %s so the column fills itself with an empty\n"+
				"action list once the Go field is gone", table, want)
	}
}

// ── Pure logic (no DB — runs in CI's -short unit job too) ─────────────────────

// TestStepsFromFlatActions pins the conversion, and above all pins the one case that
// used to produce the four bytes `null`: an EMPTY actions array. `var steps []StepSpec`
// with a loop body that never runs marshals to "null", not "[]".
func TestStepsFromFlatActions(t *testing.T) {
	t.Run("empty actions array converts to an empty steps array, never to null", func(t *testing.T) {
		steps, ok := stepsFromFlatActions(datatypes.JSON([]byte(`[]`)))
		require.True(t, ok, "an empty actions array IS convertible — it converts to no steps")
		assert.Equal(t, "[]", string(steps),
			"json.Marshal of a nil slice is `null`; writing that back re-selects the row on every boot")
	})

	t.Run("actions convert to action and delay steps", func(t *testing.T) {
		raw := flatGateActions(t,
			ActionSpec{ID: "a1", Type: "send_email", Params: map[string]any{"to": "x@example.com"}},
			ActionSpec{ID: "d1", Type: "delay", Params: map[string]any{"duration_sec": 90}},
		)
		out, ok := stepsFromFlatActions(datatypes.JSON([]byte(raw)))
		require.True(t, ok)
		steps := flatGateParseSteps(t, string(out))

		require.Len(t, steps, 2)
		assert.Equal(t, "action", steps[0].Type)
		assert.Equal(t, "a1", steps[0].ID)
		require.NotNil(t, steps[0].Action)
		assert.Equal(t, "send_email", steps[0].Action.Type)
		assert.Equal(t, "x@example.com", steps[0].Action.Params["to"])

		assert.Equal(t, "delay", steps[1].Type)
		assert.Equal(t, "d1", steps[1].ID)
		require.NotNil(t, steps[1].Delay)
		assert.Equal(t, 90, steps[1].Delay.DurationSec)
		assert.Nil(t, steps[1].Action, "a delay step carries no action")
	})

	t.Run("every action gets its OWN action pointer", func(t *testing.T) {
		// A shared pointer would give every step the last action's params — the classic
		// loop-variable capture. Go 1.22+ makes this safe; the assertion keeps it that way.
		raw := flatGateActions(t,
			ActionSpec{ID: "a1", Type: "send_email"},
			ActionSpec{ID: "a2", Type: "create_task"},
		)
		out, ok := stepsFromFlatActions(datatypes.JSON([]byte(raw)))
		require.True(t, ok)
		steps := flatGateParseSteps(t, string(out))
		require.Len(t, steps, 2)
		require.NotNil(t, steps[0].Action)
		require.NotNil(t, steps[1].Action)
		assert.Equal(t, "send_email", steps[0].Action.Type)
		assert.Equal(t, "create_task", steps[1].Action.Type)
	})

	t.Run("unconvertible blobs report not-ok rather than writing something wrong", func(t *testing.T) {
		for name, raw := range map[string]string{
			"jsonb null":        `null`,
			"a JSON object":     `{"type":"send_email"}`,
			"a JSON string":     `"send_email"`,
			"a JSON number":     `7`,
			"an absent column":  ``,
			"malformed garbage": `[{`,
		} {
			out, ok := stepsFromFlatActions(datatypes.JSON([]byte(raw)))
			assert.False(t, ok, "%s must not convert", name)
			assert.Nil(t, out, "%s must not produce a value to write", name)
		}
	})
}

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

// ── DB-backed: the backfill ───────────────────────────────────────────────────

func TestMigrateFlatActionsToSteps(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := uuid.New()

	t.Run("a flat-actions row converts to a steps tree, in BOTH tables", func(t *testing.T) {
		actions := flatGateActions(t,
			ActionSpec{ID: "a1", Type: "send_email", Params: map[string]any{"to": "x@example.com"}},
			ActionSpec{ID: "d1", Type: "delay", Params: map[string]any{"duration_sec": 60}},
		)
		// steps SQL-NULL on the workflow, jsonb 'null' on the version: both halves of the
		// migration's WHERE clause, one per table.
		wfID := flatGateWorkflow(t, db, orgID, actions, nil, false)
		verID := flatGateVersion(t, db, wfID, 1, actions, flatGateJSONPtr(`null`))

		require.NoError(t, repo.MigrateFlatActionsToSteps(ctx))

		for table, id := range map[string]uuid.UUID{
			flatGateWorkflowsTable: wfID,
			flatGateVersionsTable:  verID,
		} {
			steps := flatGateParseSteps(t, flatGateSteps(t, db, table, id))
			require.Len(t, steps, 2, "table %s", table)
			assert.Equal(t, "action", steps[0].Type)
			assert.Equal(t, "a1", steps[0].ID)
			require.NotNil(t, steps[0].Action)
			assert.Equal(t, "send_email", steps[0].Action.Type)
			assert.Equal(t, "delay", steps[1].Type)
			require.NotNil(t, steps[1].Delay)
			assert.Equal(t, 60, steps[1].Delay.DurationSec)
			assert.False(t, flatGateStillSelected(t, db, table, id),
				"table %s: a converted row must not be re-selected next boot", table)
		}
	})

	t.Run("actions='[]' becomes steps='[]', NOT 'null'", func(t *testing.T) {
		wfID := flatGateWorkflow(t, db, orgID, `[]`, nil, false)
		verID := flatGateVersion(t, db, wfID, 2, `[]`, nil)

		require.NoError(t, repo.MigrateFlatActionsToSteps(ctx))

		assert.Equal(t, "[]", flatGateSteps(t, db, flatGateWorkflowsTable, wfID),
			"the defect wrote 'null' here, which re-matches the migration's own WHERE clause")
		assert.Equal(t, "[]", flatGateSteps(t, db, flatGateVersionsTable, verID))
		// These rows ARE re-selected next boot (the predicate now includes '[]') and
		// that is fine: rule 2 skips the UPDATE, so nothing churns — asserted by the
		// updated_at subtest below. What matters is that they no longer BLOCK.
		gate, err := repo.CountFlatActionsGate(ctx)
		require.NoError(t, err)
		assert.True(t, gate.Cleared(),
			"an empty-actions row loses nothing when the column is dropped: %+v", gate)
	})

	// The row shape finding 2 is about: real flat actions with steps ALREADY '[]'. It is
	// creatable through the live API today (handlers.go's hasSteps("[]") is false, so
	// nothing derives actions from steps and validateActions accepts it), the OLD
	// predicate could never reach it, and the gate blocked on it — a permanently
	// BLOCKED gate with no automated remedy.
	t.Run("real actions with steps='[]' ARE reachable and get converted", func(t *testing.T) {
		actions := flatGateActions(t,
			ActionSpec{ID: "a1", Type: "send_email", Params: map[string]any{"to": "y@example.com"}})
		wfID := flatGateWorkflow(t, db, orgID, actions, flatGateJSONPtr(`[]`), false)
		verID := flatGateVersion(t, db, wfID, 3, actions, flatGateJSONPtr(`[]`))

		before, err := repo.CountFlatActionsGate(ctx)
		require.NoError(t, err)
		require.False(t, before.Cleared(),
			"precondition: this row's behaviour lives ONLY in `actions`, so it blocks: %+v", before)

		require.NoError(t, repo.MigrateFlatActionsToSteps(ctx))

		for table, id := range map[string]uuid.UUID{
			flatGateWorkflowsTable: wfID,
			flatGateVersionsTable:  verID,
		} {
			steps := flatGateParseSteps(t, flatGateSteps(t, db, table, id))
			require.Len(t, steps, 1, "table %s: the empty steps array must be replaced, not kept", table)
			require.NotNil(t, steps[0].Action)
			assert.Equal(t, "send_email", steps[0].Action.Type, "table %s", table)
		}

		after, err := repo.CountFlatActionsGate(ctx)
		require.NoError(t, err)
		assert.True(t, after.Cleared(),
			"the backfill must be able to UNBLOCK the gate on its own, without a SQL console: %+v", after)
	})

	t.Run("a second run does not rewrite anything — no updated_at churn", func(t *testing.T) {
		// One row of each converged shape: an empty-actions row (the churn defect's own
		// case) and a real one.
		empty := flatGateWorkflow(t, db, orgID, `[]`, nil, false)
		full := flatGateWorkflow(t, db, orgID,
			flatGateActions(t, ActionSpec{ID: "a1", Type: "send_email"}), nil, false)

		require.NoError(t, repo.MigrateFlatActionsToSteps(ctx))
		emptyAt := flatGateUpdatedAt(t, db, empty)
		fullAt := flatGateUpdatedAt(t, db, full)

		// A bare second call would be satisfied by clock resolution alone, so make the
		// gap unambiguous: any UPDATE after this point lands on a visibly later stamp.
		time.Sleep(50 * time.Millisecond)
		require.NoError(t, repo.MigrateFlatActionsToSteps(ctx))

		// The empty-actions row is STILL selected every boot — '[]' is in the predicate
		// on purpose — so this assertion is exactly the one that proves rule 2 (skip the
		// UPDATE when the value would not change) is a LIVE anti-churn control and not
		// the unreachable guard it was once documented as. It is also what holds if rule
		// 1's empty-slice fix is ever reverted.
		require.True(t, flatGateStillSelected(t, db, flatGateWorkflowsTable, empty),
			"precondition: the widened predicate re-selects this row on every boot")
		assert.True(t, emptyAt.Equal(flatGateUpdatedAt(t, db, empty)),
			"selected but NOT rewritten — rule 2 is what stops the every-boot updated_at churn")
		assert.True(t, fullAt.Equal(flatGateUpdatedAt(t, db, full)),
			"a converged row must not be re-written")
		assert.Equal(t, "[]", flatGateSteps(t, db, flatGateWorkflowsTable, empty))
	})

	t.Run("unconvertible rows are left alone and do not error the boot", func(t *testing.T) {
		jsonbNull := flatGateWorkflow(t, db, orgID, `null`, nil, false)
		notAnArray := flatGateWorkflow(t, db, orgID, `{"type":"send_email"}`, nil, false)
		verNull := flatGateVersion(t, db, jsonbNull, 1, `null`, nil)
		verObject := flatGateVersion(t, db, notAnArray, 1, `{"type":"send_email"}`, nil)

		before := map[uuid.UUID]time.Time{
			jsonbNull:  flatGateUpdatedAt(t, db, jsonbNull),
			notAnArray: flatGateUpdatedAt(t, db, notAnArray),
		}

		// The boot must survive them. This is the assertion that keeps a hand-written
		// row, or an older schema's blob, from taking the whole server down.
		require.NoError(t, repo.MigrateFlatActionsToSteps(ctx))

		for _, id := range []uuid.UUID{jsonbNull, notAnArray} {
			assert.Equal(t, flatGateSQLNull, flatGateSteps(t, db, flatGateWorkflowsTable, id),
				"an unconvertible row must be left exactly as found, not stamped with '[]'")
			assert.True(t, before[id].Equal(flatGateUpdatedAt(t, db, id)))
			assert.True(t, flatGateStillSelected(t, db, flatGateWorkflowsTable, id),
				"it stays selected — that is honest, and the gate is what makes it visible")
		}
		for _, id := range []uuid.UUID{verNull, verObject} {
			assert.Equal(t, flatGateSQLNull, flatGateSteps(t, db, flatGateVersionsTable, id))
		}
	})
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
		WHERE table_name IN ('automation_workflows','automation_workflow_versions')
		  AND column_name = 'deleted_at'`).Scan(&cols).Error)
	assert.Equal(t, int64(1), cols.Workflows, "automation_workflows is soft-deleted")
	assert.Equal(t, int64(0), cols.Versions,
		"automation_workflow_versions has NO deleted_at; the gate must not predicate on one")

	// A soft-deleted workflow is skipped by the migration (GORM scopes the Find to
	// deleted_at IS NULL) while its version row is still converted. That asymmetry is
	// deliberate: the workflow can never run again, but its version snapshot is what an
	// in-flight run executes.
	actions := flatGateActions(t, ActionSpec{ID: "a1", Type: "send_email"})
	deletedWF := flatGateWorkflow(t, db, orgID, actions, nil, true)
	itsVersion := flatGateVersion(t, db, deletedWF, 1, actions, nil)

	require.NoError(t, repo.MigrateFlatActionsToSteps(ctx))

	assert.Equal(t, flatGateSQLNull, flatGateSteps(t, db, flatGateWorkflowsTable, deletedWF),
		"a soft-deleted workflow is not migrated — it can never run")
	assert.Len(t, flatGateParseSteps(t, flatGateSteps(t, db, flatGateVersionsTable, itsVersion)), 1,
		"its version snapshot IS migrated — an in-flight run still pins it")
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
	goodSteps, ok := stepsFromFlatActions(datatypes.JSON([]byte(good)))
	require.True(t, ok)
	goodStepsText := string(goodSteps)

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

	t.Run("BLOCKING: real actions with steps='[]' — the API-creatable shape", func(t *testing.T) {
		wf := flatGateWorkflow(t, db, orgID, real, flatGateJSONPtr(`[]`), false)
		t.Cleanup(func() { require.NoError(t, db.Exec(`DELETE FROM automation_workflows WHERE id = ?`, wf).Error) })

		c, err := repo.CountFlatActionsGate(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(1), c.WorkflowsBlocking,
			"empty steps + real actions loses behaviour on drop, exactly like missing steps")
		assert.Equal(t, int64(1), c.Inert(), "it is BOTH: it executes nothing today AND blocks the teardown")
		assert.False(t, c.Cleared())

		// And the backfill can now reach it, so this is a blocker with a remedy.
		require.NoError(t, repo.MigrateFlatActionsToSteps(ctx))
		after, err := repo.CountFlatActionsGate(ctx)
		require.NoError(t, err)
		assert.True(t, after.Cleared(), "%+v", after)
		assert.Zero(t, after.Inert())
	})

	t.Run("BLOCKING: an unconvertible actions blob the backfill cannot fix", func(t *testing.T) {
		wf := flatGateWorkflow(t, db, orgID, `{"type":"send_email"}`, nil, false)
		t.Cleanup(func() { require.NoError(t, db.Exec(`DELETE FROM automation_workflows WHERE id = ?`, wf).Error) })

		require.NoError(t, repo.MigrateFlatActionsToSteps(ctx))
		c, err := repo.CountFlatActionsGate(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(1), c.WorkflowsBlocking,
			"a non-array `actions` is not empty; nobody may drop the column until a human has looked")
		assert.False(t, c.Cleared())
	})

	t.Run("a version snapshot blocks even when its workflow is fine", func(t *testing.T) {
		goodSteps, ok := stepsFromFlatActions(datatypes.JSON([]byte(real)))
		require.True(t, ok)
		goodStepsText := string(goodSteps)
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
