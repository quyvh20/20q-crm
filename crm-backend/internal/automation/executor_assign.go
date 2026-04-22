package automation

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AssignUserExecutor assigns a user to a contact or deal.
type AssignUserExecutor struct {
	db *gorm.DB
}

// NewAssignUserExecutor creates a new assign user executor.
func NewAssignUserExecutor(db *gorm.DB) *AssignUserExecutor {
	return &AssignUserExecutor{db: db}
}

// Execute assigns a user to the specified entity.
func (e *AssignUserExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	entity := getStringParam(action.Params, "entity", evalCtx)
	if entity != "contact" && entity != "deal" {
		return nil, fmt.Errorf("assign_user: entity must be 'contact' or 'deal', got '%s'", entity)
	}

	strategy := getStringParam(action.Params, "strategy", evalCtx)

	var assigneeID uuid.UUID

	switch strategy {
	case "specific":
		userIDStr := getStringParam(action.Params, "user_id", evalCtx)
		if userIDStr == "" {
			return nil, fmt.Errorf("assign_user: user_id required for strategy=specific")
		}
		uid, err := uuid.Parse(userIDStr)
		if err != nil {
			return nil, fmt.Errorf("assign_user: invalid user_id: %w", err)
		}
		assigneeID = uid

	case "round_robin":
		pool := getStringSliceParam(action.Params, "pool", evalCtx)
		if len(pool) == 0 {
			return nil, fmt.Errorf("assign_user: pool required for strategy=round_robin")
		}
		// Simple round-robin: count existing assignments per pool member, pick least assigned
		minCount := int64(-1)
		for _, idStr := range pool {
			uid, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			var count int64
			column := "owner_user_id"
			table := entity + "s"
			e.db.WithContext(ctx).
				Table(table).
				Where("org_id = ? AND "+column+" = ?", run.OrgID, uid).
				Count(&count)

			if minCount == -1 || count < minCount {
				minCount = count
				assigneeID = uid
			}
		}
		if assigneeID == uuid.Nil {
			return nil, fmt.Errorf("assign_user: no valid pool members")
		}

	case "least_loaded":
		// Find the user in the org with the fewest assigned entities
		column := "owner_user_id"
		table := entity + "s"
		var result struct {
			OwnerUserID uuid.UUID `gorm:"column:owner_user_id"`
			Count       int64     `gorm:"column:cnt"`
		}
		err := e.db.WithContext(ctx).
			Table(table).
			Select(column+" as owner_user_id, COUNT(*) as cnt").
			Where("org_id = ? AND "+column+" IS NOT NULL", run.OrgID).
			Group(column).
			Order("cnt ASC").
			Limit(1).
			Scan(&result).Error
		if err != nil || result.OwnerUserID == uuid.Nil {
			// Fallback: just pick a random user from the org
			var userID uuid.UUID
			e.db.WithContext(ctx).
				Table("users").
				Select("id").
				Where("org_id = ?", run.OrgID).
				Limit(1).
				Scan(&userID)
			if userID == uuid.Nil {
				return nil, fmt.Errorf("assign_user: no users found in org")
			}
			assigneeID = userID
		} else {
			assigneeID = result.OwnerUserID
		}

	default:
		return nil, fmt.Errorf("assign_user: unknown strategy '%s'", strategy)
	}

	// Get entity ID from context
	var entityID string
	switch entity {
	case "contact":
		if id, ok := evalCtx.Contact["id"]; ok {
			entityID = fmt.Sprintf("%v", id)
		}
	case "deal":
		if id, ok := evalCtx.Deal["id"]; ok {
			entityID = fmt.Sprintf("%v", id)
		}
	}

	if entityID == "" {
		return nil, fmt.Errorf("assign_user: no %s ID found in context", entity)
	}

	uid, err := uuid.Parse(entityID)
	if err != nil {
		return nil, fmt.Errorf("assign_user: invalid entity ID: %w", err)
	}

	// Update the entity's owner_user_id
	table := entity + "s"
	err = e.db.WithContext(ctx).
		Table(table).
		Where("id = ? AND org_id = ?", uid, run.OrgID).
		Update("owner_user_id", assigneeID).Error
	if err != nil {
		return nil, fmt.Errorf("assign_user: update error: %w", err)
	}

	slog.Info("automation: user assigned",
		"entity", entity,
		"strategy", strategy,
		"assignee_id", assigneeID.String(),
		"workflow_run_id", run.ID.String(),
	)

	return map[string]any{
		"entity":      entity,
		"entity_id":   entityID,
		"assignee_id": assigneeID.String(),
		"strategy":    strategy,
	}, nil
}
