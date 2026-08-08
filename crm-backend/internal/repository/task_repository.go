package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// taskOrdering is the sole ordering for /api/tasks (R8.1): due date first (no
// due date sorts last), id as the tiebreaker. It reuses the R4.2 keyset
// machinery so the ORDER BY and the cursor predicate cannot drift apart —
// see keyset_cursor.go's doc comment for why that drift is the actual bug
// class this guards against.
// due_at is nullable (a task can have no due date) — kept as a real
// nullable column (not folded via nullAs) so cursorPredicate's null arms
// apply, and Postgres's ASC default (NULLS LAST) puts no-due-date tasks at
// the end without an explicit NULLS clause.
var taskOrdering = keysetOrdering{
	key:   "due_at",
	idCol: "tasks.id",
	col:   keysetColumn{col: "tasks.due_at", kind: keysetTime, nullable: true},
	dir:   "ASC",
}

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
//
// The rule itself lives in TaskAccessPredicate (access_predicate.go) because the
// report runner applies the same one — see R9.5.
func taskScope(db *gorm.DB, ctx context.Context, orgID uuid.UUID) *gorm.DB {
	scope, userID, roleID, ok := extractCallerScope(ctx)
	if !ok {
		return db.Where("tasks.org_id = ?", orgID)
	}
	predSQL, predArgs := TaskAccessPredicate(orgID, scope, userID, roleID)
	if predSQL == "" {
		return db.Where("tasks.org_id = ?", orgID) // 'all' scope — no restriction
	}
	args := append([]any{orgID}, predArgs...)
	return db.Where("tasks.org_id = ? AND "+predSQL, args...)
}

func (r *taskRepository) List(ctx context.Context, orgID uuid.UUID, f domain.TaskFilter) (domain.TaskListResult, error) {
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
	if f.Q != nil && strings.TrimSpace(*f.Q) != "" {
		query = query.Where("tasks.title ILIKE ?", "%"+strings.TrimSpace(*f.Q)+"%")
	}
	// due_at range: a NULL due_at can never satisfy either bound (R8.1 doc
	// comment on TaskFilter) — an explicit inequality does that for free,
	// since NULL compared to anything is neither true nor false.
	if f.DueAfter != nil {
		query = query.Where("tasks.due_at >= ?", *f.DueAfter)
	}
	if f.DueBefore != nil {
		query = query.Where("tasks.due_at <= ?", *f.DueBefore)
	}

	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}

	cursorStr := ""
	if f.Cursor != nil {
		cursorStr = *f.Cursor
	}
	query = taskOrdering.applyCursor(query, cursorStr)

	var tasks []domain.Task
	err := query.
		Order(taskOrdering.orderClause()).
		Limit(limit + 1).
		Find(&tasks).Error
	if err != nil {
		return domain.TaskListResult{}, err
	}

	var next *string
	if len(tasks) > limit {
		tasks = tasks[:limit]
		last := tasks[len(tasks)-1]
		var dueVal any
		if last.DueAt != nil {
			dueVal = *last.DueAt
		}
		c := taskOrdering.nextCursor(dueVal, last.ID)
		next = &c
	}
	return domain.TaskListResult{Tasks: tasks, NextCursor: next}, nil
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

// DueForReminder is callerless (the reminder scanner runs org-agnostic, no
// caller in ctx) and so is unrestricted by taskScope by design — it must see
// every org's due tasks to remind every org. now is injected by the caller
// (not time.Now() here) so the scanner's tick and this query use one clock.
func (r *taskRepository) DueForReminder(ctx context.Context, now time.Time, lookahead time.Duration, limit int) ([]domain.Task, error) {
	today := now.Truncate(24 * time.Hour)
	var tasks []domain.Task
	err := r.db.WithContext(ctx).
		Where("completed_at IS NULL").
		Where("due_at IS NOT NULL AND due_at <= ?", now.Add(lookahead)).
		Where("last_reminded_at IS NULL OR last_reminded_at < ?", today).
		Order("due_at ASC").
		Limit(limit).
		Find(&tasks).Error
	return tasks, err
}

func (r *taskRepository) MarkReminded(ctx context.Context, id uuid.UUID, at time.Time) error {
	return r.db.WithContext(ctx).Model(&domain.Task{}).Where("id = ?", id).
		Update("last_reminded_at", at).Error
}
