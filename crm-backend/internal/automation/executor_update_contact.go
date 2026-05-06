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

	// Defense-in-depth: verify the contact belongs to the run's org BEFORE any mutations.
	// This is critical because tag operations (contact_tags join table) cannot include
	// org_id in their WHERE clause — they rely on this pre-check for org scoping.
	var exists bool
	if err := e.db.WithContext(ctx).
		Raw("SELECT EXISTS(SELECT 1 FROM contacts WHERE id = ? AND org_id = ? AND deleted_at IS NULL)", cid, run.OrgID).
		Scan(&exists).Error; err != nil {
		return nil, fmt.Errorf("update_contact: org ownership check failed: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("update_contact: contact %s not found in org %s", contactID, run.OrgID.String())
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

	// Re-read the updated contact from DB so downstream actions can reference
	// current values via {{actions.<id>.email}}, {{actions.<id>.first_name}}, etc.
	contactSnapshot, err := e.readContactSnapshot(ctx, run.OrgID, cid)
	if err != nil {
		// Non-fatal: return update results without snapshot
		slog.Warn("automation: could not re-read contact after update",
			"contact_id", contactID,
			"error", err,
		)
		return map[string]any{"updates": results, "count": len(results)}, nil
	}

	// Also refresh evalCtx.Contact so remaining actions in this run
	// see the latest contact state in templates like {{contact.email}}
	for k, v := range contactSnapshot {
		evalCtx.Contact[k] = v
	}

	// Merge contact fields into the output at the top level:
	// output.email, output.first_name, etc. are directly accessible
	// as {{actions.<id>.email}}, while output.updates has the mutation log.
	output := contactSnapshot
	output["updates"] = results
	output["count"] = len(results)

	return output, nil
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
// Supports: set, add (falls through to set for scalars), clear.
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
	case "set", "add":
		// "add" on a scalar column behaves as "set" (overwrite)
		value := getStringParam(params, "value", evalCtx)
		if value == "" {
			return nil, fmt.Errorf("'value' is required for '%s' on column '%s'", op, field)
		}
		err := e.db.WithContext(ctx).
			Table("contacts").
			Where("id = ? AND org_id = ?", contactID, orgID).
			Update(column, value).Error
		if err != nil {
			return nil, fmt.Errorf("%s error: %w", op, err)
		}
		return map[string]any{"field": field, "op": op, "value": value}, nil

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

	case "remove":
		return nil, fmt.Errorf("'remove' is not supported on scalar column field '%s' (use on tags)", field)

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
// Supports: set, add (falls through to set for scalars), increment, decrement, clear.
func (e *UpdateContactExecutor) handleCustomField(ctx context.Context, orgID, contactID uuid.UUID, cfKey, op string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
	switch op {
	case "set", "add":
		// "add" on a custom field (scalar) behaves as "set" (overwrite)
		value := getStringParam(params, "value", evalCtx)
		// Use JSONB set: custom_fields || '{"key": "value"}'
		patch, _ := json.Marshal(map[string]any{cfKey: value})
		err := e.db.WithContext(ctx).
			Table("contacts").
			Where("id = ? AND org_id = ?", contactID, orgID).
			Update("custom_fields", gorm.Expr("COALESCE(custom_fields, '{}'::jsonb) || ?::jsonb", string(patch))).Error
		if err != nil {
			return nil, fmt.Errorf("custom field %s error: %w", op, err)
		}
		return map[string]any{"field": "custom_fields." + cfKey, "op": op, "value": value}, nil

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

	case "remove":
		return nil, fmt.Errorf("'remove' is not supported on custom fields (use on tags)", )

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

// readContactSnapshot re-reads the contact from DB and returns a flat map
// suitable for template interpolation (e.g. {{actions.uc1.email}}).
// Keys match the EvalContext contact shape: id, first_name, last_name, email,
// phone, owner_user_id, company_id, custom_fields.*, tags (as []string of IDs).
func (e *UpdateContactExecutor) readContactSnapshot(ctx context.Context, orgID, contactID uuid.UUID) (map[string]any, error) {
	// Read core fields
	var row struct {
		ID          uuid.UUID  `gorm:"column:id"`
		FirstName   string     `gorm:"column:first_name"`
		LastName    string     `gorm:"column:last_name"`
		Email       *string    `gorm:"column:email"`
		Phone       *string    `gorm:"column:phone"`
		OwnerUserID *uuid.UUID `gorm:"column:owner_user_id"`
		CompanyID   *uuid.UUID `gorm:"column:company_id"`
		CustomFields *string   `gorm:"column:custom_fields"`
	}

	if err := e.db.WithContext(ctx).
		Table("contacts").
		Select("id, first_name, last_name, email, phone, owner_user_id, company_id, custom_fields::text").
		Where("id = ? AND org_id = ?", contactID, orgID).
		Scan(&row).Error; err != nil {
		return nil, fmt.Errorf("read contact: %w", err)
	}

	snapshot := map[string]any{
		"id":         row.ID.String(),
		"first_name": row.FirstName,
		"last_name":  row.LastName,
	}

	if row.Email != nil {
		snapshot["email"] = *row.Email
	}
	if row.Phone != nil {
		snapshot["phone"] = *row.Phone
	}
	if row.OwnerUserID != nil {
		snapshot["owner_user_id"] = row.OwnerUserID.String()
	}
	if row.CompanyID != nil {
		snapshot["company_id"] = row.CompanyID.String()
	}

	// Flatten custom_fields JSONB into snapshot
	if row.CustomFields != nil && *row.CustomFields != "" {
		var cf map[string]any
		if err := json.Unmarshal([]byte(*row.CustomFields), &cf); err == nil {
			snapshot["custom_fields"] = cf
			// Also expose each custom field as a top-level key for convenience:
			// {{actions.uc1.score}} instead of {{actions.uc1.custom_fields.score}}
			for k, v := range cf {
				snapshot[k] = v
			}
		}
	}

	// Read tags
	var tagIDs []struct {
		TagID uuid.UUID `gorm:"column:tag_id"`
	}
	if err := e.db.WithContext(ctx).
		Table("contact_tags").
		Select("tag_id").
		Where("contact_id = ?", contactID).
		Scan(&tagIDs).Error; err == nil && len(tagIDs) > 0 {
		tags := make([]string, len(tagIDs))
		for i, t := range tagIDs {
			tags[i] = t.TagID.String()
		}
		snapshot["tags"] = tags
	}

	return snapshot, nil
}
