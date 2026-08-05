package usecase

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// R7.3 — sort plumbing: handler vocabulary → typed filter vocabulary
// ============================================================
//
// Before R7.3 the chain had a hole at every layer: domain.RecordListInput carried
// no sort at all, the registry route did not reserve `sort_by`, and the adapters
// built ContactFilter/DealFilter without SortBy — even though both repositories
// had honoured SortBy since R4. The visible symptom was NOT "sorting is ignored":
// an unreserved query param becomes a jsonb field filter, so `?sort_by=value`
// asked for the contacts whose custom field "sort_by" equals "value" and returned
// an EMPTY list. These tests pin each link of that chain.

type sortRecordingContactUC struct {
	domain.ContactUseCase
	got domain.ContactFilter
}

func (f *sortRecordingContactUC) List(_ context.Context, _ uuid.UUID, ff domain.ContactFilter) ([]domain.Contact, string, error) {
	f.got = ff
	return nil, "", nil
}

type sortRecordingCompanyUC struct {
	domain.CompanyUseCase
	got domain.CompanyFilter
}

func (f *sortRecordingCompanyUC) List(_ context.Context, _ uuid.UUID, ff domain.CompanyFilter) ([]domain.Company, string, error) {
	f.got = ff
	return nil, "", nil
}

func newSortTestService(contact domain.ContactUseCase, company domain.CompanyUseCase, deal domain.DealUseCase, custom domain.CustomObjectUseCase) domain.RecordService {
	if contact == nil {
		contact = &contactAdapterStubUC{}
	}
	if company == nil {
		company = &companyAdapterStubUC{}
	}
	if deal == nil {
		deal = &fakeDealUC{}
	}
	if custom == nil {
		custom = &fakeCustomObjUC{}
	}
	return NewRecordService(custom, &recordingSettingsUC{}, contact, company, deal, nil, nil, nil)
}

// The translation is the part most likely to be written wrongly, because the two
// vocabularies agree for three of the four deal columns and disagree for exactly
// one contact column: the registry field key is "first_name", the repository
// whitelist calls that ordering "name". A pass-through would look right in every
// deal test and be silently wrong for contacts.
func TestList_Contact_TranslatesFieldKeyToRepositorySortKey(t *testing.T) {
	contact := &sortRecordingContactUC{}
	svc := newSortTestService(contact, nil, nil, nil)

	page, err := svc.List(context.Background(), uuid.New(), "contact", domain.RecordListInput{
		SortBy: "first_name", SortOrder: "asc",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if contact.got.SortBy != "name" {
		t.Errorf("ContactFilter.SortBy = %q, want %q (the repository whitelist's name for contacts.first_name)", contact.got.SortBy, "name")
	}
	if contact.got.SortOrder != "asc" {
		t.Errorf("ContactFilter.SortOrder = %q, want asc", contact.got.SortOrder)
	}
	// The echo speaks the CLIENT vocabulary, not the repository's, so the UI can
	// match it against the column header it rendered.
	if page.Sort == nil || page.Sort.By != "first_name" || page.Sort.Order != "asc" {
		t.Fatalf("echoed sort = %+v, want by=first_name order=asc", page.Sort)
	}
}

func TestList_Deal_PassesSortThroughToTypedFilter(t *testing.T) {
	for _, key := range []string{"title", "value", "probability", "created_at"} {
		t.Run(key, func(t *testing.T) {
			deal := &fakeDealUC{}
			svc := newSortTestService(nil, nil, deal, nil)

			if _, err := svc.List(context.Background(), uuid.New(), "deal", domain.RecordListInput{
				SortBy: key, SortOrder: "desc",
			}); err != nil {
				t.Fatalf("List: %v", err)
			}
			if deal.listGot.SortBy != key {
				t.Errorf("DealFilter.SortBy = %q, want %q", deal.listGot.SortBy, key)
			}
		})
	}
}

// A sort key the object does not declare must fall back to the default ordering,
// NOT reach the repository. The repository whitelist would refuse it anyway, but
// then the echoed Sort.By would advertise a key the query did not run under and
// the UI would draw its sort arrow on the wrong column.
func TestList_UnknownSortKeyFallsBackToCreatedAtEverywhere(t *testing.T) {
	contact := &sortRecordingContactUC{}
	svc := newSortTestService(contact, nil, nil, nil)

	page, err := svc.List(context.Background(), uuid.New(), "contact", domain.RecordListInput{
		SortBy: "phone", SortOrder: "asc", // phone is a real field, but not sortable
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if contact.got.SortBy != "created_at" {
		t.Errorf("ContactFilter.SortBy = %q, want created_at", contact.got.SortBy)
	}
	if page.Sort == nil || page.Sort.By != "created_at" {
		t.Fatalf("echoed sort = %+v, want by=created_at", page.Sort)
	}
}

// An object with no declared sortable columns must echo NO sort block at all —
// that absence is exactly what tells the frontend not to render sort controls it
// could not honour. Rendering them would be worse than having none: the header
// would look interactive and change nothing.
func TestList_UnsortableObjectsEchoNoSortBlock(t *testing.T) {
	t.Run("company", func(t *testing.T) {
		company := &sortRecordingCompanyUC{}
		svc := newSortTestService(nil, company, nil, nil)

		page, err := svc.List(context.Background(), uuid.New(), "company", domain.RecordListInput{
			SortBy: "name", SortOrder: "asc",
		})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if page.Sort != nil {
			t.Errorf("company echoed a sort block %+v; CompanyFilter has no SortBy field to honour it", page.Sort)
		}
	})

	t.Run("custom object", func(t *testing.T) {
		defID := uuid.New()
		custom := &fakeCustomObjUC{def: &domain.CustomObjectDef{ID: defID, Slug: "project"}}
		svc := newSortTestService(nil, nil, nil, custom)

		page, err := svc.List(context.Background(), uuid.New(), "project", domain.RecordListInput{
			SortBy: "name", SortOrder: "asc",
		})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if page.Sort != nil {
			t.Errorf("custom object echoed a sort block %+v; it pages by OFFSET and cannot honour one", page.Sort)
		}
	})
}

// The advertised menu has to match what the service will actually accept, or a
// client that renders headers straight off `sortable` gets controls that fall
// back to created_at on click.
func TestList_EverySortableKeyItAdvertisesIsAccepted(t *testing.T) {
	for _, slug := range []string{"contact", "deal"} {
		keys := domain.SortableRecordFields(slug)
		if len(keys) == 0 {
			t.Fatalf("%s advertises no sortable keys", slug)
		}
		for _, key := range keys {
			t.Run(slug+"/"+key, func(t *testing.T) {
				contact := &sortRecordingContactUC{}
				deal := &fakeDealUC{}
				svc := newSortTestService(contact, nil, deal, nil)

				page, err := svc.List(context.Background(), uuid.New(), slug, domain.RecordListInput{SortBy: key})
				if err != nil {
					t.Fatalf("List: %v", err)
				}
				if page.Sort == nil || page.Sort.By != key {
					t.Fatalf("asked for %q, ran under %+v", key, page.Sort)
				}
				// …and it must reach the typed filter as a non-empty key, i.e. the
				// translation table has an entry for everything it advertises.
				got := deal.listGot.SortBy
				if slug == "contact" {
					got = contact.got.SortBy
				}
				if got == "" {
					t.Errorf("%s sort key %q advertised but translated to an empty filter key", slug, key)
				}
			})
		}
	}
}

func TestNormalizeRecordSort_DefaultsAndDirection(t *testing.T) {
	cases := []struct {
		slug, by, order   string
		wantBy, wantOrder string
	}{
		{"contact", "", "", "created_at", "desc"},
		{"contact", "email", "ASC", "email", "asc"},      // direction is case-insensitive
		{"contact", "email", "sideways", "email", "desc"}, // anything not asc is desc
		{"deal", "value", "asc", "value", "asc"},
		{"deal", "nonsense", "asc", "created_at", "asc"},
		{"company", "name", "asc", "", ""},
		{"project", "", "", "", ""},
	}
	for _, c := range cases {
		gotBy, gotOrder := domain.NormalizeRecordSort(c.slug, c.by, c.order)
		if gotBy != c.wantBy || gotOrder != c.wantOrder {
			t.Errorf("NormalizeRecordSort(%q,%q,%q) = (%q,%q), want (%q,%q)",
				c.slug, c.by, c.order, gotBy, gotOrder, c.wantBy, c.wantOrder)
		}
	}
}
