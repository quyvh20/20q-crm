package usecase

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// Fakes for the link/tag surface
// ============================================================

type fakeLinkRepo struct {
	created  []*domain.ObjectLink
	edge     *domain.ObjectLink // returned by FindEdge (nil = no existing edge)
	listFrom []domain.ObjectLink
	getByID  *domain.ObjectLink

	softDeleted []uuid.UUID
	cascadeSlug string
	cascadeID   uuid.UUID
	cascadeHits int

	contactAdds    [][2]uuid.UUID
	contactRemoves [][2]uuid.UUID
	contactTagIDs  []uuid.UUID
}

func (f *fakeLinkRepo) Create(_ context.Context, l *domain.ObjectLink) error {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	f.created = append(f.created, l)
	return nil
}
func (f *fakeLinkRepo) GetByID(_ context.Context, _, _ uuid.UUID) (*domain.ObjectLink, error) {
	return f.getByID, nil
}
func (f *fakeLinkRepo) SoftDelete(_ context.Context, _, id uuid.UUID) (bool, error) {
	f.softDeleted = append(f.softDeleted, id)
	return true, nil
}
func (f *fakeLinkRepo) FindEdge(_ context.Context, _ uuid.UUID, _ string, _ uuid.UUID, _, _ string, _ uuid.UUID) (*domain.ObjectLink, error) {
	return f.edge, nil
}
func (f *fakeLinkRepo) ListFrom(_ context.Context, _ uuid.UUID, _ string, _ uuid.UUID) ([]domain.ObjectLink, error) {
	return f.listFrom, nil
}
func (f *fakeLinkRepo) CascadeSoftDelete(_ context.Context, _ uuid.UUID, slug string, id uuid.UUID) error {
	f.cascadeSlug = slug
	f.cascadeID = id
	f.cascadeHits++
	return nil
}
func (f *fakeLinkRepo) AddContactTag(_ context.Context, contactID, tagID uuid.UUID) error {
	f.contactAdds = append(f.contactAdds, [2]uuid.UUID{contactID, tagID})
	return nil
}
func (f *fakeLinkRepo) RemoveContactTag(_ context.Context, contactID, tagID uuid.UUID) (bool, error) {
	f.contactRemoves = append(f.contactRemoves, [2]uuid.UUID{contactID, tagID})
	return true, nil
}
func (f *fakeLinkRepo) ListContactTagIDs(_ context.Context, _ uuid.UUID) ([]uuid.UUID, error) {
	return f.contactTagIDs, nil
}

type fakeTagRepo struct {
	domain.TagRepository
	byID *domain.Tag
	list []domain.Tag
}

func (f *fakeTagRepo) GetByID(_ context.Context, _, _ uuid.UUID) (*domain.Tag, error) {
	return f.byID, nil
}
func (f *fakeTagRepo) List(_ context.Context, _ uuid.UUID) ([]domain.Tag, error) {
	return f.list, nil
}

type fakeContactUC struct {
	domain.ContactUseCase
	ret *domain.Contact
}

func (f *fakeContactUC) GetByID(_ context.Context, _, _ uuid.UUID) (*domain.Contact, error) {
	return f.ret, nil
}

type fakeCompanyUC struct {
	domain.CompanyUseCase
	ret *domain.Company
}

func (f *fakeCompanyUC) GetByID(_ context.Context, _, _ uuid.UUID) (*domain.Company, error) {
	return f.ret, nil
}

// Delete completes the DealUseCase surface the cascade-on-delete test exercises
// (fakeDealUC lives in record_service_test.go; methods can be added here as they
// share the package).
func (f *fakeDealUC) Delete(_ context.Context, _, _ uuid.UUID) error { return nil }

func newLinkTestService(custom *fakeCustomObjUC, deal *fakeDealUC, contact *fakeContactUC, company *fakeCompanyUC, link *fakeLinkRepo, tag *fakeTagRepo) domain.RecordService {
	if custom == nil {
		custom = &fakeCustomObjUC{}
	}
	if deal == nil {
		deal = &fakeDealUC{}
	}
	if contact == nil {
		contact = &fakeContactUC{}
	}
	if company == nil {
		company = &fakeCompanyUC{}
	}
	if tag == nil {
		tag = &fakeTagRepo{}
	}
	return NewRecordService(custom, &recordingSettingsUC{}, contact, company, deal, link, tag)
}

// customFakeFor builds a custom-object fake whose def/record ids match, so the
// service's slug-ownership check (rec.ObjectDefID == def.ID) passes for Get.
func customFakeFor(slug, display string) *fakeCustomObjUC {
	defID := uuid.New()
	return &fakeCustomObjUC{
		def: &domain.CustomObjectDef{ID: defID, Slug: slug},
		rec: &domain.CustomObjectRecord{ID: uuid.New(), ObjectDefID: defID, DisplayName: display},
	}
}

// ============================================================
// Relationships
// ============================================================

func TestAddLink_CustomToCompany(t *testing.T) {
	companyID := uuid.New()
	custom := customFakeFor("project", "Apollo")
	company := &fakeCompanyUC{ret: &domain.Company{ID: companyID, Name: "Acme"}}
	link := &fakeLinkRepo{}
	svc := newLinkTestService(custom, nil, nil, company, link, nil)

	view, err := svc.AddLink(context.Background(), uuid.New(), uuid.New(), "project", uuid.New(),
		domain.LinkInput{RelationKey: "account", ToSlug: "company", ToID: companyID})
	if err != nil {
		t.Fatalf("AddLink: %v", err)
	}
	if len(link.created) != 1 {
		t.Fatalf("expected one edge created, got %d", len(link.created))
	}
	e := link.created[0]
	if e.FromSlug != "project" || e.ToSlug != "company" || e.ToID != companyID || e.RelationKey != "account" {
		t.Errorf("edge wrong: %+v", e)
	}
	if view.ToDisplay != "Acme" {
		t.Errorf("target display = %q, want Acme", view.ToDisplay)
	}
}

func TestAddLink_CustomToCustom(t *testing.T) {
	// Both endpoints are custom objects; the shared custom fake resolves either
	// slug. The point is the edge records the correct cross-object slugs.
	custom := customFakeFor("project", "Apollo")
	link := &fakeLinkRepo{}
	svc := newLinkTestService(custom, nil, nil, nil, link, nil)

	_, err := svc.AddLink(context.Background(), uuid.New(), uuid.New(), "project", uuid.New(),
		domain.LinkInput{RelationKey: "blocks", ToSlug: "task", ToID: uuid.New()})
	if err != nil {
		t.Fatalf("AddLink: %v", err)
	}
	if len(link.created) != 1 || link.created[0].ToSlug != "task" || link.created[0].RelationKey != "blocks" {
		t.Fatalf("custom↔custom edge wrong: %+v", link.created)
	}
}

func TestAddLink_RejectsTagEdges(t *testing.T) {
	svc := newLinkTestService(nil, nil, nil, nil, &fakeLinkRepo{}, nil)

	// to_slug='tag' is reserved for the tag API.
	if _, err := svc.AddLink(context.Background(), uuid.New(), uuid.New(), "project", uuid.New(),
		domain.LinkInput{RelationKey: "whatever", ToSlug: "tag", ToID: uuid.New()}); !isBadRequest(err) {
		t.Errorf("expected 400 for to_slug=tag, got %v", err)
	}
	// relation_key='tags' is reserved too.
	if _, err := svc.AddLink(context.Background(), uuid.New(), uuid.New(), "project", uuid.New(),
		domain.LinkInput{RelationKey: "tags", ToSlug: "company", ToID: uuid.New()}); !isBadRequest(err) {
		t.Errorf("expected 400 for relation_key=tags, got %v", err)
	}
}

func TestAddLink_RejectsSelfLink(t *testing.T) {
	id := uuid.New()
	svc := newLinkTestService(nil, nil, nil, nil, &fakeLinkRepo{}, nil)
	if _, err := svc.AddLink(context.Background(), uuid.New(), uuid.New(), "project", id,
		domain.LinkInput{RelationKey: "rel", ToSlug: "project", ToID: id}); !isBadRequest(err) {
		t.Errorf("expected 400 for self-link, got %v", err)
	}
}

func TestAddLink_Idempotent(t *testing.T) {
	companyID := uuid.New()
	existing := &domain.ObjectLink{ID: uuid.New(), RelationKey: "account", ToSlug: "company", ToID: companyID}
	custom := customFakeFor("project", "Apollo")
	company := &fakeCompanyUC{ret: &domain.Company{ID: companyID, Name: "Acme"}}
	link := &fakeLinkRepo{edge: existing}
	svc := newLinkTestService(custom, nil, nil, company, link, nil)

	view, err := svc.AddLink(context.Background(), uuid.New(), uuid.New(), "project", uuid.New(),
		domain.LinkInput{RelationKey: "account", ToSlug: "company", ToID: companyID})
	if err != nil {
		t.Fatalf("AddLink: %v", err)
	}
	if len(link.created) != 0 {
		t.Errorf("idempotent add should not create a duplicate edge, created %d", len(link.created))
	}
	if view.ID != existing.ID {
		t.Errorf("expected the existing edge id back, got %v", view.ID)
	}
}

func TestListLinks_ExcludesTags_ResolvesDisplay(t *testing.T) {
	companyID := uuid.New()
	custom := customFakeFor("project", "Apollo")
	company := &fakeCompanyUC{ret: &domain.Company{ID: companyID, Name: "Acme"}}
	link := &fakeLinkRepo{listFrom: []domain.ObjectLink{
		{ID: uuid.New(), RelationKey: "account", ToSlug: "company", ToID: companyID},
		{ID: uuid.New(), RelationKey: "tags", ToSlug: "tag", ToID: uuid.New()},
	}}
	svc := newLinkTestService(custom, nil, nil, company, link, nil)

	views, err := svc.ListLinks(context.Background(), uuid.New(), "project", uuid.New())
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("tag edge should be excluded; got %d views", len(views))
	}
	if views[0].ToSlug != "company" || views[0].ToDisplay != "Acme" {
		t.Errorf("resolved link wrong: %+v", views[0])
	}
}

func TestRemoveLink_NotFound(t *testing.T) {
	svc := newLinkTestService(nil, nil, nil, nil, &fakeLinkRepo{getByID: nil}, nil)
	if err := svc.RemoveLink(context.Background(), uuid.New(), uuid.New()); !isNotFound(err) {
		t.Errorf("expected 404, got %v", err)
	}
}

func TestRemoveLink_SoftDeletes(t *testing.T) {
	linkID := uuid.New()
	link := &fakeLinkRepo{getByID: &domain.ObjectLink{ID: linkID}}
	svc := newLinkTestService(nil, nil, nil, nil, link, nil)
	if err := svc.RemoveLink(context.Background(), uuid.New(), linkID); err != nil {
		t.Fatalf("RemoveLink: %v", err)
	}
	if len(link.softDeleted) != 1 || link.softDeleted[0] != linkID {
		t.Errorf("expected soft-delete of %v, got %v", linkID, link.softDeleted)
	}
}

// ============================================================
// Tags (uniform across objects)
// ============================================================

func TestAddTag_Deal_WritesObjectLink(t *testing.T) {
	tagID := uuid.New()
	dealID := uuid.New()
	deal := &fakeDealUC{ret: &domain.Deal{ID: dealID, Title: "Acme renewal"}}
	link := &fakeLinkRepo{}
	tags := &fakeTagRepo{byID: &domain.Tag{ID: tagID, Name: "VIP"}}
	svc := newLinkTestService(nil, deal, nil, nil, link, tags)

	if err := svc.AddTag(context.Background(), uuid.New(), uuid.New(), "deal", dealID, tagID); err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	if len(link.created) != 1 {
		t.Fatalf("expected a tag edge, got %d", len(link.created))
	}
	e := link.created[0]
	if e.FromSlug != "deal" || e.RelationKey != "tags" || e.ToSlug != "tag" || e.ToID != tagID {
		t.Errorf("tag edge wrong: %+v", e)
	}
	if len(link.contactAdds) != 0 {
		t.Errorf("a deal must not touch contact_tags")
	}
}

func TestAddTag_Contact_UsesContactTags(t *testing.T) {
	tagID := uuid.New()
	contactID := uuid.New()
	contact := &fakeContactUC{ret: &domain.Contact{ID: contactID, FirstName: "Ada"}}
	link := &fakeLinkRepo{}
	tags := &fakeTagRepo{byID: &domain.Tag{ID: tagID, Name: "VIP"}}
	svc := newLinkTestService(nil, nil, contact, nil, link, tags)

	if err := svc.AddTag(context.Background(), uuid.New(), uuid.New(), "contact", contactID, tagID); err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	if len(link.contactAdds) != 1 || link.contactAdds[0] != [2]uuid.UUID{contactID, tagID} {
		t.Errorf("contact tag not written to contact_tags: %v", link.contactAdds)
	}
	if len(link.created) != 0 {
		t.Errorf("a contact must not write an object_links tag edge")
	}
}

func TestAddTag_UnknownTag404(t *testing.T) {
	deal := &fakeDealUC{ret: &domain.Deal{ID: uuid.New(), Title: "Acme"}}
	tags := &fakeTagRepo{byID: nil} // tag not in org
	svc := newLinkTestService(nil, deal, nil, nil, &fakeLinkRepo{}, tags)
	if err := svc.AddTag(context.Background(), uuid.New(), uuid.New(), "deal", uuid.New(), uuid.New()); !isNotFound(err) {
		t.Errorf("expected 404 for unknown tag, got %v", err)
	}
}

func TestListTags_Deal_FromObjectLinks(t *testing.T) {
	tagID := uuid.New()
	deal := &fakeDealUC{ret: &domain.Deal{ID: uuid.New(), Title: "Acme"}}
	link := &fakeLinkRepo{listFrom: []domain.ObjectLink{
		{RelationKey: "tags", ToSlug: "tag", ToID: tagID},
		{RelationKey: "account", ToSlug: "company", ToID: uuid.New()}, // not a tag
	}}
	tags := &fakeTagRepo{list: []domain.Tag{{ID: tagID, Name: "VIP"}, {ID: uuid.New(), Name: "Other"}}}
	svc := newLinkTestService(nil, deal, nil, nil, link, tags)

	got, err := svc.ListTags(context.Background(), uuid.New(), "deal", uuid.New())
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(got) != 1 || got[0].ID != tagID {
		t.Fatalf("expected only the VIP tag, got %+v", got)
	}
}

func TestListTags_Contact_FromContactTags(t *testing.T) {
	tagID := uuid.New()
	contact := &fakeContactUC{ret: &domain.Contact{ID: uuid.New(), FirstName: "Ada"}}
	link := &fakeLinkRepo{contactTagIDs: []uuid.UUID{tagID}}
	tags := &fakeTagRepo{list: []domain.Tag{{ID: tagID, Name: "VIP"}}}
	svc := newLinkTestService(nil, nil, contact, nil, link, tags)

	got, err := svc.ListTags(context.Background(), uuid.New(), "contact", uuid.New())
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(got) != 1 || got[0].ID != tagID {
		t.Fatalf("expected the contact's tag, got %+v", got)
	}
}

// ============================================================
// Cascade on delete (R3)
// ============================================================

func TestDelete_CustomObject_CascadesLinks(t *testing.T) {
	defID := uuid.New()
	recID := uuid.New()
	custom := &fakeCustomObjUC{
		def: &domain.CustomObjectDef{ID: defID, Slug: "project"},
		rec: &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID},
	}
	link := &fakeLinkRepo{}
	svc := newLinkTestService(custom, nil, nil, nil, link, nil)

	if err := svc.Delete(context.Background(), uuid.New(), "project", recID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !custom.deleted {
		t.Error("record was not deleted")
	}
	if link.cascadeHits != 1 || link.cascadeSlug != "project" || link.cascadeID != recID {
		t.Errorf("cascade not invoked for the deleted record: hits=%d slug=%q id=%v", link.cascadeHits, link.cascadeSlug, link.cascadeID)
	}
}

func TestDelete_SystemObject_CascadesLinks(t *testing.T) {
	dealID := uuid.New()
	deal := &fakeDealUC{ret: &domain.Deal{ID: dealID, Title: "Acme"}}
	link := &fakeLinkRepo{}
	svc := newLinkTestService(nil, deal, nil, nil, link, nil)

	if err := svc.Delete(context.Background(), uuid.New(), "deal", dealID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if link.cascadeHits != 1 || link.cascadeSlug != "deal" || link.cascadeID != dealID {
		t.Errorf("system-object delete must cascade links too: %+v", link)
	}
}

// ============================================================
// Helpers
// ============================================================

func isBadRequest(err error) bool { return appCode(err) == 400 }
func isNotFound(err error) bool   { return appCode(err) == 404 }

func appCode(err error) int {
	if ae, ok := err.(*domain.AppError); ok {
		return ae.Code
	}
	return 0
}
