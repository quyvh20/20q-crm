package usecase

import (
	"context"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// searchUseCase implements global, cross-object search (P6).
//
// It is the read counterpart to the write-side indexing in RecordService: a single
// query that spans every searchable object and returns OLS/FLS-safe results,
// grouped by object. Two sources feed it, unified at the end:
//
//   - Custom objects → the generic record_embeddings index (semantic + fulltext),
//     populated by the embedding worker for objects an admin marked searchable.
//   - Contacts → their native always-on semantic index (contacts.embedding). The
//     generic `searchable` flag governs the *generic* index only; contacts have
//     their own index and so are always searchable (subject to OLS).
//
// Every raw hit is resolved through RecordService.Get, so the one chokepoint that
// enforces Object- and Field-Level Security also guards search: a record the caller
// can't read is dropped, and hidden fields are stripped from what's returned. That
// is "all objects equal" extended to search — the same guard rails, one shape.
//
// System objects other than contact (deal/company) are not yet in record_embeddings
// (R1 keeps typed storage untouched until P7); they join at the P7 cutover.
type searchUseCase struct {
	embedRepo  domain.RecordEmbeddingRepository
	embedSvc   domain.QueryEmbedder
	recordSvc  domain.RecordService
	registryUC domain.ObjectRegistryUseCase
	contactUC  domain.ContactUseCase
}

func NewSearchUseCase(
	embedRepo domain.RecordEmbeddingRepository,
	embedSvc domain.QueryEmbedder,
	recordSvc domain.RecordService,
	registryUC domain.ObjectRegistryUseCase,
	contactUC domain.ContactUseCase,
) domain.SearchUseCase {
	return &searchUseCase{
		embedRepo:  embedRepo,
		embedSvc:   embedSvc,
		recordSvc:  recordSvc,
		registryUC: registryUC,
		contactUC:  contactUC,
	}
}

// semanticDistanceThreshold mirrors the contact semantic search threshold so the
// two indexes rank comparably (0 = identical, lower = closer).
const semanticDistanceThreshold = 0.5

func (uc *searchUseCase) Search(ctx context.Context, orgID uuid.UUID, query string, limit int) (*domain.SearchResult, error) {
	query = strings.TrimSpace(query)
	result := &domain.SearchResult{Query: query, Groups: []domain.SearchGroup{}}
	if query == "" {
		return result, nil
	}
	if limit <= 0 || limit > 25 {
		limit = 10
	}

	// One registry read gives labels/icons and which custom objects are searchable.
	summaries, err := uc.registryUC.ListObjects(ctx, orgID)
	if err != nil {
		return nil, err
	}
	meta := map[string]domain.ObjectSummary{}
	var searchableCustomSlugs []string
	for _, s := range summaries {
		meta[s.Slug] = s
		if !s.IsSystem && s.Searchable {
			searchableCustomSlugs = append(searchableCustomSlugs, s.Slug)
		}
	}

	// Embed the query once (best-effort). Semantic search needs it; fulltext does
	// not, so an embedding outage degrades to fulltext for custom objects rather
	// than failing the whole search.
	var queryVec []float32
	if uc.embedSvc != nil {
		if vec, verr := uc.embedSvc.EmbedText(ctx, query); verr == nil {
			queryVec = vec
		}
	}

	// Contacts first (the largest object, always searchable via its native index).
	if _, ok := meta["contact"]; ok && len(queryVec) > 0 {
		if g := uc.contactGroup(ctx, orgID, query, limit, meta["contact"]); g != nil {
			result.Groups = append(result.Groups, *g)
		}
	}

	// Custom objects via the generic index, grouped by slug in registry order.
	if len(searchableCustomSlugs) > 0 {
		byslug := uc.customHits(ctx, orgID, searchableCustomSlugs, queryVec, query, limit)
		for _, s := range summaries {
			if s.IsSystem {
				continue
			}
			scored := byslug[s.Slug]
			if len(scored) == 0 {
				continue
			}
			if g := uc.resolveGroup(ctx, orgID, s, scored, limit); g != nil {
				result.Groups = append(result.Groups, *g)
			}
		}
	}

	return result, nil
}

// scoredID is an internal pre-resolution hit: which record, and how relevant.
type scoredID struct {
	id    uuid.UUID
	score float64 // 1-distance for semantic; 0 for fulltext-only
}

// customHits queries the generic index (semantic then fulltext), merges and
// de-duplicates by record, and buckets the results per object slug, preserving
// semantic-first ordering. A generous pool is fetched so per-object grouping has
// room; each group is capped to `limit` at resolution.
func (uc *searchUseCase) customHits(ctx context.Context, orgID uuid.UUID, slugs []string, queryVec []float32, query string, limit int) map[string][]scoredID {
	pool := limit * 4
	byslug := map[string][]scoredID{}
	seen := map[string]bool{} // slug+id

	add := func(slug string, id uuid.UUID, score float64) {
		key := slug + ":" + id.String()
		if seen[key] {
			return
		}
		seen[key] = true
		byslug[slug] = append(byslug[slug], scoredID{id: id, score: score})
	}

	if len(queryVec) > 0 {
		if hits, err := uc.embedRepo.SearchSemantic(ctx, orgID, slugs, queryVec, semanticDistanceThreshold, pool); err == nil {
			for _, h := range hits {
				add(h.ObjectSlug, h.RecordID, similarity(h.Distance))
			}
		}
	}
	if hits, err := uc.embedRepo.SearchFulltext(ctx, orgID, slugs, query, pool); err == nil {
		for _, h := range hits {
			add(h.ObjectSlug, h.RecordID, 0) // fulltext-only: no semantic score
		}
	}
	return byslug
}

// resolveGroup turns pre-resolution hits for one object into a SearchGroup by
// fetching each record through RecordService (OLS/FLS enforced). Records the
// caller can't read, or that were deleted after indexing, are skipped; a group
// with no surviving hits returns nil.
func (uc *searchUseCase) resolveGroup(ctx context.Context, orgID uuid.UUID, s domain.ObjectSummary, scored []scoredID, limit int) *domain.SearchGroup {
	hits := make([]domain.SearchHit, 0, limit)
	for _, sc := range scored {
		if len(hits) >= limit {
			break
		}
		rec, err := uc.recordSvc.Get(ctx, orgID, s.Slug, sc.id)
		if err != nil || rec == nil {
			continue // not readable, or stale index row pointing at a deleted record
		}
		hits = append(hits, domain.SearchHit{Record: *rec, Score: sc.score})
	}
	if len(hits) == 0 {
		return nil
	}
	return newGroup(s, hits)
}

// contactGroup runs contact semantic search and resolves each hit through
// RecordService so contacts return in the same uniform, OLS/FLS-safe shape as
// every other object.
func (uc *searchUseCase) contactGroup(ctx context.Context, orgID uuid.UUID, query string, limit int, s domain.ObjectSummary) *domain.SearchGroup {
	contacts, err := uc.contactUC.SemanticSearch(ctx, orgID, query, limit)
	if err != nil || len(contacts) == 0 {
		return nil
	}
	hits := make([]domain.SearchHit, 0, len(contacts))
	for _, c := range contacts {
		rec, gerr := uc.recordSvc.Get(ctx, orgID, "contact", c.ID)
		if gerr != nil || rec == nil {
			continue
		}
		hits = append(hits, domain.SearchHit{Record: *rec})
	}
	if len(hits) == 0 {
		return nil
	}
	return newGroup(s, hits)
}

func newGroup(s domain.ObjectSummary, hits []domain.SearchHit) *domain.SearchGroup {
	return &domain.SearchGroup{
		Object:      s.Slug,
		Label:       s.Label,
		LabelPlural: s.LabelPlural,
		Icon:        s.Icon,
		Hits:        hits,
	}
}

// similarity maps cosine distance (0 = identical) to a [0,1] relevance score
// (1 = identical), clamped so callers get a stable, intuitive number.
func similarity(distance float32) float64 {
	s := 1.0 - float64(distance)
	if s < 0 {
		return 0
	}
	if s > 1 {
		return 1
	}
	return s
}
