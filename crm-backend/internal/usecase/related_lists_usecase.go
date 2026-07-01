package usecase

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// relatedListsUseCase builds a record's reverse related lists. For a record of
// object A, it finds every (childObject, field) in the registry where the field
// is a relation pointing at A, then lists that child object filtered to records
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

func (uc *relatedListsUseCase) ListRelatedLists(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) ([]domain.RelatedList, error) {
	objects, err := uc.registry.ListObjects(ctx, orgID)
	if err != nil {
		return nil, err
	}

	out := make([]domain.RelatedList, 0)
	for _, obj := range objects {
		schema, err := uc.registry.GetSchema(ctx, orgID, obj.Slug)
		if err != nil {
			continue // an object we can't describe simply contributes no related list
		}
		for _, f := range schema.Fields {
			// Only typed relations that point back at this record's object. The
			// pseudo "stage" relation has an empty target_slug, so it's excluded.
			if f.Type != "relation" || f.TargetSlug != slug {
				continue
			}
			// Fetch one past the display cap so we can tell "exactly N" from "N+".
			page, err := uc.records.List(ctx, orgID, obj.Slug, domain.RecordListInput{
				Filters: map[string]string{f.Key: id.String()},
				Limit:   relatedListLimit + 1,
			})
			if err != nil {
				// Most often an OLS denial on the child object — skip it rather
				// than failing the whole record page.
				continue
			}
			recs := page.Records
			hasMore := len(recs) > relatedListLimit
			if hasMore {
				recs = recs[:relatedListLimit]
			}
			out = append(out, domain.RelatedList{
				Object:     obj.Slug,
				Label:      obj.LabelPlural,
				Icon:       obj.Icon,
				FieldKey:   f.Key,
				FieldLabel: f.Label,
				Records:    recs,
				Count:      len(recs),
				HasMore:    hasMore,
			})
		}
	}
	return out, nil
}
