package worker

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// fakeEmbedRepo records the calls the worker makes against the generic index.
type fakeEmbedRepo struct {
	upserts []domain.RecordEmbedding
	deletes []deleteCall
}

type deleteCall struct {
	orgID uuid.UUID
	slug  string
	id    uuid.UUID
}

func (f *fakeEmbedRepo) Upsert(_ context.Context, e domain.RecordEmbedding) error {
	f.upserts = append(f.upserts, e)
	return nil
}
func (f *fakeEmbedRepo) Delete(_ context.Context, orgID uuid.UUID, slug string, id uuid.UUID) error {
	f.deletes = append(f.deletes, deleteCall{orgID, slug, id})
	return nil
}
func (f *fakeEmbedRepo) SearchSemantic(context.Context, uuid.UUID, []string, []float32, float32, int) ([]domain.RecordEmbeddingHit, error) {
	return nil, nil
}
func (f *fakeEmbedRepo) SearchFulltext(context.Context, uuid.UUID, []string, string, int) ([]domain.RecordEmbeddingHit, error) {
	return nil, nil
}

func newTestWorker(repo domain.RecordEmbeddingRepository) *EmbeddingWorker {
	// embedSvc nil: exercises the content-only path (no network), which is the
	// resilient fallback when embedding is unavailable.
	return NewEmbeddingWorker(nil, repo, nil, zap.NewNop(), 10)
}

// With no embed service, processRecord stores content (so fulltext works) and no
// vector — embedding failures must never drop a record from the index.
func TestProcessRecord_ContentOnlyWhenNoEmbedService(t *testing.T) {
	repo := &fakeEmbedRepo{}
	w := newTestWorker(repo)
	orgID, recID := uuid.New(), uuid.New()

	w.processRecord(context.Background(), recordEmbedJob{OrgID: orgID, Slug: "ticket", RecordID: recID, Content: "Acme ticket high"})

	if len(repo.upserts) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(repo.upserts))
	}
	got := repo.upserts[0]
	if got.OrgID != orgID || got.ObjectSlug != "ticket" || got.RecordID != recID {
		t.Fatalf("upsert identity wrong: %+v", got)
	}
	if got.Content != "Acme ticket high" {
		t.Fatalf("content = %q", got.Content)
	}
	if len(got.Embedding) != 0 {
		t.Fatalf("expected no embedding without an embed service, got %d dims", len(got.Embedding))
	}
}

// Empty content removes any stale row instead of indexing emptiness.
func TestProcessRecord_EmptyContentDeletes(t *testing.T) {
	repo := &fakeEmbedRepo{}
	w := newTestWorker(repo)
	orgID, recID := uuid.New(), uuid.New()

	w.processRecord(context.Background(), recordEmbedJob{OrgID: orgID, Slug: "ticket", RecordID: recID, Content: "   "})

	if len(repo.upserts) != 0 {
		t.Fatalf("empty content must not upsert, got %d", len(repo.upserts))
	}
	if len(repo.deletes) != 1 || repo.deletes[0].id != recID {
		t.Fatalf("expected a delete for the empty record, got %+v", repo.deletes)
	}
}

// RemoveRecord (RecordIndexer) delegates straight to the repo delete.
func TestRemoveRecord_DelegatesToRepo(t *testing.T) {
	repo := &fakeEmbedRepo{}
	w := newTestWorker(repo)
	orgID, recID := uuid.New(), uuid.New()

	if err := w.RemoveRecord(context.Background(), orgID, "ticket", recID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(repo.deletes) != 1 || repo.deletes[0].slug != "ticket" || repo.deletes[0].id != recID {
		t.Fatalf("expected one delegated delete, got %+v", repo.deletes)
	}
}

// EnqueueRecord buffers a job that a worker goroutine later drains via processRecord.
func TestEnqueueRecord_BuffersAndProcesses(t *testing.T) {
	repo := &fakeEmbedRepo{}
	w := newTestWorker(repo)
	orgID, recID := uuid.New(), uuid.New()

	w.EnqueueRecord(orgID, "ticket", recID, "hello world")

	// Drain the one job synchronously rather than racing the goroutine pool.
	select {
	case job := <-w.recordQueue:
		w.processRecord(context.Background(), job)
	default:
		t.Fatal("expected a queued record job")
	}
	if len(repo.upserts) != 1 || repo.upserts[0].Content != "hello world" {
		t.Fatalf("queued job not processed as expected: %+v", repo.upserts)
	}
}
