package usecase

import (
	"context"
	"errors"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// fakeRelRegistryUC implements just the two ObjectRegistryUseCase methods the
// related-lists usecase calls, keyed by slug (uniquely named to avoid colliding
// with the single-schema fakeRegistryUC in permission_usecase_test.go).
type fakeRelRegistryUC struct {
	domain.ObjectRegistryUseCase
	objects []domain.ObjectSummary
	schemas map[string]*domain.ObjectDescriptor
}

func (f *fakeRelRegistryUC) ListObjects(_ context.Context, _ uuid.UUID) ([]domain.ObjectSummary, error) {
	return f.objects, nil
}
func (f *fakeRelRegistryUC) GetSchema(_ context.Context, _ uuid.UUID, slug string) (*domain.ObjectDescriptor, error) {
	if s, ok := f.schemas[slug]; ok {
		return s, nil
	}
	return nil, errors.New("no schema")
}

// fakeRelRecordSvc implements just List; it records the filtered queries and can
// fail for a given slug to simulate an OLS denial on a child object.
type fakeRelRecordSvc struct {
	domain.RecordService
	bySlug   map[string][]domain.UniformRecord
	failSlug string
	calls    []relListCall
}

type relListCall struct {
	slug    string
	filters map[string]string
}

func (f *fakeRelRecordSvc) List(_ context.Context, _ uuid.UUID, slug string, in domain.RecordListInput) (*domain.RecordList, error) {
	f.calls = append(f.calls, relListCall{slug: slug, filters: in.Filters})
	if slug == f.failSlug {
		return nil, errors.New("forbidden")
	}
	return &domain.RecordList{Records: f.bySlug[slug]}, nil
}

func relField(key, label, target string) domain.FieldDescriptor {
	return domain.FieldDescriptor{Key: key, Label: label, Type: "relation", TargetSlug: target}
}

// On a Contact, the deal object's "contact" relation field should yield a "Deals"
// related list filtered to that contact's id; unrelated relations and the pseudo
// stage relation (empty target) are excluded.
func TestListRelatedLists_DerivesChildrenFromIncomingRelations(t *testing.T) {
	contactID := uuid.New()
	deals := []domain.UniformRecord{
		{ID: uuid.New(), Object: "deal", Display: "Big deal"},
		{ID: uuid.New(), Object: "deal", Display: "Small deal"},
	}
	reg := &fakeRelRegistryUC{
		objects: []domain.ObjectSummary{
			{Slug: "contact", LabelPlural: "Contacts", Icon: "👤"},
			{Slug: "deal", LabelPlural: "Deals", Icon: "💰"},
		},
		schemas: map[string]*domain.ObjectDescriptor{
			"contact": {Slug: "contact", Fields: []domain.FieldDescriptor{
				relField("company", "Company", "company"),
			}},
			"deal": {Slug: "deal", Fields: []domain.FieldDescriptor{
				relField("stage", "Stage", ""),            // pseudo relation, no target → excluded
				relField("contact", "Contact", "contact"), // points back at contact → included
				relField("company", "Company", "company"),
			}},
		},
	}
	rec := &fakeRelRecordSvc{bySlug: map[string][]domain.UniformRecord{"deal": deals}}
	uc := NewRelatedListsUseCase(reg, rec)

	lists, err := uc.ListRelatedLists(context.Background(), uuid.New(), "contact", contactID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lists) != 1 {
		t.Fatalf("expected exactly one related list (deal.contact), got %d: %+v", len(lists), lists)
	}
	got := lists[0]
	if got.Object != "deal" || got.FieldKey != "contact" || got.Label != "Deals" {
		t.Errorf("unexpected related list: %+v", got)
	}
	if got.Count != 2 || len(got.Records) != 2 {
		t.Errorf("expected 2 deals, got count=%d records=%d", got.Count, len(got.Records))
	}
	// The query must be filtered by the relation field to this contact's id.
	found := false
	for _, c := range rec.calls {
		if c.slug == "deal" && c.filters["contact"] == contactID.String() {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a filtered deal list (contact=%s); calls: %+v", contactID, rec.calls)
	}
}

// A child object the caller can't read (List errors) is skipped, not fatal.
func TestListRelatedLists_SkipsUnreadableChild(t *testing.T) {
	reg := &fakeRelRegistryUC{
		objects: []domain.ObjectSummary{{Slug: "deal", LabelPlural: "Deals"}},
		schemas: map[string]*domain.ObjectDescriptor{
			"deal": {Slug: "deal", Fields: []domain.FieldDescriptor{relField("contact", "Contact", "contact")}},
		},
	}
	rec := &fakeRelRecordSvc{failSlug: "deal"}
	uc := NewRelatedListsUseCase(reg, rec)

	lists, err := uc.ListRelatedLists(context.Background(), uuid.New(), "contact", uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lists) != 0 {
		t.Errorf("expected unreadable child to be skipped, got %+v", lists)
	}
}
