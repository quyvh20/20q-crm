package automation

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// handlers_run_now_test.go contains unit / example tests for the (*Handler).RunNow
// HTTP handler (Run Now, P20 — task 3.3). They cover the handler's fixed rejection
// order and its success response per the design's Error Handling table:
//
//	| Stage         | Condition                          | HTTP | Error code          |
//	| Auth          | Missing/invalid org context        | 401  | UNAUTHORIZED        |
//	| Authz         | Not owner/admin/manager nor creator| 403  | FORBIDDEN           |
//	| Path          | :id not a valid UUID               | 400  | INVALID_ID          |
//	| Body          | Both / neither of contact/deal id  | 400  | INVALID_REQUEST     |
//	| Body          | Present id not a valid UUID        | 400  | INVALID_ID          |
//	| Engine        | RunWorkflowNow returns error       | 500  | INTERNAL_ERROR (no id) |
//	| Success       | Run initiated                      | 201  | data{id,status}     |
//
// Two distinct kinds of test live here:
//
//   - Pure (no Docker): the early-return rejection paths — 401, the :id parse, and body
//     validation — all return BEFORE the handler touches h.repo / h.db / h.engine
//     (verified against handlers.go: getContext → parse :id → classifyRunNowRequest →
//     GetWorkflowByID). They run against a Handler whose only populated field is a discard
//     logger, so they need no database. The authorization decision itself is a pure
//     function (authorizeRunNow) and is unit-tested exhaustively without HTTP or a DB.
//   - DB-backed (skips without Docker): the engine-failure 500, the success 201, and the
//     authorization 201/403 paths require a real workflow + contact load, so they reuse the
//     package's integration scaffolding (setupTestDB / makeEngine) and the sibling seed
//     helpers from run_now_integration_test.go (runNowITWorkflow / runNowITContact) and
//     post helpers (runNowITPostRun / runNowITErrorCode). They SKIP automatically when
//     testcontainers/Docker is unavailable, consistent with the rest of the package.
//
// Authorization model (Run Now creator allowance): unlike the other workflow-mutating
// endpoints, the /:id/run route carries NO requireRole guard. owner/admin/manager may run
// any workflow in the org; any other caller may run ONLY a workflow they created. This is
// enforced inside RunNow (it needs the loaded workflow's CreatedBy), so it is tested as the
// pure authorizeRunNow matrix plus DB-backed handler tests proving a non-privileged creator
// is allowed (201) and a non-privileged non-creator is forbidden (403). A separate test
// proves the route is registered WITHOUT a role guard (a non-privileged caller reaches the
// handler rather than being rejected by route middleware).
//
// Validates: Requirements 1.2, 1.3, 1.4, 2.6, 6.7, 7.2, 7.3, 7.5

// ============================================================
// File-local helpers (uniquely named handlerRunNow*)
// ============================================================

// handlerRunNowDiscardLogger returns a logger that discards output, so tests stay quiet.
func handlerRunNowDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// handlerRunNowMinimalHandler builds a Handler suitable for the early-return rejection
// tests only. engine / repo / db are intentionally nil: the 401, :id-parse, and body
// validation paths must return before any of them is dereferenced. If a future change
// reached the DB on these paths, these tests would panic on the nil deref — a useful
// guard that the rejection order stays correct.
func handlerRunNowMinimalHandler() *Handler {
	return &Handler{logger: handlerRunNowDiscardLogger()}
}

// handlerRunNowRouterNoOrg registers the Run Now route with NO org context injected, so
// getContext writes the 401 itself (Req 1.3).
func handlerRunNowRouterNoOrg(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/workflows/:id/run", h.RunNow)
	return router
}

// handlerRunNowResponse is the decoded shape of both the success envelope
// ({ "data": { "id", "status" } }) and the error envelope ({ "error": { "code" } }). A
// nil Data with a populated Error proves a failure carried NO run id (Req 7.5, 6.7).
type handlerRunNowResponse struct {
	Data *struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// handlerRunNowDecode parses a Run Now response body into handlerRunNowResponse.
func handlerRunNowDecode(t *testing.T, w *httptest.ResponseRecorder) handlerRunNowResponse {
	t.Helper()
	var resp handlerRunNowResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp),
		"response body must be JSON, got: %s", w.Body.String())
	return resp
}

// ============================================================
// Auth: missing org context → 401 (Req 1.3)
// ============================================================

// TestRunNowHandler_MissingOrgContextReturns401 verifies that a request reaching RunNow
// without a resolvable org context is rejected with 401 UNAUTHORIZED and carries no run
// id — before any DB access (Req 1.3).
//
// Validates: Requirements 1.3
func TestRunNowHandler_MissingOrgContextReturns401(t *testing.T) {
	h := handlerRunNowMinimalHandler()
	router := handlerRunNowRouterNoOrg(h)

	w := runNowITPostRun(router, uuid.New().String(),
		map[string]any{"contact_id": uuid.New().String()})

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"missing org context must return 401, body: %s", w.Body.String())
	assert.Equal(t, "UNAUTHORIZED", runNowITErrorCode(t, w))

	resp := handlerRunNowDecode(t, w)
	assert.Nil(t, resp.Data, "a 401 response must contain no run id")
}

// ============================================================
// Authorization: pure permission matrix (Req 1.2, 1.4)
// ============================================================

// TestRunNowAuthorized exhaustively verifies the Run Now permission decision
// (authorizeRunNow), the pure function the handler delegates to. owner/admin/manager may
// run ANY workflow regardless of who created it; any other role (or a missing role) may run
// ONLY a workflow they created; a nil caller id never satisfies the creator allowance.
//
// Validates: Requirements 1.2, 1.4
func TestRunNowAuthorized(t *testing.T) {
	creator := uuid.New()
	other := uuid.New()

	cases := []struct {
		name      string
		role      string
		userID    uuid.UUID
		createdBy uuid.UUID
		want      bool
	}{
		{"owner runs any workflow", "owner", other, creator, true},
		{"admin runs any workflow", "admin", other, creator, true},
		{"manager runs any workflow", "manager", other, creator, true},
		{"viewer runs own workflow (creator allowance)", "viewer", creator, creator, true},
		{"member runs own workflow (creator allowance)", "member", creator, creator, true},
		{"empty role runs own workflow (creator allowance)", "", creator, creator, true},
		{"viewer cannot run another's workflow", "viewer", other, creator, false},
		{"member cannot run another's workflow", "member", other, creator, false},
		{"unknown role cannot run another's workflow", "guest", other, creator, false},
		{"nil caller id never satisfies creator check", "viewer", uuid.Nil, uuid.Nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, authorizeRunNow(tc.role, tc.userID, tc.createdBy),
				"authorizeRunNow(%q, creator=%v) for createdBy=%v", tc.role, tc.userID == tc.createdBy, tc.createdBy)
		})
	}
}

// TestRunNowHandler_RouteNotRoleGuarded drives the real Handler.RegisterRoutes with a
// requireRole spy to prove the POST /api/workflows/:id/run route is registered WITHOUT a
// role guard — a deliberate departure from the other workflow-mutating endpoints so the
// creator allowance can be enforced in-handler. A non-privileged ("viewer") caller must
// therefore reach the handler rather than being rejected by route middleware. The request
// uses an invalid :id so the handler returns 400 INVALID_ID immediately, before any
// repo/db/engine access (which are nil here) — proving the caller got past routing without
// a 403 from a guard, and that the spy recorded no guard for this route.
//
// Validates: Requirements 1.2
func TestRunNowHandler_RouteNotRoleGuarded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// requireCap spy: records the capability code whenever a guard it produced
	// actually runs for a request. If the run route were guarded, this would be
	// non-empty after the request below. The guard emulates capability semantics
	// (system roles admin/manager/owner pass; a viewer is rejected).
	var guardedCaps []string
	requireCap := func(code string) gin.HandlerFunc {
		return func(c *gin.Context) {
			guardedCaps = append(guardedCaps, code)
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

	// authMiddleware injects a non-privileged role. If the run route were behind a
	// capability guard, this caller would be rejected with 403.
	authMiddleware := func(c *gin.Context) {
		c.Set("org_id", uuid.New())
		c.Set("user_id", uuid.New())
		c.Set("role", "viewer")
		c.Next()
	}

	// engine/repo/db are nil: the invalid-:id request returns 400 before any is touched.
	h := &Handler{logger: handlerRunNowDiscardLogger()}
	h.RegisterRoutes(router, authMiddleware, requireCap)

	// Invalid :id → handler returns 400 INVALID_ID before reaching repo/db/engine.
	w := runNowITPostRun(router, "not-a-valid-uuid",
		map[string]any{"contact_id": uuid.New().String()})

	assert.NotEqual(t, http.StatusForbidden, w.Code,
		"a viewer must not be blocked by a role guard on /:id/run; got 403, body: %s", w.Body.String())
	assert.Equal(t, http.StatusBadRequest, w.Code,
		"the viewer must reach the handler and hit the :id parse (400 INVALID_ID), body: %s", w.Body.String())
	assert.Equal(t, "INVALID_ID", runNowITErrorCode(t, w))
	assert.Empty(t, guardedCaps,
		"the /:id/run route must NOT be registered behind a capability guard (creator allowance is enforced in-handler)")
}

// ============================================================
// Authorization: creator allowance & forbidden (Req 1.2, 1.4) — DB-backed
// ============================================================

// TestRunNowHandler_CreatorAllowanceAllowsNonPrivilegedCreator verifies the creator
// allowance end to end: a caller whose role is NOT owner/admin/manager ("viewer") may still
// Run Now a workflow they created. The seeded workflow's CreatedBy is set to the caller's
// user id, so authorizeRunNow permits it and the request succeeds with 201.
//
// Validates: Requirements 1.2
func TestRunNowHandler_CreatorAllowanceAllowsNonPrivilegedCreator(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS company_id UUID`)
	db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`)

	orgID := uuid.New()
	userID := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, handlerRunNowDiscardLogger())
	// Non-privileged caller ("viewer") — only the creator allowance can authorize this run.
	router := runNowITRouterWithRole(handler, orgID, userID, "viewer")

	// Workflow created BY this caller, so the creator allowance applies.
	wf := runNowITWorkflowCreatedBy(t, db, orgID, TriggerContactCreated, userID)
	contactID := runNowITContact(t, db, orgID)

	w := runNowITPostRun(router, wf.ID.String(),
		map[string]any{"contact_id": contactID.String()})

	require.Equal(t, http.StatusCreated, w.Code,
		"a non-privileged creator must be allowed to run their own workflow (201), body: %s", w.Body.String())
	resp := handlerRunNowDecode(t, w)
	require.NotNil(t, resp.Data, "a 201 response must carry a data object: %s", w.Body.String())
	assert.Equal(t, int64(1), countRunsForWorkflow(t, engine, wf.ID),
		"the creator's run must persist exactly one run")
}

// TestRunNowHandler_NonCreatorNonPrivilegedForbidden verifies that a caller who is neither
// privileged (owner/admin/manager) NOR the workflow's creator is rejected with 403
// FORBIDDEN and creates no run. The seeded workflow's CreatedBy is a different user, and the
// caller's role is "viewer", so authorizeRunNow denies it.
//
// Validates: Requirements 1.2, 1.4
func TestRunNowHandler_NonCreatorNonPrivilegedForbidden(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS company_id UUID`)
	db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`)

	orgID := uuid.New()
	callerID := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, handlerRunNowDiscardLogger())
	router := runNowITRouterWithRole(handler, orgID, callerID, "viewer")

	// Workflow created by someone OTHER than the caller → creator allowance does not apply.
	wf := runNowITWorkflowCreatedBy(t, db, orgID, TriggerContactCreated, uuid.New())
	contactID := runNowITContact(t, db, orgID)

	w := runNowITPostRun(router, wf.ID.String(),
		map[string]any{"contact_id": contactID.String()})

	assert.Equal(t, http.StatusForbidden, w.Code,
		"a non-privileged non-creator must be rejected with 403, body: %s", w.Body.String())
	assert.Equal(t, "FORBIDDEN", runNowITErrorCode(t, w))

	resp := handlerRunNowDecode(t, w)
	assert.Nil(t, resp.Data, "a 403 response must carry no run id")
	assert.Equal(t, int64(0), countRunsForWorkflow(t, engine, wf.ID),
		"an unauthorized request must create zero runs")
}

// ============================================================
// Path / body validation → 400 (Req 2.6, 2.3, 2.4, 2.5)
// ============================================================

// TestRunNowHandler_InvalidPathIDReturns400 verifies that a :id path parameter that is
// not a syntactically valid UUID is rejected with 400 INVALID_ID before any DB access
// (Req 2.6).
//
// Validates: Requirements 2.6
func TestRunNowHandler_InvalidPathIDReturns400(t *testing.T) {
	h := handlerRunNowMinimalHandler()
	router := runNowITRouter(h, uuid.New(), uuid.New())

	w := runNowITPostRun(router, "not-a-valid-uuid",
		map[string]any{"contact_id": uuid.New().String()})

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"a non-UUID :id must return 400, body: %s", w.Body.String())
	assert.Equal(t, "INVALID_ID", runNowITErrorCode(t, w))

	resp := handlerRunNowDecode(t, w)
	assert.Nil(t, resp.Data, "a 400 response must contain no run id")
}

// TestRunNowHandler_BothIDsReturns400 verifies that a body carrying BOTH contact_id and
// deal_id is rejected with 400 INVALID_REQUEST and creates no run (Req 2.3).
//
// Validates: Requirements 2.3
func TestRunNowHandler_BothIDsReturns400(t *testing.T) {
	h := handlerRunNowMinimalHandler()
	router := runNowITRouter(h, uuid.New(), uuid.New())

	w := runNowITPostRun(router, uuid.New().String(), map[string]any{
		"contact_id": uuid.New().String(),
		"deal_id":    uuid.New().String(),
	})

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"both ids present must return 400, body: %s", w.Body.String())
	assert.Equal(t, "INVALID_REQUEST", runNowITErrorCode(t, w))

	resp := handlerRunNowDecode(t, w)
	assert.Nil(t, resp.Data, "a 400 response must contain no run id")
}

// TestRunNowHandler_NeitherIDReturns400 verifies that a body carrying NEITHER contact_id
// nor deal_id is rejected with 400 INVALID_REQUEST and creates no run (Req 2.4).
//
// Validates: Requirements 2.4
func TestRunNowHandler_NeitherIDReturns400(t *testing.T) {
	h := handlerRunNowMinimalHandler()
	router := runNowITRouter(h, uuid.New(), uuid.New())

	w := runNowITPostRun(router, uuid.New().String(), map[string]any{})

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"neither id present must return 400, body: %s", w.Body.String())
	assert.Equal(t, "INVALID_REQUEST", runNowITErrorCode(t, w))

	resp := handlerRunNowDecode(t, w)
	assert.Nil(t, resp.Data, "a 400 response must contain no run id")
}

// TestRunNowHandler_InvalidBodyUUIDReturns400 verifies that a single provided identifier
// that is not a syntactically valid UUID is rejected with 400 INVALID_ID and creates no
// run (Req 2.5).
//
// Validates: Requirements 2.5
func TestRunNowHandler_InvalidBodyUUIDReturns400(t *testing.T) {
	h := handlerRunNowMinimalHandler()
	router := runNowITRouter(h, uuid.New(), uuid.New())

	w := runNowITPostRun(router, uuid.New().String(),
		map[string]any{"contact_id": "definitely-not-a-uuid"})

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"an invalid body UUID must return 400, body: %s", w.Body.String())
	assert.Equal(t, "INVALID_ID", runNowITErrorCode(t, w))

	resp := handlerRunNowDecode(t, w)
	assert.Nil(t, resp.Data, "a 400 response must contain no run id")
}

// ============================================================
// Engine failure → 500 with no run id (Req 6.7, 7.5) — DB-backed
// ============================================================

// TestRunNowHandler_EngineFailureReturns500NoRunID verifies that when the engine reports
// a synchronous failure initiating the run, the handler responds 500 INTERNAL_ERROR and
// the body carries NO run id (Req 6.7, 7.5). The failure is induced the same way
// engine_run_now_test.go does — by dropping the automation_workflow_runs table after the
// (intact) workflow and contact are seeded, so the request passes every earlier stage and
// fails only at CreateRun inside RunWorkflowNow.
//
// Validates: Requirements 6.7, 7.5
func TestRunNowHandler_EngineFailureReturns500NoRunID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// loadContactForRun reads company_id / owner_user_id; ensure the columns exist on the
	// minimal contacts table setupTestDB provisions (mirrors run_now_exec_integration_test.go).
	db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS company_id UUID`)
	db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`)

	orgID := uuid.New()
	userID := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, handlerRunNowDiscardLogger())
	router := runNowITRouter(handler, orgID, userID)

	// Seed a compatible contact workflow + in-org contact so the request reaches the
	// engine call.
	wf := runNowITWorkflow(t, db, orgID, TriggerContactCreated)
	contactID := runNowITContact(t, db, orgID)

	// Force RunWorkflowNow's CreateRun to fail by removing the runs table. The workflow
	// and contact tables are untouched, so every earlier validation stage still passes.
	require.NoError(t, db.Migrator().DropTable(&WorkflowRun{}))

	w := runNowITPostRun(router, wf.ID.String(),
		map[string]any{"contact_id": contactID.String()})

	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"an engine failure must return 500, body: %s", w.Body.String())
	assert.Equal(t, "INTERNAL_ERROR", runNowITErrorCode(t, w))

	resp := handlerRunNowDecode(t, w)
	assert.Nil(t, resp.Data,
		"a failed run attempt must return no run id (Req 6.7, 7.5), body: %s", w.Body.String())
}

// ============================================================
// Success → 201 with id + status (Req 7.2, 7.3) — DB-backed
// ============================================================

// TestRunNowHandler_SuccessReturns201WithIDAndStatus verifies the happy path: a
// compatible, in-org contact workflow + contact yields 201 with a valid run id and a
// status of "pending" (StatusPending), and exactly one run is persisted for the targeted
// workflow (Req 7.2, 7.3).
//
// Validates: Requirements 7.2, 7.3
func TestRunNowHandler_SuccessReturns201WithIDAndStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS company_id UUID`)
	db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`)

	orgID := uuid.New()
	userID := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := NewHandler(engine, db, handlerRunNowDiscardLogger())
	router := runNowITRouter(handler, orgID, userID)

	wf := runNowITWorkflow(t, db, orgID, TriggerContactCreated)
	contactID := runNowITContact(t, db, orgID)

	w := runNowITPostRun(router, wf.ID.String(),
		map[string]any{"contact_id": contactID.String()})

	require.Equal(t, http.StatusCreated, w.Code,
		"a valid Run Now must return 201, body: %s", w.Body.String())

	resp := handlerRunNowDecode(t, w)
	require.NotNil(t, resp.Data, "a 201 response must carry a data object: %s", w.Body.String())

	runID, err := uuid.Parse(resp.Data.ID)
	require.NoError(t, err, "the response id must be a valid UUID, got %q", resp.Data.ID)
	assert.NotEqual(t, uuid.Nil, runID, "the response id must be a non-nil run id (Req 7.2)")
	assert.Equal(t, StatusPending, resp.Data.Status,
		"the response status must be the pending run status (Req 7.3)")

	// Cross-check: exactly one run was persisted for the targeted workflow.
	assert.Equal(t, int64(1), countRunsForWorkflow(t, engine, wf.ID),
		"a successful Run Now must persist exactly one run for the targeted workflow")
}
