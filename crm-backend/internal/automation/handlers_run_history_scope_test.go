package automation

import (
	"encoding/json"
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

// handlers_run_history_scope_test.go covers the org-scoping of the two run-history READ
// endpoints — (*Handler).GetRunDetail and (*Handler).ListRuns. Before the fix neither was
// scoped to the caller's org, so any authenticated user could read another org's run
// history by guessing a runId / workflow id — a cross-org IDOR. The detail leaks the run's
// trigger context and the action logs' resolved input/output, which can carry PII.
//
// Like handlers_retry_test.go it splits into pure rejection tests (no DB) and DB-backed
// scoping tests (skip without Docker/testcontainers), and reuses that file's discard logger,
// minimal handler, seed helper, and the shared error-code extractor.
//
//	GetRunDetail — GET /api/workflows/runs/:runId
//	| Stage   | Condition                     | HTTP | Error code   |
//	| Auth    | Missing/invalid org context   | 401  | UNAUTHORIZED |
//	| Path    | :runId not a valid UUID       | 400  | INVALID_ID   |
//	| Lookup  | Unknown / foreign-org run     | 404  | NOT_FOUND    |
//	| Success | In-org run                    | 200  | data{run,…}  |
//
//	ListRuns — GET /api/workflows/:id/runs
//	| Stage   | Condition                       | HTTP | Error code   |
//	| Auth    | Missing/invalid org context     | 401  | UNAUTHORIZED |
//	| Path    | :id not a valid UUID            | 400  | INVALID_ID   |
//	| Lookup  | Unknown / foreign-org workflow | 404  | NOT_FOUND    |
//	| Success | In-org workflow                 | 200  | data{runs,…} |

// --- file-local helpers (runHistIT*) ---

// runHistITRouter injects org/user/role context the way the auth middleware does, then
// registers both run-history read routes (in the same order RegisterRoutes uses, proving
// the static /runs/:runId and the param /:id/runs coexist).
func runHistITRouter(h *Handler, orgID, userID uuid.UUID, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Set("role", role)
		c.Next()
	})
	router.GET("/api/workflows/:id/runs", h.ListRuns)
	router.GET("/api/workflows/runs/:runId", h.GetRunDetail)
	return router
}

// runHistITRouterNoOrg registers both routes with NO context injected, so getContext writes
// the 401 itself.
func runHistITRouterNoOrg(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/workflows/:id/runs", h.ListRuns)
	router.GET("/api/workflows/runs/:runId", h.GetRunDetail)
	return router
}

// runHistITGetRunDetail issues GET /api/workflows/runs/:runId and returns the recorder.
func runHistITGetRunDetail(router *gin.Engine, runID string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workflows/runs/"+runID, nil)
	router.ServeHTTP(w, req)
	return w
}

// runHistITListRuns issues GET /api/workflows/:id/runs and returns the recorder.
func runHistITListRuns(router *gin.Engine, workflowID string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workflows/"+workflowID+"/runs", nil)
	router.ServeHTTP(w, req)
	return w
}

// runHistITSeedRunForWorkflow inserts a completed run tied to a specific workflow + org, so
// the ListRuns happy path can prove an in-org workflow's runs are actually returned.
func runHistITSeedRunForWorkflow(t *testing.T, db *gorm.DB, orgID, workflowID uuid.UUID) *WorkflowRun {
	t.Helper()
	now := time.Now()
	run := &WorkflowRun{
		ID:              uuid.New(),
		WorkflowID:      workflowID,
		WorkflowVersion: 1,
		OrgID:           orgID,
		Status:          StatusCompleted,
		TriggerContext:  datatypes.JSON(`{}`),
		IdempotencyKey:  "run-hist-seed-" + uuid.New().String(),
		FinishedAt:      &now,
	}
	require.NoError(t, db.Create(run).Error)
	return run
}

// runHistITDetail is the decoded shape of the GetRunDetail success / error envelopes. A nil
// Data with a populated Error proves a failure leaked no run.
type runHistITDetail struct {
	Data *struct {
		Run struct {
			ID    string `json:"id"`
			OrgID string `json:"org_id"`
		} `json:"run"`
		ActionLogs []struct {
			ID string `json:"id"`
		} `json:"action_logs"`
	} `json:"data"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

// runHistITDecodeDetail parses a GetRunDetail response body.
func runHistITDecodeDetail(t *testing.T, w *httptest.ResponseRecorder) runHistITDetail {
	t.Helper()
	var resp runHistITDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp),
		"response body must be JSON, got: %s", w.Body.String())
	return resp
}

// runHistITList is the decoded shape of the ListRuns success / error envelopes.
type runHistITList struct {
	Data *struct {
		Runs []struct {
			ID         string `json:"id"`
			WorkflowID string `json:"workflow_id"`
		} `json:"runs"`
		Total int64 `json:"total"`
	} `json:"data"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

// runHistITDecodeList parses a ListRuns response body.
func runHistITDecodeList(t *testing.T, w *httptest.ResponseRecorder) runHistITList {
	t.Helper()
	var resp runHistITList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp),
		"response body must be JSON, got: %s", w.Body.String())
	return resp
}

// ============================================================
// Pure rejection paths (no DB)
// ============================================================

// TestGetRunDetailHandler_MissingOrgContextReturns401 verifies a detail request without a
// resolvable org context is rejected 401 before any DB access (the minimal handler's
// repo/db are nil).
func TestGetRunDetailHandler_MissingOrgContextReturns401(t *testing.T) {
	h := handlerRunNowMinimalHandler()
	router := runHistITRouterNoOrg(h)

	w := runHistITGetRunDetail(router, uuid.New().String())

	assert.Equal(t, http.StatusUnauthorized, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "UNAUTHORIZED", runNowITErrorCode(t, w))
	assert.Nil(t, runHistITDecodeDetail(t, w).Data, "a 401 must carry no run")
}

// TestGetRunDetailHandler_InvalidRunIDReturns400 verifies a non-UUID :runId is rejected 400
// INVALID_ID before any DB access.
func TestGetRunDetailHandler_InvalidRunIDReturns400(t *testing.T) {
	h := handlerRunNowMinimalHandler()
	router := runHistITRouter(h, uuid.New(), uuid.New(), "admin")

	w := runHistITGetRunDetail(router, "not-a-valid-uuid")

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "INVALID_ID", runNowITErrorCode(t, w))
	assert.Nil(t, runHistITDecodeDetail(t, w).Data, "a 400 must carry no run")
}

// TestListRunsHandler_MissingOrgContextReturns401 verifies a list request without a
// resolvable org context is rejected 401 before any DB access.
func TestListRunsHandler_MissingOrgContextReturns401(t *testing.T) {
	h := handlerRunNowMinimalHandler()
	router := runHistITRouterNoOrg(h)

	w := runHistITListRuns(router, uuid.New().String())

	assert.Equal(t, http.StatusUnauthorized, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "UNAUTHORIZED", runNowITErrorCode(t, w))
	assert.Nil(t, runHistITDecodeList(t, w).Data, "a 401 must carry no runs")
}

// TestListRunsHandler_InvalidWorkflowIDReturns400 verifies a non-UUID :id is rejected 400
// INVALID_ID before any DB access.
func TestListRunsHandler_InvalidWorkflowIDReturns400(t *testing.T) {
	h := handlerRunNowMinimalHandler()
	router := runHistITRouter(h, uuid.New(), uuid.New(), "admin")

	w := runHistITListRuns(router, "not-a-valid-uuid")

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "INVALID_ID", runNowITErrorCode(t, w))
	assert.Nil(t, runHistITDecodeList(t, w).Data, "a 400 must carry no runs")
}

// ============================================================
// DB-backed: cross-org scoping (the IDOR fix)
// ============================================================

// TestGetRunDetailHandler_ForeignOrgRunReturns404 verifies the core fix: a run that exists
// but belongs to a DIFFERENT org is reported 404 (existence + PII-bearing detail not leaked)
// when read by a caller from another org.
func TestGetRunDetailHandler_ForeignOrgRunReturns404(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	ownerOrg := uuid.New()
	callerOrg := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, handlerRunNowDiscardLogger(), capAllow{}, nil)
	router := runHistITRouter(handler, callerOrg, uuid.New(), "admin")

	// A real run owned by another org. retryITSeedRun (handlers_retry_test.go) seeds a run
	// scoped to ownerOrg.
	run := retryITSeedRun(t, db, ownerOrg, StatusCompleted, []int{0})

	w := runHistITGetRunDetail(router, run.ID.String())

	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "NOT_FOUND", runNowITErrorCode(t, w))
	assert.Nil(t, runHistITDecodeDetail(t, w).Data, "a foreign-org run must leak no detail")
}

// TestGetRunDetailHandler_InOrgRunReturns200 is the positive control proving the 404 above
// is due to org-scoping, not a blanket rejection: the SAME run, read by its OWN org, returns
// 200 with that run's id and org id.
func TestGetRunDetailHandler_InOrgRunReturns200(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, handlerRunNowDiscardLogger(), capAllow{}, nil)
	router := runHistITRouter(handler, orgID, uuid.New(), "admin")

	run := retryITSeedRun(t, db, orgID, StatusCompleted, []int{0})

	w := runHistITGetRunDetail(router, run.ID.String())

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := runHistITDecodeDetail(t, w)
	require.NotNil(t, resp.Data, "an in-org run must be returned: %s", w.Body.String())
	assert.Equal(t, run.ID.String(), resp.Data.Run.ID, "the caller's own run is returned")
	assert.Equal(t, orgID.String(), resp.Data.Run.OrgID, "the returned run carries the caller's org")
}

// TestListRunsHandler_ForeignOrgWorkflowReturns404 verifies the core fix for the list
// endpoint: listing the runs of a workflow that belongs to a DIFFERENT org returns 404 — the
// workflow is resolved in the caller's org BEFORE any run is listed, so a run that exists for
// that foreign workflow is never enumerated.
func TestListRunsHandler_ForeignOrgWorkflowReturns404(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	ownerOrg := uuid.New()
	callerOrg := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, handlerRunNowDiscardLogger(), capAllow{}, nil)
	router := runHistITRouter(handler, callerOrg, uuid.New(), "admin")

	// A workflow (with a run) owned by another org. runNowITWorkflow (run_now_integration_
	// test.go) seeds an org-scoped workflow.
	foreignWf := runNowITWorkflow(t, db, ownerOrg, TriggerContactCreated)
	runHistITSeedRunForWorkflow(t, db, ownerOrg, foreignWf.ID)

	w := runHistITListRuns(router, foreignWf.ID.String())

	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "NOT_FOUND", runNowITErrorCode(t, w))
	assert.Nil(t, runHistITDecodeList(t, w).Data,
		"a foreign-org workflow's runs must not be enumerable")

	// An unknown workflow id resolves to nothing in the caller's org → also 404.
	wUnknown := runHistITListRuns(router, uuid.New().String())
	assert.Equal(t, http.StatusNotFound, wUnknown.Code, "body: %s", wUnknown.Body.String())
	assert.Equal(t, "NOT_FOUND", runNowITErrorCode(t, wUnknown))
}

// TestListRunsHandler_InOrgWorkflowReturns200 is the positive control: the caller's OWN
// workflow lists its runs (200, the seeded run present), proving the scoping does not
// over-block.
func TestListRunsHandler_InOrgWorkflowReturns200(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, handlerRunNowDiscardLogger(), capAllow{}, nil)
	router := runHistITRouter(handler, orgID, uuid.New(), "admin")

	wf := runNowITWorkflow(t, db, orgID, TriggerContactCreated)
	seeded := runHistITSeedRunForWorkflow(t, db, orgID, wf.ID)

	w := runHistITListRuns(router, wf.ID.String())

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := runHistITDecodeList(t, w)
	require.NotNil(t, resp.Data, "an in-org workflow must list its runs: %s", w.Body.String())
	assert.Equal(t, int64(1), resp.Data.Total, "the workflow's single run is counted")
	require.Len(t, resp.Data.Runs, 1, "the workflow's single run is listed")
	assert.Equal(t, seeded.ID.String(), resp.Data.Runs[0].ID, "the listed run is the seeded one")
	assert.Equal(t, wf.ID.String(), resp.Data.Runs[0].WorkflowID, "the listed run belongs to the workflow")
}
