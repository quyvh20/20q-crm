package marketing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"crm-backend/internal/automation"
	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// sequenceWorkflowInfo is the automation seam the sequence usecase needs: resolve a
// workflow (name/active/existence) and roll up its runs by status for the recipient
// view. Satisfied by *automation.Engine.
type sequenceWorkflowInfo interface {
	LoadWorkflow(ctx context.Context, orgID, wfID uuid.UUID) (*automation.Workflow, error)
	CountRunsByEnrollment(ctx context.Context, orgID, workflowID, enrollmentID uuid.UUID) (map[string]int64, error)
}

// SequenceUseCase is the management surface behind /api/marketing/sequences: it wires a
// segment (M5) to a drip-sequence workflow (the automation builder) and the feeder
// drains the rest. It writes only the marketing_sequence_enrollments tracking table —
// never a CRM record — so it takes no RecordAuthorizer.
type SequenceUseCase struct {
	repo     *Repository
	segments domain.SegmentUseCase
	wf       sequenceWorkflowInfo
	logger   *slog.Logger
}

// NewSequenceUseCase builds the usecase.
func NewSequenceUseCase(repo *Repository, segments domain.SegmentUseCase, wf sequenceWorkflowInfo, logger *slog.Logger) *SequenceUseCase {
	if logger == nil {
		logger = slog.Default()
	}
	return &SequenceUseCase{repo: repo, segments: segments, wf: wf, logger: logger}
}

// SequenceEnrollmentDTO is the API shape (names resolved for the list/detail UI).
type SequenceEnrollmentDTO struct {
	ID                 uuid.UUID `json:"id"`
	SequenceWorkflowID uuid.UUID `json:"sequence_workflow_id"`
	SequenceName       string    `json:"sequence_name"`
	SegmentID          uuid.UUID `json:"segment_id"`
	SegmentName        string    `json:"segment_name"`
	Status             string    `json:"status"`
	EnrolledCount      int       `json:"enrolled_count"`
	LastError          string    `json:"last_error,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

// Create validates the sequence workflow + segment and starts an enrollment. It rejects
// a duplicate ACTIVE enrollment for the same (sequence, segment). Enrollment is allowed
// even for an inactive sequence (the feeder pauses until it is activated), so an admin
// can wire the audience before flipping the drip on.
func (uc *SequenceUseCase) Create(ctx context.Context, orgID, userID, seqWFID, segmentID uuid.UUID) (*SequenceEnrollmentDTO, error) {
	wf, err := uc.wf.LoadWorkflow(ctx, orgID, seqWFID)
	if err != nil {
		return nil, err
	}
	if wf == nil {
		return nil, domain.NewAppError(http.StatusNotFound, "sequence workflow not found")
	}
	// A sequence must actually send marketing mail — enrolling a segment into a workflow
	// with no channel=marketing send step does nothing useful (and would silently create
	// runs that never mail). Require at least one before wiring the audience.
	if !workflowHasMarketingSend(wf) {
		return nil, domain.NewAppError(http.StatusUnprocessableEntity,
			"this workflow has no marketing send step — add a send step with channel = marketing before enrolling a segment")
	}
	seg, err := uc.segments.Get(ctx, orgID, segmentID)
	if err != nil {
		return nil, err
	}
	if seg == nil {
		return nil, domain.NewAppError(http.StatusNotFound, "segment not found")
	}
	existing, err := uc.repo.ActiveEnrollmentForSegment(ctx, orgID, seqWFID, segmentID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, domain.NewAppError(http.StatusConflict, "an active enrollment for this segment and sequence already exists")
	}

	row := &SequenceEnrollment{
		OrgID:              orgID,
		SequenceWorkflowID: seqWFID,
		SegmentID:          segmentID,
		Status:             SeqEnrollActive,
	}
	if userID != uuid.Nil {
		row.CreatedBy = &userID
	}
	if err := uc.repo.CreateEnrollment(ctx, row); err != nil {
		return nil, err
	}
	return &SequenceEnrollmentDTO{
		ID:                 row.ID,
		SequenceWorkflowID: seqWFID,
		SequenceName:       wf.Name,
		SegmentID:          segmentID,
		SegmentName:        seg.Name,
		Status:             row.Status,
		EnrolledCount:      row.EnrolledCount,
		CreatedAt:          row.CreatedAt,
	}, nil
}

// List returns the org's enrollments with sequence + segment names resolved. Missing
// (deleted) names resolve to empty rather than erroring the whole list.
func (uc *SequenceUseCase) List(ctx context.Context, orgID uuid.UUID) ([]SequenceEnrollmentDTO, error) {
	rows, err := uc.repo.ListEnrollments(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]SequenceEnrollmentDTO, 0, len(rows))
	wfNames := map[uuid.UUID]string{}
	segNames := map[uuid.UUID]string{}
	for i := range rows {
		r := rows[i]
		out = append(out, SequenceEnrollmentDTO{
			ID:                 r.ID,
			SequenceWorkflowID: r.SequenceWorkflowID,
			SequenceName:       uc.workflowName(ctx, orgID, r.SequenceWorkflowID, wfNames),
			SegmentID:          r.SegmentID,
			SegmentName:        uc.segmentName(ctx, orgID, r.SegmentID, segNames),
			Status:             r.Status,
			EnrolledCount:      r.EnrolledCount,
			LastError:          r.LastError,
			CreatedAt:          r.CreatedAt,
		})
	}
	return out, nil
}

// Get returns one enrollment (404 as nil).
func (uc *SequenceUseCase) Get(ctx context.Context, orgID, id uuid.UUID) (*SequenceEnrollmentDTO, error) {
	r, err := uc.repo.GetEnrollment(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return &SequenceEnrollmentDTO{
		ID:                 r.ID,
		SequenceWorkflowID: r.SequenceWorkflowID,
		SequenceName:       uc.workflowName(ctx, orgID, r.SequenceWorkflowID, nil),
		SegmentID:          r.SegmentID,
		SegmentName:        uc.segmentName(ctx, orgID, r.SegmentID, nil),
		Status:             r.Status,
		EnrolledCount:      r.EnrolledCount,
		LastError:          r.LastError,
		CreatedAt:          r.CreatedAt,
	}, nil
}

// Cancel stops further enrollment (already-enrolled runs finish). Returns false when
// the enrollment does not exist or is already terminal.
func (uc *SequenceUseCase) Cancel(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	return uc.repo.CancelEnrollment(ctx, orgID, id)
}

// SequenceProgress is the recipient-state rollup: the enrolled runs bucketed into
// active / completed / failed / skipped, plus the total.
type SequenceProgress struct {
	Total     int64            `json:"total"`
	Active    int64            `json:"active"`
	Completed int64            `json:"completed"`
	Failed    int64            `json:"failed"`
	Skipped   int64            `json:"skipped"`
	ByStatus  map[string]int64 `json:"by_status"`
}

// Progress rolls up the enrolled runs for a sequence's workflow. 404-as-nil when the
// enrollment is unknown.
func (uc *SequenceUseCase) Progress(ctx context.Context, orgID, id uuid.UUID) (*SequenceProgress, error) {
	r, err := uc.repo.GetEnrollment(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	counts, err := uc.wf.CountRunsByEnrollment(ctx, orgID, r.SequenceWorkflowID, r.ID)
	if err != nil {
		return nil, err
	}
	p := &SequenceProgress{ByStatus: counts}
	for status, n := range counts {
		p.Total += n
		switch status {
		case "pending", "running", "waiting":
			p.Active += n
		case "completed":
			p.Completed += n
		case "failed":
			p.Failed += n
		case "skipped":
			p.Skipped += n
		}
	}
	return p, nil
}

func (uc *SequenceUseCase) workflowName(ctx context.Context, orgID, wfID uuid.UUID, cache map[uuid.UUID]string) string {
	if cache != nil {
		if n, ok := cache[wfID]; ok {
			return n
		}
	}
	name := ""
	if wf, err := uc.wf.LoadWorkflow(ctx, orgID, wfID); err == nil && wf != nil {
		name = wf.Name
	}
	if cache != nil {
		cache[wfID] = name
	}
	return name
}

// workflowHasMarketingSend reports whether a workflow contains at least one send_email
// step with channel=marketing (scanning the full flattened tree, so steps inside If/Else
// branches count). This is the load-bearing "is this a drip sequence?" check.
func workflowHasMarketingSend(wf *automation.Workflow) bool {
	for _, a := range flattenWorkflowActions(wf) {
		if a.Type == automation.ActionSendEmail {
			if ch, _ := a.Params["channel"].(string); ch == "marketing" {
				return true
			}
		}
	}
	return false
}

// flattenWorkflowActions returns the workflow's actions in DFS order. It prefers the
// Steps tree (branches inlined) and falls back to the deprecated flat Actions list, so
// it stays correct once the Actions column is removed.
func flattenWorkflowActions(wf *automation.Workflow) []automation.ActionSpec {
	if len(wf.Steps) > 0 {
		var steps []automation.StepSpec
		if err := json.Unmarshal(wf.Steps, &steps); err == nil && len(steps) > 0 {
			return automation.FlattenStepsToActions(steps)
		}
	}
	var actions []automation.ActionSpec
	_ = json.Unmarshal(wf.Actions, &actions)
	return actions
}

func (uc *SequenceUseCase) segmentName(ctx context.Context, orgID, segID uuid.UUID, cache map[uuid.UUID]string) string {
	if cache != nil {
		if n, ok := cache[segID]; ok {
			return n
		}
	}
	name := ""
	if seg, err := uc.segments.Get(ctx, orgID, segID); err == nil && seg != nil {
		name = seg.Name
	}
	if cache != nil {
		cache[segID] = name
	}
	return name
}
