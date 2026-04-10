package usecase

import (
	"context"
	"fmt"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

type taskUseCase struct {
	taskRepo domain.TaskRepository
}

func NewTaskUseCase(taskRepo domain.TaskRepository) domain.TaskUseCase {
	return &taskUseCase{taskRepo: taskRepo}
}

func (uc *taskUseCase) List(ctx context.Context, orgID uuid.UUID, f domain.TaskFilter) ([]domain.Task, error) {
	return uc.taskRepo.List(ctx, orgID, f)
}

func (uc *taskUseCase) Create(ctx context.Context, orgID uuid.UUID, input domain.CreateTaskInput) (*domain.Task, error) {
	task := domain.Task{
		OrgID:      orgID,
		Title:      input.Title,
		DealID:     input.DealID,
		ContactID:  input.ContactID,
		AssignedTo: input.AssignedTo,
		Priority:   input.Priority,
	}

	if task.Priority == "" {
		task.Priority = "medium"
	}

	if input.DueAt != nil && *input.DueAt != "" {
		t, err := time.Parse(time.RFC3339, *input.DueAt)
		if err != nil {
			return nil, fmt.Errorf("invalid due_at format: %w", err)
		}
		task.DueAt = &t
	}

	if err := uc.taskRepo.Create(ctx, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (uc *taskUseCase) Update(ctx context.Context, orgID uuid.UUID, id uuid.UUID, input domain.UpdateTaskInput) (*domain.Task, error) {
	task, err := uc.taskRepo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task not found")
	}

	if input.Title != nil {
		task.Title = *input.Title
	}
	if input.AssignedTo != nil {
		task.AssignedTo = input.AssignedTo
	}
	if input.Priority != nil {
		task.Priority = *input.Priority
	}
	if input.DueAt != nil {
		if *input.DueAt == "" {
			task.DueAt = nil
		} else {
			t, err := time.Parse(time.RFC3339, *input.DueAt)
			if err != nil {
				return nil, fmt.Errorf("invalid due_at format: %w", err)
			}
			task.DueAt = &t
		}
	}
	if input.Completed != nil {
		if *input.Completed {
			now := time.Now()
			task.CompletedAt = &now
		} else {
			task.CompletedAt = nil
		}
	}

	if err := uc.taskRepo.Update(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

func (uc *taskUseCase) Delete(ctx context.Context, orgID uuid.UUID, id uuid.UUID) error {
	return uc.taskRepo.SoftDelete(ctx, orgID, id)
}
