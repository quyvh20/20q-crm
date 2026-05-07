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

// UpdateRecordExecutor modifies entity fields from workflow actions.
// Resolves the target entity (contact or deal) from the trigger type.
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
//
// All updates execute inside a single DB transaction for multi-field atomicity.
type UpdateRecordExecutor struct {
	db *gorm.DB
}

// NewUpdateRecordExecutor creates a new update record executor.
func NewUpdateRecordExecutor(db *gorm.DB) *UpdateRecordExecutor {
	return &UpdateRecordExecutor{db: db}
}

// FieldUpdate describes a single field mutation within an update_record action.
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

// resolveEntity determines the target entity type from the trigger context.
// Returns "contact" or "deal". Defaults to "contact" for backward compatibility.
func resolveEntity(evalCtx EvalContext) string {
	triggerType, _ := evalCtx.Trigger["type"].(string)
	switch {
	case strings.HasPrefix(triggerType, "deal"):
		return "deal"
	default:
		return "contact"
	}
}

// Execute applies all field updates to the record identified by the trigger source.
// All mutations run inside a single transaction — if any update fails, the entire
// batch is rolled back (all-or-nothing semantics).
//
// Infinite-loop safety: this executor uses direct SQL (GORM) to mutate records,
// which bypasses the HTTP handler and does NOT emit _updated events. The engine
// also has a _internal_update guard in triggerEventInternal() as defense-in-depth.
// If event emission is ever added here, the payload MUST include _internal_update=true.
func (e *UpdateRecordExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	updates, err := e.parseUpdates(action.Params)
	if err != nil {
		return nil, fmt.Errorf("update_record: %w", err)
	}
	if len(updates) == 0 {
		return nil, fmt.Errorf("update_record: 'updates' array is empty — nothing to do")
	}

	entity := resolveEntity(evalCtx)

	switch entity {
	case "deal":
		return e.executeDeal(ctx, run, updates, evalCtx)
	default:
		return e.executeContact(ctx, run, updates, evalCtx)
	}
}

// executeContact handles updates targeting a contact record.
func (e *UpdateRecordExecutor) executeContact(ctx context.Context, run *WorkflowRun, updates []FieldUpdate, evalCtx EvalContext) (any, error) {
	contactID := ""
	if id, ok := evalCtx.Contact["id"]; ok {
		contactID = fmt.Sprintf("%v", id)
	}
	if contactID == "" {
		return nil, fmt.Errorf("update_record: no contact ID found in context")
	}

	cid, err := uuid.Parse(contactID)
	if err != nil {
		return nil, fmt.Errorf("update_record: invalid contact ID: %w", err)
	}

	var exists bool
	if err := e.db.WithContext(ctx).
		Raw("SELECT EXISTS(SELECT 1 FROM contacts WHERE id = ? AND org_id = ? AND deleted_at IS NULL)", cid, run.OrgID).
		Scan(&exists).Error; err != nil {
		return nil, fmt.Errorf("update_record: org ownership check failed: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("update_record: contact %s not found in org %s", contactID, run.OrgID.String())
	}

	var results []map[string]any
	txErr := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		results = make([]map[string]any, 0, len(updates))
		for i, upd := range updates {
			field := InterpolateTemplate(upd.Field, evalCtx)
			op := upd.Op
			if field == "" {
				return fmt.Errorf("updates[%d].field is empty", i)
			}
			if !validUpdateOperations[op] {
				return fmt.Errorf("updates[%d] invalid op '%s'", i, op)
			}
			field = strings.TrimPrefix(field, "contact.")
			updateParams := map[string]any{"value": upd.Value}
			var result map[string]any
			var err error
			switch {
			case field == "tags":
				result, err = e.handleContactTags(ctx, tx, run.OrgID, cid, op, updateParams, evalCtx)
			case strings.HasPrefix(field, "custom_fields."):
				cfKey := strings.TrimPrefix(field, "custom_fields.")
				result, err = e.handleCustomField(ctx, tx, "contacts", run.OrgID, cid, cfKey, op, updateParams, evalCtx)
			default:
				result, err = e.handleContactColumn(ctx, tx, run.OrgID, cid, field, op, updateParams, evalCtx)
			}
			if err != nil {
				return fmt.Errorf("updates[%d] (%s.%s): %w", i, field, op, err)
			}
			results = append(results, result)
			slog.Info("automation: record field updated",
				"entity", "contact", "record_id", contactID,
				"field", field, "op", op,
				"workflow_id", run.WorkflowID.String(),
				"workflow_run_id", run.ID.String(),
			)
		}
		return nil
	})
	if txErr != nil {
		return nil, fmt.Errorf("update_record: %w", txErr)
	}

	snapshot, err := e.readContactSnapshot(ctx, run.OrgID, cid)
	if err != nil {
		slog.Warn("automation: could not re-read contact after update", "contact_id", contactID, "error", err)
		return map[string]any{"entity": "contact", "updates": results, "count": len(results)}, nil
	}
	for k, v := range snapshot {
		evalCtx.Contact[k] = v
	}
	output := snapshot
	output["entity"] = "contact"
	output["updates"] = results
	output["count"] = len(results)
	return output, nil
}

// executeDeal handles updates targeting a deal record.
func (e *UpdateRecordExecutor) executeDeal(ctx context.Context, run *WorkflowRun, updates []FieldUpdate, evalCtx EvalContext) (any, error) {
	dealID := ""
	if id, ok := evalCtx.Deal["id"]; ok {
		dealID = fmt.Sprintf("%v", id)
	}
	if dealID == "" {
		return nil, fmt.Errorf("update_record: no deal ID found in context")
	}

	did, err := uuid.Parse(dealID)
	if err != nil {
		return nil, fmt.Errorf("update_record: invalid deal ID: %w", err)
	}

	var exists bool
	if err := e.db.WithContext(ctx).
		Raw("SELECT EXISTS(SELECT 1 FROM deals WHERE id = ? AND org_id = ? AND deleted_at IS NULL)", did, run.OrgID).
		Scan(&exists).Error; err != nil {
		return nil, fmt.Errorf("update_record: org ownership check failed: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("update_record: deal %s not found in org %s", dealID, run.OrgID.String())
	}

	var results []map[string]any
	txErr := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		results = make([]map[string]any, 0, len(updates))
		for i, upd := range updates {
			field := InterpolateTemplate(upd.Field, evalCtx)
			op := upd.Op
			if field == "" {
				return fmt.Errorf("updates[%d].field is empty", i)
			}
			if !validUpdateOperations[op] {
				return fmt.Errorf("updates[%d] invalid op '%s'", i, op)
			}
			field = strings.TrimPrefix(field, "deal.")
			updateParams := map[string]any{"value": upd.Value}
			var result map[string]any
			var err error
			switch {
			case strings.HasPrefix(field, "custom_fields."):
				cfKey := strings.TrimPrefix(field, "custom_fields.")
				result, err = e.handleCustomField(ctx, tx, "deals", run.OrgID, did, cfKey, op, updateParams, evalCtx)
			default:
				result, err = e.handleDealColumn(ctx, tx, run.OrgID, did, field, op, updateParams, evalCtx)
			}
			if err != nil {
				return fmt.Errorf("updates[%d] (%s.%s): %w", i, field, op, err)
			}
			results = append(results, result)
			slog.Info("automation: record field updated",
				"entity", "deal", "record_id", dealID,
				"field", field, "op", op,
				"workflow_id", run.WorkflowID.String(),
				"workflow_run_id", run.ID.String(),
			)
		}
		return nil
	})
	if txErr != nil {
		return nil, fmt.Errorf("update_record: %w", txErr)
	}

	snapshot, err := e.readDealSnapshot(ctx, run.OrgID, did)
	if err != nil {
		slog.Warn("automation: could not re-read deal after update", "deal_id", dealID, "error", err)
		return map[string]any{"entity": "deal", "updates": results, "count": len(results)}, nil
	}
	for k, v := range snapshot {
		evalCtx.Deal[k] = v
	}
	output := snapshot
	output["entity"] = "deal"
	output["updates"] = results
	output["count"] = len(results)
	return output, nil
}

// parseUpdates extracts the []FieldUpdate from action params.
// Supports both the new "updates" array format and legacy flat format for backwards compat.
func (e *UpdateRecordExecutor) parseUpdates(params map[string]any) ([]FieldUpdate, error) {
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

// handleContactColumn updates a direct column on the contacts table.
func (e *UpdateRecordExecutor) handleContactColumn(ctx context.Context, tx *gorm.DB, orgID, contactID uuid.UUID, field, op string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
	columnMap := map[string]string{
		"first_name":    "first_name",
		"last_name":     "last_name",
		"email":         "email",
		"phone":         "phone",
		"owner_user_id": "owner_user_id",
		"company_id":    "company_id",
	}
	return e.handleGenericColumn(ctx, tx, "contacts", columnMap, orgID, contactID, field, op, params, evalCtx)
}

// handleDealColumn updates a direct column on the deals table.
func (e *UpdateRecordExecutor) handleDealColumn(ctx context.Context, tx *gorm.DB, orgID, dealID uuid.UUID, field, op string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
	columnMap := map[string]string{
		"title":           "title",
		"value":           "value",
		"probability":     "probability",
		"stage_id":        "stage_id",
		"contact_id":      "contact_id",
		"company_id":      "company_id",
		"owner_user_id":   "owner_user_id",
		"is_won":          "is_won",
		"is_lost":         "is_lost",
	}
	return e.handleGenericColumn(ctx, tx, "deals", columnMap, orgID, dealID, field, op, params, evalCtx)
}

// handleGenericColumn updates a direct column on any table.
func (e *UpdateRecordExecutor) handleGenericColumn(ctx context.Context, tx *gorm.DB, table string, columnMap map[string]string, orgID, recordID uuid.UUID, field, op string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
	column, ok := columnMap[field]
	if !ok {
		valid := make([]string, 0, len(columnMap))
		for k := range columnMap {
			valid = append(valid, k)
		}
		return nil, fmt.Errorf("unknown column field '%s'. Valid: %s", field, strings.Join(valid, ", "))
	}

	switch op {
	case "set", "add":
		value := getStringParam(params, "value", evalCtx)
		if value == "" {
			return nil, fmt.Errorf("'value' is required for '%s' on column '%s'", op, field)
		}
		err := tx.WithContext(ctx).Table(table).Where("id = ? AND org_id = ?", recordID, orgID).Update(column, value).Error
		if err != nil {
			return nil, fmt.Errorf("%s error: %w", op, err)
		}
		return map[string]any{"field": field, "op": op, "value": value}, nil
	case "clear":
		err := tx.WithContext(ctx).Table(table).Where("id = ? AND org_id = ?", recordID, orgID).Update(column, nil).Error
		if err != nil {
			return nil, fmt.Errorf("clear error: %w", err)
		}
		return map[string]any{"field": field, "op": "clear"}, nil
	case "increment", "decrement":
		return nil, fmt.Errorf("'%s' is not supported on column field '%s' (use on number custom fields)", op, field)
	case "remove":
		return nil, fmt.Errorf("'remove' is not supported on scalar column field '%s'", field)
	default:
		return nil, fmt.Errorf("unsupported op '%s' on column field '%s'", op, field)
	}
}

// handleContactTags manages tag add/remove/set/clear on a contact via the contact_tags join table.
func (e *UpdateRecordExecutor) handleContactTags(ctx context.Context, tx *gorm.DB, orgID, contactID uuid.UUID, op string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
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
		if err := tx.WithContext(ctx).Exec(sql, args...).Error; err != nil {
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
		if err := tx.WithContext(ctx).
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
		if err := tx.WithContext(ctx).
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
			if err := tx.WithContext(ctx).Exec(sql, args...).Error; err != nil {
				return nil, fmt.Errorf("tags set insert error: %w", err)
			}
		}
		return map[string]any{"field": "tags", "op": "set", "tag_ids": uuidsToStrings(tagIDs)}, nil

	case "clear":
		if err := tx.WithContext(ctx).
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
// All operations use the provided tx handle for transactional atomicity.
func (e *UpdateRecordExecutor) handleCustomField(ctx context.Context, tx *gorm.DB, table string, orgID, recordID uuid.UUID, cfKey, op string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
	switch op {
	case "set", "add":
		// "add" on a custom field (scalar) behaves as "set" (overwrite)
		value := getStringParam(params, "value", evalCtx)
		// Use JSONB set: custom_fields || '{"key": "value"}'
		patch, _ := json.Marshal(map[string]any{cfKey: value})
		err := tx.WithContext(ctx).
			Table(table).
			Where("id = ? AND org_id = ?", recordID, orgID).
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
		err := tx.WithContext(ctx).
			Table(table).
			Where("id = ? AND org_id = ?", recordID, orgID).
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
		err := tx.WithContext(ctx).
			Table(table).
			Where("id = ? AND org_id = ?", recordID, orgID).
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
		err := tx.WithContext(ctx).
			Table(table).
			Where("id = ? AND org_id = ?", recordID, orgID).
			Update("custom_fields", gorm.Expr("COALESCE(custom_fields, '{}'::jsonb) - ?", cfKey)).Error
		if err != nil {
			return nil, fmt.Errorf("custom field clear error: %w", err)
		}
		return map[string]any{"field": "custom_fields." + cfKey, "op": "clear"}, nil

	case "remove":
		return nil, fmt.Errorf("'remove' is not supported on custom fields (use on tags)")

	default:
		return nil, fmt.Errorf("unsupported op '%s' on custom field '%s'", op, cfKey)
	}
}

// resolveTagIDs extracts tag UUIDs from the update's "value" param.
// Accepts: []string of UUIDs, []any of UUIDs, or a single UUID string.
func (e *UpdateRecordExecutor) resolveTagIDs(params map[string]any, evalCtx EvalContext) ([]uuid.UUID, error) {
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
// Uses e.db (not tx) because this runs after the transaction commits.
func (e *UpdateRecordExecutor) readContactSnapshot(ctx context.Context, orgID, contactID uuid.UUID) (map[string]any, error) {
	// Read core fields
	var row struct {
		ID           uuid.UUID  `gorm:"column:id"`
		FirstName    string     `gorm:"column:first_name"`
		LastName     string     `gorm:"column:last_name"`
		Email        *string    `gorm:"column:email"`
		Phone        *string    `gorm:"column:phone"`
		OwnerUserID  *uuid.UUID `gorm:"column:owner_user_id"`
		CompanyID    *uuid.UUID `gorm:"column:company_id"`
		CustomFields *string    `gorm:"column:custom_fields"`
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

// readDealSnapshot re-reads the deal from DB and returns a flat map
// suitable for template interpolation (e.g. {{actions.ur1.title}}).
func (e *UpdateRecordExecutor) readDealSnapshot(ctx context.Context, orgID, dealID uuid.UUID) (map[string]any, error) {
	var row struct {
		ID              uuid.UUID  `gorm:"column:id"`
		Title           string     `gorm:"column:title"`
		Value           float64    `gorm:"column:value"`
		Probability     int        `gorm:"column:probability"`
		StageID         *uuid.UUID `gorm:"column:stage_id"`
		ContactID       *uuid.UUID `gorm:"column:contact_id"`
		CompanyID       *uuid.UUID `gorm:"column:company_id"`
		OwnerUserID     *uuid.UUID `gorm:"column:owner_user_id"`
		IsWon           bool       `gorm:"column:is_won"`
		IsLost          bool       `gorm:"column:is_lost"`
		CustomFields    *string    `gorm:"column:custom_fields"`
	}

	if err := e.db.WithContext(ctx).
		Table("deals").
		Select("id, title, value, probability, stage_id, contact_id, company_id, owner_user_id, is_won, is_lost, custom_fields::text").
		Where("id = ? AND org_id = ?", dealID, orgID).
		Scan(&row).Error; err != nil {
		return nil, fmt.Errorf("read deal: %w", err)
	}

	snapshot := map[string]any{
		"id":          row.ID.String(),
		"title":       row.Title,
		"value":       row.Value,
		"probability": row.Probability,
		"is_won":      row.IsWon,
		"is_lost":     row.IsLost,
	}
	if row.StageID != nil {
		snapshot["stage_id"] = row.StageID.String()
	}
	if row.ContactID != nil {
		snapshot["contact_id"] = row.ContactID.String()
	}
	if row.CompanyID != nil {
		snapshot["company_id"] = row.CompanyID.String()
	}
	if row.OwnerUserID != nil {
		snapshot["owner_user_id"] = row.OwnerUserID.String()
	}
	if row.CustomFields != nil && *row.CustomFields != "" {
		var cf map[string]any
		if err := json.Unmarshal([]byte(*row.CustomFields), &cf); err == nil {
			snapshot["custom_fields"] = cf
			for k, v := range cf {
				snapshot[k] = v
			}
		}
	}
	return snapshot, nil
}
