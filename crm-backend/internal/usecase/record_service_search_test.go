package usecase

import (
	"context"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// fakeIndexer captures RecordIndexer calls so the indexing hooks can be asserted
// without a real worker or DB.
type fakeIndexer struct {
	enqueued []indexerCall
	removed  []indexerCall
}

type indexerCall struct {
	slug    string
	id      uuid.UUID
	content string
}

func (f *fakeIndexer) EnqueueRecord(_ uuid.UUID, slug string, id uuid.UUID, content string) {
	f.enqueued = append(f.enqueued, indexerCall{slug: slug, id: id, content: content})
}

func (f *fakeIndexer) RemoveRecord(_ context.Context, _ uuid.UUID, slug string, id uuid.UUID) error {
	f.removed = append(f.removed, indexerCall{slug: slug, id: id})
	return nil
}

// newSearchIndexService wires a RecordService over a custom-object fake with the
// indexer attached, returning both so a test can drive writes and inspect calls.
func newSearchIndexService(def *domain.CustomObjectDef, rec *domain.CustomObjectRecord) (domain.RecordService, *fakeCustomObjUC, *fakeIndexer) {
	custom := &fakeCustomObjUC{def: def, rec: rec}
	svc := newTestService(custom, nil, nil)
	idx := &fakeIndexer{}
	svc.SetSearchIndexer(idx)
	return svc, custom, idx
}

func TestIndex_CreateSearchableCustom_Enqueues(t *testing.T) {
	defID := uuid.New()
	recID := uuid.New()
	def := &domain.CustomObjectDef{ID: defID, Slug: "ticket", Searchable: true,
		Fields: domain.JSON(`[{"key":"subject","label":"Subject","type":"text"},{"key":"priority","label":"Priority","type":"text"}]`)}
	rec := &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, DisplayName: "Acme ticket",
		Data: domain.JSON(`{"subject":"Acme ticket","priority":"high"}`)}

	svc, _, idx := newSearchIndexService(def, rec)

	_, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "ticket", domain.RecordWriteInput{
		Fields: map[string]interface{}{"subject": "Acme ticket", "priority": "high"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if len(idx.enqueued) != 1 {
		t.Fatalf("expected 1 enqueue, got %d", len(idx.enqueued))
	}
	call := idx.enqueued[0]
	if call.slug != "ticket" || call.id != recID {
		t.Fatalf("enqueue identity = %s/%s, want ticket/%s", call.slug, call.id, recID)
	}
	// Content carries the display title and field values for embedding + fulltext.
	if !strings.Contains(call.content, "Acme ticket") || !strings.Contains(call.content, "priority: high") {
		t.Fatalf("content missing expected text: %q", call.content)
	}
}

func TestIndex_CreateNonSearchableCustom_Skips(t *testing.T) {
	defID := uuid.New()
	def := &domain.CustomObjectDef{ID: defID, Slug: "note", Searchable: false}
	rec := &domain.CustomObjectRecord{ID: uuid.New(), ObjectDefID: defID, DisplayName: "n"}

	svc, _, idx := newSearchIndexService(def, rec)

	if _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "note", domain.RecordWriteInput{
		Fields: map[string]interface{}{"body": "x"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(idx.enqueued) != 0 {
		t.Fatalf("non-searchable object must not be indexed, got %d enqueues", len(idx.enqueued))
	}
}

func TestIndex_UpdateSearchableCustom_Enqueues(t *testing.T) {
	defID := uuid.New()
	recID := uuid.New()
	def := &domain.CustomObjectDef{ID: defID, Slug: "ticket", Searchable: true,
		Fields: domain.JSON(`[{"key":"subject","label":"Subject","type":"text"}]`)}
	rec := &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, DisplayName: "Renamed",
		Data: domain.JSON(`{"subject":"Renamed"}`)}

	svc, _, idx := newSearchIndexService(def, rec)

	if _, err := svc.Update(context.Background(), uuid.New(), "ticket", recID, domain.RecordWriteInput{
		Fields: map[string]interface{}{"subject": "Renamed"},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(idx.enqueued) != 1 {
		t.Fatalf("expected 1 enqueue on update, got %d", len(idx.enqueued))
	}
	if !strings.Contains(idx.enqueued[0].content, "Renamed") {
		t.Fatalf("update content missing new value: %q", idx.enqueued[0].content)
	}
}

func TestIndex_DeleteCustom_RemovesIndex(t *testing.T) {
	defID := uuid.New()
	recID := uuid.New()
	def := &domain.CustomObjectDef{ID: defID, Slug: "ticket", Searchable: true}
	rec := &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, DisplayName: "x"}

	svc, _, idx := newSearchIndexService(def, rec)

	if err := svc.Delete(context.Background(), uuid.New(), "ticket", recID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(idx.removed) != 1 {
		t.Fatalf("expected 1 remove, got %d", len(idx.removed))
	}
	if idx.removed[0].slug != "ticket" || idx.removed[0].id != recID {
		t.Fatalf("remove identity = %s/%s, want ticket/%s", idx.removed[0].slug, idx.removed[0].id, recID)
	}
}

func TestBuildRecordContent_DeterministicAndComplete(t *testing.T) {
	rec := &domain.UniformRecord{
		Display: "Acme ticket",
		Fields: map[string]interface{}{
			"subject":  "Acme ticket",
			"priority": "high",
			"empty":    "",
			"count":    float64(3),
		},
	}
	// Keys are sorted, empties dropped, display leads.
	got := buildRecordContent(rec)
	want := "Acme ticket count: 3 priority: high subject: Acme ticket"
	if got != want {
		t.Fatalf("buildRecordContent = %q, want %q", got, want)
	}
}
