package automation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

// maxEnrollDepth bounds enrollment chains: a run at depth >= this can't enroll
// again. Depth 0 = a normally-triggered run; each enroll increments it. Rejecting
// at depth 2 caps the chain at two levels of enrollment (plan A6: "reject > 2"),
// so A→B→C stops before a fourth generation and can't run away.
const maxEnrollDepth = 2

// Enroller is the narrow port enroll_records uses to create runs in a target
// workflow — satisfied by *Engine. Kept minimal so the executor stays testable
// with a fake.
type Enroller interface {
	// LoadWorkflow fetches a workflow by id within an org (nil, nil when absent).
	LoadWorkflow(ctx context.Context, orgID, wfID uuid.UUID) (*Workflow, error)
	// EnrollRun creates + dispatches a run for target with the given trigger
	// context and idempotency key. inserted=false means the key already existed
	// (a re-enroll of the same source-run+record — a no-op).
	EnrollRun(ctx context.Context, orgID uuid.UUID, target *Workflow, triggerCtx map[string]any, idempotencyKey string) (bool, error)
}

// EnrollRecordsExecutor enrolls every record matching an object+filter query into
// a target workflow, one run per record.
type EnrollRecordsExecutor struct {
	enroller Enroller
	lister   RecordLister
}

func NewEnrollRecordsExecutor(enroller Enroller, lister RecordLister) *EnrollRecordsExecutor {
	return &EnrollRecordsExecutor{enroller: enroller, lister: lister}
}

func (e *EnrollRecordsExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	if e.enroller == nil || e.lister == nil {
		return nil, fmt.Errorf("enroll_records: engine is not configured")
	}

	// Runaway guard: refuse if this run is already at the enroll-depth limit.
	depth := enrollDepthOf(run)
	if depth >= maxEnrollDepth {
		return nil, fmt.Errorf("enroll_records: enroll depth limit (%d) reached — not enrolling further", maxEnrollDepth)
	}

	targetIDStr := getStringParam(action.Params, "workflow_id", evalCtx)
	targetID, err := uuid.Parse(targetIDStr)
	if err != nil {
		return nil, fmt.Errorf("enroll_records: invalid target workflow_id %q", targetIDStr)
	}
	target, err := e.enroller.LoadWorkflow(ctx, run.OrgID, targetID)
	if err != nil {
		return nil, NewRetryableError(fmt.Errorf("enroll_records: load target workflow: %w", err))
	}
	if target == nil {
		return nil, fmt.Errorf("enroll_records: target workflow not found")
	}
	if !target.IsActive {
		return nil, fmt.Errorf("enroll_records: target workflow is not active")
	}

	object := getStringParam(action.Params, "object", evalCtx)
	if object == "" {
		return nil, fmt.Errorf("enroll_records: object is required")
	}

	list, err := listRecords(ctx, e.lister, run.OrgID, object, action.Params, evalCtx)
	if err != nil {
		return nil, err
	}

	targetTrigger := triggerTypeOf(target.Trigger)
	enrolled := 0
	for i := range list.Records {
		rec := list.Records[i]
		tc := buildEnrollContext(object, rec.ID, rec.Fields, targetTrigger, depth+1)
		key := enrollIdempotencyKey(run.ID, targetID, rec.ID)
		inserted, err := e.enroller.EnrollRun(ctx, run.OrgID, target, tc, key)
		if err != nil {
			// Idempotency makes a full retry safe (already-enrolled records dedupe),
			// so surface a transient failure as retryable rather than losing the batch.
			return nil, NewRetryableError(fmt.Errorf("enroll_records: enrol record %s: %w", rec.ID, err))
		}
		if inserted {
			enrolled++
		}
	}

	slog.Info("automation: records enrolled",
		"target_workflow", targetID.String(),
		"matched", len(list.Records),
		"enrolled", enrolled,
		"workflow_run_id", run.ID.String(),
	)

	return map[string]any{
		"workflow_id": targetID.String(),
		"matched":     len(list.Records),
		"enrolled":    enrolled,
	}, nil
}

// enrollDepthOf reads the source run's enroll depth from its trigger context
// (absent → 0, i.e. a normally-triggered run).
func enrollDepthOf(run *WorkflowRun) int {
	if run == nil || len(run.TriggerContext) == 0 {
		return 0
	}
	var m map[string]any
	if json.Unmarshal(run.TriggerContext, &m) != nil {
		return 0
	}
	switch v := m["_enroll_depth"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return 0
	}
}

// buildEnrollContext builds the trigger context for an enrolled run. The record is
// placed under its object-slug key (contact/deal → ctx.Contact/Deal; anything else
// → ctx.Extra[slug]) exactly as buildEvalContext reads it, so the target's
// conditions and templates resolve against the enrolled record. _enroll_depth
// carries the runaway counter forward.
func buildEnrollContext(object string, recordID uuid.UUID, fields map[string]interface{}, targetTrigger string, depth int) map[string]any {
	recMap := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		recMap[k] = v
	}
	recMap["id"] = recordID.String()
	return map[string]any{
		"entity_id":     recordID.String(),
		object:          recMap,
		"trigger":       map[string]any{"type": targetTrigger, "source": "enroll"},
		"_enroll_depth": depth,
	}
}

// enrollIdempotencyKey ties an enrolled run to its (source run, target workflow,
// record) so a retry or re-run of the source enrolls each record at most once.
func enrollIdempotencyKey(sourceRun, targetWF, recordID uuid.UUID) string {
	return fmt.Sprintf("enroll:%x", sha256.Sum256([]byte(sourceRun.String()+":"+targetWF.String()+":"+recordID.String())))
}

// triggerTypeOf extracts a workflow's trigger type from its stored Trigger JSON.
func triggerTypeOf(trigger []byte) string {
	var spec TriggerSpec
	if json.Unmarshal(trigger, &spec) != nil {
		return ""
	}
	return spec.Type
}
