package usecase

import (
	"context"
	"sort"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// Search indexing (P6).
//
// RecordService is the single write chokepoint, so it is also where a searchable
// object's records are kept in sync with the generic record_embeddings index:
// enqueue (re)indexing on create/update, remove on delete. Indexing is best-effort
// and asynchronous — the indexer is the embedding worker — so it never blocks or
// fails a write. A nil indexer (unit tests, or before SetSearchIndexer runs at
// startup) makes every hook a no-op.
//
// Scope (P6, honest): only CUSTOM objects are indexed here. Contacts keep their
// native contacts.embedding index (R1 — typed storage is untouched before P7); the
// other system objects (deal/company) join record_embeddings at the P7 cutover.
// The hooks therefore live on the custom-object branch of Create/Update/Delete.

// SetSearchIndexer wires the generic search indexer, called once at startup from
// main.go (mirrors SetEventEmitter).
func (s *recordService) SetSearchIndexer(idx domain.RecordIndexer) {
	s.indexer = idx
}

// indexRecord enqueues (re)indexing of a custom record when its object is marked
// searchable. The content is built from the FULL record (before any FLS masking),
// so search quality is unaffected by who happens to be writing; FLS still applies
// at read/resolution time. The def lookup is skipped entirely when no indexer is
// wired, so non-search builds pay nothing.
func (s *recordService) indexRecord(ctx context.Context, orgID uuid.UUID, slug string, rec *domain.UniformRecord) {
	if s.indexer == nil || rec == nil {
		return
	}
	def, err := s.customObjUC.GetDefBySlug(ctx, orgID, slug)
	if err != nil || def == nil || !def.Searchable {
		return
	}
	s.indexer.EnqueueRecord(orgID, slug, rec.ID, buildRecordContent(rec))
}

// unindexRecord removes a custom record from the generic index on delete. Errors
// are intentionally swallowed: a lingering index row is harmless because search
// resolves every hit through RecordService.Get, which drops a row pointing at a
// now-deleted record. (The indexer's own delete is idempotent.)
func (s *recordService) unindexRecord(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) {
	if s.indexer == nil {
		return
	}
	_ = s.indexer.RemoveRecord(ctx, orgID, slug, id)
}

// buildRecordContent renders a record into the text that gets embedded and
// fulltext-indexed: its display title followed by "key: value" for each non-empty
// field. Field keys are sorted so the content (and thus the embedding) is
// deterministic for a given record, independent of Go's map iteration order.
func buildRecordContent(rec *domain.UniformRecord) string {
	parts := make([]string, 0, len(rec.Fields)+1)
	if rec.Display != "" {
		parts = append(parts, rec.Display)
	}

	keys := make([]string, 0, len(rec.Fields))
	for k := range rec.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v := displayString(rec.Fields[k]); v != "" {
			parts = append(parts, k+": "+v)
		}
	}
	return strings.Join(parts, " ")
}
