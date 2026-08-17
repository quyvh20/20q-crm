package usecase

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// Record-list filter engine (record_filter.go, filtering overhaul F2/F3)
// ============================================================
//
// compileListFilter is what killed the silent-empty bug class (sort_by pre-R7.3,
// pipeline_id R9.3, lead_score R9.4): a legacy ?field=value param that names a
// NATIVE column used to fall through to `custom_fields ->> key = value` — a key
// no row has — and return an empty page with no error. These tests pin the
// engine's whole contract: native keys compile against their real column, the
// consumed keys leave in.Filters (so the legacy jsonb test can't ALSO run),
// unknown keys are a 400, FLS-hidden keys a 403, typed adapter keys stay typed,
// and an unwired registry keeps the exact pre-engine behaviour.

// filterEngineRegistry seeds a contact def whose catalog carries all three field
// shapes the engine has to resolve: a NATIVE column (email — the shape the
// silent-empty bug ate), a jsonb text field (industry), and a jsonb number
// field (salary — the one the FLS tests hide). Field shapes mirror what
// reportCatalogForDef consumes: StorageKind "column" + MapsToColumn vs "jsonb".
func filterEngineRegistry() *fakeRegistryRepo {
	contactDefID := uuid.MustParse("cccccccc-0000-0000-0000-0000000020b2")
	table := "contacts"
	return &fakeRegistryRepo{
		defs: []domain.ObjectDef{{
			ID: contactDefID, Slug: "contact", Label: "Contact", LabelPlural: "Contacts",
			IsSystem: true, Storage: "table", RecordTable: &table,
		}},
		fields: map[uuid.UUID][]domain.ObjectField{
			contactDefID: {
				{Key: "email", Label: "Email", Type: "text", StorageKind: "column", MapsToColumn: reportStrPtr("email"), IsSystem: true},
				{Key: "industry", Label: "Industry", Type: "text", StorageKind: "jsonb"},
				{Key: "salary", Label: "Salary", Type: "number", StorageKind: "jsonb"},
			},
		},
	}
}

// newFilterEngineService builds a RecordService around a recording contact stub.
// reg == nil leaves the filter registry UNWIRED — the legacy pass-through mode
// every pre-engine unit test runs in; authz == nil disables OLS/FLS.
func newFilterEngineService(contact domain.ContactUseCase, authz domain.RecordAuthorizer, reg domain.ObjectRegistryRepository) domain.RecordService {
	if contact == nil {
		contact = &contactAdapterStubUC{}
	}
	svc := NewRecordService(&fakeCustomObjUC{}, &recordingSettingsUC{}, contact, &companyAdapterStubUC{}, &fakeDealUC{}, nil, nil, authz)
	if reg != nil {
		svc.SetFilterRegistry(reg)
	}
	return svc
}

// ============================================================
// Legacy param conversion — the silent-empty trap is dead
// ============================================================

// A flat ?email=x@y.com on contacts must compile against contacts.email (the
// real column) and must NOT also reach the adapter as a CustomFilters entry —
// the double-run is exactly what would empty the page: the jsonb test
// `custom_fields ->> 'email'` matches no contact ever.
func TestListFilter_LegacyNativeKeyCompilesAgainstItsColumn(t *testing.T) {
	contact := &sortRecordingContactUC{}
	svc := newFilterEngineService(contact, nil, filterEngineRegistry())

	callerFilters := map[string]string{"email": "x@y.com"}
	if _, err := svc.List(context.Background(), uuid.New(), "contact", domain.RecordListInput{
		Filters: callerFilters,
	}); err != nil {
		t.Fatalf("List: %v", err)
	}

	got := contact.got
	if got.Compiled == nil {
		t.Fatal("ContactFilter.Compiled is nil — the native key was not compiled")
	}
	if !strings.Contains(got.Compiled.SQL, "contacts.email = ?") {
		t.Errorf("compiled SQL does not address the real column:\n%s", got.Compiled.SQL)
	}
	if len(got.Compiled.Args) != 1 || got.Compiled.Args[0] != "x@y.com" {
		t.Errorf("compiled args = %v, want [x@y.com]", got.Compiled.Args)
	}
	if strings.Count(got.Compiled.SQL, "?") != len(got.Compiled.Args) {
		t.Errorf("placeholder/arg mismatch: %s / %v", got.Compiled.SQL, got.Compiled.Args)
	}
	if len(got.CustomFilters) != 0 {
		t.Errorf("CustomFilters = %v — the consumed key would ALSO run as a custom_fields ->> test and empty the result", got.CustomFilters)
	}
	// The engine rebuilds in.Filters rather than mutating the caller's map:
	// ExportCSV reuses ONE input across pages, so a mutated map would strip the
	// filter from every page after the first.
	if callerFilters["email"] != "x@y.com" {
		t.Errorf("caller's Filters map was mutated: %v", callerFilters)
	}
}

// A filter key that resolves nowhere is a 400 — never a silently empty list.
func TestListFilter_UnknownKeyIs400(t *testing.T) {
	svc := newFilterEngineService(nil, nil, filterEngineRegistry())

	_, err := svc.List(context.Background(), uuid.New(), "contact", domain.RecordListInput{
		Filters: map[string]string{"nope": "v"},
	})
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 for an unknown filter key", code)
	}
}

// ============================================================
// FLS — filters were an equality oracle on hidden fields
// ============================================================

// Filtering on a field the caller's mask hides is a 403, for BOTH filter
// languages. Before this gate, result membership leaked hidden values (an
// equality oracle), and the richer operators would have upgraded that leak to
// efficient bisection (salary gte X…).
func TestListFilter_FLSHiddenFieldIs403(t *testing.T) {
	newSvc := func(contact *sortRecordingContactUC) domain.RecordService {
		authz := &fakeAuthorizer{masks: map[string]domain.FieldMask{
			"contact": {Hidden: map[string]bool{"salary": true}},
		}}
		return newFilterEngineService(contact, authz, filterEngineRegistry())
	}

	t.Run("legacy param", func(t *testing.T) {
		contact := &sortRecordingContactUC{}
		_, err := newSvc(contact).List(context.Background(), uuid.New(), "contact", domain.RecordListInput{
			Filters: map[string]string{"salary": "50"},
		})
		if code := appErrCode(t, err); code != http.StatusForbidden {
			t.Fatalf("code = %d, want 403 for a hidden-field filter", code)
		}
		if contact.got.Compiled != nil || contact.got.CustomFilters != nil {
			t.Fatal("a 403'd filter must never reach the adapter")
		}
	})

	t.Run("structured AST", func(t *testing.T) {
		contact := &sortRecordingContactUC{}
		_, err := newSvc(contact).List(context.Background(), uuid.New(), "contact", domain.RecordListInput{
			Filter: &domain.ReportFilterGroup{Field: "salary", Operator: "gt", Value: float64(50)},
		})
		if code := appErrCode(t, err); code != http.StatusForbidden {
			t.Fatalf("code = %d, want 403 for a hidden-field AST condition", code)
		}
	})
}

// The TYPED adapter keys must not skip the gate. "company" is a registry field
// an admin can FLS-hide, and ?company=<uuid> is a membership oracle on the
// hidden relation even though the response masks the value on each row — the
// review caught the first cut gating only catalog-compiled keys, which left
// the same predicate 403ing as an AST and passing as a legacy param. The gate
// needs no catalog, so it must hold even with the registry UNWIRED.
func TestListFilter_FLSGateCoversTypedAdapterKeys(t *testing.T) {
	authz := &fakeAuthorizer{masks: map[string]domain.FieldMask{
		"contact": {Hidden: map[string]bool{"company": true}},
	}}
	for name, reg := range map[string]domain.ObjectRegistryRepository{
		"wired":    filterEngineRegistry(),
		"unwired":  nil,
	} {
		t.Run(name, func(t *testing.T) {
			contact := &sortRecordingContactUC{}
			var svc domain.RecordService
			if reg != nil {
				svc = newFilterEngineService(contact, authz, reg)
			} else {
				svc = newFilterEngineService(contact, authz, nil)
			}
			_, err := svc.List(context.Background(), uuid.New(), "contact", domain.RecordListInput{
				Filters: map[string]string{"company": uuid.NewString()},
			})
			if code := appErrCode(t, err); code != http.StatusForbidden {
				t.Fatalf("code = %d, want 403 — a typed key on a hidden field is the same oracle", code)
			}
			if contact.got.CompanyID != nil {
				t.Fatal("the hidden typed filter still reached the adapter")
			}
		})
	}
}

// ============================================================
// AST + legacy merge — one compiled fragment
// ============================================================

// A structured AST and leftover legacy params merge into ONE fragment: the AST
// nests as a rule (preserving its own AND/OR) and each legacy param becomes an
// eq leaf, so the repositories AND a single predicate onto their chains.
func TestListFilter_ASTAndLegacyParamsMergeIntoOneFragment(t *testing.T) {
	contact := &sortRecordingContactUC{}
	svc := newFilterEngineService(contact, nil, filterEngineRegistry())

	if _, err := svc.List(context.Background(), uuid.New(), "contact", domain.RecordListInput{
		Filter:  &domain.ReportFilterGroup{Field: "email", Operator: "contains", Value: "@acme"},
		Filters: map[string]string{"industry": "tech"},
	}); err != nil {
		t.Fatalf("List: %v", err)
	}

	got := contact.got
	if got.Compiled == nil {
		t.Fatal("expected one compiled fragment carrying both predicates")
	}
	// The AST condition compiles against the native column…
	if !strings.Contains(got.Compiled.SQL, "contacts.email ILIKE ?") {
		t.Errorf("AST predicate missing:\n%s", got.Compiled.SQL)
	}
	// …and the legacy param against its jsonb storage, in the same fragment.
	if !strings.Contains(got.Compiled.SQL, "contacts.custom_fields->>'industry' = ?") {
		t.Errorf("legacy-param predicate missing:\n%s", got.Compiled.SQL)
	}
	if len(got.Compiled.Args) != 2 || got.Compiled.Args[0] != "%@acme%" || got.Compiled.Args[1] != "tech" {
		t.Errorf("args = %v, want [%%@acme%% tech]", got.Compiled.Args)
	}
	if len(got.CustomFilters) != 0 {
		t.Errorf("CustomFilters = %v, want empty — the engine consumed the legacy key", got.CustomFilters)
	}
}

// ============================================================
// Unwired registry — byte-for-byte legacy behaviour
// ============================================================

// Until SetFilterRegistry is called (nil in every pre-engine unit test), a
// non-typed legacy param must reach the adapter EXACTLY as before the engine
// existed: as a CustomFilters entry, with nothing compiled.
func TestListFilter_UnwiredRegistry_LegacyPassThrough(t *testing.T) {
	contact := &sortRecordingContactUC{}
	svc := newFilterEngineService(contact, nil, nil) // registry never wired

	if _, err := svc.List(context.Background(), uuid.New(), "contact", domain.RecordListInput{
		Filters: map[string]string{"industry": "tech"},
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if contact.got.CustomFilters["industry"] != "tech" {
		t.Errorf("CustomFilters = %v, want the legacy pass-through entry", contact.got.CustomFilters)
	}
	if contact.got.Compiled != nil {
		t.Errorf("Compiled = %+v, want nil with no registry wired", contact.got.Compiled)
	}
}

// A structured AST needs the catalog to validate against, so with no registry it
// is refused outright — a 400, not a filter silently ignored.
func TestListFilter_UnwiredRegistry_ASTRejected(t *testing.T) {
	svc := newFilterEngineService(nil, nil, nil)

	_, err := svc.List(context.Background(), uuid.New(), "contact", domain.RecordListInput{
		Filter: &domain.ReportFilterGroup{Field: "email", Operator: "eq", Value: "x"},
	})
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", code)
	}
	if !strings.Contains(err.Error(), "structured filters are not available") {
		t.Errorf("error = %q, want the unwired-registry message", err)
	}
}

// ============================================================
// Typed adapter keys stay typed
// ============================================================

// The adapters bind "company"/"owner_user_id" (etc.) to typed columns
// themselves, including filterUUID's tolerant unparseable-value drop that the
// kanban and find_records rely on. The engine must leave those keys alone: a
// company filter arrives as ContactFilter.CompanyID, and with nothing else to
// compile, Compiled stays nil.
func TestListFilter_TypedKeysStayTyped(t *testing.T) {
	companyID := uuid.New()
	contact := &sortRecordingContactUC{}
	svc := newFilterEngineService(contact, nil, filterEngineRegistry())

	if _, err := svc.List(context.Background(), uuid.New(), "contact", domain.RecordListInput{
		Filters: map[string]string{"company": companyID.String()},
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if contact.got.CompanyID == nil || *contact.got.CompanyID != companyID {
		t.Errorf("CompanyID = %v, want %s bound via the typed path", contact.got.CompanyID, companyID)
	}
	if contact.got.Compiled != nil {
		t.Errorf("Compiled = %+v, want nil when only typed keys are present", contact.got.Compiled)
	}
	if len(contact.got.CustomFilters) != 0 {
		t.Errorf("CustomFilters = %v, want empty — company is a reserved typed key", contact.got.CustomFilters)
	}
}
