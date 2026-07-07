package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// run_now_exec_integration_test.go contains DB-backed integration tests for Run Now's
// real, single-workflow execution path (P20 — task 5.2). They drive the full stack:
// the HTTP handler (*Handler).RunNow → Engine.RunWorkflowNow → the worker (processRun),
// using a real Postgres (testcontainers, via the package-shared setupTestDB) so the
// persisted rows and the run-history endpoint are exercised end-to-end.
//
// These tests assert the behaviors that distinguish Run Now from both natural triggers
// and the dry-run test-run endpoint:
//   - exactly one run is created for the targeted :id, and none for sibling workflows
//     sharing the trigger type (Req 6.1, 6.2);
//   - the workflow's configured action really runs and produces an observable side
//     effect, unlike test-run which performs none (Req 6.3);
//   - an inactive/draft workflow still runs (Req 6.4);
//   - the run carries the caller's org and the stored Trigger_Context (Req 6.6);
//   - the run appears in GET /api/workflows/:id/runs with trigger.source == "run_now"
//     (Req 7.1, 7.4).
//
// They REUSE the package-level helpers setupTestDB / makeEngine (integration_test.go)
// and countRunsForWorkflow (engine_run_now_test.go). All helpers introduced here are
// prefixed runNowExec* to stay unique to this file.
//
// Validates: Requirements 6.1, 6.2, 6.3, 6.4, 6.6, 7.1, 7.4

// runNowExecSpyActionType is the action type the seeded workflows use; the spy executor
// below is registered for it so each real execution produces an observable invocation.
const runNowExecSpyActionType = "run_now_spy_action"

// runNowExecSpyExecutor is a spy ActionExecutor that records every real invocation:
// the run it executed under and the contact/deal id resolved from the EvalContext (which
// the engine reconstructs from the stored Trigger_Context). A test-run never reaches an
// executor, so a non-zero call count is proof a real side effect occurred (Req 6.3).
type runNowExecSpyExecutor struct {
	mu         sync.Mutex
	calls      int
	runIDs     []uuid.UUID
	contactIDs []string
	dealIDs    []string
	sources    []string
}

func (s *runNowExecSpyExecutor) Execute(_ context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.runIDs = append(s.runIDs, run.ID)
	if evalCtx.Contact != nil {
		if id, ok := evalCtx.Contact["id"].(string); ok {
			s.contactIDs = append(s.contactIDs, id)
		}
	}
	if evalCtx.Deal != nil {
		if id, ok := evalCtx.Deal["id"].(string); ok {
			s.dealIDs = append(s.dealIDs, id)
		}
	}
	if evalCtx.Trigger != nil {
		if src, ok := evalCtx.Trigger["source"].(string); ok {
			s.sources = append(s.sources, src)
		}
	}
	return map[string]any{"executed": action.ID}, nil
}

func (s *runNowExecSpyExecutor) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *runNowExecSpyExecutor) snapshotContactIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.contactIDs))
	copy(out, s.contactIDs)
	return out
}

func (s *runNowExecSpyExecutor) snapshotDealIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.dealIDs))
	copy(out, s.dealIDs)
	return out
}

func (s *runNowExecSpyExecutor) snapshotSources() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.sources))
	copy(out, s.sources)
	return out
}

// runNowExecEnv bundles everything an exec integration test needs: the DB, the engine
// (with its worker pool started so processRun runs asynchronously off the jobs channel),
// the registered spy executor, the HTTP router with the Run Now + run-history routes,
// and the caller's org id.
type runNowExecEnv struct {
	db     *gorm.DB
	engine *Engine
	spy    *runNowExecSpyExecutor
	router *gin.Engine
	orgID  uuid.UUID
	userID uuid.UUID
}

// runNowExecSetup spins up a real DB, ensures the contacts/deals tables exist, creates
// an engine whose worker pool is running (so dispatched jobs execute the same way they
// do in production), registers the spy executor, and wires the authenticated
// Run Now + run-history routes behind an admin context. The returned cleanup stops the
// engine and terminates the container.
func runNowExecSetup(t *testing.T) (*runNowExecEnv, func()) {
	t.Helper()

	db, cleanupDB := setupTestDB(t)

	// The contacts table is created by setupTestDB, but ensure the columns the loader
	// reads exist, and create a deals table for deal-triggered workflows. These mirror
	// the columns loadContactForRun / loadDealForRun select.
	db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS company_id UUID`)
	db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`)
	db.Exec(`CREATE TABLE IF NOT EXISTS deals (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		value DOUBLE PRECISION NOT NULL DEFAULT 0,
		probability INT NOT NULL DEFAULT 0,
		is_won BOOLEAN NOT NULL DEFAULT false,
		is_lost BOOLEAN NOT NULL DEFAULT false,
		contact_id UUID,
		company_id UUID,
		stage_id UUID,
		owner_user_id UUID,
		expected_close_at TIMESTAMPTZ,
		closed_at TIMESTAMPTZ,
		deleted_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)

	spy := &runNowExecSpyExecutor{}

	// Build the engine directly (not via setupTestDB's makeEngine) so we can start its
	// worker pool and have dispatched jobs processed asynchronously, exactly like prod.
	ctx, cancel := context.WithCancel(context.Background())
	engine := &Engine{
		db:        db,
		repo:      NewRepository(db),
		logger:    slog.Default(),
		jobs:      make(chan WorkflowRunJob, 100),
		workers:   2,
		ctx:       ctx,
		cancel:    cancel,
		executors: map[string]ActionExecutor{runNowExecSpyActionType: spy},
	}
	// Start the worker goroutines manually (Engine.Start would also run AutoMigrate,
	// the scheduler, and register default executors — we only need the workers here).
	for i := 0; i < engine.workers; i++ {
		engine.wg.Add(1)
		go engine.worker(i)
	}

	orgID := uuid.New()
	userID := uuid.New()

	handler := &Handler{
		engine:      engine,
		repo:        engine.repo,
		db:          db,
		logger:      slog.Default(),
		rateLimiter: newTokenBucket(),
		capChecker:  capAllow{},
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Set("role", "admin")
		c.Next()
	})
	router.POST("/api/workflows/:id/run", handler.RunNow)
	router.POST("/api/workflows/:id/test-run", handler.TestRun)
	router.GET("/api/workflows/:id/runs", handler.ListRuns)

	cleanup := func() {
		cancel()
		cleanupDB()
	}

	return &runNowExecEnv{
		db:     db,
		engine: engine,
		spy:    spy,
		router: router,
		orgID:  orgID,
		userID: userID,
	}, cleanup
}

// runNowExecSeedWorkflow inserts a workflow + its version snapshot with a single spy
// action and no conditions (so the action always runs when the run executes). triggerType
// is stored as the workflow's trigger; isActive controls is_active so inactive-workflow
// behavior can be asserted (Req 6.4).
func runNowExecSeedWorkflow(t *testing.T, db *gorm.DB, orgID uuid.UUID, triggerType string, isActive bool) *Workflow {
	t.Helper()

	trigger, _ := json.Marshal(TriggerSpec{Type: triggerType})
	actions, _ := json.Marshal([]ActionSpec{
		{ID: "action_0", Type: runNowExecSpyActionType, Params: map[string]any{}},
	})

	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     orgID,
		Name:      fmt.Sprintf("run-now-exec-%s-%s", triggerType, uuid.New().String()[:8]),
		IsActive:  isActive,
		Trigger:   datatypes.JSON(trigger),
		Actions:   datatypes.JSON(actions),
		Version:   1,
		CreatedBy: uuid.New(),
	}
	require.NoError(t, db.Create(wf).Error)

	ver := &WorkflowVersion{
		ID:         uuid.New(),
		WorkflowID: wf.ID,
		Version:    1,
		Trigger:    wf.Trigger,
		Actions:    wf.Actions,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, db.Create(ver).Error)

	return wf
}

// runNowExecSeedContact inserts a contact scoped to org and returns its id.
func runNowExecSeedContact(t *testing.T, db *gorm.DB, orgID uuid.UUID, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name, last_name, email) VALUES (?, ?, ?, ?, ?)`,
		id, orgID, "Jane", "Doe", email,
	).Error)
	return id
}

// runNowExecSeedDeal inserts a deal scoped to org (with a stage) and returns its id.
func runNowExecSeedDeal(t *testing.T, db *gorm.DB, orgID uuid.UUID, title string) (dealID, stageID uuid.UUID) {
	t.Helper()
	dealID = uuid.New()
	stageID = uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO deals (id, org_id, title, value, stage_id) VALUES (?, ?, ?, ?, ?)`,
		dealID, orgID, title, 1000.0, stageID,
	).Error)
	return dealID, stageID
}

// runNowExecPostRun issues POST /api/workflows/:id/run with the given body and returns
// the recorder.
func runNowExecPostRun(t *testing.T, router *gin.Engine, workflowID uuid.UUID, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/workflows/%s/run", workflowID), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	return w
}

// runNowExecDecodeRunID extracts the created run id from a 201 RunNow response body
// ({ "data": { "id": "...", "status": "..." } }).
func runNowExecDecodeRunID(t *testing.T, w *httptest.ResponseRecorder) uuid.UUID {
	t.Helper()
	var resp struct {
		Data struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "decode run-now response: %s", w.Body.String())
	require.NotEmpty(t, resp.Data.ID, "response must carry a run id: %s", w.Body.String())
	id, err := uuid.Parse(resp.Data.ID)
	require.NoError(t, err)
	return id
}

// runNowExecWaitForTerminal polls GetRunByID until the run reaches a terminal status
// (completed / failed / skipped), mirroring how the worker drains the jobs channel
// asynchronously. It returns the terminal run.
func runNowExecWaitForTerminal(t *testing.T, engine *Engine, runID uuid.UUID) *WorkflowRun {
	t.Helper()
	var run *WorkflowRun
	require.Eventually(t, func() bool {
		r, err := engine.repo.GetRunByID(context.Background(), runID)
		if err != nil || r == nil {
			return false
		}
		switch r.Status {
		case StatusCompleted, StatusFailed, StatusSkipped:
			run = r
			return true
		default:
			return false
		}
	}, 10*time.Second, 50*time.Millisecond, "run %s did not reach a terminal status", runID)
	return run
}

// TestRunNowExec_ContactTargeted_SingleRun_RealSideEffect_RunHistory exercises the full
// happy path for a contact-triggered workflow:
//   - Run Now targets exactly the requested :id and creates exactly one run, with no run
//     created for a sibling workflow that shares the contact_created trigger (Req 6.1, 6.2);
//   - the worker executes the workflow's configured action for real — the spy executor is
//     invoked exactly once for the targeted run with the seeded contact's id, an observable
//     side effect that a dry-run test-run never produces (Req 6.3);
//   - the run carries the caller's org id and the stored Trigger_Context (Req 6.6);
//   - the run is visible through GET /api/workflows/:id/runs and its trigger_context records
//     trigger.source == "run_now" (Req 7.1, 7.4).
//
// Validates: Requirements 6.1, 6.2, 6.3, 6.6, 7.1, 7.4
func TestRunNowExec_ContactTargeted_SingleRun_RealSideEffect_RunHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed integration test in short mode")
	}

	env, cleanup := runNowExecSetup(t)
	defer cleanup()

	// Target workflow + a sibling sharing the SAME trigger type. Run Now must target
	// only the requested :id and never fan out to the sibling (Req 6.2).
	target := runNowExecSeedWorkflow(t, env.db, env.orgID, TriggerContactCreated, true)
	sibling := runNowExecSeedWorkflow(t, env.db, env.orgID, TriggerContactCreated, true)
	contactID := runNowExecSeedContact(t, env.db, env.orgID, "vip@example.com")

	w := runNowExecPostRun(t, env.router, target.ID, map[string]any{"contact_id": contactID.String()})
	require.Equal(t, http.StatusCreated, w.Code, "expected 201, body: %s", w.Body.String())
	runID := runNowExecDecodeRunID(t, w)

	// Wait for the worker to drain the job and reach a terminal status.
	run := runNowExecWaitForTerminal(t, env.engine, runID)
	assert.Equal(t, StatusCompleted, run.Status, "the run must complete (no conditions block it)")

	// Exactly one run for the targeted workflow, none for the sibling, one overall (Req 6.1, 6.2).
	assert.Equal(t, int64(1), countRunsForWorkflow(t, env.engine, target.ID),
		"exactly one run must be created for the targeted workflow")
	assert.Equal(t, int64(0), countRunsForWorkflow(t, env.engine, sibling.ID),
		"no run must be created for a sibling workflow sharing the trigger type")
	var totalRuns int64
	require.NoError(t, env.db.Model(&WorkflowRun{}).Count(&totalRuns).Error)
	assert.Equal(t, int64(1), totalRuns, "Run Now must create exactly one run overall")

	// Real side effect: the spy executor ran exactly once, for this run, against the
	// seeded contact — something test-run (dry run) never does (Req 6.3).
	assert.Equal(t, 1, env.spy.count(), "the workflow's action must really execute exactly once")
	assert.Equal(t, []string{contactID.String()}, env.spy.snapshotContactIDs(),
		"the executor must resolve the targeted contact from the trigger context")
	assert.Equal(t, []string{"run_now"}, env.spy.snapshotSources(),
		"the executed run must carry the run_now provenance")

	// The run carries the caller's org and the stored Trigger_Context (Req 6.6).
	assert.Equal(t, env.orgID, run.OrgID, "the run must carry the caller's org id")
	require.NotEmpty(t, run.TriggerContext, "the run must store a trigger context")
	var storedCtx map[string]any
	require.NoError(t, json.Unmarshal(run.TriggerContext, &storedCtx))
	assert.Equal(t, contactID.String(), storedCtx["entity_id"])
	if trig, ok := storedCtx["trigger"].(map[string]any); assert.True(t, ok, "trigger context must carry a trigger object") {
		assert.Equal(t, TriggerContactCreated, trig["type"])
		assert.Equal(t, "run_now", trig["source"])
	}

	// Run history: the run appears in GET /api/workflows/:id/runs with source run_now (Req 7.1, 7.4).
	histRun := runNowExecGetRunFromHistory(t, env.router, target.ID, runID)
	require.NotNil(t, histRun, "the created run must appear in run history")
	histTrigger, ok := histRun.triggerContext["trigger"].(map[string]any)
	require.True(t, ok, "history trigger_context must carry a trigger object")
	assert.Equal(t, "run_now", histTrigger["source"],
		"run history must record trigger.source == run_now, distinguishing it from natural runs")
}

// TestRunNowExec_TestRunHasNoSideEffectOrRun is the explicit contrast to the happy path:
// hitting the dry-run test-run endpoint for the same workflow neither invokes the real
// executor nor creates any Workflow_Run, confirming that the observable side effect and
// the run row are unique to Run Now (Req 6.3).
//
// Validates: Requirements 6.3
func TestRunNowExec_TestRunHasNoSideEffectOrRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed integration test in short mode")
	}

	env, cleanup := runNowExecSetup(t)
	defer cleanup()

	wf := runNowExecSeedWorkflow(t, env.db, env.orgID, TriggerContactCreated, true)
	contactID := runNowExecSeedContact(t, env.db, env.orgID, "dry@example.com")

	// Dry run: test-run evaluates conditions and resolves params but performs NO side
	// effects and creates NO run.
	body, _ := json.Marshal(map[string]any{
		"context": map[string]any{
			"contact": map[string]any{"id": contactID.String(), "email": "dry@example.com"},
			"trigger": map[string]any{"type": TriggerContactCreated, "source": "test_run"},
		},
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/workflows/%s/test-run", wf.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "test-run should return 200, body: %s", w.Body.String())

	// Give any (incorrectly) dispatched job a brief window — there should be none.
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, 0, env.spy.count(), "test-run must NOT invoke real executors (no side effects)")
	assert.Equal(t, int64(0), countRunsForWorkflow(t, env.engine, wf.ID),
		"test-run must NOT create a Workflow_Run")
}

// TestRunNowExec_InactiveWorkflowStillRuns verifies that Run Now creates and executes a
// run for an inactive/draft workflow — the natural-trigger active-only filter does not
// apply (Req 6.4). The spy executor invocation proves the inactive workflow's action
// really ran.
//
// Validates: Requirements 6.4
func TestRunNowExec_InactiveWorkflowStillRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed integration test in short mode")
	}

	env, cleanup := runNowExecSetup(t)
	defer cleanup()

	// isActive == false: an inactive/draft workflow.
	wf := runNowExecSeedWorkflow(t, env.db, env.orgID, TriggerContactCreated, false)
	contactID := runNowExecSeedContact(t, env.db, env.orgID, "draft@example.com")

	w := runNowExecPostRun(t, env.router, wf.ID, map[string]any{"contact_id": contactID.String()})
	require.Equal(t, http.StatusCreated, w.Code, "inactive workflow must still accept a Run Now, body: %s", w.Body.String())
	runID := runNowExecDecodeRunID(t, w)

	run := runNowExecWaitForTerminal(t, env.engine, runID)
	assert.Equal(t, StatusCompleted, run.Status, "the inactive workflow's run must execute and complete")

	assert.Equal(t, int64(1), countRunsForWorkflow(t, env.engine, wf.ID),
		"a run must be created and executed for the inactive workflow")
	assert.Equal(t, 1, env.spy.count(), "the inactive workflow's action must really execute")
	assert.Equal(t, env.orgID, run.OrgID, "the run must carry the caller's org id")
}

// TestRunNowExec_DealTargeted_SingleRun_RealSideEffect_RunHistory mirrors the contact
// happy path for a deal-triggered workflow, including a sibling deal_stage_changed
// workflow that must not get a run, and asserts the deal-shaped trigger context, the real
// executor invocation against the seeded deal, and run-history visibility with source
// run_now.
//
// Validates: Requirements 6.1, 6.2, 6.3, 6.6, 7.1, 7.4
func TestRunNowExec_DealTargeted_SingleRun_RealSideEffect_RunHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed integration test in short mode")
	}

	env, cleanup := runNowExecSetup(t)
	defer cleanup()

	target := runNowExecSeedWorkflow(t, env.db, env.orgID, TriggerDealStageChanged, true)
	sibling := runNowExecSeedWorkflow(t, env.db, env.orgID, TriggerDealStageChanged, true)
	dealID, stageID := runNowExecSeedDeal(t, env.db, env.orgID, "Big Deal")

	w := runNowExecPostRun(t, env.router, target.ID, map[string]any{"deal_id": dealID.String()})
	require.Equal(t, http.StatusCreated, w.Code, "expected 201, body: %s", w.Body.String())
	runID := runNowExecDecodeRunID(t, w)

	run := runNowExecWaitForTerminal(t, env.engine, runID)
	assert.Equal(t, StatusCompleted, run.Status)

	assert.Equal(t, int64(1), countRunsForWorkflow(t, env.engine, target.ID),
		"exactly one run for the targeted deal workflow")
	assert.Equal(t, int64(0), countRunsForWorkflow(t, env.engine, sibling.ID),
		"no run for the sibling deal workflow sharing the trigger")

	assert.Equal(t, 1, env.spy.count(), "the deal workflow's action must really execute once")
	assert.Equal(t, []string{dealID.String()}, env.spy.snapshotDealIDs(),
		"the executor must resolve the targeted deal from the trigger context")

	// Deal-shaped trigger context: entity_id, deal.id, new_stage_id == current stage,
	// trigger.source == run_now (Req 6.6, 7.4).
	assert.Equal(t, env.orgID, run.OrgID)
	var storedCtx map[string]any
	require.NoError(t, json.Unmarshal(run.TriggerContext, &storedCtx))
	assert.Equal(t, dealID.String(), storedCtx["entity_id"])
	assert.Equal(t, stageID.String(), storedCtx["new_stage_id"], "new_stage_id must equal the deal's current stage")
	if deal, ok := storedCtx["deal"].(map[string]any); assert.True(t, ok, "trigger context must carry the deal map") {
		assert.Equal(t, dealID.String(), deal["id"])
	}
	if trig, ok := storedCtx["trigger"].(map[string]any); assert.True(t, ok) {
		assert.Equal(t, TriggerDealStageChanged, trig["type"])
		assert.Equal(t, "run_now", trig["source"])
	}

	// Run history visibility + provenance (Req 7.1, 7.4).
	histRun := runNowExecGetRunFromHistory(t, env.router, target.ID, runID)
	require.NotNil(t, histRun, "the created deal run must appear in run history")
	histTrigger, ok := histRun.triggerContext["trigger"].(map[string]any)
	require.True(t, ok, "history trigger_context must carry a trigger object")
	assert.Equal(t, "run_now", histTrigger["source"])
}

// runNowExecHistoryRun is a minimal projection of a run-history entry the tests assert on.
type runNowExecHistoryRun struct {
	id             uuid.UUID
	status         string
	triggerContext map[string]any
}

// runNowExecGetRunFromHistory issues GET /api/workflows/:id/runs, parses the
// { data: { runs: [...], total, page, size } } envelope, and returns the entry matching
// runID (or nil if absent). It also asserts the run is present in the listing.
func runNowExecGetRunFromHistory(t *testing.T, router *gin.Engine, workflowID, runID uuid.UUID) *runNowExecHistoryRun {
	t.Helper()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/workflows/%s/runs", workflowID), nil)
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "run history must return 200, body: %s", w.Body.String())

	var resp struct {
		Data struct {
			Runs []struct {
				ID             string          `json:"id"`
				Status         string          `json:"status"`
				TriggerContext json.RawMessage `json:"trigger_context"`
			} `json:"runs"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "decode run history: %s", w.Body.String())

	for _, r := range resp.Data.Runs {
		if r.ID != runID.String() {
			continue
		}
		var tc map[string]any
		require.NoError(t, json.Unmarshal(r.TriggerContext, &tc), "decode history trigger_context")
		return &runNowExecHistoryRun{
			id:             runID,
			status:         r.Status,
			triggerContext: tc,
		}
	}
	return nil
}
