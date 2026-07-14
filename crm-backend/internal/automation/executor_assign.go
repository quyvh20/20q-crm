package automation

import (
	"context"
	"fmt"
	"log/slog"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AssignUserExecutor assigns a user to a contact or deal.
type AssignUserExecutor struct {
	db    *gorm.DB
	authz domain.RecordAuthorizer
}

// NewAssignUserExecutor creates a new assign user executor. authz enforces the
// workflow author's OLS/FLS/own-scope and audits the reassignment (P8); nil
// disables enforcement (unit tests).
func NewAssignUserExecutor(db *gorm.DB, authz domain.RecordAuthorizer) *AssignUserExecutor {
	return &AssignUserExecutor{db: db, authz: authz}
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

	// Run-as-creator gate (P8): reassigning ownership is an edit — OLS(edit) on the
	// object, FLS on owner_user_id, and own-scope on the target record, as the
	// workflow author.
	if err := e.authorize(ctx, run, entity, uid); err != nil {
		return nil, err
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

	// Attribute the reassignment to the workflow author in the audit trail (P8).
	if e.authz != nil {
		caller, _ := domain.CallerFromContext(ctx)
		e.authz.Audit(ctx, domain.AuditEntry{
			OrgID:      run.OrgID,
			ActorID:    caller.UserID,
			ObjectSlug: entity,
			RecordID:   uid,
			Action:     domain.ActionEdit,
			Changes:    map[string]interface{}{"owner_user_id": map[string]interface{}{"new": assigneeID.String()}},
		})
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

// authorize enforces the workflow author's OLS(edit) + FLS(owner_user_id) + row
// scope before a reassignment (P8). A nil authz (unit tests) is a no-op.
func (e *AssignUserExecutor) authorize(ctx context.Context, run *WorkflowRun, entity string, recordID uuid.UUID) error {
	if e.authz == nil {
		return nil
	}
	if err := e.authz.Authorize(ctx, run.OrgID, entity, domain.ActionEdit); err != nil {
		return err
	}
	if mask := e.authz.FieldMask(ctx, run.OrgID, entity); !mask.CanWrite("owner_user_id") {
		return fmt.Errorf("assign_user: your role may not reassign the owner of %s records", entity)
	}
	caller, ok := domain.CallerFromContext(ctx)
	if !ok || caller.IsOwner || caller.DataScope == domain.DataScopeAll {
		return nil
	}
	// Reassignment is a write: a 'view' share is not enough.
	allowed, err := rowScopeAllows(ctx, e.db, run.OrgID, entity+"s", entity, recordID, caller, true)
	if err != nil {
		return fmt.Errorf("assign_user: row-scope check failed: %w", err)
	}
	if !allowed {
		return fmt.Errorf("assign_user: your role may only reassign %s records you own or have edit access to", entity)
	}
	return nil
}
