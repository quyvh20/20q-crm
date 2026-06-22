package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ============================================================
// Generic search index (P6)
// ============================================================
//
// record_embeddings is the single additive index that makes any object — custom
// objects first — semantically + fulltext searchable, without a per-object
// column. It is keyed by (org_id, object_slug, record_id), the same cross-stack
// identifier object_links / object_permissions / field_permissions use, so it
// stays consistent with "all objects equal above storage". Typed objects keep
// their own native index (contacts.embedding) until the P7 cutover.

// RecordEmbedding is one row of record_embeddings: the vector that powers
// semantic search plus the text that was embedded (which also powers fulltext).
// Embedding is empty until the async embed succeeds — fulltext still works in the
// meantime, because content is stored regardless.
type RecordEmbedding struct {
	OrgID      uuid.UUID
	ObjectSlug string
	RecordID   uuid.UUID
	Embedding  []float32
	Content    string
	UpdatedAt  time.Time
}

// RecordEmbeddingHit is a ranked match from the generic index. Distance is the
// cosine distance for semantic matches (0 = identical, lower = closer); it is 0
// for fulltext-only matches, which carry no vector distance.
type RecordEmbeddingHit struct {
	ObjectSlug string
	RecordID   uuid.UUID
	Distance   float32
}

// RecordEmbeddingRepository persists and queries the generic search index. All
// methods are org-scoped; the slugs filter restricts results to the objects the
// caller is allowed to search (searchable + readable), so stale rows for an
// object later un-marked searchable are simply ignored.
type RecordEmbeddingRepository interface {
	// Upsert writes (or replaces) a record's index row. A nil/empty Embedding
	// stores NULL for the vector while keeping content for fulltext.
	Upsert(ctx context.Context, e RecordEmbedding) error
	// Delete removes a record's index row (on record delete). Removing a missing
	// row is a no-op.
	Delete(ctx context.Context, orgID uuid.UUID, slug string, recordID uuid.UUID) error
	// SearchSemantic returns the nearest rows by cosine distance below threshold.
	SearchSemantic(ctx context.Context, orgID uuid.UUID, slugs []string, vec []float32, threshold float32, limit int) ([]RecordEmbeddingHit, error)
	// SearchFulltext returns rows whose content matches the query (tsquery).
	SearchFulltext(ctx context.Context, orgID uuid.UUID, slugs []string, query string, limit int) ([]RecordEmbeddingHit, error)
}

// QueryEmbedder turns a search query into a vector. It is the minimal slice of
// the embedding service the search usecase needs; depending on this interface
// (rather than the concrete *ai.EmbeddingService) keeps the usecase unit-testable
// with a canned embedder. *ai.EmbeddingService satisfies it.
type QueryEmbedder interface {
	EmbedText(ctx context.Context, text string) ([]float32, error)
}

// RecordIndexer maintains the generic search index for searchable objects. It is
// implemented by the embedding worker and injected into RecordService, which
// calls it from the write path (enqueue on create/update of a searchable record,
// remove on delete). Indexing is best-effort and asynchronous — a nil indexer
// (unit tests) makes every call a no-op.
type RecordIndexer interface {
	// EnqueueRecord schedules (re)indexing of a record's content. Non-blocking.
	EnqueueRecord(orgID uuid.UUID, slug string, recordID uuid.UUID, content string)
	// RemoveRecord deletes a record's index entry synchronously.
	RemoveRecord(ctx context.Context, orgID uuid.UUID, slug string, recordID uuid.UUID) error
}

// ============================================================
// Global search (P6) — the cross-object query surface
// ============================================================

// SearchHit is one matched record, resolved through RecordService.Get so it is
// already OLS/FLS-enforced and carries the uniform shape every object shares.
type SearchHit struct {
	Record UniformRecord `json:"record"`
	// Score is a relevance hint in [0,1] (higher = closer) for semantic matches;
	// 0 for fulltext-only matches. The UI may surface or ignore it.
	Score float64 `json:"score,omitempty"`
}

// SearchGroup bundles the hits for a single object type, so results are grouped
// by object (a reversible presentation choice, plan §16). The UI can render one
// section per object.
type SearchGroup struct {
	Object      string      `json:"object"`
	Label       string      `json:"label"`
	LabelPlural string      `json:"label_plural"`
	Icon        string      `json:"icon"`
	Hits        []SearchHit `json:"hits"`
}

// SearchResult is the global-search response: the echoed query plus one group per
// object that produced matches.
type SearchResult struct {
	Query  string        `json:"query"`
	Groups []SearchGroup `json:"groups"`
}

// SearchUseCase runs a global, cross-object semantic + fulltext search. It spans
// every searchable custom object (via record_embeddings) plus contacts (via their
// native index), resolves each hit through RecordService so the same OLS/FLS guard
// rails apply, and groups results by object.
type SearchUseCase interface {
	Search(ctx context.Context, orgID uuid.UUID, query string, limit int) (*SearchResult, error)
}
