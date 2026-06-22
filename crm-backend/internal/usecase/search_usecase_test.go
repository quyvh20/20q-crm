package usecase

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ---- fakes for the search usecase --------------------------------------------
// (fakeRegistryUC is defined in permission_usecase_test.go and reused here.)

type fakeEmbedRepo struct {
	domain.RecordEmbeddingRepository
	semantic         []domain.RecordEmbeddingHit
	fulltext         []domain.RecordEmbeddingHit
	gotSemanticSlugs []string
	gotFulltextSlugs []string
	semanticCalled   bool
}

func (f *fakeEmbedRepo) SearchSemantic(_ context.Context, _ uuid.UUID, slugs []string, _ []float32, _ float32, _ int) ([]domain.RecordEmbeddingHit, error) {
	f.semanticCalled = true
	f.gotSemanticSlugs = slugs
	return f.semantic, nil
}

func (f *fakeEmbedRepo) SearchFulltext(_ context.Context, _ uuid.UUID, slugs []string, _ string, _ int) ([]domain.RecordEmbeddingHit, error) {
	f.gotFulltextSlugs = slugs
	return f.fulltext, nil
}

// fakeRecordSvc embeds RecordService so only Get needs a body; any other call
// would panic on the nil embedded interface (search must only ever call Get).
type fakeRecordSvc struct {
	domain.RecordService
	recs    map[string]*domain.UniformRecord
	errKeys map[string]bool
}

func (f *fakeRecordSvc) Get(_ context.Context, _ uuid.UUID, slug string, id uuid.UUID) (*domain.UniformRecord, error) {
	key := slug + ":" + id.String()
	if f.errKeys[key] {
		return nil, domain.NewAppError(403, "forbidden")
	}
	if r, ok := f.recs[key]; ok {
		return r, nil
	}
	return nil, domain.NewAppError(404, "not found")
}

type searchContactUC struct {
	domain.ContactUseCase
	contacts []domain.Contact
}

func (f *searchContactUC) SemanticSearch(_ context.Context, _ uuid.UUID, _ string, _ int) ([]domain.Contact, error) {
	return f.contacts, nil
}

type fakeEmbedder struct {
	vec []float32
	err error
}

func (f *fakeEmbedder) EmbedText(_ context.Context, _ string) ([]float32, error) {
	return f.vec, f.err
}

func uniRec(slug string, id uuid.UUID, display string) *domain.UniformRecord {
	return &domain.UniformRecord{ID: id, Object: slug, Display: display, Fields: map[string]interface{}{}}
}

// ---- tests -------------------------------------------------------------------

// Full path: semantic + fulltext + contacts, grouped by object, with OLS skip,
// dedup, and the searchable-slug filter all exercised together.
func TestSearch_GroupsAcrossObjects_WithOLSAndDedup(t *testing.T) {
	orgID := uuid.New()
	ticketR1, ticketR2, ticketR3 := uuid.New(), uuid.New(), uuid.New()
	productP1 := uuid.New()
	contactC1 := uuid.New()

	reg := &fakeRegistryUC{objects: []domain.ObjectSummary{
		{Slug: "contact", Label: "Contact", LabelPlural: "Contacts", Icon: "👤", IsSystem: true},
		{Slug: "ticket", Label: "Ticket", LabelPlural: "Tickets", Icon: "🎫", Searchable: true},
		{Slug: "product", Label: "Product", LabelPlural: "Products", Icon: "📦", Searchable: true},
		{Slug: "note", Label: "Note", LabelPlural: "Notes", Icon: "📝", Searchable: false},
	}}

	embedRepo := &fakeEmbedRepo{
		semantic: []domain.RecordEmbeddingHit{
			{ObjectSlug: "ticket", RecordID: ticketR1, Distance: 0.1},
			{ObjectSlug: "ticket", RecordID: ticketR2, Distance: 0.2},
			{ObjectSlug: "product", RecordID: productP1, Distance: 0.15},
		},
		fulltext: []domain.RecordEmbeddingHit{
			{ObjectSlug: "ticket", RecordID: ticketR3},   // new (fulltext-only)
			{ObjectSlug: "product", RecordID: productP1}, // dup of a semantic hit
		},
	}

	recordSvc := &fakeRecordSvc{
		recs: map[string]*domain.UniformRecord{
			"ticket:" + ticketR1.String():   uniRec("ticket", ticketR1, "Ticket 1"),
			"ticket:" + ticketR3.String():   uniRec("ticket", ticketR3, "Ticket 3"),
			"product:" + productP1.String(): uniRec("product", productP1, "Product 1"),
			"contact:" + contactC1.String(): uniRec("contact", contactC1, "Jane Doe"),
		},
		errKeys: map[string]bool{
			"ticket:" + ticketR2.String(): true, // OLS denies r2
		},
	}

	contactUC := &searchContactUC{contacts: []domain.Contact{{ID: contactC1}}}

	uc := NewSearchUseCase(embedRepo, &fakeEmbedder{vec: []float32{0.1, 0.2, 0.3}}, recordSvc, reg, contactUC)

	res, err := uc.Search(context.Background(), orgID, "acme", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	// Only searchable custom objects are queried (note excluded).
	if got := embedRepo.gotSemanticSlugs; len(got) != 2 || got[0] != "ticket" || got[1] != "product" {
		t.Fatalf("semantic slug filter = %v, want [ticket product]", got)
	}

	// Groups: contact first, then ticket, then product (registry order).
	if len(res.Groups) != 3 {
		t.Fatalf("expected 3 groups, got %d: %+v", len(res.Groups), res.Groups)
	}
	if res.Groups[0].Object != "contact" || res.Groups[1].Object != "ticket" || res.Groups[2].Object != "product" {
		t.Fatalf("group order = %s,%s,%s", res.Groups[0].Object, res.Groups[1].Object, res.Groups[2].Object)
	}

	// ticket: r1 + r3 (r2 dropped by OLS), in semantic-then-fulltext order.
	ticket := res.Groups[1]
	if len(ticket.Hits) != 2 {
		t.Fatalf("ticket hits = %d, want 2", len(ticket.Hits))
	}
	if ticket.Hits[0].Record.ID != ticketR1 || ticket.Hits[1].Record.ID != ticketR3 {
		t.Fatalf("ticket hit order wrong: %s, %s", ticket.Hits[0].Record.ID, ticket.Hits[1].Record.ID)
	}
	// Semantic hit carries a similarity score (1 - 0.1); fulltext-only is 0.
	if ticket.Hits[0].Score < 0.89 || ticket.Hits[0].Score > 0.91 {
		t.Fatalf("semantic score = %v, want ~0.9", ticket.Hits[0].Score)
	}
	if ticket.Hits[1].Score != 0 {
		t.Fatalf("fulltext-only score = %v, want 0", ticket.Hits[1].Score)
	}

	// product: deduped to a single hit.
	if len(res.Groups[2].Hits) != 1 {
		t.Fatalf("product hits = %d, want 1 (deduped)", len(res.Groups[2].Hits))
	}

	// contact group resolved through RecordService too.
	if res.Groups[0].Hits[0].Record.Display != "Jane Doe" {
		t.Fatalf("contact hit display = %q", res.Groups[0].Hits[0].Record.Display)
	}
}

// No embedder → semantic + contacts disabled; custom objects still searchable via
// fulltext.
func TestSearch_NoEmbedder_FulltextOnly(t *testing.T) {
	orgID := uuid.New()
	r1 := uuid.New()

	reg := &fakeRegistryUC{objects: []domain.ObjectSummary{
		{Slug: "contact", Label: "Contact", LabelPlural: "Contacts", IsSystem: true},
		{Slug: "ticket", Label: "Ticket", LabelPlural: "Tickets", Searchable: true},
	}}
	embedRepo := &fakeEmbedRepo{
		fulltext: []domain.RecordEmbeddingHit{{ObjectSlug: "ticket", RecordID: r1}},
	}
	recordSvc := &fakeRecordSvc{recs: map[string]*domain.UniformRecord{
		"ticket:" + r1.String(): uniRec("ticket", r1, "Ticket 1"),
	}}
	// Contact semantic search would return a contact, but no vector → skipped.
	contactUC := &searchContactUC{contacts: []domain.Contact{{ID: uuid.New()}}}

	uc := NewSearchUseCase(embedRepo, nil, recordSvc, reg, contactUC)

	res, err := uc.Search(context.Background(), orgID, "acme", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if embedRepo.semanticCalled {
		t.Fatalf("semantic search must be skipped without a query vector")
	}
	if len(res.Groups) != 1 || res.Groups[0].Object != "ticket" {
		t.Fatalf("expected only a ticket group, got %+v", res.Groups)
	}
}

// Whole object denied by OLS → group omitted entirely (no leak).
func TestSearch_ObjectFullyDenied_OmitsGroup(t *testing.T) {
	orgID := uuid.New()
	r1 := uuid.New()
	reg := &fakeRegistryUC{objects: []domain.ObjectSummary{
		{Slug: "ticket", Label: "Ticket", LabelPlural: "Tickets", Searchable: true},
	}}
	embedRepo := &fakeEmbedRepo{fulltext: []domain.RecordEmbeddingHit{{ObjectSlug: "ticket", RecordID: r1}}}
	recordSvc := &fakeRecordSvc{errKeys: map[string]bool{"ticket:" + r1.String(): true}}

	uc := NewSearchUseCase(embedRepo, nil, recordSvc, reg, &searchContactUC{})
	res, err := uc.Search(context.Background(), orgID, "acme", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Groups) != 0 {
		t.Fatalf("expected no groups when all hits are OLS-denied, got %+v", res.Groups)
	}
}

// Empty query → empty result, no work.
func TestSearch_EmptyQuery_NoOp(t *testing.T) {
	embedRepo := &fakeEmbedRepo{}
	uc := NewSearchUseCase(embedRepo, &fakeEmbedder{vec: []float32{1}}, &fakeRecordSvc{}, &fakeRegistryUC{}, &searchContactUC{})
	res, err := uc.Search(context.Background(), uuid.New(), "   ", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Groups) != 0 {
		t.Fatalf("empty query should yield no groups")
	}
	if embedRepo.semanticCalled {
		t.Fatalf("empty query must not hit the index")
	}
}
