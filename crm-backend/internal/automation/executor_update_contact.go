package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UpdateContactExecutor modifies contact fields from workflow actions.
// Supports operations: set, add, remove, increment, decrement, clear.
type UpdateContactExecutor struct {
	db *gorm.DB
}

// NewUpdateContactExecutor creates a new update contact executor.
func NewUpdateContactExecutor(db *gorm.DB) *UpdateContactExecutor {
	return &UpdateContactExecutor{db: db}
}

// Valid update operations
var validUpdateOperations = map[string]bool{
	"set":       true,
	"add":       true,
	"remove":    true,
	"increment": true,
	"decrement": true,
	"clear":     true,
}

// Execute updates a contact field based on the configured operation.
//
// Params:
//   - field:     the field path to update (e.g. "first_name", "tags", "custom_fields.industry")
//   - operation: set | add | remove | increment | decrement | clear
//   - value:     the value to use (not needed for "clear")
func (e *UpdateContactExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	field := getStringParam(action.Params, "field", evalCtx)
	operation := getStringParam(action.Params, "operation", evalCtx)

	if field == "" {
		return nil, fmt.Errorf("update_contact: 'field' parameter is required")
	}
	if !validUpdateOperations[operation] {
		return nil, fmt.Errorf("update_contact: invalid operation '%s'. Valid: set, add, remove, increment, decrement, clear", operation)
	}

	// Get contact ID from context
	contactID := ""
	if id, ok := evalCtx.Contact["id"]; ok {
		contactID = fmt.Sprintf("%v", id)
	}
	if contactID == "" {
		return nil, fmt.Errorf("update_contact: no contact ID found in context")
	}

	cid, err := uuid.Parse(contactID)
	if err != nil {
		return nil, fmt.Errorf("update_contact: invalid contact ID: %w", err)
	}

	// Dispatch to the appropriate handler
	var result map[string]any

	switch {
	case field == "tags":
		result, err = e.handleTags(ctx, run.OrgID, cid, operation, action.Params, evalCtx)
	case strings.HasPrefix(field, "custom_fields."):
		cfKey := strings.TrimPrefix(field, "custom_fields.")
		result, err = e.handleCustomField(ctx, run.OrgID, cid, cfKey, operation, action.Params, evalCtx)
	default:
		result, err = e.handleColumnField(ctx, run.OrgID, cid, field, operation, action.Params, evalCtx)
	}

	if err != nil {
		return nil, err
	}

	slog.Info("automation: contact updated",
		"contact_id", contactID,
		"field", field,
		"operation", operation,
		"workflow_run_id", run.ID.String(),
	)

	return result, nil
}

// handleColumnField updates a direct column on the contacts table.
// Supports: set, increment, decrement, clear.
func (e *UpdateContactExecutor) handleColumnField(ctx context.Context, orgID, contactID uuid.UUID, field, operation string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
	// Map workflow field names to actual DB column names
	columnMap := map[string]string{
		"first_name":    "first_name",
		"last_name":     "last_name",
		"email":         "email",
		"phone":         "phone",
		"owner_user_id": "owner_user_id",
		"company_id":    "company_id",
	}

	column, ok := columnMap[field]
	if !ok {
		return nil, fmt.Errorf("update_contact: unknown field '%s'. Valid column fields: first_name, last_name, email, phone, owner_user_id, company_id", field)
	}

	switch operation {
	case "set":
		value := getStringParam(params, "value", evalCtx)
		if value == "" {
			return nil, fmt.Errorf("update_contact: 'value' is required for 'set' operation")
		}
		err := e.db.WithContext(ctx).
			Table("contacts").
			Where("id = ? AND org_id = ?", contactID, orgID).
			Update(column, value).Error
		if err != nil {
			return nil, fmt.Errorf("update_contact: set column error: %w", err)
		}
		return map[string]any{"field": field, "operation": "set", "value": value}, nil

	case "clear":
		err := e.db.WithContext(ctx).
			Table("contacts").
			Where("id = ? AND org_id = ?", contactID, orgID).
			Update(column, nil).Error
		if err != nil {
			return nil, fmt.Errorf("update_contact: clear column error: %w", err)
		}
		return map[string]any{"field": field, "operation": "clear"}, nil

	case "increment", "decrement":
		return nil, fmt.Errorf("update_contact: '%s' operation is not supported on column field '%s' (use on number custom fields instead)", operation, field)

	case "add", "remove":
		return nil, fmt.Errorf("update_contact: '%s' operation is not supported on column field '%s' (use on tags instead)", operation, field)

	default:
		return nil, fmt.Errorf("update_contact: unsupported operation '%s' on column field '%s'", operation, field)
	}
}

// handleTags manages tag add/remove/set/clear on a contact via the contact_tags join table.
func (e *UpdateContactExecutor) handleTags(ctx context.Context, orgID, contactID uuid.UUID, operation string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
	switch operation {
	case "add":
		tagIDs, err := e.resolveTagIDs(params, evalCtx)
		if err != nil {
			return nil, fmt.Errorf("update_contact: tags add: %w", err)
		}
		if len(tagIDs) == 0 {
			return nil, fmt.Errorf("update_contact: 'value' (tag IDs) required for 'add' on tags")
		}
		// Insert into contact_tags ON CONFLICT DO NOTHING
		sql := "INSERT INTO contact_tags (contact_id, tag_id) VALUES "
		args := make([]interface{}, 0, len(tagIDs)*2)
		for i, tid := range tagIDs {
			if i > 0 {
				sql += ","
			}
			sql += "(?,?)"
			args = append(args, contactID, tid)
		}
		sql += " ON CONFLICT DO NOTHING"
		if err := e.db.WithContext(ctx).Exec(sql, args...).Error; err != nil {
			return nil, fmt.Errorf("update_contact: tags add error: %w", err)
		}
		return map[string]any{"field": "tags", "operation": "add", "tag_ids": uuidsToStrings(tagIDs)}, nil

	case "remove":
		tagIDs, err := e.resolveTagIDs(params, evalCtx)
		if err != nil {
			return nil, fmt.Errorf("update_contact: tags remove: %w", err)
		}
		if len(tagIDs) == 0 {
			return nil, fmt.Errorf("update_contact: 'value' (tag IDs) required for 'remove' on tags")
		}
		if err := e.db.WithContext(ctx).
			Exec("DELETE FROM contact_tags WHERE contact_id = ? AND tag_id IN ?", contactID, tagIDs).Error; err != nil {
			return nil, fmt.Errorf("update_contact: tags remove error: %w", err)
		}
		return map[string]any{"field": "tags", "operation": "remove", "tag_ids": uuidsToStrings(tagIDs)}, nil

	case "set":
		tagIDs, err := e.resolveTagIDs(params, evalCtx)
		if err != nil {
			return nil, fmt.Errorf("update_contact: tags set: %w", err)
		}
		// Replace all tags: delete all, then insert new
		if err := e.db.WithContext(ctx).
			Exec("DELETE FROM contact_tags WHERE contact_id = ?", contactID).Error; err != nil {
			return nil, fmt.Errorf("update_contact: tags clear before set error: %w", err)
		}
		if len(tagIDs) > 0 {
			sql := "INSERT INTO contact_tags (contact_id, tag_id) VALUES "
			args := make([]interface{}, 0, len(tagIDs)*2)
			for i, tid := range tagIDs {
				if i > 0 {
					sql += ","
				}
				sql += "(?,?)"
				args = append(args, contactID, tid)
			}
			sql += " ON CONFLICT DO NOTHING"
			if err := e.db.WithContext(ctx).Exec(sql, args...).Error; err != nil {
				return nil, fmt.Errorf("update_contact: tags set insert error: %w", err)
			}
		}
		return map[string]any{"field": "tags", "operation": "set", "tag_ids": uuidsToStrings(tagIDs)}, nil

	case "clear":
		if err := e.db.WithContext(ctx).
			Exec("DELETE FROM contact_tags WHERE contact_id = ?", contactID).Error; err != nil {
			return nil, fmt.Errorf("update_contact: tags clear error: %w", err)
		}
		return map[string]any{"field": "tags", "operation": "clear"}, nil

	default:
		return nil, fmt.Errorf("update_contact: unsupported operation '%s' on tags (valid: add, remove, set, clear)", operation)
	}
}

// handleCustomField updates a key in the contact's custom_fields JSONB column.
// Supports: set, increment, decrement, clear.
func (e *UpdateContactExecutor) handleCustomField(ctx context.Context, orgID, contactID uuid.UUID, cfKey, operation string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
	switch operation {
	case "set":
		value := getStringParam(params, "value", evalCtx)
		// Use JSONB set: custom_fields || '{"key": "value"}'
		patch, _ := json.Marshal(map[string]any{cfKey: value})
		err := e.db.WithContext(ctx).
			Table("contacts").
			Where("id = ? AND org_id = ?", contactID, orgID).
			Update("custom_fields", gorm.Expr("COALESCE(custom_fields, '{}'::jsonb) || ?::jsonb", string(patch))).Error
		if err != nil {
			return nil, fmt.Errorf("update_contact: custom field set error: %w", err)
		}
		return map[string]any{"field": "custom_fields." + cfKey, "operation": "set", "value": value}, nil

	case "increment":
		delta := getIntParam(params, "value")
		if delta == 0 {
			delta = 1
		}
		// JSONB: set key to (current value + delta)
		// Postgres expression: custom_fields || jsonb_build_object(key, COALESCE((custom_fields->>key)::numeric, 0) + delta)
		err := e.db.WithContext(ctx).
			Table("contacts").
			Where("id = ? AND org_id = ?", contactID, orgID).
			Update("custom_fields", gorm.Expr(
				"COALESCE(custom_fields, '{}'::jsonb) || jsonb_build_object(?, (COALESCE((custom_fields->>?)::numeric, 0) + ?)::numeric)",
				cfKey, cfKey, delta,
			)).Error
		if err != nil {
			return nil, fmt.Errorf("update_contact: custom field increment error: %w", err)
		}
		return map[string]any{"field": "custom_fields." + cfKey, "operation": "increment", "delta": delta}, nil

	case "decrement":
		delta := getIntParam(params, "value")
		if delta == 0 {
			delta = 1
		}
		err := e.db.WithContext(ctx).
			Table("contacts").
			Where("id = ? AND org_id = ?", contactID, orgID).
			Update("custom_fields", gorm.Expr(
				"COALESCE(custom_fields, '{}'::jsonb) || jsonb_build_object(?, (COALESCE((custom_fields->>?)::numeric, 0) - ?)::numeric)",
				cfKey, cfKey, delta,
			)).Error
		if err != nil {
			return nil, fmt.Errorf("update_contact: custom field decrement error: %w", err)
		}
		return map[string]any{"field": "custom_fields." + cfKey, "operation": "decrement", "delta": delta}, nil

	case "clear":
		// Remove the key from JSONB: custom_fields - 'key'
		err := e.db.WithContext(ctx).
			Table("contacts").
			Where("id = ? AND org_id = ?", contactID, orgID).
			Update("custom_fields", gorm.Expr("COALESCE(custom_fields, '{}'::jsonb) - ?", cfKey)).Error
		if err != nil {
			return nil, fmt.Errorf("update_contact: custom field clear error: %w", err)
		}
		return map[string]any{"field": "custom_fields." + cfKey, "operation": "clear"}, nil

	case "add", "remove":
		return nil, fmt.Errorf("update_contact: '%s' operation is not supported on custom fields (use on tags instead)", operation)

	default:
		return nil, fmt.Errorf("update_contact: unsupported operation '%s' on custom field '%s'", operation, cfKey)
	}
}

// resolveTagIDs extracts tag UUIDs from the action's "value" param.
// Accepts: []string of UUIDs, []any of UUIDs, or a single UUID string.
func (e *UpdateContactExecutor) resolveTagIDs(params map[string]any, evalCtx EvalContext) ([]uuid.UUID, error) {
	val, ok := params["value"]
	if !ok {
		return nil, nil
	}

	var rawIDs []string

	switch v := val.(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				rawIDs = append(rawIDs, InterpolateTemplate(s, evalCtx))
			}
		}
	case []string:
		for _, s := range v {
			rawIDs = append(rawIDs, InterpolateTemplate(s, evalCtx))
		}
	case string:
		interpolated := InterpolateTemplate(v, evalCtx)
		if interpolated != "" {
			rawIDs = append(rawIDs, interpolated)
		}
	}

	var ids []uuid.UUID
	for _, raw := range rawIDs {
		uid, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid tag ID '%s': %w", raw, err)
		}
		ids = append(ids, uid)
	}
	return ids, nil
}

// uuidsToStrings converts a slice of UUIDs to strings for output logging.
func uuidsToStrings(ids []uuid.UUID) []string {
	result := make([]string, len(ids))
	for i, id := range ids {
		result[i] = id.String()
	}
	return result
}
