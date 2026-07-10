package automation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// RecordCreator is the narrow port the create_record executor writes through —
// satisfied by the platform RecordService. Going through RecordService (rather
// than a raw INSERT) means the create gets uniform validation, OLS/FLS enforced as
// the workflow author (the P8 actor already on ctx), the P5a audit trail, and the
// {slug}_created automation event — all for free. Creation has no multi-op
// atomicity needs, so this is pure win (plan A6).
type RecordCreator interface {
	Create(ctx context.Context, orgID, userID uuid.UUID, slug string, in domain.RecordWriteInput) (*domain.UniformRecord, error)
}

// CreateRecordExecutor creates a record of any object through RecordService.
type CreateRecordExecutor struct {
	creator RecordCreator
}

func NewCreateRecordExecutor(creator RecordCreator) *CreateRecordExecutor {
	return &CreateRecordExecutor{creator: creator}
}

func (e *CreateRecordExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	if e.creator == nil {
		return nil, fmt.Errorf("create_record: record service is not configured")
	}

	object := getStringParam(action.Params, "object", evalCtx)
	if object == "" {
		return nil, fmt.Errorf("create_record: object is required")
	}

	fields := buildCreateFields(object, action.Params, evalCtx)
	if len(fields) == 0 {
		return nil, fmt.Errorf("create_record: at least one field value is required")
	}

	// Run as the workflow author (P8): RecordService authorizes OLS(create) + FLS
	// against the caller already on ctx and attributes the audit to them. userID is
	// the created record's creator/owner.
	caller, _ := domain.CallerFromContext(ctx)

	rec, err := e.creator.Create(ctx, run.OrgID, caller.UserID, object, domain.RecordWriteInput{Fields: fields})
	if err != nil {
		return nil, classifyRecordServiceError(fmt.Errorf("create_record: %w", err), err)
	}

	slog.Info("automation: record created",
		"object", object,
		"record_id", rec.ID.String(),
		"workflow_run_id", run.ID.String(),
	)

	return map[string]any{
		"object":    object,
		"record_id": rec.ID.String(),
	}, nil
}

// buildCreateFields turns the action's field config into a bare-keyed write map.
// The builder stores fields as [{field: "<object>.<key>", value}], mirroring
// update_record's rows; here we strip the object prefix (RecordService keys by the
// object's own field keys) and interpolate string values against the run context.
func buildCreateFields(object string, params map[string]any, evalCtx EvalContext) map[string]interface{} {
	raw, ok := params["fields"]
	if !ok {
		return nil
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make(map[string]interface{}, len(entries))
	prefix := object + "."
	for _, item := range entries {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		field, _ := m["field"].(string)
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		key := strings.TrimPrefix(field, prefix) // "company.name" → "name"; bare stays bare
		out[key] = interpolateFieldValue(m["value"], evalCtx)
	}
	return out
}

// interpolateFieldValue resolves templates in a field value. Strings are
// interpolated (a "{{deal.title}}" becomes its runtime value); non-string values
// (numbers, booleans set literally in the form) pass through unchanged for
// RecordService to validate against the field's type.
func interpolateFieldValue(v any, evalCtx EvalContext) any {
	if s, ok := v.(string); ok {
		return InterpolateTemplate(s, evalCtx)
	}
	return v
}

// classifyRecordServiceError marks OLS/FLS/validation failures (surfaced as
// *domain.AppError — 400/403/404) as permanent, and everything else (a raw DB
// error) as retryable. Retrying a denied or invalid write never succeeds, so it
// must not be requeued; a transient DB blip should be.
func classifyRecordServiceError(wrapped, original error) error {
	var appErr *domain.AppError
	if errors.As(original, &appErr) {
		return wrapped // permanent
	}
	return NewRetryableError(wrapped)
}
