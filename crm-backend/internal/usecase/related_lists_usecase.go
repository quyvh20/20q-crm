package usecase

import (
	"context"
	"sync"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// relatedListsUseCase builds a record's reverse related lists. For a record of
// object A, it asks the registry for every (childObject, field) whose relation
// points at A — one query — then lists each child object filtered to records
// whose relation value is this record's id. Each group becomes a RelatedList.
//
// It composes the registry and record services so OLS/FLS, storage dispatch, and
// display resolution all come for free from RecordService.List — a child object
// the caller can't read simply errors on List and is skipped, yielding graceful
// partial results rather than a failed page.
type relatedListsUseCase struct {
	registry domain.ObjectRegistryUseCase
	records  domain.RecordService
}

// NewRelatedListsUseCase wires the registry + record services.
func NewRelatedListsUseCase(registry domain.ObjectRegistryUseCase, records domain.RecordService) domain.RelatedListsUseCase {
	return &relatedListsUseCase{registry: registry, records: records}
}

// relatedListLimit caps each related list. A record page shows a preview, not an
// unbounded scroll; deep lists get their own filtered list view later.
const relatedListLimit = 50

// relatedListConcurrency bounds the parallel child-list queries so one record
// page can't monopolize the DB pool however many relations point at an object.
const relatedListConcurrency = 5

func (uc *relatedListsUseCase) ListRelatedLists(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) ([]domain.RelatedList, error) {
	rels, err := uc.registry.ListIncomingRelations(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}

	// The child lists are independent reads, so they run concurrently (bounded).
	// Each result lands at its input index to keep the registry's stable order.
	results := make([]*domain.RelatedList, len(rels))
	sem := make(chan struct{}, relatedListConcurrency)
	var wg sync.WaitGroup
	for i, rel := range rels {
		wg.Add(1)
		go func(i int, rel domain.IncomingRelation) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Fetch one past the display cap so we can tell "exactly N" from "N+".
			page, err := uc.records.List(ctx, orgID, rel.ChildSlug, domain.RecordListInput{
				Filters: map[string]string{rel.FieldKey: id.String()},
				Limit:   relatedListLimit + 1,
			})
			if err != nil {
				// Most often an OLS denial on the child object — skip it rather
				// than failing the whole record page.
				return
			}
			recs := page.Records
			hasMore := len(recs) > relatedListLimit
			if hasMore {
				recs = recs[:relatedListLimit]
			}
			results[i] = &domain.RelatedList{
				Object:     rel.ChildSlug,
				Label:      rel.ChildLabelPlural,
				Icon:       rel.ChildIcon,
				FieldKey:   rel.FieldKey,
				FieldLabel: rel.FieldLabel,
				Records:    recs,
				Count:      len(recs),
				HasMore:    hasMore,
			}
		}(i, rel)
	}
	wg.Wait()

	out := make([]domain.RelatedList, 0, len(rels))
	for _, r := range results {
		if r != nil {
			out = append(out, *r)
		}
	}
	return out, nil
}
