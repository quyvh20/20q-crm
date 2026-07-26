package marketing

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"testing"

	"crm-backend/internal/automation"
	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ── fakes ───────────────────────────────────────────────────────────────────────

// fakeFeederStore models the enrollment row + keyset segment resolution in memory. It
// enforces the same monotonic-cursor guard the real UPDATE does.
type fakeFeederStore struct {
	rows []RecipientRow // the whole segment, sorted by ContactID ascending
	enr  *SequenceEnrollment
	note []string
}

func (s *fakeFeederStore) ListActiveEnrollments(context.Context, int) ([]SequenceEnrollment, error) {
	if s.enr == nil || s.enr.Status != SeqEnrollActive {
		return nil, nil
	}
	cp := *s.enr // value copy (as a DB read would give)
	return []SequenceEnrollment{cp}, nil
}

func (s *fakeFeederStore) ResolveSegmentPage(_ context.Context, _ domain.AudienceQuery, after uuid.UUID, limit int) ([]RecipientRow, error) {
	out := []RecipientRow{}
	for _, r := range s.rows {
		if bytes.Compare(r.ContactID[:], after[:]) > 0 {
			out = append(out, r)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *fakeFeederStore) AdvanceEnrollmentCursor(_ context.Context, _ uuid.UUID, cursor uuid.UUID, delta int) error {
	// Monotonic guard: never rewind.
	if s.enr.FeederCursor != nil && bytes.Compare(cursor[:], s.enr.FeederCursor[:]) <= 0 {
		return nil
	}
	c := cursor
	s.enr.FeederCursor = &c
	s.enr.EnrolledCount += delta
	return nil
}

func (s *fakeFeederStore) CompleteEnrollment(context.Context, uuid.UUID) error {
	s.enr.Status = SeqEnrollCompleted
	return nil
}
func (s *fakeFeederStore) BlockEnrollment(_ context.Context, _ uuid.UUID, reason string) error {
	s.enr.Status = SeqEnrollBlocked
	s.enr.LastError = reason
	return nil
}
func (s *fakeFeederStore) NoteEnrollmentError(_ context.Context, _ uuid.UUID, reason string) error {
	s.note = append(s.note, reason)
	return nil
}

type fakeSeqEnroller struct {
	wf       *automation.Workflow
	enrolled map[uuid.UUID]bool
	failOn   *uuid.UUID
}

func (e *fakeSeqEnroller) LoadWorkflow(context.Context, uuid.UUID, uuid.UUID) (*automation.Workflow, error) {
	return e.wf, nil
}
func (e *fakeSeqEnroller) EnrollContact(_ context.Context, _ uuid.UUID, _ *automation.Workflow, contactID, _ uuid.UUID, _ string) (bool, error) {
	if e.failOn != nil && *e.failOn == contactID {
		return false, errors.New("transient enroll failure")
	}
	if e.enrolled[contactID] {
		return false, nil // idempotent no-op
	}
	e.enrolled[contactID] = true
	return true, nil
}

// dummyResolver satisfies the shared audienceResolver interface; the fake store ignores
// the returned query (it paginates its in-memory rows directly).
type dummyResolver struct{}

func (dummyResolver) AudienceQueryForSegments(context.Context, uuid.UUID, []uuid.UUID, []uuid.UUID) (*domain.AudienceQuery, error) {
	return &domain.AudienceQuery{SelectSQL: "SELECT 1", Args: nil}, nil
}
func (dummyResolver) Get(context.Context, uuid.UUID, uuid.UUID) (*domain.Segment, error) {
	return &domain.Segment{Name: "Seg"}, nil
}

// ── helpers ─────────────────────────────────────────────────────────────────────

func sortedRecipients(n int) []RecipientRow {
	rows := make([]RecipientRow, n)
	for i := 0; i < n; i++ {
		rows[i] = RecipientRow{ContactID: uuid.New(), EmailNormalized: "x@y.com"}
	}
	sort.Slice(rows, func(i, j int) bool {
		return bytes.Compare(rows[i].ContactID[:], rows[j].ContactID[:]) < 0
	})
	return rows
}

func activeEnrollment() *SequenceEnrollment {
	return &SequenceEnrollment{ID: uuid.New(), OrgID: uuid.New(), SequenceWorkflowID: uuid.New(), SegmentID: uuid.New(), Status: SeqEnrollActive}
}

func activeWorkflow() *automation.Workflow {
	return &automation.Workflow{ID: uuid.New(), IsActive: true}
}

func runToDrain(f *SequenceFeeder, store *fakeFeederStore, maxTicks int) {
	for i := 0; i < maxTicks && store.enr.Status == SeqEnrollActive; i++ {
		f.tick(context.Background())
	}
}

// ── tests ───────────────────────────────────────────────────────────────────────

// TestFeeder_DrainsWholeSegment_NoCap proves a 250-contact segment (well past the 100
// cap the enroll_records action silently imposes) is enrolled FULLY across ticks.
func TestFeeder_DrainsWholeSegment_NoCap(t *testing.T) {
	store := &fakeFeederStore{rows: sortedRecipients(250), enr: activeEnrollment()}
	enr := &fakeSeqEnroller{wf: activeWorkflow(), enrolled: map[uuid.UUID]bool{}}
	f := NewSequenceFeeder(store, dummyResolver{}, enr, nil)

	runToDrain(f, store, 30)

	if store.enr.Status != SeqEnrollCompleted {
		t.Fatalf("enrollment not completed: %s", store.enr.Status)
	}
	if len(enr.enrolled) != 250 {
		t.Fatalf("enrolled %d contacts, want 250 (no 100 cap)", len(enr.enrolled))
	}
	if store.enr.EnrolledCount != 250 {
		t.Fatalf("enrolled_count = %d, want 250", store.enr.EnrolledCount)
	}
}

// TestFeeder_IdempotentRefeed proves re-feeding the same segment (cursor reset) enrolls
// nobody new — a contact enrolls into a sequence at most once.
func TestFeeder_IdempotentRefeed(t *testing.T) {
	rows := sortedRecipients(120)
	store := &fakeFeederStore{rows: rows, enr: activeEnrollment()}
	enr := &fakeSeqEnroller{wf: activeWorkflow(), enrolled: map[uuid.UUID]bool{}}
	f := NewSequenceFeeder(store, dummyResolver{}, enr, nil)
	runToDrain(f, store, 30)
	first := len(enr.enrolled)

	// Reset the cursor + reactivate (a re-created enrollment over the same audience).
	store.enr.FeederCursor = nil
	store.enr.Status = SeqEnrollActive
	store.enr.EnrolledCount = 0
	runToDrain(f, store, 30)

	if len(enr.enrolled) != first {
		t.Fatalf("re-feed enrolled new contacts: %d then %d (must be idempotent)", first, len(enr.enrolled))
	}
	if store.enr.EnrolledCount != 0 {
		t.Fatalf("re-feed counted %d new enrollments, want 0", store.enr.EnrolledCount)
	}
}

// TestFeeder_TransientFailureAdvancesToLastSuccess proves a mid-page enroll error stops
// at the failure, advances the cursor only to the last SUCCESS, and resumes cleanly once
// the failure clears — nobody is skipped.
func TestFeeder_TransientFailureAdvancesToLastSuccess(t *testing.T) {
	rows := sortedRecipients(60)
	failID := rows[30].ContactID
	store := &fakeFeederStore{rows: rows, enr: activeEnrollment()}
	enr := &fakeSeqEnroller{wf: activeWorkflow(), enrolled: map[uuid.UUID]bool{}, failOn: &failID}
	f := NewSequenceFeeder(store, dummyResolver{}, enr, nil)

	// A few ticks stall at the failing contact (cursor pinned to rows[29]).
	for i := 0; i < 5; i++ {
		f.tick(context.Background())
	}
	if store.enr.Status != SeqEnrollActive {
		t.Fatalf("must still be active while stalled, got %s", store.enr.Status)
	}
	if store.enr.FeederCursor == nil || *store.enr.FeederCursor != rows[29].ContactID {
		t.Fatalf("cursor should pin to last success rows[29], got %v", store.enr.FeederCursor)
	}
	if len(enr.enrolled) != 30 {
		t.Fatalf("only the 30 before the failure should be enrolled, got %d", len(enr.enrolled))
	}

	// Clear the failure; the feed resumes and completes.
	enr.failOn = nil
	runToDrain(f, store, 30)
	if store.enr.Status != SeqEnrollCompleted || len(enr.enrolled) != 60 {
		t.Fatalf("did not resume to completion: status=%s enrolled=%d", store.enr.Status, len(enr.enrolled))
	}
}

// TestFeeder_MissingWorkflowBlocks proves a deleted sequence workflow blocks the
// enrollment (terminal); an inactive one just pauses (notes, stays active).
func TestFeeder_MissingWorkflowBlocks(t *testing.T) {
	store := &fakeFeederStore{rows: sortedRecipients(10), enr: activeEnrollment()}
	enr := &fakeSeqEnroller{wf: nil, enrolled: map[uuid.UUID]bool{}} // LoadWorkflow → nil
	f := NewSequenceFeeder(store, dummyResolver{}, enr, nil)
	f.tick(context.Background())
	if store.enr.Status != SeqEnrollBlocked {
		t.Fatalf("missing workflow must block, got %s", store.enr.Status)
	}
	if len(enr.enrolled) != 0 {
		t.Fatalf("blocked enrollment must enroll nobody, got %d", len(enr.enrolled))
	}
}

func TestFeeder_InactiveWorkflowPauses(t *testing.T) {
	store := &fakeFeederStore{rows: sortedRecipients(10), enr: activeEnrollment()}
	enr := &fakeSeqEnroller{wf: &automation.Workflow{ID: uuid.New(), IsActive: false}, enrolled: map[uuid.UUID]bool{}}
	f := NewSequenceFeeder(store, dummyResolver{}, enr, nil)
	f.tick(context.Background())
	if store.enr.Status != SeqEnrollActive {
		t.Fatalf("inactive workflow must keep the enrollment active (resumable), got %s", store.enr.Status)
	}
	if len(store.note) == 0 {
		t.Fatal("inactive workflow should record a note")
	}
	if len(enr.enrolled) != 0 {
		t.Fatalf("inactive workflow must enroll nobody, got %d", len(enr.enrolled))
	}
}
