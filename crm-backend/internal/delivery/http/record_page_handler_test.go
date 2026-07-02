package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---- fakes for the composite record-page endpoint ------------------------------

type pageFakeRegistry struct {
	domain.ObjectRegistryUseCase
	schema *domain.ObjectDescriptor
}

func (f *pageFakeRegistry) GetSchema(context.Context, uuid.UUID, string) (*domain.ObjectDescriptor, error) {
	if f.schema == nil {
		return nil, domain.NewAppError(http.StatusNotFound, "object not found")
	}
	return f.schema, nil
}

// pageFakeRecords serves Get by slug/id and ListTags; a missing record 404s the
// same way RecordService does.
type pageFakeRecords struct {
	domain.RecordService
	recs map[string]*domain.UniformRecord // "slug/id" → record
	tags []domain.Tag
}

func (f *pageFakeRecords) Get(_ context.Context, _ uuid.UUID, slug string, id uuid.UUID) (*domain.UniformRecord, error) {
	if r, ok := f.recs[slug+"/"+id.String()]; ok {
		return r, nil
	}
	return nil, domain.NewAppError(http.StatusNotFound, "record not found")
}

func (f *pageFakeRecords) ListTags(context.Context, uuid.UUID, string, uuid.UUID) ([]domain.Tag, error) {
	return f.tags, nil
}

type pageFakeRelated struct {
	lists []domain.RelatedList
	err   error
}

func (f *pageFakeRelated) ListRelatedLists(context.Context, uuid.UUID, string, uuid.UUID) ([]domain.RelatedList, error) {
	return f.lists, f.err
}

type pageFakeTags struct {
	domain.TagUseCase
	all []domain.Tag
}

func (f *pageFakeTags) List(context.Context, uuid.UUID) ([]domain.Tag, error) {
	return f.all, nil
}

func servePage(t *testing.T, h *RecordHandler, slug, id string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("org_id", uuid.New())
	c.Params = gin.Params{{Key: "slug", Value: slug}, {Key: "id", Value: id}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	h.GetPage(c)
	return w
}

// The composite endpoint aggregates schema, record, related lists, both tag
// sets, and pre-resolves relation labels and mirror values from the linked
// records — the whole record page in one response.
func TestGetPage_AggregatesEverything(t *testing.T) {
	companyID := uuid.New()
	contactID := uuid.New()

	registry := &pageFakeRegistry{schema: &domain.ObjectDescriptor{
		Slug: "contact",
		Fields: []domain.FieldDescriptor{
			{Key: "name", Label: "Name", Type: "text"},
			{Key: "company", Label: "Company", Type: "relation", TargetSlug: "company"},
			{Key: "company_email", Label: "Company Email", Type: "mirror", ViaField: "company", SourceField: "email"},
		},
	}}
	records := &pageFakeRecords{
		recs: map[string]*domain.UniformRecord{
			"contact/" + contactID.String(): {
				ID: contactID, Object: "contact", Display: "Tony Stark",
				Fields: map[string]interface{}{"name": "Tony Stark", "company": companyID.String()},
			},
			"company/" + companyID.String(): {
				ID: companyID, Object: "company", Display: "Stark Industries",
				Fields: map[string]interface{}{"email": "info@stark.com"},
			},
		},
		tags: []domain.Tag{{Name: "VIP"}},
	}
	related := &pageFakeRelated{lists: []domain.RelatedList{{Object: "deal", Label: "Deals", FieldKey: "contact"}}}
	h := NewRecordHandler(records, related, registry, nil, nil, &pageFakeTags{all: []domain.Tag{{Name: "VIP"}, {Name: "Hot Lead"}}}, nil)

	w := servePage(t, h, "contact", contactID.String())
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data recordPage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad response JSON: %v", err)
	}
	p := resp.Data
	if p.Schema == nil || p.Schema.Slug != "contact" {
		t.Errorf("schema missing or wrong: %+v", p.Schema)
	}
	if p.Record == nil || p.Record.Display != "Tony Stark" {
		t.Errorf("record missing or wrong: %+v", p.Record)
	}
	if len(p.RelatedLists) != 1 || p.RelatedLists[0].Object != "deal" {
		t.Errorf("related lists wrong: %+v", p.RelatedLists)
	}
	if len(p.Tags) != 1 || len(p.AllTags) != 2 {
		t.Errorf("tags wrong: tags=%+v allTags=%+v", p.Tags, p.AllTags)
	}
	if p.RelationLabels["company"] != "Stark Industries" {
		t.Errorf("relation label not resolved: %+v", p.RelationLabels)
	}
	if p.MirrorValues["company_email"] != "info@stark.com" {
		t.Errorf("mirror value not resolved: %+v", p.MirrorValues)
	}
}

// A missing record is fatal (the caller needs the 404); auxiliary panel errors
// are not (the page still renders without them).
func TestGetPage_RecordNotFoundIsFatal(t *testing.T) {
	registry := &pageFakeRegistry{schema: &domain.ObjectDescriptor{Slug: "contact"}}
	records := &pageFakeRecords{recs: map[string]*domain.UniformRecord{}}
	h := NewRecordHandler(records, &pageFakeRelated{}, registry, nil, nil, nil, nil)

	w := servePage(t, h, "contact", uuid.New().String())
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetPage_AuxiliaryFailureDegradesToEmpty(t *testing.T) {
	contactID := uuid.New()
	registry := &pageFakeRegistry{schema: &domain.ObjectDescriptor{Slug: "contact"}}
	records := &pageFakeRecords{recs: map[string]*domain.UniformRecord{
		"contact/" + contactID.String(): {ID: contactID, Object: "contact", Display: "Tony Stark", Fields: map[string]interface{}{}},
	}}
	related := &pageFakeRelated{err: domain.NewAppError(http.StatusForbidden, "denied")}
	h := NewRecordHandler(records, related, registry, nil, nil, nil, nil) // nil tag usecase too

	w := servePage(t, h, "contact", contactID.String())
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 despite auxiliary failures, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data recordPage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad response JSON: %v", err)
	}
	if resp.Data.RelatedLists == nil || len(resp.Data.RelatedLists) != 0 {
		t.Errorf("expected empty related lists, got %+v", resp.Data.RelatedLists)
	}
	if resp.Data.AllTags == nil || len(resp.Data.AllTags) != 0 {
		t.Errorf("expected empty all_tags, got %+v", resp.Data.AllTags)
	}
}
