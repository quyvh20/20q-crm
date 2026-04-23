package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrNilTransaction is returned when a nil transaction is passed to a method that requires one.
var ErrNilTransaction = fmt.Errorf("automation: nil transaction passed to method that requires explicit tx")

// Repository provides data access for the automation engine.
type Repository struct {
	db *gorm.DB
}

// NewRepository creates a new automation repository.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// AutoMigrate creates/updates tables and indexes for the automation engine.
func (r *Repository) AutoMigrate() error {
	if err := r.db.AutoMigrate(
		&Workflow{},
		&WorkflowVersion{},
		&WorkflowRun{},
		&WorkflowActionLog{},
		&WorkflowOrgToken{},
	); err != nil {
		return err
	}

	// Composite indexes per spec
	r.db.Exec(`CREATE INDEX IF NOT EXISTS idx_wf_runs_status_retry ON automation_workflow_runs (status, next_retry_at)`)
	r.db.Exec(`CREATE INDEX IF NOT EXISTS idx_wf_action_logs_run_action ON automation_workflow_action_logs (run_id, action_idx)`)
	r.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_wf_versions_wf_ver ON automation_workflow_versions (workflow_id, version)`)
	r.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_wf_runs_wf_idemp ON automation_workflow_runs (workflow_id, idempotency_key)`)

	return nil
}

// --- Workflow CRUD ---

// CreateWorkflow creates a new workflow and its initial version snapshot.
func (r *Repository) CreateWorkflow(ctx context.Context, wf *Workflow) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if wf.ID == uuid.Nil {
			wf.ID = uuid.New()
		}
		wf.Version = 1
		if err := tx.Create(wf).Error; err != nil {
			return err
		}
		// Create version snapshot
		ver := WorkflowVersion{
			ID:         uuid.New(),
			WorkflowID: wf.ID,
			Version:    wf.Version,
			Trigger:    wf.Trigger,
			Conditions: wf.Conditions,
			Actions:    wf.Actions,
			CreatedAt:  time.Now(),
		}
		return tx.Create(&ver).Error
	})
}

// UpdateWorkflow updates a workflow and creates a new version snapshot.
func (r *Repository) UpdateWorkflow(ctx context.Context, wf *Workflow) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		wf.Version++
		if err := tx.Save(wf).Error; err != nil {
			return err
		}
		ver := WorkflowVersion{
			ID:         uuid.New(),
			WorkflowID: wf.ID,
			Version:    wf.Version,
			Trigger:    wf.Trigger,
			Conditions: wf.Conditions,
			Actions:    wf.Actions,
			CreatedAt:  time.Now(),
		}
		return tx.Create(&ver).Error
	})
}

// GetWorkflowByID retrieves a workflow by ID within an org.
func (r *Repository) GetWorkflowByID(ctx context.Context, orgID, id uuid.UUID) (*Workflow, error) {
	var wf Workflow
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		First(&wf).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &wf, nil
}

// ListWorkflows lists workflows for an org with optional filtering and pagination.
func (r *Repository) ListWorkflows(ctx context.Context, orgID uuid.UUID, activeOnly bool, page, size int) ([]Workflow, int64, error) {
	query := r.db.WithContext(ctx).Where("org_id = ?", orgID)
	if activeOnly {
		query = query.Where("is_active = ?", true)
	}

	var total int64
	if err := query.Model(&Workflow{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	offset := (page - 1) * size

	var workflows []Workflow
	err := query.Order("created_at DESC").
		Offset(offset).Limit(size).
		Find(&workflows).Error
	return workflows, total, err
}

// SoftDeleteWorkflow soft-deletes a workflow and deactivates it.
func (r *Repository) SoftDeleteWorkflow(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Deactivate first
		if err := tx.Model(&Workflow{}).
			Where("org_id = ? AND id = ?", orgID, id).
			Update("is_active", false).Error; err != nil {
			return err
		}
		return tx.Where("org_id = ? AND id = ?", orgID, id).
			Delete(&Workflow{}).Error
	})
}

// ToggleWorkflow flips the is_active flag.
func (r *Repository) ToggleWorkflow(ctx context.Context, orgID, id uuid.UUID) (*Workflow, error) {
	var wf Workflow
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("org_id = ? AND id = ?", orgID, id).First(&wf).Error; err != nil {
			return err
		}
		wf.IsActive = !wf.IsActive
		return tx.Save(&wf).Error
	})
	if err != nil {
		return nil, err
	}
	return &wf, nil
}

// GetWorkflowVersion retrieves a specific version snapshot.
func (r *Repository) GetWorkflowVersion(ctx context.Context, workflowID uuid.UUID, version int) (*WorkflowVersion, error) {
	var ver WorkflowVersion
	err := r.db.WithContext(ctx).
		Where("workflow_id = ? AND version = ?", workflowID, version).
		First(&ver).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ver, nil
}

// GetActiveWorkflowsByTrigger returns active workflows matching a trigger type for an org.
func (r *Repository) GetActiveWorkflowsByTrigger(ctx context.Context, orgID uuid.UUID, triggerType string) ([]Workflow, error) {
	var workflows []Workflow
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND is_active = ? AND trigger->>'type' = ?", orgID, true, triggerType).
		Find(&workflows).Error
	return workflows, err
}

// --- WorkflowRun CRUD ---

// CreateRun inserts a new workflow run. Returns false if idempotency key already exists.
func (r *Repository) CreateRun(ctx context.Context, run *WorkflowRun) (bool, error) {
	if run.ID == uuid.Nil {
		run.ID = uuid.New()
	}
	err := r.db.WithContext(ctx).Create(run).Error
	if err != nil {
		// Check for unique constraint violation on idempotency_key
		if errors.Is(err, gorm.ErrDuplicatedKey) || isDuplicateKeyError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// LockAndGetRun locks a run row with SELECT FOR UPDATE SKIP LOCKED.
func (r *Repository) LockAndGetRun(ctx context.Context, tx *gorm.DB, runID uuid.UUID) (*WorkflowRun, error) {
	var run WorkflowRun
	err := tx.WithContext(ctx).
		Raw("SELECT * FROM automation_workflow_runs WHERE id = ? AND status IN (?, ?) FOR UPDATE SKIP LOCKED", runID, StatusPending, StatusRunning).
		Scan(&run).Error
	if err != nil {
		return nil, err
	}
	if run.ID == uuid.Nil {
		return nil, nil
	}
	return &run, nil
}

// UpdateRun updates a workflow run within a transaction. tx must not be nil.
func (r *Repository) UpdateRun(ctx context.Context, tx *gorm.DB, run *WorkflowRun) error {
	if tx == nil {
		return ErrNilTransaction
	}
	return tx.WithContext(ctx).Save(run).Error
}

// UpdateRunNoTx updates a workflow run without a transaction.
// Use only for terminal/idempotent writes where no action log atomicity is needed
// (e.g. failRun, skipRun, crash recovery reset).
func (r *Repository) UpdateRunNoTx(ctx context.Context, run *WorkflowRun) error {
	return r.db.WithContext(ctx).Save(run).Error
}

// ListRunsByWorkflow returns paginated runs for a workflow.
func (r *Repository) ListRunsByWorkflow(ctx context.Context, workflowID uuid.UUID, page, size int) ([]WorkflowRun, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	offset := (page - 1) * size

	var total int64
	r.db.WithContext(ctx).Model(&WorkflowRun{}).
		Where("workflow_id = ?", workflowID).
		Count(&total)

	var runs []WorkflowRun
	err := r.db.WithContext(ctx).
		Where("workflow_id = ?", workflowID).
		Order("created_at DESC").
		Offset(offset).Limit(size).
		Find(&runs).Error
	return runs, total, err
}

// GetRunByID retrieves a run by ID.
func (r *Repository) GetRunByID(ctx context.Context, runID uuid.UUID) (*WorkflowRun, error) {
	var run WorkflowRun
	err := r.db.WithContext(ctx).
		Where("id = ?", runID).
		First(&run).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

// GetPendingRuns returns runs ready for processing (pending with no future retry).
func (r *Repository) GetPendingRuns(ctx context.Context, limit int) ([]WorkflowRun, error) {
	var runs []WorkflowRun
	err := r.db.WithContext(ctx).
		Where("status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)", StatusPending, time.Now()).
		Order("created_at ASC").
		Limit(limit).
		Find(&runs).Error
	return runs, err
}

// GetRunningRuns returns runs with status='running' (for crash recovery).
func (r *Repository) GetRunningRuns(ctx context.Context) ([]WorkflowRun, error) {
	var runs []WorkflowRun
	err := r.db.WithContext(ctx).
		Where("status = ?", StatusRunning).
		Find(&runs).Error
	return runs, err
}

// SweepRetries finds pending runs whose retry time has arrived.
func (r *Repository) SweepRetries(ctx context.Context) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := r.db.WithContext(ctx).
		Model(&WorkflowRun{}).
		Select("id").
		Where("status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?", StatusPending, time.Now()).
		Find(&ids).Error
	return ids, err
}

// --- WorkflowActionLog ---

// CreateActionLog inserts an action log entry within a transaction. tx must not be nil.
func (r *Repository) CreateActionLog(ctx context.Context, tx *gorm.DB, log *WorkflowActionLog) error {
	if tx == nil {
		return ErrNilTransaction
	}
	if log.ID == uuid.Nil {
		log.ID = uuid.New()
	}
	return tx.WithContext(ctx).Create(log).Error
}

// CreateActionLogNoTx inserts an action log entry without a transaction.
// Use only for pre-execution informational logs (status=running) where
// loss on crash is acceptable.
func (r *Repository) CreateActionLogNoTx(ctx context.Context, log *WorkflowActionLog) error {
	if log.ID == uuid.Nil {
		log.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Create(log).Error
}

// UpdateActionLog updates an existing action log entry within a transaction. tx must not be nil.
func (r *Repository) UpdateActionLog(ctx context.Context, tx *gorm.DB, log *WorkflowActionLog) error {
	if tx == nil {
		return ErrNilTransaction
	}
	return tx.WithContext(ctx).Save(log).Error
}

// GetActionLogsByRunID returns all action logs for a run.
func (r *Repository) GetActionLogsByRunID(ctx context.Context, runID uuid.UUID) ([]WorkflowActionLog, error) {
	var logs []WorkflowActionLog
	err := r.db.WithContext(ctx).
		Where("run_id = ?", runID).
		Order("action_idx ASC, created_at ASC").
		Find(&logs).Error
	return logs, err
}

// --- WorkflowOrgToken ---

// GetOrgToken retrieves a token record by its token string.
func (r *Repository) GetOrgToken(ctx context.Context, token string) (*WorkflowOrgToken, error) {
	var t WorkflowOrgToken
	err := r.db.WithContext(ctx).Where("token = ?", token).First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// CreateOrgToken creates a new org token.
func (r *Repository) CreateOrgToken(ctx context.Context, t *WorkflowOrgToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// BeginTx starts a transaction.
func (r *Repository) BeginTx(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).Begin()
}

// DB returns the underlying database connection.
func (r *Repository) DB() *gorm.DB {
	return r.db
}

// --- Helpers ---

// GetCompletedActionIndices parses the completed_actions JSON array from a run.
func GetCompletedActionIndices(run *WorkflowRun) map[int]bool {
	result := make(map[int]bool)
	if run.CompletedActions == nil {
		return result
	}
	var indices []int
	if err := json.Unmarshal(run.CompletedActions, &indices); err != nil {
		return result
	}
	for _, idx := range indices {
		result[idx] = true
	}
	return result
}

// SetCompletedActions marshals a slice of completed action indices to JSON.
func SetCompletedActions(indices []int) ([]byte, error) {
	return json.Marshal(indices)
}

// isDuplicateKeyError checks if an error is a unique constraint violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	// Postgres duplicate key error code: 23505
	return errors.Is(err, gorm.ErrDuplicatedKey) ||
		containsString(err.Error(), "duplicate key") ||
		containsString(err.Error(), "23505")
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
