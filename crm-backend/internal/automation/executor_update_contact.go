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
//
// Params contract:
//
//	{
//	  "updates": [
//	    { "field": "first_name",           "op": "set",       "value": "Jane" },
//	    { "field": "tags",                 "op": "add",       "value": ["uuid1","uuid2"] },
//	    { "field": "custom_fields.score",  "op": "increment", "value": 5 },
//	    { "field": "phone",                "op": "clear" }
//	  ]
//	}
type UpdateContactExecutor struct {
	db *gorm.DB
}

// NewUpdateContactExecutor creates a new update contact executor.
func NewUpdateContactExecutor(db *gorm.DB) *UpdateContactExecutor {
	return &UpdateContactExecutor{db: db}
}

// FieldUpdate describes a single field mutation within an update_contact action.
type FieldUpdate struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
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

// Execute applies all field updates from params.updates to the contact in context.
func (e *UpdateContactExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	// Parse updates array from params
	updates, err := e.parseUpdates(action.Params)
	if err != nil {
		return nil, fmt.Errorf("update_contact: %w", err)
	}
	if len(updates) == 0 {
		return nil, fmt.Errorf("update_contact: 'updates' array is empty — nothing to do")
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

	// Execute each field update
	results := make([]map[string]any, 0, len(updates))
	for i, upd := range updates {
		field := InterpolateTemplate(upd.Field, evalCtx)
		op := upd.Op

		if field == "" {
			return nil, fmt.Errorf("update_contact: updates[%d].field is empty", i)
		}
		if !validUpdateOperations[op] {
			return nil, fmt.Errorf("update_contact: updates[%d] invalid op '%s'. Valid: set, add, remove, increment, decrement, clear", i, op)
		}

		// Strip "contact." prefix if present (UI emits "contact.first_name", executor needs "first_name")
		field = strings.TrimPrefix(field, "contact.")

		// Build a per-update params map for handler dispatch
		updateParams := map[string]any{"value": upd.Value}

		var result map[string]any

		switch {
		case field == "tags":
			result, err = e.handleTags(ctx, run.OrgID, cid, op, updateParams, evalCtx)
		case strings.HasPrefix(field, "custom_fields."):
			cfKey := strings.TrimPrefix(field, "custom_fields.")
			result, err = e.handleCustomField(ctx, run.OrgID, cid, cfKey, op, updateParams, evalCtx)
		default:
			result, err = e.handleColumnField(ctx, run.OrgID, cid, field, op, updateParams, evalCtx)
		}

		if err != nil {
			return nil, fmt.Errorf("update_contact: updates[%d] (%s.%s): %w", i, field, op, err)
		}

		results = append(results, result)

		slog.Info("automation: contact field updated",
			"contact_id", contactID,
			"field", field,
			"op", op,
			"workflow_run_id", run.ID.String(),
		)
	}

	return map[string]any{"updates": results, "count": len(results)}, nil
}

// parseUpdates extracts the []FieldUpdate from action params.
// Supports both the new "updates" array format and legacy flat format for backwards compat.
func (e *UpdateContactExecutor) parseUpdates(params map[string]any) ([]FieldUpdate, error) {
	// Primary: "updates" key
	if raw, ok := params["updates"]; ok {
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal 'updates': %w", err)
		}
		var updates []FieldUpdate
		if err := json.Unmarshal(data, &updates); err != nil {
			return nil, fmt.Errorf("failed to parse 'updates' array: %w", err)
		}
		return updates, nil
	}

	// Legacy fallback: flat { field, operation, value } → single-item array
	if field, ok := params["field"]; ok {
		op, _ := params["operation"].(string)
		if op == "" {
			op, _ = params["op"].(string)
		}
		return []FieldUpdate{{
			Field: fmt.Sprintf("%v", field),
			Op:    op,
			Value: params["value"],
		}}, nil
	}

	return nil, fmt.Errorf("'updates' array is required")
}

// handleColumnField updates a direct column on the contacts table.
// Supports: set, clear.
func (e *UpdateContactExecutor) handleColumnField(ctx context.Context, orgID, contactID uuid.UUID, field, op string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
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
		return nil, fmt.Errorf("unknown column field '%s'. Valid: first_name, last_name, email, phone, owner_user_id, company_id", field)
	}

	switch op {
	case "set":
		value := getStringParam(params, "value", evalCtx)
		if value == "" {
			return nil, fmt.Errorf("'value' is required for 'set' on column '%s'", field)
		}
		err := e.db.WithContext(ctx).
			Table("contacts").
			Where("id = ? AND org_id = ?", contactID, orgID).
			Update(column, value).Error
		if err != nil {
			return nil, fmt.Errorf("set error: %w", err)
		}
		return map[string]any{"field": field, "op": "set", "value": value}, nil

	case "clear":
		err := e.db.WithContext(ctx).
			Table("contacts").
			Where("id = ? AND org_id = ?", contactID, orgID).
			Update(column, nil).Error
		if err != nil {
			return nil, fmt.Errorf("clear error: %w", err)
		}
		return map[string]any{"field": field, "op": "clear"}, nil

	case "increment", "decrement":
		return nil, fmt.Errorf("'%s' is not supported on column field '%s' (use on number custom fields)", op, field)

	case "add", "remove":
		return nil, fmt.Errorf("'%s' is not supported on column field '%s' (use on tags)", op, field)

	default:
		return nil, fmt.Errorf("unsupported op '%s' on column field '%s'", op, field)
	}
}

// handleTags manages tag add/remove/set/clear on a contact via the contact_tags join table.
func (e *UpdateContactExecutor) handleTags(ctx context.Context, orgID, contactID uuid.UUID, op string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
	switch op {
	case "add":
		tagIDs, err := e.resolveTagIDs(params, evalCtx)
		if err != nil {
			return nil, fmt.Errorf("tags add: %w", err)
		}
		if len(tagIDs) == 0 {
			return nil, fmt.Errorf("'value' (tag IDs) required for 'add' on tags")
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
			return nil, fmt.Errorf("tags add error: %w", err)
		}
		return map[string]any{"field": "tags", "op": "add", "tag_ids": uuidsToStrings(tagIDs)}, nil

	case "remove":
		tagIDs, err := e.resolveTagIDs(params, evalCtx)
		if err != nil {
			return nil, fmt.Errorf("tags remove: %w", err)
		}
		if len(tagIDs) == 0 {
			return nil, fmt.Errorf("'value' (tag IDs) required for 'remove' on tags")
		}
		if err := e.db.WithContext(ctx).
			Exec("DELETE FROM contact_tags WHERE contact_id = ? AND tag_id IN ?", contactID, tagIDs).Error; err != nil {
			return nil, fmt.Errorf("tags remove error: %w", err)
		}
		return map[string]any{"field": "tags", "op": "remove", "tag_ids": uuidsToStrings(tagIDs)}, nil

	case "set":
		tagIDs, err := e.resolveTagIDs(params, evalCtx)
		if err != nil {
			return nil, fmt.Errorf("tags set: %w", err)
		}
		// Replace all tags: delete all, then insert new
		if err := e.db.WithContext(ctx).
			Exec("DELETE FROM contact_tags WHERE contact_id = ?", contactID).Error; err != nil {
			return nil, fmt.Errorf("tags clear before set error: %w", err)
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
				return nil, fmt.Errorf("tags set insert error: %w", err)
			}
		}
		return map[string]any{"field": "tags", "op": "set", "tag_ids": uuidsToStrings(tagIDs)}, nil

	case "clear":
		if err := e.db.WithContext(ctx).
			Exec("DELETE FROM contact_tags WHERE contact_id = ?", contactID).Error; err != nil {
			return nil, fmt.Errorf("tags clear error: %w", err)
		}
		return map[string]any{"field": "tags", "op": "clear"}, nil

	default:
		return nil, fmt.Errorf("unsupported op '%s' on tags (valid: add, remove, set, clear)", op)
	}
}

// handleCustomField updates a key in the contact's custom_fields JSONB column.
// Supports: set, increment, decrement, clear.
func (e *UpdateContactExecutor) handleCustomField(ctx context.Context, orgID, contactID uuid.UUID, cfKey, op string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
	switch op {
	case "set":
		value := getStringParam(params, "value", evalCtx)
		// Use JSONB set: custom_fields || '{"key": "value"}'
		patch, _ := json.Marshal(map[string]any{cfKey: value})
		err := e.db.WithContext(ctx).
			Table("contacts").
			Where("id = ? AND org_id = ?", contactID, orgID).
			Update("custom_fields", gorm.Expr("COALESCE(custom_fields, '{}'::jsonb) || ?::jsonb", string(patch))).Error
		if err != nil {
			return nil, fmt.Errorf("custom field set error: %w", err)
		}
		return map[string]any{"field": "custom_fields." + cfKey, "op": "set", "value": value}, nil

	case "increment":
		delta := getIntParam(params, "value")
		if delta == 0 {
			delta = 1
		}
		err := e.db.WithContext(ctx).
			Table("contacts").
			Where("id = ? AND org_id = ?", contactID, orgID).
			Update("custom_fields", gorm.Expr(
				"COALESCE(custom_fields, '{}'::jsonb) || jsonb_build_object(?, (COALESCE((custom_fields->>?)::numeric, 0) + ?)::numeric)",
				cfKey, cfKey, delta,
			)).Error
		if err != nil {
			return nil, fmt.Errorf("custom field increment error: %w", err)
		}
		return map[string]any{"field": "custom_fields." + cfKey, "op": "increment", "delta": delta}, nil

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
			return nil, fmt.Errorf("custom field decrement error: %w", err)
		}
		return map[string]any{"field": "custom_fields." + cfKey, "op": "decrement", "delta": delta}, nil

	case "clear":
		// Remove the key from JSONB: custom_fields - 'key'
		err := e.db.WithContext(ctx).
			Table("contacts").
			Where("id = ? AND org_id = ?", contactID, orgID).
			Update("custom_fields", gorm.Expr("COALESCE(custom_fields, '{}'::jsonb) - ?", cfKey)).Error
		if err != nil {
			return nil, fmt.Errorf("custom field clear error: %w", err)
		}
		return map[string]any{"field": "custom_fields." + cfKey, "op": "clear"}, nil

	case "add", "remove":
		return nil, fmt.Errorf("'%s' is not supported on custom fields (use on tags)", op)

	default:
		return nil, fmt.Errorf("unsupported op '%s' on custom field '%s'", op, cfKey)
	}
}

// resolveTagIDs extracts tag UUIDs from the update's "value" param.
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
