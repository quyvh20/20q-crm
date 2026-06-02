package automation

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// run_now_integration_test.go contains DB-backed integration tests for the Run Now
// feature (P20 — task 5.1). They exercise the end-to-end handler path
// ((*Handler).RunNow → repo / gorm DB) for the resolution and rejection cases:
//
//   - org-scoped workflow lookup: an unknown or foreign-org workflow id → 404 (Req 1.5, 3.1);
//   - org-scoped entity loading: a missing or foreign-org contact/deal → 404 (Req 3.2–3.4);
//   - an incompatible request (entity kind ≠ workflow trigger kind) creates ZERO runs in
//     automation_workflow_runs, with the check happening before any run is created (Req 4.4).
//
// These reuse the package's existing integration scaffolding (setupTestDB, makeEngine,
// countRunsForWorkflow) and SKIP automatically when Docker/testcontainers is unavailable.
//
// Validates: Requirements 1.5, 3.1, 3.2, 3.3, 3.4, 4.4

// ============================================================
// File-local seed + request helpers (uniquely named runNowIT*)
// ============================================================

// runNowITRouter builds a gin router that injects org/user context the way the real
// auth middleware does, then registers only the Run Now route. Role is "admin" so the
// privileged-role branch of the in-handler authorization always passes — keeping these
// resolution/rejection tests focused on lookup behavior rather than permissions.
func runNowITRouter(h *Handler, orgID, userID uuid.UUID) *gin.Engine {
	return runNowITRouterWithRole(h, orgID, userID, "admin")
}

// runNowITRouterWithRole is runNowITRouter with an explicit caller role, so authorization
// tests can drive a non-privileged caller (e.g. "viewer") through the same route the real
// RegisterRoutes uses (no requireRole guard — authorization is enforced inside RunNow).
func runNowITRouterWithRole(h *Handler, orgID, userID uuid.UUID, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Set("role", role)
		c.Next()
	})
	router.POST("/api/workflows/:id/run", h.RunNow)
	return router
}

// runNowITPostRun issues POST /api/workflows/:id/run with a JSON body and returns the
// recorded response.
func runNowITPostRun(router *gin.Engine, workflowID string, body map[string]any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/workflows/"+workflowID+"/run", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	return w
}

// runNowITErrorCode extracts the error.code field from an error-envelope response body.
func runNowITErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp),
		"response body must be JSON, got: %s", w.Body.String())
	errBody, ok := resp["error"].(map[string]any)
	require.True(t, ok, "response must contain an 'error' object, got: %s", w.Body.String())
	code, _ := errBody["code"].(string)
	return code
}

// runNowITWorkflow seeds a workflow (+ pinned version) with a specific trigger type in
// the given org. createTestWorkflow only produces webhook_inbound workflows, so this
// file-local helper parameterizes the trigger so we can build both contact- and
// deal-triggered workflows. CreatedBy is a fresh random id; use runNowITWorkflowCreatedBy
// when a test needs the caller to be (or not be) the creator.
func runNowITWorkflow(t *testing.T, db *gorm.DB, orgID uuid.UUID, triggerType string) *Workflow {
	t.Helper()
	return runNowITWorkflowCreatedBy(t, db, orgID, triggerType, uuid.New())
}

// runNowITWorkflowCreatedBy is runNowITWorkflow with an explicit CreatedBy, so authorization
// tests can seed a workflow owned by (or not by) the calling user to exercise the creator
// allowance.
func runNowITWorkflowCreatedBy(t *testing.T, db *gorm.DB, orgID uuid.UUID, triggerType string, createdBy uuid.UUID) *Workflow {
	t.Helper()

	trigger, _ := json.Marshal(TriggerSpec{Type: triggerType})
	actions, _ := json.Marshal([]ActionSpec{
		{ID: "action_0", Type: "test_action", Params: map[string]any{}},
	})

	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     orgID,
		Name:      "run-now-it-" + uuid.New().String()[:8],
		IsActive:  true,
		Trigger:   datatypes.JSON(trigger),
		Actions:   datatypes.JSON(actions),
		Version:   1,
		CreatedBy: createdBy,
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

// runNowITContact inserts a contact scoped to org into the contacts table that
// setupTestDB provisions, returning its id. Only columns guaranteed to exist in that
// minimal table are populated.
func runNowITContact(t *testing.T, db *gorm.DB, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name, last_name, email) VALUES (?, ?, ?, ?, ?)`,
		id, orgID, "Jane", "Doe", "jane@example.com",
	).Error)
	return id
}

// runNowITEnsureDealsTable creates a deals table with the columns loadDealForRun reads.
// setupTestDB only provisions contacts + the automation tables, so deal-targeted tests
// create this table themselves. Idempotent (IF NOT EXISTS).
func runNowITEnsureDealsTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS deals (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		contact_id UUID,
		company_id UUID,
		stage_id UUID,
		value NUMERIC DEFAULT 0,
		probability INT DEFAULT 0,
		owner_user_id UUID,
		expected_close_at TIMESTAMPTZ,
		is_won BOOLEAN NOT NULL DEFAULT FALSE,
		is_lost BOOLEAN NOT NULL DEFAULT FALSE,
		closed_at TIMESTAMPTZ,
		custom_fields JSONB DEFAULT '{}',
		deleted_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)
}

// runNowITDeal inserts a deal scoped to org (with a stage) into the deals table,
// returning its id.
func runNowITDeal(t *testing.T, db *gorm.DB, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	stageID := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO deals (id, org_id, title, value, stage_id) VALUES (?, ?, ?, ?, ?)`,
		id, orgID, "Big Deal", 1000, stageID,
	).Error)
	return id
}

// runNowITCountAllRuns counts every row in automation_workflow_runs.
func runNowITCountAllRuns(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&WorkflowRun{}).Count(&n).Error)
	return n
}

// ============================================================
// Integration: org-scoped workflow resolution (Req 1.5, 3.1)
// ============================================================

// TestRunNowIntegration_WorkflowResolutionScopedToOrg verifies that the workflow lookup
// is scoped to the caller's org: an unknown workflow id and a workflow that exists only
// in a different org both produce a 404 and never create a run.
//
// Validates: Requirements 1.5, 3.1
func TestRunNowIntegration_WorkflowResolutionScopedToOrg(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgA := uuid.New()
	orgB := uuid.New()
	userID := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, slog.Default())
	router := runNowITRouter(handler, orgA, userID)

	// A valid in-org contact so the request fails only at workflow resolution.
	contactID := runNowITContact(t, db, orgA)

	// Case 1: no workflow with this id exists anywhere → 404 (Req 3.1).
	w1 := runNowITPostRun(router, uuid.New().String(),
		map[string]any{"contact_id": contactID.String()})
	assert.Equal(t, http.StatusNotFound, w1.Code,
		"unknown workflow id must return 404, body: %s", w1.Body.String())
	assert.Equal(t, "NOT_FOUND", runNowITErrorCode(t, w1))

	// Case 2: workflow exists, but in another org → org-scoped lookup yields 404 (Req 1.5).
	foreignWf := runNowITWorkflow(t, db, orgB, TriggerContactCreated)
	w2 := runNowITPostRun(router, foreignWf.ID.String(),
		map[string]any{"contact_id": contactID.String()})
	assert.Equal(t, http.StatusNotFound, w2.Code,
		"foreign-org workflow must return 404, body: %s", w2.Body.String())
	assert.Equal(t, "NOT_FOUND", runNowITErrorCode(t, w2))

	// Neither unresolved request created a run.
	assert.Equal(t, int64(0), runNowITCountAllRuns(t, db),
		"no run must be created when the workflow cannot be resolved in the caller's org")
}

// ============================================================
// Integration: org-scoped entity resolution (Req 3.2, 3.3, 3.4)
// ============================================================

// TestRunNowIntegration_EntityResolutionScopedToOrg verifies that the Sample_Entity is
// loaded scoped to the caller's org: a missing contact/deal and a contact/deal that
// exists only in another org both produce a 404 and never create a run.
//
// Validates: Requirements 3.2, 3.3, 3.4
func TestRunNowIntegration_EntityResolutionScopedToOrg(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()
	runNowITEnsureDealsTable(t, db)

	orgA := uuid.New()
	orgB := uuid.New()
	userID := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, slog.Default())
	router := runNowITRouter(handler, orgA, userID)

	// --- Contact entity resolution (Req 3.2, 3.4) ---
	contactWf := runNowITWorkflow(t, db, orgA, TriggerContactCreated)

	// Missing contact → 404.
	wMissingContact := runNowITPostRun(router, contactWf.ID.String(),
		map[string]any{"contact_id": uuid.New().String()})
	assert.Equal(t, http.StatusNotFound, wMissingContact.Code,
		"missing contact must return 404, body: %s", wMissingContact.Body.String())
	assert.Equal(t, "NOT_FOUND", runNowITErrorCode(t, wMissingContact))

	// Contact exists only in another org → org-scoped load yields 404.
	foreignContact := runNowITContact(t, db, orgB)
	wForeignContact := runNowITPostRun(router, contactWf.ID.String(),
		map[string]any{"contact_id": foreignContact.String()})
	assert.Equal(t, http.StatusNotFound, wForeignContact.Code,
		"foreign-org contact must return 404, body: %s", wForeignContact.Body.String())
	assert.Equal(t, "NOT_FOUND", runNowITErrorCode(t, wForeignContact))

	// --- Deal entity resolution (Req 3.3, 3.4) ---
	dealWf := runNowITWorkflow(t, db, orgA, TriggerDealStageChanged)

	// Missing deal → 404.
	wMissingDeal := runNowITPostRun(router, dealWf.ID.String(),
		map[string]any{"deal_id": uuid.New().String()})
	assert.Equal(t, http.StatusNotFound, wMissingDeal.Code,
		"missing deal must return 404, body: %s", wMissingDeal.Body.String())
	assert.Equal(t, "NOT_FOUND", runNowITErrorCode(t, wMissingDeal))

	// Deal exists only in another org → org-scoped load yields 404.
	foreignDeal := runNowITDeal(t, db, orgB)
	wForeignDeal := runNowITPostRun(router, dealWf.ID.String(),
		map[string]any{"deal_id": foreignDeal.String()})
	assert.Equal(t, http.StatusNotFound, wForeignDeal.Code,
		"foreign-org deal must return 404, body: %s", wForeignDeal.Body.String())
	assert.Equal(t, "NOT_FOUND", runNowITErrorCode(t, wForeignDeal))

	// No run was created for any unresolved entity.
	assert.Equal(t, int64(0), runNowITCountAllRuns(t, db),
		"no run must be created when the entity cannot be resolved in the caller's org")
}

// ============================================================
// Integration: incompatible request creates zero runs (Req 4.4)
// ============================================================

// TestRunNowIntegration_IncompatibleRequestCreatesZeroRuns verifies that a request whose
// entity kind does not match the workflow's trigger kind is rejected with 400
// INCOMPATIBLE_ENTITY and creates ZERO runs. Both entities are seeded in-org so the only
// reason for rejection is incompatibility (not a missing entity), proving the
// compatibility check runs before any run is created.
//
// Validates: Requirements 4.4
func TestRunNowIntegration_IncompatibleRequestCreatesZeroRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()
	runNowITEnsureDealsTable(t, db)

	orgID := uuid.New()
	userID := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, slog.Default())
	router := runNowITRouter(handler, orgID, userID)

	// Real, in-org entities — so incompatibility is the sole rejection cause.
	contactID := runNowITContact(t, db, orgID)
	dealID := runNowITDeal(t, db, orgID)

	// Contact-triggered workflow + a deal entity → incompatible.
	contactWf := runNowITWorkflow(t, db, orgID, TriggerContactCreated)
	wDealOnContactWf := runNowITPostRun(router, contactWf.ID.String(),
		map[string]any{"deal_id": dealID.String()})
	assert.Equal(t, http.StatusBadRequest, wDealOnContactWf.Code,
		"deal entity against a contact-triggered workflow must be 400, body: %s", wDealOnContactWf.Body.String())
	assert.Equal(t, "INCOMPATIBLE_ENTITY", runNowITErrorCode(t, wDealOnContactWf))

	// Deal-triggered workflow + a contact entity → incompatible.
	dealWf := runNowITWorkflow(t, db, orgID, TriggerDealStageChanged)
	wContactOnDealWf := runNowITPostRun(router, dealWf.ID.String(),
		map[string]any{"contact_id": contactID.String()})
	assert.Equal(t, http.StatusBadRequest, wContactOnDealWf.Code,
		"contact entity against a deal-triggered workflow must be 400, body: %s", wContactOnDealWf.Body.String())
	assert.Equal(t, "INCOMPATIBLE_ENTITY", runNowITErrorCode(t, wContactOnDealWf))

	// Critically: an incompatible request creates ZERO runs (Req 4.4) — checked both
	// per-workflow and across the whole table.
	assert.Equal(t, int64(0), countRunsForWorkflow(t, engine, contactWf.ID),
		"incompatible request must not create a run for the contact workflow")
	assert.Equal(t, int64(0), countRunsForWorkflow(t, engine, dealWf.ID),
		"incompatible request must not create a run for the deal workflow")
	assert.Equal(t, int64(0), runNowITCountAllRuns(t, db),
		"an incompatible request must create zero runs in automation_workflow_runs")
}

// ============================================================
// Integration: Run Now bypasses idempotency dedupe (Req 6.5)
// ============================================================

// TestRunNowIntegration_BypassesIdempotencyDedupe verifies that Run Now never
// de-duplicates: two manual runs of the SAME workflow against the SAME contact, issued
// back-to-back (as a double-click within the spec's 30s window would), create TWO distinct
// runs — not one. Natural triggers dedupe on a stable idempotency key, but each Run Now
// uses a unique-per-call key (newRunNowIdempotencyKey), so CreateRun always inserts a fresh
// run. CreateRun is synchronous within each request, so both rows exist by the time the
// second POST returns.
//
// Validates: Requirements 6.5
func TestRunNowIntegration_BypassesIdempotencyDedupe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// loadContactForRun reads company_id / owner_user_id — ensure they exist on the
	// minimal contacts table setupTestDB provisions.
	db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS company_id UUID`)
	db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`)

	orgID := uuid.New()
	userID := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, slog.Default())
	router := runNowITRouter(handler, orgID, userID)

	wf := runNowITWorkflow(t, db, orgID, TriggerContactCreated)
	contactID := runNowITContact(t, db, orgID)
	body := map[string]any{"contact_id": contactID.String()}

	// Two "clicks" in immediate succession — a stronger case than the spec's 30s window.
	w1 := runNowITPostRun(router, wf.ID.String(), body)
	w2 := runNowITPostRun(router, wf.ID.String(), body)

	require.Equal(t, http.StatusCreated, w1.Code, "first Run Now must succeed, body: %s", w1.Body.String())
	require.Equal(t, http.StatusCreated, w2.Code, "second Run Now must succeed, body: %s", w2.Body.String())

	// Distinct run ids — the second call was NOT de-duplicated onto the first.
	r1 := handlerRunNowDecode(t, w1)
	r2 := handlerRunNowDecode(t, w2)
	require.NotNil(t, r1.Data, "first response must carry a run id: %s", w1.Body.String())
	require.NotNil(t, r2.Data, "second response must carry a run id: %s", w2.Body.String())
	assert.NotEqual(t, r1.Data.ID, r2.Data.ID, "each Run Now must create a distinct run id")

	// Exactly two runs persisted — no dedupe (Req 6.5).
	assert.Equal(t, int64(2), countRunsForWorkflow(t, engine, wf.ID),
		"two Run Now calls against the same workflow+contact must create two runs (no idempotency dedupe)")
	assert.Equal(t, int64(2), runNowITCountAllRuns(t, db),
		"exactly two runs overall — the manual re-run is never de-duplicated")
}
