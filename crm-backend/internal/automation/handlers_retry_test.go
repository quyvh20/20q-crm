package automation

import (
	"context"
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

// handlers_retry_test.go covers the (*Handler).RetryRun HTTP handler (Retry failed run,
// P21). Like handlers_run_now_test.go it splits into pure rejection tests (no DB) and
// DB-backed state tests (skip without Docker), and reuses that file's discard logger,
// minimal handler, decode helper, and error-code extractor.
//
//	| Stage    | Condition                          | HTTP | Error code     |
//	| Auth     | Missing/invalid org context        | 401  | UNAUTHORIZED    |
//	| Path     | :runId not a valid UUID            | 400  | INVALID_ID      |
//	| Lookup   | Unknown / foreign-org run          | 404  | NOT_FOUND       |
//	| State    | Run not in the failed state        | 409  | INVALID_STATE   |
//	| Success  | Failed run re-queued               | 200  | data{id,status} |

// --- file-local helpers (retryIT*) ---

// retryITRouter injects org/user/role context the way the auth middleware does, then
// registers only the retry route.
func retryITRouter(h *Handler, orgID, userID uuid.UUID, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Set("role", role)
		c.Next()
	})
	router.POST("/api/workflows/runs/:runId/retry", h.RetryRun)
	return router
}

// retryITRouterNoOrg registers the retry route with NO context injected, so getContext
// writes the 401 itself.
func retryITRouterNoOrg(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/workflows/runs/:runId/retry", h.RetryRun)
	return router
}

// retryITPost issues POST /api/workflows/runs/:runId/retry and returns the recorder.
func retryITPost(router *gin.Engine, runID string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/workflows/runs/"+runID+"/retry", nil)
	router.ServeHTTP(w, req)
	return w
}

// retryITSeedRun inserts a run with the given status (and, for terminal statuses, the
// matching bookkeeping) under a fresh random WorkflowID — enough for the lookup/state
// branches, which read only the run. Use retryITSeedRunForWorkflow when a test needs the
// run linked to a real workflow (authorization).
func retryITSeedRun(t *testing.T, db *gorm.DB, orgID uuid.UUID, status string, completed []int) *WorkflowRun {
	t.Helper()
	return retryITSeedRunForWorkflow(t, db, orgID, uuid.New(), status, completed)
}

// retryITSeedRunForWorkflow is retryITSeedRun with an explicit WorkflowID, so authorization
// tests can link the run to a workflow whose CreatedBy is known.
func retryITSeedRunForWorkflow(t *testing.T, db *gorm.DB, orgID, workflowID uuid.UUID, status string, completed []int) *WorkflowRun {
	t.Helper()
	cj, _ := json.Marshal(completed)
	run := &WorkflowRun{
		ID:               uuid.New(),
		WorkflowID:       workflowID,
		WorkflowVersion:  1,
		OrgID:            orgID,
		Status:           status,
		TriggerContext:   datatypes.JSON(`{}`),
		CompletedActions: datatypes.JSON(cj),
		CurrentActionIdx: len(completed),
		IdempotencyKey:   "retry-seed-" + uuid.New().String(),
	}
	if status == StatusFailed {
		now := time.Now()
		run.RetryCount = 3
		run.LastError = "boom"
		run.FinishedAt = &now
	}
	if status == StatusCompleted {
		now := time.Now()
		run.FinishedAt = &now
	}
	require.NoError(t, db.Create(run).Error)
	return run
}

// ============================================================
// Pure rejection paths (no DB)
// ============================================================

// TestRetryRunHandler_MissingOrgContextReturns401 verifies a request without a resolvable
// org context is rejected 401 before any DB access.
func TestRetryRunHandler_MissingOrgContextReturns401(t *testing.T) {
	h := handlerRunNowMinimalHandler()
	router := retryITRouterNoOrg(h)

	w := retryITPost(router, uuid.New().String())

	assert.Equal(t, http.StatusUnauthorized, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "UNAUTHORIZED", runNowITErrorCode(t, w))
	assert.Nil(t, handlerRunNowDecode(t, w).Data, "a 401 must carry no run id")
}

// TestRetryRunHandler_InvalidRunIDReturns400 verifies a non-UUID :runId is rejected 400
// INVALID_ID before any DB access (the minimal handler's repo/db are nil).
func TestRetryRunHandler_InvalidRunIDReturns400(t *testing.T) {
	h := handlerRunNowMinimalHandler()
	router := retryITRouter(h, uuid.New(), uuid.New(), "admin")

	w := retryITPost(router, "not-a-valid-uuid")

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "INVALID_ID", runNowITErrorCode(t, w))
	assert.Nil(t, handlerRunNowDecode(t, w).Data, "a 400 must carry no run id")
}

// ============================================================
// DB-backed: lookup / state / success
// ============================================================

// TestRetryRunHandler_ForeignOrgRunReturns404 verifies a run that exists but belongs to a
// different org is reported 404 (existence not leaked) and is NOT reset.
func TestRetryRunHandler_ForeignOrgRunReturns404(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	ownerOrg := uuid.New()
	callerOrg := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, handlerRunNowDiscardLogger())
	router := retryITRouter(handler, callerOrg, uuid.New(), "admin")

	run := retryITSeedRun(t, db, ownerOrg, StatusFailed, []int{0})

	w := retryITPost(router, run.ID.String())

	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "NOT_FOUND", runNowITErrorCode(t, w))

	// The foreign-org run must be untouched (still failed, and nothing enqueued).
	reloaded, err := engine.repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, reloaded.Status, "a foreign-org run must not be reset")
	assert.Equal(t, 0, len(engine.jobs), "no job must be enqueued for a 404")
}

// TestRetryRunHandler_RejectsNonFailedRun verifies that any run NOT in the failed state —
// in flight (pending/running) or already terminal (completed/skipped) — is rejected and
// left unchanged, with no job enqueued. The status is a state conflict (the request is
// well-formed), so it returns 409 INVALID_STATE.
//
// NOTE: an earlier spec said 400 here; we deliberately switched to 409 (state conflict).
// If 400 is required, flip http.StatusConflict in handlers.go (RetryRun) and this assertion.
func TestRetryRunHandler_RejectsNonFailedRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	for _, status := range []string{StatusPending, StatusRunning, StatusCompleted, StatusSkipped} {
		t.Run(status, func(t *testing.T) {
			orgID := uuid.New()

			engine := makeEngine(db, map[string]ActionExecutor{})
			defer engine.cancel()
			handler := NewHandler(engine, db, handlerRunNowDiscardLogger())
			router := retryITRouter(handler, orgID, uuid.New(), "admin")

			run := retryITSeedRun(t, db, orgID, status, []int{0})

			w := retryITPost(router, run.ID.String())

			assert.Equal(t, http.StatusConflict, w.Code, "status %q, body: %s", status, w.Body.String())
			assert.Equal(t, "INVALID_STATE", runNowITErrorCode(t, w))

			reloaded, err := engine.repo.GetRunByID(context.Background(), run.ID)
			require.NoError(t, err)
			assert.Equal(t, status, reloaded.Status, "a %q run must not change status", status)
			assert.Equal(t, 0, len(engine.jobs), "no job must be enqueued for a rejected retry")
		})
	}
}

// TestRetryRunHandler_FailedRunReturns200AndResets verifies the happy path: a failed run
// returns 200 with status "pending", is reset for resume (retry bookkeeping cleared,
// CompletedActions + CurrentActionIdx preserved), and is re-queued on the jobs channel.
func TestRetryRunHandler_FailedRunReturns200AndResets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, handlerRunNowDiscardLogger())
	router := retryITRouter(handler, orgID, uuid.New(), "admin")

	run := retryITSeedRun(t, db, orgID, StatusFailed, []int{0})

	w := retryITPost(router, run.ID.String())

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := handlerRunNowDecode(t, w)
	require.NotNil(t, resp.Data, "a 200 must carry a data object: %s", w.Body.String())
	assert.Equal(t, run.ID.String(), resp.Data.ID, "the same run id resumes (it is re-queued, not cloned)")
	assert.Equal(t, StatusPending, resp.Data.Status)

	reloaded, err := engine.repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, reloaded.Status, "the run is flipped back to pending")
	assert.Equal(t, 0, reloaded.RetryCount, "retry counter is reset")
	assert.Empty(t, reloaded.LastError, "last error is cleared")
	assert.Nil(t, reloaded.FinishedAt, "finished_at is cleared")
	assert.True(t, GetCompletedActionIndices(reloaded)[0],
		"completed actions are preserved so execution resumes from the failed step")
	assert.Equal(t, 1, reloaded.CurrentActionIdx, "current action index is preserved")

	assert.Equal(t, 1, len(engine.jobs), "the retried run must be enqueued exactly once")
}

// ============================================================
// Authorization: creator allowance (org admin OR workflow creator)
// ============================================================

// TestRetryRunHandler_RouteNotRoleGuarded proves the retry route carries NO requireRole
// guard (authorization is in-handler, like Run Now): a non-privileged "viewer" must reach
// the handler rather than be rejected by route middleware. The request uses an invalid
// :runId so the handler returns 400 INVALID_ID before any repo/db/engine access.
func TestRetryRunHandler_RouteNotRoleGuarded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// requireCap spy that actually rejects a non-privileged role — so if the retry
	// route WERE guarded by a capability, a viewer would get 403.
	requireCap := func(_ string) gin.HandlerFunc {
		return func(c *gin.Context) {
			roleVal, _ := c.Get("role")
			roleStr, _ := roleVal.(string)
			if roleStr == "owner" || roleStr == "admin" || roleStr == "manager" {
				c.Next()
				return
			}
			c.AbortWithStatusJSON(http.StatusForbidden,
				ErrorResponse{Error: ErrorBody{Code: "FORBIDDEN", Message: "insufficient permissions"}})
		}
	}
	authMiddleware := func(c *gin.Context) {
		c.Set("org_id", uuid.New())
		c.Set("user_id", uuid.New())
		c.Set("role", "viewer")
		c.Next()
	}

	// engine/repo/db nil: an invalid :runId returns 400 before any is touched.
	h := &Handler{logger: handlerRunNowDiscardLogger()}
	h.RegisterRoutes(router, authMiddleware, requireCap)

	w := retryITPost(router, "not-a-valid-uuid")

	assert.NotEqual(t, http.StatusForbidden, w.Code,
		"a viewer must not be blocked by a role guard on the retry route; body: %s", w.Body.String())
	assert.Equal(t, http.StatusBadRequest, w.Code,
		"the viewer must reach the handler and hit the :runId parse (400 INVALID_ID), body: %s", w.Body.String())
	assert.Equal(t, "INVALID_ID", runNowITErrorCode(t, w))
}

// TestRetryRunHandler_CreatorAllowanceAllowsNonPrivilegedCreator verifies the creator
// allowance: a non-privileged ("viewer") caller may retry a failed run whose workflow they
// created. The run is reset and re-queued (200).
func TestRetryRunHandler_CreatorAllowanceAllowsNonPrivilegedCreator(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	userID := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, handlerRunNowDiscardLogger())
	router := retryITRouter(handler, orgID, userID, "viewer") // non-privileged

	// Workflow created BY the caller → creator allowance applies.
	wf := runNowITWorkflowCreatedBy(t, db, orgID, TriggerContactCreated, userID)
	run := retryITSeedRunForWorkflow(t, db, orgID, wf.ID, StatusFailed, []int{0})

	w := retryITPost(router, run.ID.String())

	require.Equal(t, http.StatusOK, w.Code,
		"a non-privileged creator must be allowed to retry their own workflow's run, body: %s", w.Body.String())
	reloaded, err := engine.repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, reloaded.Status)
	assert.Equal(t, 1, len(engine.jobs), "the creator's retry must enqueue the run")
}

// TestRetryRunHandler_NonCreatorNonPrivilegedForbidden verifies a caller who is neither
// privileged nor the workflow's creator is rejected 403 FORBIDDEN, and the run is NOT reset.
func TestRetryRunHandler_NonCreatorNonPrivilegedForbidden(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	callerID := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, handlerRunNowDiscardLogger())
	router := retryITRouter(handler, orgID, callerID, "viewer")

	// Workflow created by SOMEONE ELSE → creator allowance does not apply.
	wf := runNowITWorkflowCreatedBy(t, db, orgID, TriggerContactCreated, uuid.New())
	run := retryITSeedRunForWorkflow(t, db, orgID, wf.ID, StatusFailed, []int{0})

	w := retryITPost(router, run.ID.String())

	assert.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "FORBIDDEN", runNowITErrorCode(t, w))

	reloaded, err := engine.repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, reloaded.Status, "a forbidden retry must not reset the run")
	assert.Equal(t, 0, len(engine.jobs), "no job must be enqueued for a 403")
}
