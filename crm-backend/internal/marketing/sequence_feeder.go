package marketing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"crm-backend/internal/automation"
	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

const (
	sequenceFeedInterval = 15 * time.Second
	sequenceFeedJobs     = 20 // active enrollments processed per tick
	// sequenceFeedPageSize is the number of contacts enrolled per enrollment per tick.
	// Kept modest so a burst of feeders does not overflow the engine's 100-buffer jobs
	// channel — an overflow is not lost (the row is durably inserted; the 30s
	// stranded-pending sweep re-dispatches), but a gentle rate avoids churn.
	sequenceFeedPageSize = 50
)

// feederStore is the persistence slice the feeder needs.
type feederStore interface {
	ListActiveEnrollments(ctx context.Context, limit int) ([]SequenceEnrollment, error)
	ResolveSegmentPage(ctx context.Context, aud domain.AudienceQuery, afterContactID uuid.UUID, limit int) ([]RecipientRow, error)
	AdvanceEnrollmentCursor(ctx context.Context, id, cursor uuid.UUID, enrolledDelta int) error
	CompleteEnrollment(ctx context.Context, id uuid.UUID) error
	BlockEnrollment(ctx context.Context, id uuid.UUID, reason string) error
	NoteEnrollmentError(ctx context.Context, id uuid.UUID, reason string) error
}

// The feeder resolves a segment via the shared audienceResolver interface (declared in
// campaign_usecase.go, satisfied by the domain.SegmentUseCase) — callerless / org-wide,
// since the send-time gate, not the feed, is the mailability authority.

// sequenceEnroller loads a target workflow and enrolls one contact at depth 0, tagging
// the run with the enrollment id (satisfied by *automation.Engine).
type sequenceEnroller interface {
	LoadWorkflow(ctx context.Context, orgID, wfID uuid.UUID) (*automation.Workflow, error)
	EnrollContact(ctx context.Context, orgID uuid.UUID, target *automation.Workflow, contactID, enrollmentID uuid.UUID, idempotencyKey string) (bool, error)
}

// SequenceFeeder drains M5 segments into drip-sequence workflows in bounded,
// cursor-paginated batches. It runs callerless with an explicit org filter on every
// query (org_id carried per enrollment row). Enrollment is idempotent per (sequence,
// contact), so a re-fed page is a no-op and the keyset cursor only ever moves forward —
// which is what makes the feed safe to run on multiple app instances at once.
type SequenceFeeder struct {
	store    feederStore
	segments audienceResolver
	enroller sequenceEnroller
	pageSize int
	logger   *slog.Logger
}

// NewSequenceFeeder builds the feeder.
func NewSequenceFeeder(store feederStore, segments audienceResolver, enroller sequenceEnroller, logger *slog.Logger) *SequenceFeeder {
	if logger == nil {
		logger = slog.Default()
	}
	return &SequenceFeeder{store: store, segments: segments, enroller: enroller, pageSize: sequenceFeedPageSize, logger: logger}
}

// StartSequenceFeeder runs the feed loop until ctx is done.
func StartSequenceFeeder(ctx context.Context, f *SequenceFeeder) {
	if f.enroller == nil || f.segments == nil {
		f.logger.Warn("marketing: sequence feeder not started (missing engine or segment resolver)")
		return
	}
	ticker := time.NewTicker(sequenceFeedInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.tick(ctx)
		}
	}
}

func (f *SequenceFeeder) tick(ctx context.Context) {
	jobs, err := f.store.ListActiveEnrollments(ctx, sequenceFeedJobs)
	if err != nil {
		f.logger.Error("marketing: list active enrollments failed", "error", err)
		return
	}
	for i := range jobs {
		if ctx.Err() != nil {
			return
		}
		f.feedOne(ctx, jobs[i])
	}
}

// feedOne resolves + enrolls ONE page of a segment into a sequence, then advances the
// cursor (or completes / blocks). One page per tick keeps the jobs channel from
// overflowing; enrollment is idempotent so a re-run is harmless.
func (f *SequenceFeeder) feedOne(ctx context.Context, e SequenceEnrollment) {
	target, err := f.enroller.LoadWorkflow(ctx, e.OrgID, e.SequenceWorkflowID)
	if err != nil {
		_ = f.store.NoteEnrollmentError(ctx, e.ID, "load sequence workflow: "+err.Error())
		return
	}
	if target == nil {
		// The sequence workflow is gone — enrollment can never run. Terminal.
		_ = f.store.BlockEnrollment(ctx, e.ID, "sequence workflow not found")
		return
	}
	if !target.IsActive {
		// Resumable: an inactive sequence pauses feeding; re-activating resumes it.
		_ = f.store.NoteEnrollmentError(ctx, e.ID, "sequence workflow is not active")
		return
	}

	aud, err := f.segments.AudienceQueryForSegments(ctx, e.OrgID, []uuid.UUID{e.SegmentID}, nil)
	if err != nil {
		_ = f.store.NoteEnrollmentError(ctx, e.ID, "resolve segment: "+err.Error())
		return
	}

	cursor := uuid.Nil
	if e.FeederCursor != nil {
		cursor = *e.FeederCursor
	}
	rows, err := f.store.ResolveSegmentPage(ctx, *aud, cursor, f.pageSize)
	if err != nil {
		_ = f.store.NoteEnrollmentError(ctx, e.ID, "resolve segment page: "+err.Error())
		return
	}
	if len(rows) == 0 {
		if err := f.store.CompleteEnrollment(ctx, e.ID); err != nil {
			f.logger.Error("marketing: complete enrollment failed", "enrollment", e.ID.String(), "error", err)
		}
		return
	}

	enrolled := 0
	lastOK := cursor
	for _, row := range rows {
		key := sequenceEnrollKey(e.SequenceWorkflowID, row.ContactID)
		inserted, err := f.enroller.EnrollContact(ctx, e.OrgID, target, row.ContactID, e.ID, key)
		if err != nil {
			// Transient enroll failure: stop the page, advance only to the last success,
			// record it, and retry the rest next tick (idempotent, so no double-enroll).
			_ = f.store.NoteEnrollmentError(ctx, e.ID, "enroll contact: "+err.Error())
			break
		}
		if inserted {
			enrolled++
		}
		lastOK = row.ContactID
	}

	// Advance the cursor to the last contact we actually processed (rows are ordered by
	// contact_id, so nothing between the old cursor and lastOK is skipped). Only advance
	// when it moved — a first-row transient failure leaves the cursor for a clean retry.
	if lastOK != cursor {
		if err := f.store.AdvanceEnrollmentCursor(ctx, e.ID, lastOK, enrolled); err != nil {
			f.logger.Error("marketing: advance enrollment cursor failed", "enrollment", e.ID.String(), "error", err)
		}
	}
	if enrolled > 0 {
		f.logger.Info("marketing: sequence feeder enrolled",
			"enrollment", e.ID.String(),
			"sequence", e.SequenceWorkflowID.String(),
			"enrolled", enrolled,
		)
	}
}

// sequenceEnrollKey is the deterministic per-(sequence, contact) idempotency key. Because
// the dedupe is on (workflow_id, idempotency_key) and workflow_id is the sequence, this
// enrolls each contact into a given sequence at most once, forever.
//
// "Forever" is backed by automation_run_idempotency_claims, NOT by the workflow run row:
// EnrollContact routes this key through automation's durable-claim path precisely because
// the run row is reclaimed by the 90-day run pruner. While the run WAS the ledger, a
// segment re-enrolled after that window re-mailed the entire drip to every contact who had
// already completed it. Re-enrollment stays deliberately open (the marketing-side unique
// index only forbids a second ACTIVE enrollment of the same sequence+segment) so that a
// re-enrolled segment picks up its NEW members — the per-contact claim is what keeps the
// existing members from being mailed twice.
func sequenceEnrollKey(seqWFID, contactID uuid.UUID) string {
	return fmt.Sprintf("seq:%s:contact:%s", seqWFID, contactID)
}
