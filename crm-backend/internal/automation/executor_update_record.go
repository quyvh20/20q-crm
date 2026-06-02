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
// Returns "contact", "deal", or the custom object slug.
// Defaults to "contact" for backward compatibility.
func resolveEntity(evalCtx EvalContext) string {
	triggerType, _ := evalCtx.Trigger["type"].(string)
	switch {
	case strings.HasPrefix(triggerType, "contact"):
		return "contact"
	case strings.HasPrefix(triggerType, "deal"):
		return "deal"
	default:
		// Custom object: extract slug from trigger type (e.g. "ticket_created" → "ticket")
		suffixes := []string{"_created", "_updated", "_deleted", "_any"}
		for _, suffix := range suffixes {
			if strings.HasSuffix(triggerType, suffix) {
				slug := strings.TrimSuffix(triggerType, suffix)
				if slug != "" {
					return slug
				}
			}
		}
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
	case "contact":
		return e.executeContact(ctx, run, updates, evalCtx)
	case "deal":
		return e.executeDeal(ctx, run, updates, evalCtx)
	default:
		// Custom object entity — update the JSONB data column
		return e.executeCustomObject(ctx, run, updates, evalCtx, entity)
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

// executeCustomObject handles updates targeting a custom object record.
// Custom objects store data in a JSONB "data" column, so all field updates
// are applied as patches to that column.
func (e *UpdateRecordExecutor) executeCustomObject(ctx context.Context, run *WorkflowRun, updates []FieldUpdate, evalCtx EvalContext, slug string) (any, error) {
	// Get entity ID from trigger payload
	recordID := ""
	if extra, ok := evalCtx.Extra[slug]; ok {
		if m, ok2 := extra.(map[string]any); ok2 {
			if id, ok3 := m["id"]; ok3 {
				recordID = fmt.Sprintf("%v", id)
			}
		}
	}
	if recordID == "" {
		return nil, fmt.Errorf("update_record: no %s record ID found in context", slug)
	}

	rid, err := uuid.Parse(recordID)
	if err != nil {
		return nil, fmt.Errorf("update_record: invalid %s record ID: %w", slug, err)
	}

	// Verify record exists and belongs to org
	var exists bool
	if err := e.db.WithContext(ctx).
		Raw("SELECT EXISTS(SELECT 1 FROM custom_object_records WHERE id = ? AND org_id = ? AND deleted_at IS NULL)", rid, run.OrgID).
		Scan(&exists).Error; err != nil {
		return nil, fmt.Errorf("update_record: ownership check failed: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("update_record: %s record %s not found in org %s", slug, recordID, run.OrgID.String())
	}

	// Read current data JSONB — scan into string first (GORM can't scan JSONB → json.RawMessage)
	var dataStr string
	if err := e.db.WithContext(ctx).
		Raw("SELECT COALESCE(data::text, '{}') FROM custom_object_records WHERE id = ?", rid).
		Scan(&dataStr).Error; err != nil {
		return nil, fmt.Errorf("update_record: failed to read %s data: %w", slug, err)
	}

	dataMap := make(map[string]any)
	if dataStr != "" {
		json.Unmarshal([]byte(dataStr), &dataMap)
	}

	// Apply updates to the data map
	var results []map[string]any
	tx := e.db.WithContext(ctx).Begin()

	for _, upd := range updates {
		// Strip slug prefix from field path (e.g. "ticket.status" → "status")
		field := upd.Field
		if strings.HasPrefix(field, slug+".") {
			field = strings.TrimPrefix(field, slug+".")
		}

		result := map[string]any{
			"field": upd.Field,
			"op":    upd.Op,
		}

		switch upd.Op {
		case "set":
			dataMap[field] = upd.Value
			result["new_value"] = upd.Value
		case "clear":
			delete(dataMap, field)
			result["new_value"] = nil
		case "increment":
			current, _ := toFloat64(dataMap[field])
			delta, _ := toFloat64(upd.Value)
			dataMap[field] = current + delta
			result["new_value"] = current + delta
		case "decrement":
			current, _ := toFloat64(dataMap[field])
			delta, _ := toFloat64(upd.Value)
			dataMap[field] = current - delta
			result["new_value"] = current - delta
		case "add":
			// For arrays — append values
			existing, _ := dataMap[field].([]any)
			if vals, ok := upd.Value.([]any); ok {
				existing = append(existing, vals...)
			} else {
				existing = append(existing, upd.Value)
			}
			dataMap[field] = existing
			result["new_value"] = existing
		case "remove":
			// For arrays — remove matching values
			existing, _ := dataMap[field].([]any)
			toRemove := make(map[string]bool)
			if vals, ok := upd.Value.([]any); ok {
				for _, v := range vals {
					toRemove[toString(v)] = true
				}
			} else {
				toRemove[toString(upd.Value)] = true
			}
			var filtered []any
			for _, v := range existing {
				if !toRemove[toString(v)] {
					filtered = append(filtered, v)
				}
			}
			dataMap[field] = filtered
			result["new_value"] = filtered
		default:
			result["error"] = fmt.Sprintf("unsupported op '%s'", upd.Op)
		}

		results = append(results, result)
	}

	// Write updated data back
	updatedJSON, err := json.Marshal(dataMap)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("update_record: failed to marshal %s data: %w", slug, err)
	}

	if err := tx.Exec(
		"UPDATE custom_object_records SET data = ?, updated_at = NOW() WHERE id = ?",
		string(updatedJSON), rid,
	).Error; err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("update_record: failed to update %s record: %w", slug, err)
	}

	if err := tx.Commit().Error; err != nil {
		return nil, fmt.Errorf("update_record: commit failed: %w", err)
	}

	slog.Info("update_record: custom object updated",
		"slug", slug,
		"record_id", recordID,
		"update_count", len(results),
	)

	output := map[string]any{
		"entity":    slug,
		"record_id": recordID,
		"updates":   results,
		"count":     len(results),
	}
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
			case field == "stage" || field == "stage_id":
				// Stage changes route through changeDealStage semantics (P14) so they
				// record an activity log + is_won/is_lost/closed_at, identical to a
				// stage change made via the CRM UI — never a bare stage_id column write.
				result, err = e.handleDealStageChange(ctx, tx, run, did, op, updateParams, evalCtx)
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
// Contacts have no numeric columns, so increment/decrement is never valid here.
func (e *UpdateRecordExecutor) handleContactColumn(ctx context.Context, tx *gorm.DB, orgID, contactID uuid.UUID, field, op string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
	columnMap := map[string]string{
		"first_name":    "first_name",
		"last_name":     "last_name",
		"email":         "email",
		"phone":         "phone",
		"owner_user_id": "owner_user_id",
		"company_id":    "company_id",
	}
	return e.handleGenericColumn(ctx, tx, "contacts", columnMap, nil, orgID, contactID, field, op, params, evalCtx)
}

// handleDealColumn updates a direct column on the deals table.
//
// stage_id is intentionally absent: stage changes are handled by
// handleDealStageChange so they preserve the activity log + won/lost flags.
func (e *UpdateRecordExecutor) handleDealColumn(ctx context.Context, tx *gorm.DB, orgID, dealID uuid.UUID, field, op string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
	// is_won / is_lost are intentionally absent: like stage_id, they are managed
	// fields. Winning or losing a deal happens by moving it to a won/lost stage
	// (handleDealStageChange), which keeps is_won/is_lost coupled with closed_at and
	// a stage_change activity. A bare boolean write here would mark a deal won while
	// it still sits in an open stage with no closed_at — a state no other write path
	// in the system can produce. KEEP IN SYNC with dealFieldTypes in validator.go.
	columnMap := map[string]string{
		"title":         "title",
		"value":         "value",
		"probability":   "probability",
		"contact_id":    "contact_id",
		"company_id":    "company_id",
		"owner_user_id": "owner_user_id",
	}
	// value (money) and probability (0-100) are numeric → support increment/decrement.
	numericCols := map[string]bool{"value": true, "probability": true}
	return e.handleGenericColumn(ctx, tx, "deals", columnMap, numericCols, orgID, dealID, field, op, params, evalCtx)
}

// handleDealStageChange moves a deal to a new pipeline stage and records the same
// side effects a normal stage change produces: is_won / is_lost / closed_at flags
// and an auto-created "stage_change" activity, so a stage change driven by automation
// is indistinguishable from one made through the CRM UI (P14).
//
// KEEP IN SYNC: these side-effects intentionally duplicate dealUseCase.ChangeStage.
// We do NOT call that method because (1) it runs on its own db-bound repos, so calling
// it would commit the stage change outside this executor's transaction and break the
// all-or-nothing guarantee across a multi-field updates[] array, and (2) it would
// invert layering (the engine holds only *gorm.DB, no usecase). If you change the
// stage side-effects in ChangeStage, change them here too. The drift is guarded by
// the TestUpdateRecord_DealStageChange_* integration tests.
//
// Runs inside the caller's transaction (tx) so it commits/rolls back atomically with
// sibling field updates, via direct SQL — like the rest of this executor.
//
// Only "set" is supported: the value is the target stage's UUID (the StageDropdown in
// the builder emits the stage ID).
func (e *UpdateRecordExecutor) handleDealStageChange(ctx context.Context, tx *gorm.DB, run *WorkflowRun, dealID uuid.UUID, op string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
	orgID := run.OrgID
	if op != "set" {
		return nil, fmt.Errorf("stage only supports the 'set' operation, got '%s'", op)
	}

	stageIDStr := getStringParam(params, "value", evalCtx)
	if stageIDStr == "" {
		return nil, fmt.Errorf("'value' (target stage ID) is required to change the stage")
	}
	stageID, err := uuid.Parse(stageIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid stage ID '%s': %w", stageIDStr, err)
	}

	// Load the target stage (scoped to org) for its name + won/lost flags.
	var stage struct {
		Name   string `gorm:"column:name"`
		IsWon  bool   `gorm:"column:is_won"`
		IsLost bool   `gorm:"column:is_lost"`
		Found  bool   `gorm:"column:found"`
	}
	if err := tx.WithContext(ctx).
		Table("pipeline_stages").
		Select("name, is_won, is_lost, true AS found").
		Where("id = ? AND org_id = ? AND deleted_at IS NULL", stageID, orgID).
		Scan(&stage).Error; err != nil {
		return nil, fmt.Errorf("load target stage: %w", err)
	}
	if !stage.Found {
		return nil, fmt.Errorf("stage %s not found in org %s", stageID, orgID.String())
	}

	// Build the deal update — mirror dealUseCase.ChangeStage side effects.
	//
	// is_won/is_lost/closed_at are a pure function of the destination stage: they are
	// seeded to the "open stage" values (false/false/NULL) and overwritten only when the
	// target is a won/lost stage. This is why we use a map (not a struct) with .Updates —
	// a map writes every key, including false and nil, so moving a previously won/lost
	// deal to an open stage CLEARS the terminal state instead of leaving it stale (which
	// would drop the reopened deal from is_won=false/is_lost=false reports like Forecast).
	updateCols := map[string]any{
		"stage_id":   stageID,
		"updated_at": gorm.Expr("NOW()"),
		"is_won":     false,
		"is_lost":    false,
		"closed_at":  nil,
	}
	activityTitle := fmt.Sprintf("Stage changed to %s", stage.Name)
	if stage.IsWon {
		updateCols["is_won"] = true
		updateCols["closed_at"] = gorm.Expr("NOW()")
		activityTitle = "Deal won! 🏆"
	} else if stage.IsLost {
		updateCols["is_lost"] = true
		updateCols["closed_at"] = gorm.Expr("NOW()")
		activityTitle = "Deal lost"
	}

	if err := tx.WithContext(ctx).
		Table("deals").
		Where("id = ? AND org_id = ?", dealID, orgID).
		Updates(updateCols).Error; err != nil {
		return nil, fmt.Errorf("update stage: %w", err)
	}

	// Auto-create the stage_change activity (same type the UI path records).
	if err := tx.WithContext(ctx).Exec(
		`INSERT INTO activities (id, org_id, type, deal_id, title, occurred_at, created_at, updated_at)
		 VALUES (?, ?, 'stage_change', ?, ?, NOW(), NOW(), NOW())`,
		uuid.New(), orgID, dealID, activityTitle,
	).Error; err != nil {
		return nil, fmt.Errorf("create stage_change activity: %w", err)
	}

	return map[string]any{
		"field":    "stage",
		"op":       "set",
		"stage_id": stageID.String(),
		"is_won":   stage.IsWon,
		"is_lost":  stage.IsLost,
		"activity": activityTitle,
	}, nil
}

// handleGenericColumn updates a direct column on any table.
//
// numericCols lists the columns that accept increment/decrement (a delta added to
// COALESCE(col, 0)). Columns absent from it reject increment/decrement, matching
// the validator's type rules (numeric fields only).
func (e *UpdateRecordExecutor) handleGenericColumn(ctx context.Context, tx *gorm.DB, table string, columnMap map[string]string, numericCols map[string]bool, orgID, recordID uuid.UUID, field, op string, params map[string]any, evalCtx EvalContext) (map[string]any, error) {
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
		if !numericCols[field] {
			return nil, fmt.Errorf("'%s' is not supported on non-numeric column field '%s' (use on a number field or number custom field)", op, field)
		}
		delta, ok := resolveNumericParam(params, "value", evalCtx)
		if !ok {
			return nil, fmt.Errorf("'%s' on column '%s' requires a numeric 'value'", op, field)
		}
		if op == "decrement" {
			delta = -delta
		}
		// column comes from the hardcoded columnMap (not user input) → safe to embed.
		err := tx.WithContext(ctx).Table(table).
			Where("id = ? AND org_id = ?", recordID, orgID).
			Update(column, gorm.Expr("COALESCE("+column+", 0) + ?", delta)).Error
		if err != nil {
			return nil, fmt.Errorf("%s error: %w", op, err)
		}
		return map[string]any{"field": field, "op": op, "delta": delta}, nil
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
		// resolveNumericParam (not getIntParam) so templated deltas like
		// {{trigger.amount}} resolve to their real value and fractional amounts
		// survive — matching the numeric-column increment path. A missing/non-numeric
		// value errors rather than silently defaulting to 1.
		delta, ok := resolveNumericParam(params, "value", evalCtx)
		if !ok {
			return nil, fmt.Errorf("'increment' on custom field '%s' requires a numeric 'value'", cfKey)
		}
		err := tx.WithContext(ctx).
			Table(table).
			Where("id = ? AND org_id = ?", recordID, orgID).
			Update("custom_fields", gorm.Expr(
				// Cast both key params to ::text: jsonb_build_object is VARIADIC "any" (its
				// key param has no inferable type → 42P18), and ->> has text/int overloads
				// (an uncast param is ambiguous). The delta is inferred numeric from the +.
				"COALESCE(custom_fields, '{}'::jsonb) || jsonb_build_object(?::text, (COALESCE((custom_fields->>?::text)::numeric, 0) + ?)::numeric)",
				cfKey, cfKey, delta,
			)).Error
		if err != nil {
			return nil, fmt.Errorf("custom field increment error: %w", err)
		}
		return map[string]any{"field": "custom_fields." + cfKey, "op": "increment", "delta": delta}, nil

	case "decrement":
		delta, ok := resolveNumericParam(params, "value", evalCtx)
		if !ok {
			return nil, fmt.Errorf("'decrement' on custom field '%s' requires a numeric 'value'", cfKey)
		}
		err := tx.WithContext(ctx).
			Table(table).
			Where("id = ? AND org_id = ?", recordID, orgID).
			Update("custom_fields", gorm.Expr(
				// See the increment path: ::text casts disambiguate jsonb_build_object's
				// VARIADIC "any" key and the ->> text/int overload (42P18 otherwise).
				"COALESCE(custom_fields, '{}'::jsonb) || jsonb_build_object(?::text, (COALESCE((custom_fields->>?::text)::numeric, 0) - ?)::numeric)",
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
