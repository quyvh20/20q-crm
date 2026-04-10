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

func (r *taskRepository) List(ctx context.Context, orgID uuid.UUID, f domain.TaskFilter) ([]domain.Task, error) {
	query := r.db.WithContext(ctx).
		Where("org_id = ?", orgID)

	if f.DealID != nil {
		query = query.Where("deal_id = ?", *f.DealID)
	}
	if f.ContactID != nil {
		query = query.Where("contact_id = ?", *f.ContactID)
	}
	if f.AssignedTo != nil {
		query = query.Where("assigned_to = ?", *f.AssignedTo)
	}
	if f.Completed != nil {
		if *f.Completed {
			query = query.Where("completed_at IS NOT NULL")
		} else {
			query = query.Where("completed_at IS NULL")
		}
	}

	var tasks []domain.Task
	err := query.
		Order("COALESCE(due_at, '9999-12-31') ASC, created_at DESC").
		Limit(200).
		Find(&tasks).Error
	return tasks, err
}

func (r *taskRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Task, error) {
	var task domain.Task
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		First(&task).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
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
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *taskRepository) Update(ctx context.Context, t *domain.Task) error {
	return r.db.WithContext(ctx).Save(t).Error
}

func (r *taskRepository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		Delete(&domain.Task{}).Error
}
