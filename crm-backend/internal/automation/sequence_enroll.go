package automation

import (
	"context"

	"github.com/google/uuid"
)

// marketingEnrollmentKey tags a sequence-enrolled run with the enrollment that created
// it, so per-enrollment progress counts only THIS enrollment's runs — a workflow may
// back several enrollments (different segments), and must never conflate them nor count
// the workflow's own natural-trigger runs. It is an underscore key that is not a known
// payload key, so buildEvalContext and the suppression/silence/depth predicates ignore it.
const marketingEnrollmentKey = "_marketing_enrollment_id"

// EnrollContact enrolls a single contact into target at DEPTH 0, building the same
// trigger context a natural contact trigger produces (contact fields hydrated from the
// DB), tagged with enrollmentID, with the given idempotency key. It is the entry the
// marketing sequence feeder (M8) uses to enroll a segment one contact at a time —
// bypassing the 100-capped enroll_records action AND keeping the full depth-2 headroom
// for any enroll steps inside the sequence itself (a depth-0 run is one whose context
// omits _enroll_depth). inserted=false ⇒ the (workflow_id, idempotency_key) was already
// claimed (an idempotent re-feed no-op), so a contact enrolls into a given sequence at
// most once.
//
// "At most once" here means FOREVER, and that word is load-bearing: re-enrolling a segment
// into a sequence is allowed once the first enrollment completes (the marketing-side guard
// is a partial index over ACTIVE rows only), so the feeder re-walks the whole segment and
// asks to enroll every contact again. This is therefore the one enroll path that takes the
// durable-claim route: its dedupe is recorded in automation_run_idempotency_claims and
// survives the 90-day run pruner that used to delete the only copy of it. Every other
// caller goes through EnrollRun and keeps the run-lifetime dedupe.
func (e *Engine) EnrollContact(ctx context.Context, orgID uuid.UUID, target *Workflow, contactID, enrollmentID uuid.UUID, idempotencyKey string) (bool, error) {
	fields := loadContactForTrigger(ctx, e.db, orgID, contactID)
	if fields == nil {
		// The contact was deleted between segment resolution and enroll (a rare race) —
		// enroll with a minimal context so nothing is silently lost; the send step's own
		// email validation then drops it.
		fields = map[string]any{"id": contactID.String()}
	}
	tc := contactEnrollContext(fields, contactID, triggerTypeOf(target.Trigger), enrollmentID)
	return e.enrollRun(ctx, orgID, target, tc, idempotencyKey, true)
}

// contactEnrollContext builds the trigger context for a sequence-enrolled contact run.
// It deliberately omits _enroll_depth ⇒ the run is DEPTH 0 (enrollDepthOf returns 0 for
// an absent key), preserving the full depth-2 headroom for any enroll steps inside the
// sequence itself. The record sits under the "contact" slug key exactly as
// buildEvalContext reads it, so {{contact.*}} resolves against the enrolled contact.
func contactEnrollContext(fields map[string]any, contactID uuid.UUID, triggerType string, enrollmentID uuid.UUID) map[string]any {
	tc := map[string]any{
		"entity_id": contactID.String(),
		"contact":   fields,
		"trigger":   map[string]any{"type": triggerType, "source": "marketing_sequence"},
	}
	if enrollmentID != uuid.Nil {
		tc[marketingEnrollmentKey] = enrollmentID.String()
	}
	return tc
}

// CountRunsByEnrollment returns a {status: count} rollup of the runs an enrollment
// produced — the data behind the M8 sequence "enrolled / active / completed" recipient
// view. Scoped to the enrollment's tag (never workflow-wide, so sibling enrollments and
// the workflow's own natural-trigger runs are excluded). Callerless and explicitly
// org-scoped; workflow_id (indexed) narrows before the jsonb tag filter.
func (e *Engine) CountRunsByEnrollment(ctx context.Context, orgID, workflowID, enrollmentID uuid.UUID) (map[string]int64, error) {
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	if err := e.db.WithContext(ctx).
		Raw(`SELECT status, count(*) AS count FROM automation_workflow_runs
			WHERE org_id = ? AND workflow_id = ? AND trigger_context->>? = ?
			GROUP BY status`, orgID, workflowID, marketingEnrollmentKey, enrollmentID.String()).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Status] = r.Count
	}
	return out, nil
}
