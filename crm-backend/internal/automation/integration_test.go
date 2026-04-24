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
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ============================================================
// Test Helpers
// ============================================================

// countingExecutor tracks how many times Execute is called.
type countingExecutor struct {
	mu    sync.Mutex
	calls []int // action indices executed, in order
	count int64
}

func (e *countingExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	atomic.AddInt64(&e.count, 1)
	e.mu.Lock()
	e.calls = append(e.calls, 0) // placeholder, overwritten below
	idx := len(e.calls) - 1
	e.mu.Unlock()

	// Determine action index from action.ID
	var actionIdx int
	fmt.Sscanf(action.ID, "action_%d", &actionIdx)
	e.mu.Lock()
	e.calls[idx] = actionIdx
	e.mu.Unlock()

	return map[string]any{"executed": action.ID}, nil
}

func (e *countingExecutor) getCallCount() int64 {
	return atomic.LoadInt64(&e.count)
}

func (e *countingExecutor) getCalls() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]int, len(e.calls))
	copy(result, e.calls)
	return result
}

// failingExecutor returns a retryable error on every call.
type failingExecutor struct {
	mu    sync.Mutex
	calls int
}

func (e *failingExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	return nil, NewRetryableError(fmt.Errorf("simulated server error 500"))
}

func (e *failingExecutor) getCallCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

// setupTestDB starts a Postgres container via testcontainers-go,
// connects GORM, runs AutoMigrate, and returns the DB + cleanup func.
func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	ctx := context.Background()

	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
		tcpostgres.WithSQLDriver("pgx"),
	)
	if err != nil {
		t.Skipf("Docker not available — skipping integration test: %v", err)
	}

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err, "failed to connect to test database")

	repo := NewRepository(db)
	require.NoError(t, repo.AutoMigrate(), "migration failed")

	// Also create contacts table (needed for webhook inbound tests)
	db.Exec(`CREATE TABLE IF NOT EXISTS contacts (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		first_name TEXT DEFAULT '',
		last_name TEXT DEFAULT '',
		email TEXT,
		phone TEXT DEFAULT '',
		custom_fields JSONB DEFAULT '{}',
		deleted_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)

	cleanup := func() {
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Logf("warning: failed to terminate container: %v", err)
		}
	}

	return db, cleanup
}

// createTestWorkflow inserts a workflow + version with N actions.
func createTestWorkflow(t *testing.T, db *gorm.DB, orgID uuid.UUID, numActions int) *Workflow {
	t.Helper()

	trigger, _ := json.Marshal(map[string]any{"type": "webhook_inbound"})
	actions := make([]ActionSpec, numActions)
	for i := 0; i < numActions; i++ {
		actions[i] = ActionSpec{
			ID:     fmt.Sprintf("action_%d", i),
			Type:   "test_action",
			Params: map[string]any{"index": float64(i)},
		}
	}
	actionsJSON, _ := json.Marshal(actions)

	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     orgID,
		Name:      fmt.Sprintf("integration-test-%s", uuid.New().String()[:8]),
		IsActive:  true,
		Trigger:   datatypes.JSON(trigger),
		Actions:   datatypes.JSON(actionsJSON),
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

// makeEngine creates a test engine with the given executor map.
func makeEngine(db *gorm.DB, executors map[string]ActionExecutor) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		db:        db,
		repo:      NewRepository(db),
		logger:    slog.Default(),
		jobs:      make(chan WorkflowRunJob, 100),
		workers:   1,
		ctx:       ctx,
		cancel:    cancel,
		executors: executors,
	}
}

// ============================================================
// Integration Test 1: Crash Recovery (Kill & Resume)
// ============================================================

// TestIntegration_KillAndResume:
//  1. Creates a workflow with 3 actions
//  2. Enqueues a run
//  3. Executes action[0] successfully
//  4. Simulates a process kill (PostActionLogHook panics after action[1])
//  5. Verifies crash recovery resets the run to pending
//  6. Re-processes the run
//  7. Asserts: action[0] NOT re-executed, action[1] re-executed (crash before commit),
//     action[2] executed once. Total: 4 executor calls (0,1 first pass + 1,2 on recovery).
func TestIntegration_KillAndResume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	wf := createTestWorkflow(t, db, orgID, 3)
	repo := NewRepository(db)
	executor := &countingExecutor{}

	// --- Phase 1: First engine pass (will crash after action[1]) ---
	engine1 := makeEngine(db, map[string]ActionExecutor{"test_action": executor})

	var hookCallCount int64
	engine1.PostActionLogHook = func() {
		n := atomic.AddInt64(&hookCallCount, 1)
		if n == 2 {
			panic("simulated process kill after action[1]")
		}
	}

	// Create a run
	triggerCtx, _ := json.Marshal(map[string]any{
		"contact": map[string]any{"id": uuid.New().String(), "email": "test@example.com"},
		"trigger": map[string]any{"type": "webhook_inbound"},
	})
	run := &WorkflowRun{
		ID:              uuid.New(),
		WorkflowID:      wf.ID,
		WorkflowVersion: 1,
		OrgID:           orgID,
		Status:          StatusPending,
		TriggerContext:  datatypes.JSON(triggerCtx),
		IdempotencyKey:  fmt.Sprintf("test-%s", uuid.New().String()),
	}
	inserted, err := repo.CreateRun(context.Background(), run)
	require.NoError(t, err)
	require.True(t, inserted)

	// Process — PostActionLogHook will panic after action[1].
	// processRun has defer+recover, so the panic is caught internally.
	// We verify the crash effect through DB state.
	engine1.processRun(run.ID)
	engine1.cancel()

	// Verify crash state: action[0] committed, action[1] rolled back
	crashedRun, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	completedSet := GetCompletedActionIndices(crashedRun)
	assert.True(t, completedSet[0], "action[0] committed before crash")
	assert.False(t, completedSet[1], "action[1] tx rolled back")
	assert.Equal(t, int64(2), executor.getCallCount(), "executor called for action[0] and action[1]")

	// --- Phase 2: Recovery ---
	RequeueInFlight(context.Background(), repo, make(chan WorkflowRunJob, 100), slog.Default())

	recoveredRun, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, recoveredRun.Status)
	assert.Equal(t, 1, recoveredRun.RecoveryCount)

	// New engine, no crash hook
	engine2 := makeEngine(db, map[string]ActionExecutor{"test_action": executor})
	defer engine2.cancel()
	engine2.processRun(recoveredRun.ID)

	// Verify final state
	finalRun, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, finalRun.Status)
	assert.NotNil(t, finalRun.FinishedAt)

	finalCompleted := GetCompletedActionIndices(finalRun)
	assert.True(t, finalCompleted[0] && finalCompleted[1] && finalCompleted[2])
	assert.Equal(t, int64(4), executor.getCallCount(),
		"total: action[0]×1 + action[1]×2 + action[2]×1 = 4")
	assert.Equal(t, []int{0, 1, 1, 2}, executor.getCalls())

	// Explicit: action[0] must appear exactly once — it was committed in
	// Phase 1 and must NOT be re-executed during the Phase 2 resume.
	calls := executor.getCalls()
	action0Count := 0
	for _, idx := range calls {
		if idx == 0 {
			action0Count++
		}
	}
	assert.Equal(t, 1, action0Count,
		"completed action[0] must NOT be re-executed on resume (idempotency)")

	logs, err := repo.GetActionLogsByRunID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(logs), 3)
}

// ============================================================
// Integration Test 2: HTTP Create Workflow (happy + validation)
// ============================================================

func TestIntegration_CreateWorkflow_HappyAndValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	userID := uuid.New()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	handler := &Handler{
		engine:      engine,
		repo:        engine.repo,
		db:          db,
		logger:      slog.Default(),
		rateLimiter: newTokenBucket(),
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Simulate auth middleware by injecting org_id + user_id as uuid.UUID (matching real middleware)
	router.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Set("role", "admin")
		c.Next()
	})
	router.POST("/api/workflows", handler.CreateWorkflow)
	router.GET("/api/workflows", handler.ListWorkflows)

	// --- Happy path: valid workflow ---
	payload := map[string]any{
		"name":        "Test Workflow",
		"description": "Integration test",
		"trigger":     map[string]any{"type": "contact_created"},
		"actions": []map[string]any{
			{"type": "send_email", "id": "a1", "params": map[string]any{"to": "{{contact.email}}"}},
		},
	}
	body, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workflows", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code, "expected 201 Created, body: %s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data, ok := resp["data"].(map[string]any)
	require.True(t, ok, "response must have 'data' object, got: %s", w.Body.String())
	assert.Equal(t, "Test Workflow", data["name"])
	assert.NotEmpty(t, data["id"])

	// --- List should return the created workflow ---
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/workflows?page=1&size=20", nil)
	router.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code, "list body: %s", w2.Body.String())

	var listResp map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &listResp))
	listData, ok := listResp["data"].(map[string]any)
	require.True(t, ok, "list response must have 'data' object, got: %s", w2.Body.String())
	workflows, ok := listData["workflows"].([]any)
	require.True(t, ok, "list data must have 'workflows' array")
	assert.Len(t, workflows, 1)
	wfResp := workflows[0].(map[string]any)
	assert.Equal(t, float64(1), wfResp["action_count"])

	// --- Validation failure: unknown trigger type ---
	badPayload := map[string]any{
		"name":    "Bad Workflow",
		"trigger": map[string]any{"type": "nonexistent_trigger"},
		"actions": []map[string]any{
			{"type": "send_email", "id": "a1", "params": map[string]any{"to": "x"}},
		},
	}
	body2, _ := json.Marshal(badPayload)
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("POST", "/api/workflows", bytes.NewReader(body2))
	req3.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w3, req3)

	assert.Equal(t, http.StatusBadRequest, w3.Code, "expected 400 for invalid trigger")
}

// ============================================================
// Integration Test 3: Retry Sweeper
// ============================================================

func TestIntegration_RetrySweeper(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	wf := createTestWorkflow(t, db, orgID, 2)
	repo := NewRepository(db)

	// Create a run stuck in "pending" with next_retry_at in the past
	pastRetry := time.Now().Add(-5 * time.Minute)
	triggerCtx, _ := json.Marshal(map[string]any{"type": "test"})
	run := &WorkflowRun{
		ID:              uuid.New(),
		WorkflowID:      wf.ID,
		WorkflowVersion: 1,
		OrgID:           orgID,
		Status:          StatusPending,
		TriggerContext:  datatypes.JSON(triggerCtx),
		RetryCount:      1,
		NextRetryAt:     &pastRetry,
		IdempotencyKey:  fmt.Sprintf("retry-test-%s", uuid.New().String()),
	}
	inserted, err := repo.CreateRun(context.Background(), run)
	require.NoError(t, err)
	require.True(t, inserted)

	// Sweep should pick it up
	ids, err := repo.SweepRetries(context.Background())
	require.NoError(t, err)
	assert.Contains(t, ids, run.ID, "sweeper must find run with expired next_retry_at")

	// Now process it with a working executor
	executor := &countingExecutor{}
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": executor})
	defer engine.cancel()

	engine.processRun(run.ID)

	finalRun, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, finalRun.Status, "run should complete after retry sweep picks it up")
	assert.Equal(t, int64(2), executor.getCallCount(), "both actions executed")
}

// ============================================================
// Integration Test 4: Retryable Action Fails 3x Then Permanent Failure
// ============================================================

func TestIntegration_RetryableAction_ExhaustsRetries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()

	// Create workflow with 1 action of type "failing_action"
	trigger, _ := json.Marshal(map[string]any{"type": "contact_created"})
	actions, _ := json.Marshal([]ActionSpec{
		{ID: "a1", Type: "failing_action", Params: map[string]any{}},
	})
	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     orgID,
		Name:      "retry-exhaust-test",
		IsActive:  true,
		Trigger:   datatypes.JSON(trigger),
		Actions:   datatypes.JSON(actions),
		Version:   1,
		CreatedBy: uuid.New(),
	}
	require.NoError(t, db.Create(wf).Error)
	ver := &WorkflowVersion{
		ID: uuid.New(), WorkflowID: wf.ID, Version: 1,
		Trigger: wf.Trigger, Actions: wf.Actions, CreatedAt: time.Now(),
	}
	require.NoError(t, db.Create(ver).Error)

	repo := NewRepository(db)
	failExec := &failingExecutor{}

	// Simulate 4 attempts (initial + 3 retries)
	for attempt := 0; attempt < 4; attempt++ {
		engine := makeEngine(db, map[string]ActionExecutor{"failing_action": failExec})

		if attempt == 0 {
			// Create initial run
			triggerCtx, _ := json.Marshal(map[string]any{"trigger": map[string]any{"type": "contact_created"}})
			run := &WorkflowRun{
				ID:              uuid.New(),
				WorkflowID:      wf.ID,
				WorkflowVersion: 1,
				OrgID:           orgID,
				Status:          StatusPending,
				TriggerContext:  datatypes.JSON(triggerCtx),
				IdempotencyKey:  fmt.Sprintf("exhaust-%s", uuid.New().String()),
			}
			inserted, err := repo.CreateRun(context.Background(), run)
			require.NoError(t, err)
			require.True(t, inserted)

			engine.processRun(run.ID)

			// After 1st failure: should be pending with retry scheduled
			afterRun, err := repo.GetRunByID(context.Background(), run.ID)
			require.NoError(t, err)
			assert.Equal(t, StatusPending, afterRun.Status)
			assert.Equal(t, 1, afterRun.RetryCount)
			assert.NotNil(t, afterRun.NextRetryAt)

			// Next iterations reprocess this same run
			for i := 1; i < 4; i++ {
				engine2 := makeEngine(db, map[string]ActionExecutor{"failing_action": failExec})
				engine2.processRun(run.ID)
				engine2.cancel()
			}

			// After 4th attempt (retryCount=3, exceeds max): should be FAILED
			finalRun, err := repo.GetRunByID(context.Background(), run.ID)
			require.NoError(t, err)
			assert.Equal(t, StatusFailed, finalRun.Status, "run must be failed after exhausting retries")
			assert.Equal(t, 3, finalRun.RetryCount)
			assert.NotNil(t, finalRun.FinishedAt)
			assert.Contains(t, finalRun.LastError, "simulated server error 500")

			// failingExecutor called 4 times total
			assert.Equal(t, 4, failExec.getCallCount(), "executor called 4 times: initial + 3 retries")

			engine.cancel()
			break
		}

		engine.cancel()
	}
}

// ============================================================
// Integration Test 5: Webhook Inbound → Contact Upsert → Trigger Fires
// ============================================================

func TestIntegration_WebhookInbound_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	executor := &countingExecutor{}
	engine := makeEngine(db, map[string]ActionExecutor{
		"send_email": executor,
		"test_action": executor,
	})
	defer engine.cancel()

	repo := NewRepository(db)

	// Create an org token for webhook auth
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	token := &WorkflowOrgToken{
		OrgID:     orgID,
		Token:     fmt.Sprintf("test-token-%s", uuid.New().String()[:8]),
		Secret:    "test-secret",
		CreatedAt: time.Now(),
	}
	require.NoError(t, db.Create(token).Error)

	// Create a workflow triggered by contact_created
	trigger, _ := json.Marshal(map[string]any{"type": "contact_created"})
	actions, _ := json.Marshal([]ActionSpec{
		{ID: "email1", Type: "test_action", Params: map[string]any{"to": "{{contact.email}}"}},
	})
	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     orgID,
		Name:      "webhook-e2e-test",
		IsActive:  true,
		Trigger:   datatypes.JSON(trigger),
		Actions:   datatypes.JSON(actions),
		Version:   1,
		CreatedBy: uuid.New(),
	}
	require.NoError(t, db.Create(wf).Error)
	ver := &WorkflowVersion{
		ID: uuid.New(), WorkflowID: wf.ID, Version: 1,
		Trigger: wf.Trigger, Actions: wf.Actions, CreatedAt: time.Now(),
	}
	require.NoError(t, db.Create(ver).Error)

	// Set up HTTP handler
	handler := &Handler{
		engine:      engine,
		repo:        repo,
		db:          db,
		logger:      slog.Default(),
		rateLimiter: newTokenBucket(),
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/webhooks/inbound/:org_token", handler.WebhookInbound)

	// Send webhook payload
	webhookPayload := map[string]any{
		"email":      "newlead@example.com",
		"first_name": "Jane",
		"last_name":  "Doe",
		"company":    "Acme Inc",
		"utm_source": "google",
	}
	body, _ := json.Marshal(webhookPayload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/webhooks/inbound/"+token.Token, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "webhook should return 200")

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "accepted", resp["status"])
	assert.NotEmpty(t, resp["contact_id"])

	// Verify contact was created
	var contact struct {
		ID    uuid.UUID `gorm:"column:id"`
		Email string    `gorm:"column:email"`
	}
	err := db.Raw("SELECT id, email FROM contacts WHERE org_id = ? AND email = ?", orgID, "newlead@example.com").Scan(&contact).Error
	require.NoError(t, err)
	assert.Equal(t, "newlead@example.com", contact.Email)

	// Wait for trigger event goroutine to fire and engine to process
	// TriggerEvent is fire-and-forget (goroutine), so we poll
	var runs []WorkflowRun
	require.Eventually(t, func() bool {
		db.Where("workflow_id = ?", wf.ID).Find(&runs)
		return len(runs) > 0
	}, 5*time.Second, 100*time.Millisecond, "trigger should create a run")

	// Process the created run
	engine.processRun(runs[0].ID)

	// Verify run completed
	finalRun, err := repo.GetRunByID(context.Background(), runs[0].ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, finalRun.Status)
	assert.GreaterOrEqual(t, executor.getCallCount(), int64(1))
}

// ============================================================
// Integration Test 6: Full Pipeline
// contact_created → if tags contains vip → send_email → delay → create_task
// ============================================================

// recordingEmailExecutor captures the email params for assertion.
type recordingEmailExecutor struct {
	mu     sync.Mutex
	called bool
	to     string
}

func (e *recordingEmailExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.called = true
	e.to = getStringParam(action.Params, "to", evalCtx)
	return map[string]any{"status": "sent", "to": e.to}, nil
}

func TestIntegration_FullPipeline_VIPContact(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	contactID := uuid.New()
	repo := NewRepository(db)

	// Create the tasks table (TaskExecutor writes directly to it)
	db.Exec(`CREATE TABLE IF NOT EXISTS tasks (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		title TEXT NOT NULL,
		contact_id UUID,
		deal_id UUID,
		assigned_to UUID,
		due_at TIMESTAMPTZ,
		priority TEXT DEFAULT 'medium',
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)

	// --- Build the workflow ---
	// Trigger: contact_created
	// Conditions: contact.tags contains "vip"
	// Actions: send_email → delay 1s → create_task

	trigger, _ := json.Marshal(TriggerSpec{Type: TriggerContactCreated})

	conditions, _ := json.Marshal(ConditionGroup{
		Op: "AND",
		Rules: []ConditionRule{{
			Field:    "contact.tags",
			Operator: "contains",
			Value:    "vip",
		}},
	})

	actions, _ := json.Marshal([]ActionSpec{
		{
			ID:   "email_vip",
			Type: ActionSendEmail,
			Params: map[string]any{
				"to":        "{{contact.email}}",
				"subject":   "Welcome VIP!",
				"body_html": "<h1>Hello {{contact.first_name}}</h1>",
			},
		},
		{
			ID:   "delay_1s",
			Type: ActionDelay,
			Params: map[string]any{
				"duration_sec": float64(1),
			},
		},
		{
			ID:   "create_followup",
			Type: ActionCreateTask,
			Params: map[string]any{
				"title":      "Follow up with VIP: {{contact.first_name}}",
				"priority":   "high",
				"due_in_days": float64(3),
			},
		},
	})

	wf := &Workflow{
		ID:         uuid.New(),
		OrgID:      orgID,
		Name:       "VIP Welcome Pipeline",
		IsActive:   true,
		Trigger:    datatypes.JSON(trigger),
		Conditions: datatypes.JSON(conditions),
		Actions:    datatypes.JSON(actions),
		Version:    1,
		CreatedBy:  uuid.New(),
	}
	require.NoError(t, db.Create(wf).Error)

	ver := &WorkflowVersion{
		ID:         uuid.New(),
		WorkflowID: wf.ID,
		Version:    1,
		Trigger:    wf.Trigger,
		Conditions: wf.Conditions,
		Actions:    wf.Actions,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, db.Create(ver).Error)

	// --- Set up executors ---
	emailExec := &recordingEmailExecutor{}
	taskExec := NewTaskExecutor(db) // real executor, writes to tasks table
	delayExec := NewDelayExecutor() // real executor, actually waits

	engine := makeEngine(db, map[string]ActionExecutor{
		ActionSendEmail:  emailExec,
		ActionDelay:      delayExec,
		ActionCreateTask: taskExec,
	})
	defer engine.cancel()

	// --- Create the trigger context (VIP contact) ---
	triggerCtx, _ := json.Marshal(map[string]any{
		"entity_id": contactID.String(),
		"contact": map[string]any{
			"id":         contactID.String(),
			"email":      "vip@example.com",
			"first_name": "Jane",
			"last_name":  "Doe",
			"tags":       []any{"enterprise", "vip", "priority"},
		},
		"trigger": map[string]any{
			"type":   TriggerContactCreated,
			"source": "webhook_inbound",
		},
	})

	run := &WorkflowRun{
		ID:              uuid.New(),
		WorkflowID:      wf.ID,
		WorkflowVersion: 1,
		OrgID:           orgID,
		Status:          StatusPending,
		TriggerContext:  datatypes.JSON(triggerCtx),
		IdempotencyKey:  fmt.Sprintf("vip-test-%s", uuid.New().String()),
	}
	inserted, err := repo.CreateRun(context.Background(), run)
	require.NoError(t, err)
	require.True(t, inserted)

	// --- Execute the run ---
	startTime := time.Now()
	engine.processRun(run.ID)
	elapsed := time.Since(startTime)

	// --- Verify: run completed ---
	finalRun, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, finalRun.Status, "run must complete")
	assert.NotNil(t, finalRun.FinishedAt, "FinishedAt must be set")
	assert.Equal(t, 3, finalRun.CurrentActionIdx, "all 3 actions executed")

	// --- Verify: email sent with correct address ---
	emailExec.mu.Lock()
	assert.True(t, emailExec.called, "send_email executor must be called")
	assert.Equal(t, "vip@example.com", emailExec.to,
		"email 'to' must resolve {{contact.email}} to vip@example.com")
	emailExec.mu.Unlock()

	// --- Verify: delay was observed (≥1s) ---
	assert.GreaterOrEqual(t, elapsed, 1*time.Second,
		"delay action must wait at least 1s")

	// --- Verify: task was created in DB ---
	var taskCount int64
	db.Raw("SELECT COUNT(*) FROM tasks WHERE org_id = ?", orgID).Scan(&taskCount)
	assert.Equal(t, int64(1), taskCount, "create_task must insert exactly one task row")

	var taskTitle string
	db.Raw("SELECT title FROM tasks WHERE org_id = ?", orgID).Scan(&taskTitle)
	assert.Equal(t, "Follow up with VIP: Jane", taskTitle,
		"task title must resolve {{contact.first_name}}")

	// --- Verify: exactly 3 action logs ---
	logs, err := repo.GetActionLogsByRunID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Len(t, logs, 3, "must have exactly 3 action logs")

	// Verify log types in order
	logTypes := make([]string, len(logs))
	for i, l := range logs {
		logTypes[i] = l.ActionType
	}
	assert.Equal(t, []string{ActionSendEmail, ActionDelay, ActionCreateTask}, logTypes,
		"action log types must match pipeline order")

	// All logs should be success
	for _, l := range logs {
		assert.Equal(t, LogStatusSuccess, l.Status,
			"action log %s must be success", l.ActionType)
	}
}

// ============================================================
// Integration Test 7: Condition rejection (non-VIP contact skips)
// ============================================================

func TestIntegration_FullPipeline_NonVIP_Skipped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	repo := NewRepository(db)

	// Same workflow as above
	trigger, _ := json.Marshal(TriggerSpec{Type: TriggerContactCreated})
	conditions, _ := json.Marshal(ConditionGroup{
		Op: "AND",
		Rules: []ConditionRule{{
			Field:    "contact.tags",
			Operator: "contains",
			Value:    "vip",
		}},
	})
	actions, _ := json.Marshal([]ActionSpec{
		{ID: "a1", Type: ActionSendEmail, Params: map[string]any{"to": "x@y.com"}},
	})

	wf := &Workflow{
		ID: uuid.New(), OrgID: orgID, Name: "VIP-only",
		IsActive: true, Trigger: datatypes.JSON(trigger),
		Conditions: datatypes.JSON(conditions), Actions: datatypes.JSON(actions),
		Version: 1, CreatedBy: uuid.New(),
	}
	require.NoError(t, db.Create(wf).Error)
	ver := &WorkflowVersion{
		ID: uuid.New(), WorkflowID: wf.ID, Version: 1,
		Trigger: wf.Trigger, Conditions: wf.Conditions,
		Actions: wf.Actions, CreatedAt: time.Now(),
	}
	require.NoError(t, db.Create(ver).Error)

	executor := &countingExecutor{}
	engine := makeEngine(db, map[string]ActionExecutor{ActionSendEmail: executor})
	defer engine.cancel()

	// Non-VIP contact — tags do NOT contain "vip"
	triggerCtx, _ := json.Marshal(map[string]any{
		"contact": map[string]any{
			"id":   uuid.New().String(),
			"tags": []any{"regular", "newsletter"},
		},
		"trigger": map[string]any{"type": TriggerContactCreated},
	})

	run := &WorkflowRun{
		ID: uuid.New(), WorkflowID: wf.ID, WorkflowVersion: 1,
		OrgID: orgID, Status: StatusPending,
		TriggerContext: datatypes.JSON(triggerCtx),
		IdempotencyKey: fmt.Sprintf("nonvip-%s", uuid.New().String()),
	}
	inserted, err := repo.CreateRun(context.Background(), run)
	require.NoError(t, err)
	require.True(t, inserted)

	engine.processRun(run.ID)

	finalRun, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusSkipped, finalRun.Status,
		"run must be SKIPPED when conditions not met")
	assert.Equal(t, int64(0), executor.getCallCount(),
		"no actions should execute when conditions fail")
}

// ============================================================
// Integration Test 8: send_webhook → 500 → retries at ~30s, ~2m, ~10m → failed
// ============================================================

func TestIntegration_SendWebhook_500_RetriesAndFails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	repo := NewRepository(db)

	// --- Spin up a test HTTP server that always returns 500 ---
	var webhookHits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&webhookHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"service unavailable"}`))
	}))
	defer srv.Close()

	// --- Create workflow with send_webhook action ---
	trigger, _ := json.Marshal(TriggerSpec{Type: TriggerContactCreated})
	actions, _ := json.Marshal([]ActionSpec{
		{
			ID:   "webhook_500",
			Type: ActionSendWebhook,
			Params: map[string]any{
				"url":    srv.URL,
				"method": "POST",
			},
		},
	})

	wf := &Workflow{
		ID: uuid.New(), OrgID: orgID, Name: "webhook-retry-test",
		IsActive: true, Trigger: datatypes.JSON(trigger),
		Actions: datatypes.JSON(actions), Version: 1, CreatedBy: uuid.New(),
	}
	require.NoError(t, db.Create(wf).Error)
	ver := &WorkflowVersion{
		ID: uuid.New(), WorkflowID: wf.ID, Version: 1,
		Trigger: wf.Trigger, Actions: wf.Actions, CreatedAt: time.Now(),
	}
	require.NoError(t, db.Create(ver).Error)

	// --- Use the REAL WebhookExecutor (not a mock) ---
	webhookExec := NewWebhookExecutor()
	engine := makeEngine(db, map[string]ActionExecutor{
		ActionSendWebhook: webhookExec,
	})
	defer engine.cancel()

	triggerCtx, _ := json.Marshal(map[string]any{
		"contact": map[string]any{"id": uuid.New().String()},
		"trigger": map[string]any{"type": TriggerContactCreated},
	})

	run := &WorkflowRun{
		ID: uuid.New(), WorkflowID: wf.ID, WorkflowVersion: 1,
		OrgID: orgID, Status: StatusPending,
		TriggerContext: datatypes.JSON(triggerCtx),
		IdempotencyKey: fmt.Sprintf("wh500-%s", uuid.New().String()),
	}
	inserted, err := repo.CreateRun(context.Background(), run)
	require.NoError(t, err)
	require.True(t, inserted)

	// === Attempt 1: initial run → retryable 500 → status=pending, retryCount=1 ===
	beforeAttempt1 := time.Now()
	engine.processRun(run.ID)

	after1, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, after1.Status, "attempt 1: should be pending (retry scheduled)")
	assert.Equal(t, 1, after1.RetryCount)
	require.NotNil(t, after1.NextRetryAt)
	// Backoff 1 = 30s → NextRetryAt should be ~30s from now
	expectedRetry1 := beforeAttempt1.Add(30 * time.Second)
	assert.WithinDuration(t, expectedRetry1, *after1.NextRetryAt, 5*time.Second,
		"attempt 1: NextRetryAt should be ~30s from now")
	assert.Contains(t, after1.LastError, "500",
		"attempt 1: LastError should mention 500")

	// === Attempt 2: re-process → retryCount=2, backoff ~2m ===
	beforeAttempt2 := time.Now()
	engine.processRun(run.ID)

	after2, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, after2.Status, "attempt 2: should be pending")
	assert.Equal(t, 2, after2.RetryCount)
	require.NotNil(t, after2.NextRetryAt)
	expectedRetry2 := beforeAttempt2.Add(2 * time.Minute)
	assert.WithinDuration(t, expectedRetry2, *after2.NextRetryAt, 5*time.Second,
		"attempt 2: NextRetryAt should be ~2m from now")

	// === Attempt 3: re-process → retryCount=3, backoff ~10m ===
	beforeAttempt3 := time.Now()
	engine.processRun(run.ID)

	after3, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, after3.Status, "attempt 3: should be pending")
	assert.Equal(t, 3, after3.RetryCount)
	require.NotNil(t, after3.NextRetryAt)
	expectedRetry3 := beforeAttempt3.Add(10 * time.Minute)
	assert.WithinDuration(t, expectedRetry3, *after3.NextRetryAt, 5*time.Second,
		"attempt 3: NextRetryAt should be ~10m from now")

	// === Attempt 4: retryCount=3, exceeds max → permanent failure ===
	engine.processRun(run.ID)

	finalRun, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, finalRun.Status,
		"attempt 4: run must be FAILED after exhausting retries")
	assert.Equal(t, 3, finalRun.RetryCount,
		"retryCount stays at 3 (max reached, not incremented)")
	assert.NotNil(t, finalRun.FinishedAt)
	assert.Contains(t, finalRun.LastError, "500",
		"LastError must reference the 500 status code")

	// === Verify: httptest server was hit exactly 4 times ===
	assert.Equal(t, int64(4), atomic.LoadInt64(&webhookHits),
		"webhook endpoint must be called exactly 4 times (1 initial + 3 retries)")

	// === Verify: action logs show status transitions ===
	logs, err := repo.GetActionLogsByRunID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Len(t, logs, 4, "must have 4 action logs (one per attempt)")

	retryingCount := 0
	failedCount := 0
	for _, l := range logs {
		switch l.Status {
		case LogStatusRetrying:
			retryingCount++
		case LogStatusFailed:
			failedCount++
		}
		assert.Equal(t, ActionSendWebhook, l.ActionType)
	}
	assert.Equal(t, 3, retryingCount, "first 3 logs should be 'retrying'")
	assert.Equal(t, 1, failedCount, "last log should be 'failed'")
}
