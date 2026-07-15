package repository

import (
	"context"
	"errors"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type taskRepository struct {
	db *gorm.DB
}

func NewTaskRepository(db *gorm.DB) domain.TaskRepository {
	return &taskRepository{db: db}
}

// taskScope restricts tasks to those a row-scoped caller may see (U0.1-ext).
// Before this, GET /api/tasks returned up to 200 tasks across the whole org to
// any authenticated user — including tasks on a contact/deal they'd 404 on
// directly — and Update/Delete edited another rep's task freely.
//
// A task is reachable if ANY of the following holds:
//   - the caller is 'all'-scoped (admin/manager/owner/any all-scoped role) — full org
//   - it is assigned to the caller, or was created by them (their own work, even
//     when it has no linked record and nobody else can see it)
//   - its linked contact OR deal is reachable by the caller (same predicate the
//     contact/deal list pages use — access_predicate.go)
//
// This mirrors voiceNoteScope; !ok is a trusted in-process call (worker, seeder,
// unit test) and is unrestricted by design. The 'all' guard tests for 'all', not
// "not own", so a new scope value can never silently widen to the whole org.
func taskScope(db *gorm.DB, ctx context.Context, orgID uuid.UUID) *gorm.DB {
	scope, userID, roleID, ok := extractCallerScope(ctx)
	if !ok || scope == domain.DataScopeAll {
		return db.Where("tasks.org_id = ?", orgID)
	}

	cSQL, cArgs := RecordAccessPredicate(RecordAccessArgs{
		Table: "c", RecordType: "contact", OrgID: orgID, Scope: scope, UserID: userID, RoleID: roleID,
	})
	dSQL, dArgs := RecordAccessPredicate(RecordAccessArgs{
		Table: "d", RecordType: "deal", OrgID: orgID, Scope: scope, UserID: userID, RoleID: roleID,
	})

	args := []any{orgID, userID, userID}
	args = append(args, cArgs...)
	args = append(args, dArgs...)

	return db.Where(`tasks.org_id = ? AND (
		tasks.assigned_to = ?
		OR tasks.created_by = ?
		OR (tasks.contact_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM contacts c
			WHERE c.id = tasks.contact_id
			  AND c.deleted_at IS NULL
			  AND `+cSQL+`
		))
		OR (tasks.deal_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM deals d
			WHERE d.id = tasks.deal_id
			  AND d.deleted_at IS NULL
			  AND `+dSQL+`
		))
	)`, args...)
}

func (r *taskRepository) List(ctx context.Context, orgID uuid.UUID, f domain.TaskFilter) ([]domain.Task, error) {
	query := taskScope(r.db.WithContext(ctx), ctx, orgID)

	if f.DealID != nil {
		query = query.Where("tasks.deal_id = ?", *f.DealID)
	}
	if f.ContactID != nil {
		query = query.Where("tasks.contact_id = ?", *f.ContactID)
	}
	if f.AssignedTo != nil {
		query = query.Where("tasks.assigned_to = ?", *f.AssignedTo)
	}
	if f.Completed != nil {
		if *f.Completed {
			query = query.Where("tasks.completed_at IS NOT NULL")
		} else {
			query = query.Where("tasks.completed_at IS NULL")
		}
	}

	var tasks []domain.Task
	err := query.
		Order("COALESCE(tasks.due_at, '9999-12-31') ASC, tasks.created_at DESC").
		Limit(200).
		Find(&tasks).Error
	return tasks, err
}

func (r *taskRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Task, error) {
	var task domain.Task
	err := taskScope(r.db.WithContext(ctx), ctx, orgID).
		Where("tasks.id = ?", id).
		First(&task).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Either it does not exist or the caller may not see it — both are "not
			// found", so a row-scoped user can't probe for tasks outside their scope.
			return nil, nil
		}
		return nil, err
	}
	return &task, nil
}

func (r *taskRepository) Create(ctx context.Context, t *domain.Task) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	// Stamp the creating user from the request caller so their own unlinked/
	// unassigned tasks stay visible to them under row scope. An explicit CreatedBy
	// wins (AI/tests set it directly); a trusted in-process call has no caller and
	// leaves it nil.
	if t.CreatedBy == nil {
		if _, userID, ok := extractDataScope(ctx); ok && userID != uuid.Nil {
			t.CreatedBy = &userID
		}
	}
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *taskRepository) Update(ctx context.Context, t *domain.Task) error {
	// The usecase reads the task through the scoped GetByID first, so an
	// unreachable task never reaches this Save (which writes by primary key alone).
	return r.db.WithContext(ctx).Save(t).Error
}

func (r *taskRepository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) error {
	res := taskScope(r.db.WithContext(ctx), ctx, orgID).
		Where("tasks.id = ?", id).
		Delete(&domain.Task{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// Unreachable or already gone — a row-scoped caller must not be able to
		// delete (or confirm the existence of) another rep's task.
		return domain.ErrTaskNotFound
	}
	return nil
}
