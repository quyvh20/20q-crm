package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// fakeBacklog stands in for the DB-backed backlog so the sweep's policy — arm
// order, caps, truncation reporting, lock handling — is asserted without Docker.
// The SQL those methods stand for is covered by the repository integration tests.
type fakeBacklog struct {
	missing    []domain.RecordIndexCandidate
	unvectored []domain.RecordIndexCandidate
	contacts   []domain.Contact

	missingErr error
	locked     bool // false = another pod holds the lock
	lockErr    error

	gotPerOrg, gotLimit int
	calls               []string
}

func (f *fakeBacklog) MissingRecords(_ context.Context, perOrg, limit int) ([]domain.RecordIndexCandidate, error) {
	f.calls = append(f.calls, "missing")
	f.gotPerOrg, f.gotLimit = perOrg, limit
	return f.missing, f.missingErr
}

func (f *fakeBacklog) UnvectoredRecords(_ context.Context, _, _ int) ([]domain.RecordIndexCandidate, error) {
	f.calls = append(f.calls, "unvectored")
	return f.unvectored, nil
}

func (f *fakeBacklog) UnvectoredContacts(_ context.Context, _, _ int) ([]domain.Contact, error) {
	f.calls = append(f.calls, "contacts")
	return f.contacts, nil
}

func (f *fakeBacklog) WithReconcileLock(_ context.Context, fn func() error) (bool, error) {
	if f.lockErr != nil {
		return false, f.lockErr
	}
	if !f.locked {
		return false, nil
	}
	return true, fn()
}

func candidate(slug, display string, fields map[string]interface{}) domain.RecordIndexCandidate {
	data := "{}"
	if len(fields) > 0 {
		b, err := json.Marshal(fields)
		if err != nil {
			panic(err)
		}
		data = string(b)
	}
	return domain.RecordIndexCandidate{
		OrgID:       uuid.New(),
		ObjectSlug:  slug,
		RecordID:    uuid.New(),
		DisplayName: display,
		Data:        domain.JSON(data),
	}
}

// The sweep must index a candidate with byte-identical content to what the online
// write path would have produced — a second renderer here would silently split the
// index into two populations.
func TestReconcile_IndexesMissingRecordsWithWritePathContent(t *testing.T) {
	repo := &fakeEmbedRepo{}
	w := newTestWorker(repo)
	c := candidate("ticket", "Roof leak", map[string]interface{}{"priority": "high"})
	w.SetBacklog(&fakeBacklog{locked: true, missing: []domain.RecordIndexCandidate{c}})

	stats, ran, err := w.ReconcileOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("reconcile: ran=%v err=%v", ran, err)
	}
	if stats.MissingRecords != 1 {
		t.Fatalf("MissingRecords = %d, want 1", stats.MissingRecords)
	}
	if len(repo.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(repo.upserts))
	}
	got := repo.upserts[0]
	if got.RecordID != c.RecordID || got.ObjectSlug != "ticket" || got.OrgID != c.OrgID {
		t.Fatalf("upsert identity wrong: %+v", got)
	}
	want := domain.BuildRecordContent(&domain.UniformRecord{
		Display: "Roof leak",
		Fields:  map[string]interface{}{"priority": "high"},
	})
	if got.Content != want {
		t.Fatalf("content = %q, want the write path's %q", got.Content, want)
	}
}

// Multi-pod safety: losing the advisory lock means doing NOTHING, not doing the
// same paid embedding calls a second time.
func TestReconcile_SkipsEntirelyWhenAnotherPodHoldsTheLock(t *testing.T) {
	repo := &fakeEmbedRepo{}
	w := newTestWorker(repo)
	b := &fakeBacklog{locked: false, missing: []domain.RecordIndexCandidate{candidate("ticket", "Ignored", nil)}}
	w.SetBacklog(b)

	stats, ran, err := w.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if ran {
		t.Fatal("must report it did not run")
	}
	if len(b.calls) != 0 {
		t.Fatalf("must not even query the backlog: %v", b.calls)
	}
	if len(repo.upserts) != 0 || stats.total() != 0 {
		t.Fatalf("must not index anything: %d upserts, %+v", len(repo.upserts), stats)
	}
}

// No silent caps: a pass that hit its ceiling has to say so, or a permanent
// backlog looks exactly like a healthy empty one.
func TestReconcile_ReportsTruncationWhenAnArmHitsItsCap(t *testing.T) {
	repo := &fakeEmbedRepo{}
	w := newTestWorker(repo)

	full := make([]domain.RecordIndexCandidate, reconcileMaxPerPass)
	for i := range full {
		full[i] = candidate("ticket", fmt.Sprintf("T%d", i), nil)
	}
	b := &fakeBacklog{locked: true, missing: full}
	w.SetBacklog(b)

	stats, ran, err := w.ReconcileOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("reconcile: ran=%v err=%v", ran, err)
	}
	if stats.MissingRecords != reconcileMaxPerPass {
		t.Fatalf("MissingRecords = %d, want %d", stats.MissingRecords, reconcileMaxPerPass)
	}
	if len(stats.Truncated) != 1 || stats.Truncated[0] != "records_missing" {
		t.Fatalf("Truncated = %v, want [records_missing]", stats.Truncated)
	}
	// The caps the worker actually asks for are the cost ceiling; assert them
	// rather than trusting the constants to stay wired.
	if b.gotLimit != reconcileMaxPerPass || b.gotPerOrg != reconcileMaxPerOrg {
		t.Fatalf("asked for perOrg=%d limit=%d, want %d/%d", b.gotPerOrg, b.gotLimit, reconcileMaxPerOrg, reconcileMaxPerPass)
	}
}

// Without an embed service the vector-fill arms are pure cost with no possible
// progress, so they must not run — but the anti-join arm still must, because it
// restores fulltext (content-only) all on its own.
func TestReconcile_SkipsVectorArmsWithoutAnEmbedService(t *testing.T) {
	repo := &fakeEmbedRepo{}
	w := newTestWorker(repo) // embedSvc nil
	b := &fakeBacklog{
		locked:     true,
		missing:    []domain.RecordIndexCandidate{candidate("ticket", "Indexed anyway", nil)},
		unvectored: []domain.RecordIndexCandidate{candidate("ticket", "Would cost money", nil)},
		contacts:   []domain.Contact{{ID: uuid.New(), OrgID: uuid.New(), FirstName: "Ada"}},
	}
	w.SetBacklog(b)

	stats, ran, err := w.ReconcileOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("reconcile: ran=%v err=%v", ran, err)
	}
	if len(b.calls) != 1 || b.calls[0] != "missing" {
		t.Fatalf("arms queried = %v, want only [missing]", b.calls)
	}
	if stats.MissingRecords != 1 || stats.UnvectoredRecords != 0 || stats.UnvectoredContact != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(repo.upserts) != 1 {
		t.Fatalf("expected the fulltext repair to still happen, got %d upserts", len(repo.upserts))
	}
}

// A backlog query failure aborts the pass and surfaces — it must not be swallowed
// into a "found nothing" that looks healthy.
func TestReconcile_PropagatesBacklogError(t *testing.T) {
	w := newTestWorker(&fakeEmbedRepo{})
	w.SetBacklog(&fakeBacklog{locked: true, missingErr: errors.New("boom")})

	_, ran, err := w.ReconcileOnce(context.Background())
	if err == nil {
		t.Fatal("expected the query error to propagate")
	}
	if !ran {
		t.Fatal("the lock was held, so the pass did run")
	}
}

// A cancelled context stops mid-batch instead of burning the rest of the budget on
// a shutting-down pod.
func TestReconcile_StopsOnContextCancellation(t *testing.T) {
	repo := &fakeEmbedRepo{}
	w := newTestWorker(repo)
	w.SetBacklog(&fakeBacklog{locked: true, missing: []domain.RecordIndexCandidate{
		candidate("ticket", "A", nil), candidate("ticket", "B", nil),
	}})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stats, _, err := w.ReconcileOnce(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if stats.MissingRecords != 0 || len(repo.upserts) != 0 {
		t.Fatalf("cancelled pass still worked: %+v / %d upserts", stats, len(repo.upserts))
	}
}

// contactEmbedJob is shared with EnqueueContact so a reconciled contact embeds the
// same text a freshly-written one does — including the company name.
func TestContactEmbedJob_MatchesTheWritePathAdaptation(t *testing.T) {
	email := "ada@example.com"
	c := &domain.Contact{
		ID: uuid.New(), OrgID: uuid.New(),
		FirstName: "Ada", LastName: "Lovelace", Email: &email,
		CustomFields: domain.JSON(`{"tier":"gold"}`),
		Company:      &domain.Company{Name: "Acme"},
	}
	job := contactEmbedJob(c)
	if job.ContactID != c.ID || job.OrgID != c.OrgID {
		t.Fatalf("identity wrong: %+v", job)
	}
	if job.CompanyName == nil || *job.CompanyName != "Acme" {
		t.Fatalf("company name not carried: %+v", job.CompanyName)
	}
	if string(job.CustomFields) != `{"tier":"gold"}` {
		t.Fatalf("custom fields not carried: %s", job.CustomFields)
	}
}

// StartReconciler with no backlog wired must return quietly rather than spawning a
// ticker that panics an hour later.
func TestStartReconciler_NoBacklogIsInert(t *testing.T) {
	w := NewEmbeddingWorker(nil, &fakeEmbedRepo{}, nil, zap.NewNop(), 1)
	w.StartReconciler(context.Background())
}
