package automation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// task_trigger_test.go covers the task automation surface added alongside the
// Task.Status field: the task_status_changed trigger's validation, the "task"
// schema entity (what makes date_field-on-task.due_at and task-field conditions
// work), and the task→contact/deal hydration a task-triggered run needs for
// {{contact.*}}/{{deal.*}} to resolve.

// ── trigger type + validation ────────────────────────────────────────────────

func TestIsValidTriggerType_Task(t *testing.T) {
	assert.True(t, IsValidTriggerType("task_created"), "generic CRUD via the {slug}_created suffix pattern")
	assert.True(t, IsValidTriggerType("task_updated"))
	assert.True(t, IsValidTriggerType("task_deleted"))
	assert.True(t, IsValidTriggerType("task_status_changed"), "bespoke, like deal_stage_changed")
}

func TestValidateWorkflowPayload_TaskStatusChanged_RequiresParams(t *testing.T) {
	trigger := `{"type":"task_status_changed"}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, stepsFromActionsJSON(t, []byte(actions)))
	assert.False(t, result.Valid, "task_status_changed requires params")
}

func TestValidateWorkflowPayload_TaskStatusChanged_RequiresToStatus(t *testing.T) {
	trigger := `{"type":"task_status_changed","params":{"other":"value"}}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, stepsFromActionsJSON(t, []byte(actions)))
	assert.False(t, result.Valid)
}

func TestValidateWorkflowPayload_TaskStatusChanged_RejectsAnUnknownStatus(t *testing.T) {
	// Task.Status is a closed, compile-time-known enum (unlike deal stages, a
	// free-form id the validator has no DB access to check) — a typo must be
	// caught at save, not silently never match at run time.
	trigger := `{"type":"task_status_changed","params":{"to_status":"archived"}}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, stepsFromActionsJSON(t, []byte(actions)))
	assert.False(t, result.Valid)
}

func TestValidateWorkflowPayload_TaskStatusChanged_Valid(t *testing.T) {
	trigger := `{"type":"task_status_changed","params":{"to_status":"completed"}}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, stepsFromActionsJSON(t, []byte(actions)))
	assert.True(t, result.Valid, "%+v", result.Errors)
}

func TestValidateWorkflowPayload_TaskStatusChanged_WithFromStatus(t *testing.T) {
	trigger := `{"type":"task_status_changed","params":{"to_status":"in_progress","from_status":"open"}}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, stepsFromActionsJSON(t, []byte(actions)))
	assert.True(t, result.Valid, "%+v", result.Errors)
}

func TestValidateWorkflowPayload_TaskStatusChanged_WildcardFromStatus(t *testing.T) {
	trigger := `{"type":"task_status_changed","params":{"to_status":"completed","from_status":"*"}}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, stepsFromActionsJSON(t, []byte(actions)))
	assert.True(t, result.Valid, "%+v", result.Errors)
}

func TestValidateWorkflowPayload_TaskStatusChanged_RejectsAnUnknownFromStatus(t *testing.T) {
	trigger := `{"type":"task_status_changed","params":{"to_status":"completed","from_status":"archived"}}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, stepsFromActionsJSON(t, []byte(actions)))
	assert.False(t, result.Valid)
}

// ── schema entity ─────────────────────────────────────────────────────────────

func TestBuiltinObjectMeta_IncludesTask(t *testing.T) {
	found := false
	for _, m := range builtinObjectMeta {
		if m.Slug == "task" {
			found = true
			assert.Equal(t, "Task", m.Label)
		}
	}
	assert.True(t, found, "task must be a workflow-schema entity for date_field-on-due_at and task-field conditions to work")
}

func TestBuiltinObjectFieldDefs_Task(t *testing.T) {
	fields := builtinObjectFieldDefs("task")
	byPath := map[string]SchemaField{}
	for _, f := range fields {
		byPath[f.Path] = f
	}

	require.Contains(t, byPath, "task.status")
	assert.Equal(t, "select", byPath["task.status"].Type)
	assert.ElementsMatch(t, []string{"open", "in_progress", "completed"}, byPath["task.status"].Options)

	require.Contains(t, byPath, "task.due_at")
	assert.Equal(t, "date", byPath["task.due_at"].Type, "must be 'date' — this is what makes it selectable in the date_field trigger's field picker")

	require.Contains(t, byPath, "task.assigned_to")
	assert.Equal(t, "user", byPath["task.assigned_to"].PickerType)
}

// ── task → contact/deal hydration ───────────────────────────────────────────

func TestBuildEvalContext_HydratesTaskContact(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`).Error)
	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS company_id UUID`).Error)

	orgID := uuid.New()
	contactID := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name, last_name, email, phone) VALUES (?, ?, 'Jane', 'Doe', 'jane@acme.com', '+1555')`,
		contactID, orgID).Error)

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	triggerCtx := datatypes.JSON(`{
		"entity_id": "` + uuid.New().String() + `",
		"task": {"id": "` + uuid.New().String() + `", "title": "Call Jane", "contact_id": "` + contactID.String() + `"},
		"trigger": {"type": "task_created"}
	}`)
	run := &WorkflowRun{OrgID: orgID, TriggerContext: triggerCtx}

	ctx := engine.buildEvalContext(run)

	require.NotNil(t, ctx.Contact, "a task's contact must be hydrated so {{contact.email}} resolves for a task trigger")
	assert.Equal(t, "jane@acme.com", ctx.Contact["email"])
	assert.Equal(t, "jane@acme.com", InterpolateTemplate("{{contact.email}}", ctx))

	taskExtra, ok := ctx.Extra["task"].(map[string]any)
	require.True(t, ok, "the task itself must still resolve via ctx.Extra — this is the generic path {{task.title}} depends on")
	assert.Equal(t, "Call Jane", taskExtra["title"])
}

func TestBuildEvalContext_HydratesTaskDeal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS deals (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, title TEXT DEFAULT '',
		value NUMERIC DEFAULT 0, probability INT DEFAULT 0, stage_id UUID,
		pipeline_id UUID, contact_id UUID, company_id UUID, owner_user_id UUID,
		is_won BOOLEAN NOT NULL DEFAULT FALSE, is_lost BOOLEAN NOT NULL DEFAULT FALSE,
		expected_close_at TIMESTAMPTZ, closed_at TIMESTAMPTZ, custom_fields JSONB DEFAULT '{}',
		deleted_at TIMESTAMPTZ, created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)

	orgID := uuid.New()
	dealID := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO deals (id, org_id, title, value) VALUES (?, ?, 'Acme renewal', 5000)`, dealID, orgID).Error)

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	triggerCtx := datatypes.JSON(`{
		"task": {"id": "` + uuid.New().String() + `", "title": "Follow up", "deal_id": "` + dealID.String() + `"},
		"trigger": {"type": "task_updated"}
	}`)
	run := &WorkflowRun{OrgID: orgID, TriggerContext: triggerCtx}

	ctx := engine.buildEvalContext(run)
	require.NotNil(t, ctx.Deal, "a task's deal must be hydrated so {{deal.title}} resolves for a task trigger")
	assert.Equal(t, "Acme renewal", ctx.Deal["title"])
	assert.Equal(t, "Acme renewal", InterpolateTemplate("{{deal.title}}", ctx))
}

func TestBuildEvalContext_TaskWithMissingContact_LeavesEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`).Error)
	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS company_id UUID`).Error)

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	triggerCtx := datatypes.JSON(`{
		"task": {"id": "` + uuid.New().String() + `", "contact_id": "` + uuid.New().String() + `"},
		"trigger": {"type": "task_created"}
	}`)
	run := &WorkflowRun{OrgID: uuid.New(), TriggerContext: triggerCtx}

	ctx := engine.buildEvalContext(run)
	assert.Nil(t, ctx.Contact, "a task pointing at a missing contact must not fabricate one")
	assert.Equal(t, "", InterpolateTemplate("{{contact.email}}", ctx))
}

func TestBuildEvalContext_TaskWithNoLinks_NoHydrationAttempted(t *testing.T) {
	// A task with neither contact_id nor deal_id (the common case — most tasks
	// aren't linked to a record) must not even attempt a DB read.
	engine := &Engine{} // no db: a hydration attempt would nil-panic
	triggerCtx := datatypes.JSON(`{"task": {"id": "` + uuid.New().String() + `", "title": "Standalone"}, "trigger": {"type": "task_created"}}`)
	run := &WorkflowRun{OrgID: uuid.New(), TriggerContext: triggerCtx}

	assert.NotPanics(t, func() {
		ctx := engine.buildEvalContext(run)
		assert.Nil(t, ctx.Contact)
		assert.Nil(t, ctx.Deal)
	})
}

// ── date_field backfill scanner for tasks ───────────────────────────────────

func TestScanTasksForBackfill(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS tasks (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, title TEXT DEFAULT '',
		contact_id UUID, deal_id UUID, assigned_to UUID, created_by UUID,
		due_at TIMESTAMPTZ, completed_at TIMESTAMPTZ,
		priority TEXT NOT NULL DEFAULT 'medium', status TEXT NOT NULL DEFAULT 'open',
		last_reminded_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)

	orgID := uuid.New()
	due := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	completedAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	assignee := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO tasks (id, org_id, title, priority, status, due_at, assigned_to) VALUES (?, ?, 'Renew contract', 'high', 'open', ?, ?)`,
		uuid.New(), orgID, due, assignee).Error)
	// A task that is ALREADY completed when the backfill runs — the case a
	// re-materialize-after-activate scan exists for. Its payload must carry
	// completed_at just as taskAutomationMap does for the live event path.
	completedTaskID := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO tasks (id, org_id, title, priority, status, completed_at) VALUES (?, ?, 'Already done', 'medium', 'completed', ?)`,
		completedTaskID, orgID, completedAt).Error)
	// A different org's task must never be scanned.
	require.NoError(t, db.Exec(
		`INSERT INTO tasks (id, org_id, title, priority, status) VALUES (?, ?, 'Other org', 'medium', 'open')`,
		uuid.New(), uuid.New()).Error)
	// A soft-deleted task must never be scanned.
	require.NoError(t, db.Exec(
		`INSERT INTO tasks (id, org_id, title, priority, status, deleted_at) VALUES (?, ?, 'Deleted', 'medium', 'open', NOW())`,
		uuid.New(), orgID).Error)

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	scan, err := engine.scanTasksForBackfill(engine.ctx, db, orgID, 100)
	require.NoError(t, err)
	require.Len(t, scan.records, 2, "%+v", scan.records)

	byID := map[string]map[string]any{}
	for _, r := range scan.records {
		byID[r.id] = r.record
	}

	rec := byID[completedTaskID.String()]
	require.NotNil(t, rec, "%+v", byID)
	assert.Equal(t, completedAt.Format(time.RFC3339), rec["completed_at"],
		"the same field taskAutomationMap includes on the live path must survive the backfill scan, or a task activated after already being completed diverges depending on which path armed its timer")

	openRec := byID[uuidOfTitle(t, scan, "Renew contract")]
	assert.Equal(t, "Renew contract", openRec["title"])
	assert.Equal(t, "high", openRec["priority"])
	assert.Equal(t, "open", openRec["status"])
	assert.Equal(t, assignee.String(), openRec["assigned_to"])
	assert.Equal(t, due.Format(time.RFC3339), openRec["due_at"], "must match parseDateValue's expected layout")
	assert.NotContains(t, openRec, "contact_id", "an absent relation must be omitted, not present-and-empty")
	assert.NotContains(t, openRec, "completed_at", "an open task has no completion time to include")
}

func uuidOfTitle(t *testing.T, scan backfillScan, title string) string {
	t.Helper()
	for _, r := range scan.records {
		if r.record["title"] == title {
			return r.id
		}
	}
	t.Fatalf("no backfill record titled %q among %+v", title, scan.records)
	return ""
}

func TestScanRecordsForBackfill_DispatchesTaskSlugToTheTaskScanner(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS tasks (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, title TEXT DEFAULT '',
		contact_id UUID, deal_id UUID, assigned_to UUID, created_by UUID,
		due_at TIMESTAMPTZ, completed_at TIMESTAMPTZ,
		priority TEXT NOT NULL DEFAULT 'medium', status TEXT NOT NULL DEFAULT 'open',
		last_reminded_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)
	orgID := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO tasks (id, org_id, title, priority, status) VALUES (?, ?, 'x', 'medium', 'open')`,
		uuid.New(), orgID).Error)

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	// scanRecordsForBackfill is what reconcileDateFieldTimers actually calls when
	// a date_field workflow with object="task" is activated — this is the
	// dispatch-table entry that makes "task overdue" reachable without any new
	// scan-trigger machinery, per the design note on builtinObjectMeta.
	scan, err := engine.scanRecordsForBackfill(engine.ctx, db, orgID, "task", 100)
	require.NoError(t, err)
	assert.Len(t, scan.records, 1)
}

// ── task_status_changed runtime filtering ───────────────────────────────────
//
// The trigger's to_status/from_status params are validated at SAVE time
// (validator.go), but validation proves nothing about what runs at EVENT time —
// that is a completely separate code path (triggerEventInternal's per-eventType
// filter blocks). This pins the runtime side directly.

func createTaskStatusChangedWF(t *testing.T, repo *Repository, orgID uuid.UUID, toStatus, fromStatus string) *Workflow {
	t.Helper()
	params := map[string]any{"to_status": toStatus}
	if fromStatus != "" {
		params["from_status"] = fromStatus
	}
	trig, _ := json.Marshal(map[string]any{"type": "task_status_changed", "params": params})
	steps := []StepSpec{{Type: "action", ID: "a1", Action: &ActionSpec{ID: "a1", Type: "test_action", Params: map[string]any{}}}}
	stepsJSON, _ := json.Marshal(steps)
	wf := &Workflow{
		OrgID:     orgID,
		Name:      "task-status-" + uuid.NewString()[:8],
		IsActive:  true,
		Trigger:   datatypes.JSON(trig),
		Steps:     datatypes.JSON(stepsJSON),
		CreatedBy: uuid.New(),
	}
	require.NoError(t, repo.CreateWorkflow(context.Background(), wf))
	return wf
}

func taskStatusChangedPayload(oldStatus, newStatus string) map[string]any {
	return map[string]any{
		"entity_id":  uuid.New().String(),
		"task":       map[string]any{"id": uuid.New().String(), "status": newStatus},
		"old_status": oldStatus,
		"new_status": newStatus,
		"trigger":    map[string]any{"type": "task_status_changed"},
	}
}

func TestTriggerEvent_TaskStatusChanged_SkipsWhenToStatusDoesNotMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgID := uuid.New()
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": &idRecordingExecutor{}})
	defer engine.cancel()

	wf := createTaskStatusChangedWF(t, repo, orgID, "completed", "")

	// The exact false-positive the review caught: a workflow configured for
	// "to completed" must not enroll on a move to in_progress.
	engine.TriggerEvent(context.Background(), orgID, "task_status_changed", taskStatusChangedPayload("open", "in_progress"))
	time.Sleep(50 * time.Millisecond) // give the fan-out goroutine a chance to (wrongly) enroll
	assert.Zero(t, countRuns(t, engine, wf.ID), "to_status='completed' must not enroll on open->in_progress")

	engine.TriggerEvent(context.Background(), orgID, "task_status_changed", taskStatusChangedPayload("in_progress", "completed"))
	require.Eventually(t, func() bool { return countRuns(t, engine, wf.ID) == 1 }, 2*time.Second, 20*time.Millisecond,
		"to_status='completed' must enroll on a move that actually lands on completed")
}

func TestTriggerEvent_TaskStatusChanged_FromStatusFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgID := uuid.New()
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": &idRecordingExecutor{}})
	defer engine.cancel()

	wf := createTaskStatusChangedWF(t, repo, orgID, "completed", "in_progress")

	engine.TriggerEvent(context.Background(), orgID, "task_status_changed", taskStatusChangedPayload("open", "completed"))
	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, countRuns(t, engine, wf.ID), "from_status='in_progress' must not match a move that started at open")

	engine.TriggerEvent(context.Background(), orgID, "task_status_changed", taskStatusChangedPayload("in_progress", "completed"))
	require.Eventually(t, func() bool { return countRuns(t, engine, wf.ID) == 1 }, 2*time.Second, 20*time.Millisecond,
		"from_status='in_progress' must match a move that started there")
}

func TestTriggerEvent_TaskStatusChanged_WildcardFromStatusMatchesAny(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgID := uuid.New()
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": &idRecordingExecutor{}})
	defer engine.cancel()

	wf := createTaskStatusChangedWF(t, repo, orgID, "completed", "*")

	engine.TriggerEvent(context.Background(), orgID, "task_status_changed", taskStatusChangedPayload("open", "completed"))
	require.Eventually(t, func() bool { return countRuns(t, engine, wf.ID) == 1 }, 2*time.Second, 20*time.Millisecond)

	engine.TriggerEvent(context.Background(), orgID, "task_status_changed", taskStatusChangedPayload("in_progress", "completed"))
	require.Eventually(t, func() bool { return countRuns(t, engine, wf.ID) == 2 }, 2*time.Second, 20*time.Millisecond,
		"from_status='*' matches a move that started anywhere")
}
