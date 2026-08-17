package usecase

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// listViewUseCase is the saved-views surface over the uniform record list. A
// view stores the record-list inputs (filter AST / q / tags / sort) verbatim;
// EXECUTION always happens later through RecordService.List as whoever opens
// the view, so OLS/FLS/row scope are re-derived per viewer and the view grants
// nothing by itself.
//
// What must be airtight is the save side (the segment save-time doctrine):
// shared views are listed org-wide, so a definition filtering on an FLS-hidden
// field would leak an equality/range oracle on the hidden values to every
// teammate. Create/Update therefore run OLS → shape validation → FLS on every
// touched filter field → a dry-run compile against the object's real catalog,
// and reject before anything is stored.
type listViewUseCase struct {
	repo     domain.ListViewRepository
	registry domain.ObjectRegistryRepository
	authz    domain.RecordAuthorizer
}

// NewListViewUseCase builds the usecase. registry sources the same per-org
// field catalog the record-list filter engine compiles against; authz is the
// OLS/FLS authorizer (the same permissionUC handed to RecordService).
func NewListViewUseCase(repo domain.ListViewRepository, registry domain.ObjectRegistryRepository, authz domain.RecordAuthorizer) domain.ListViewUseCase {
	return &listViewUseCase{repo: repo, registry: registry, authz: authz}
}

// maxListViewNameLen matches list_views.name VARCHAR(120), counted in runes so
// a multi-byte name is bounded the same way Postgres bounds it (avoiding a
// 22001 → HTTP 500 on save — the segment name lesson).
const maxListViewNameLen = 120

// maxListViewDefinitionBytes bounds the RAW definition blob. The filter AST is
// already depth/leaf-capped by the compiler, but q/tags/sort ride outside the
// AST, and without a byte cap a saved view becomes an unbounded jsonb write
// amplified onto every List response that returns it.
const maxListViewDefinitionBytes = 16 * 1024

// maxListViewsPerObject caps views per (org, object). 400 rather than 403: the
// caller did nothing forbidden, their org's shelf is full.
const maxListViewsPerObject = 100

func validateListViewName(name string) error {
	if name == "" {
		return domain.NewAppError(http.StatusBadRequest, "name is required")
	}
	if utf8.RuneCountInString(name) > maxListViewNameLen {
		return domain.NewAppError(http.StatusBadRequest, "name must be 120 characters or fewer")
	}
	return nil
}

// listViewWriteScopeAllowed gates view MUTATIONS for personal access tokens.
// The OLS check on these routes is ActionRead — saving a view is a read-level
// activity for a human — but that maps to the records.read token scope, so
// without this a read-only integration token could create, reshape or delete
// saved views (including org-shared ones). Same intersection rule as record
// writes: a token is always a subset of its owner. Normal sessions pass
// untouched (tokenScopeAllows no-ops for them).
func listViewWriteScopeAllowed(ctx context.Context) error {
	caller, ok := domain.CallerFromContext(ctx)
	if !ok {
		return nil
	}
	return tokenScopeAllows(caller, domain.CapRecordsWrite)
}

func (uc *listViewUseCase) List(ctx context.Context, orgID, userID uuid.UUID, objectSlug string) ([]domain.ListView, error) {
	if err := uc.authz.Authorize(ctx, orgID, objectSlug, domain.ActionRead); err != nil {
		return nil, err
	}
	views, err := uc.repo.List(ctx, orgID, objectSlug, userID)
	if err != nil {
		return nil, err
	}

	// FLS on the read side, not just at save: a shared view is validated against
	// its AUTHOR's field mask, but this listing serves every org member — and a
	// definition names its filter fields and constants verbatim. Handing a
	// role-restricted reader {field:"commission", gte:100000} leaks the hidden
	// field's existence and the org's threshold, the exact leak foldLayout
	// strips filterable_fields to prevent. Views whose filter touches a field
	// the CALLER can't see are omitted outright (not redacted): the caller
	// couldn't apply them anyway — replay 403s in compileListFilter — so a
	// name-only stub would just be a dead menu entry. The caller's OWN views
	// always pass: they authored the definition, so nothing in it is news to
	// them, and hiding their view with no edit path would strand it.
	if mask := uc.authz.FieldMask(ctx, orgID, objectSlug); !mask.Empty() {
		kept := views[:0]
		for _, v := range views {
			if v.OwnerUserID != userID && listViewTouchesHidden(v.Definition, mask) {
				continue
			}
			kept = append(kept, v)
		}
		views = kept
	}

	if views == nil {
		// Empty-slice init (never nil): the handler marshals this straight into
		// "data", and a null array white-screens the frontend list.
		views = []domain.ListView{}
	}
	return views, nil
}

// listViewTouchesHidden reports whether a stored definition's filter names any
// field the mask hides. An unparseable definition is treated as touching
// nothing — save-side validation makes that unreachable, and failing open here
// would only re-show a name, not data.
func listViewTouchesHidden(raw datatypes.JSON, mask domain.FieldMask) bool {
	if len(raw) == 0 {
		return false
	}
	var def domain.ListViewDefinition
	if err := json.Unmarshal(raw, &def); err != nil || def.Filter == nil {
		return false
	}
	touched := map[string]bool{}
	collectReportFilterFields(def.Filter, touched)
	for key := range touched {
		if mask.IsHidden(key) {
			return true
		}
	}
	return false
}

func (uc *listViewUseCase) Create(ctx context.Context, orgID, userID uuid.UUID, objectSlug string, in domain.ListViewInput) (*domain.ListView, error) {
	// Bounded before anything touches storage: object_slug is VARCHAR(100), and
	// a filterless definition skips the catalog resolve that would otherwise
	// reject an unknown slug — without this, an oversized slug is a 22001 → 500.
	if objectSlug == "" || len(objectSlug) > 100 {
		return nil, domain.NewAppError(http.StatusBadRequest, "invalid object")
	}
	if err := uc.authz.Authorize(ctx, orgID, objectSlug, domain.ActionRead); err != nil {
		return nil, err
	}
	if err := listViewWriteScopeAllowed(ctx); err != nil {
		return nil, err
	}
	name := ""
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	if err := validateListViewName(name); err != nil {
		return nil, err
	}
	def := []byte(in.Definition)
	if len(def) == 0 {
		def = []byte("{}")
	}
	if err := uc.validateDefinition(ctx, orgID, objectSlug, def); err != nil {
		return nil, err
	}
	// The cap is checked at the usecase (not a DB constraint) so it reads as a
	// clean 400. It races with itself across two concurrent creates, which can
	// land an org at 101 — acceptable for a shelf limit; a UNIQUE-style guard
	// would buy exactness this limit doesn't need.
	n, err := uc.repo.CountForObject(ctx, orgID, objectSlug)
	if err != nil {
		return nil, err
	}
	if n >= maxListViewsPerObject {
		return nil, domain.NewAppError(http.StatusBadRequest, "view limit reached")
	}
	v := &domain.ListView{
		OrgID:       orgID,
		OwnerUserID: userID,
		ObjectSlug:  objectSlug,
		Name:        name,
		Definition:  datatypes.JSON(def),
	}
	if in.Shared != nil {
		v.Shared = *in.Shared
	}
	if err := uc.repo.Create(ctx, v); err != nil {
		return nil, err
	}
	return v, nil
}

func (uc *listViewUseCase) Update(ctx context.Context, orgID, userID, id uuid.UUID, in domain.ListViewInput) (*domain.ListView, error) {
	v, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, domain.NewAppError(http.StatusNotFound, "view not found")
	}
	// OLS on the STORED slug (the route carries only the view id): losing read
	// on an object must also close editing its saved views.
	if err := uc.authz.Authorize(ctx, orgID, v.ObjectSlug, domain.ActionRead); err != nil {
		return nil, err
	}
	if err := listViewWriteScopeAllowed(ctx); err != nil {
		return nil, err
	}
	// Owner-only, shared or not: `shared` widens who can USE a view, never who
	// can rewrite it — otherwise sharing would hand every reader a pen over the
	// owner's saved state.
	if v.OwnerUserID != userID {
		return nil, domain.NewAppError(http.StatusForbidden, "only the view's owner can modify it")
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if err := validateListViewName(name); err != nil {
			return nil, err
		}
		v.Name = name
	}
	if in.Shared != nil {
		v.Shared = *in.Shared
	}
	if in.Definition != nil {
		def := []byte(in.Definition)
		if len(def) == 0 {
			def = []byte("{}")
		}
		if err := uc.validateDefinition(ctx, orgID, v.ObjectSlug, def); err != nil {
			return nil, err
		}
		v.Definition = datatypes.JSON(def)
	}
	if err := uc.repo.Update(ctx, v); err != nil {
		return nil, err
	}
	return v, nil
}

func (uc *listViewUseCase) Delete(ctx context.Context, orgID, userID, id uuid.UUID) error {
	v, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return err
	}
	if v == nil {
		return domain.NewAppError(http.StatusNotFound, "view not found")
	}
	if err := uc.authz.Authorize(ctx, orgID, v.ObjectSlug, domain.ActionRead); err != nil {
		return err
	}
	if err := listViewWriteScopeAllowed(ctx); err != nil {
		return err
	}
	if v.OwnerUserID != userID {
		return domain.NewAppError(http.StatusForbidden, "only the view's owner can delete it")
	}
	removed, err := uc.repo.SoftDelete(ctx, orgID, id)
	if err != nil {
		return err
	}
	if !removed {
		return domain.NewAppError(http.StatusNotFound, "view not found")
	}
	return nil
}

// ── internals ─────────────────────────────────────────────────────────────────

// validateDefinition is the airtight save-side gate: size → shape → scalars →
// FLS → dry-run compile. Order matters — the FLS check runs BEFORE the compile
// so a hidden field is a 403 (policy), not a 400 (input), and so a compile
// error message can never echo details about a field the caller cannot see.
func (uc *listViewUseCase) validateDefinition(ctx context.Context, orgID uuid.UUID, slug string, raw []byte) error {
	if len(raw) > maxListViewDefinitionBytes {
		return domain.NewAppError(http.StatusBadRequest, "definition is too large (16KB max)")
	}
	var def domain.ListViewDefinition
	if err := json.Unmarshal(raw, &def); err != nil {
		return domain.NewAppError(http.StatusBadRequest, "invalid view definition")
	}
	switch def.SortOrder {
	case "", "asc", "desc":
	default:
		return domain.NewAppError(http.StatusBadRequest, `sort_order must be "asc" or "desc"`)
	}
	// Tags are stored as strings (JSON), replayed as tag_ids. Parsed here so a
	// saved view can never smuggle a non-UUID into the replayed query string.
	for _, t := range def.Tags {
		if _, err := uuid.Parse(t); err != nil {
			return domain.NewAppError(http.StatusBadRequest, "invalid tag id in definition")
		}
	}
	if def.Filter == nil {
		return nil
	}

	// FLS gate. Needs no catalog (mirrors compileListFilter), and must hold even
	// for a filter key that doesn't resolve — an unknown-field 400 for a hidden
	// key would itself confirm the field exists.
	touched := map[string]bool{}
	collectReportFilterFields(def.Filter, touched)
	if mask := uc.authz.FieldMask(ctx, orgID, slug); !mask.Empty() {
		for key := range touched {
			if mask.IsHidden(key) {
				return domain.NewAppError(http.StatusForbidden, "the \""+key+"\" field is restricted for your role and cannot be used in a view")
			}
		}
	}

	catalog, target, err := uc.filterCatalog(ctx, orgID, slug)
	if err != nil {
		return err
	}
	// Dry-run compile: the result is discarded — the point is that a view that
	// SAVES is a view that will compile when replayed, so a stale/typo'd field
	// dies here as a 400 instead of surfacing as a broken view later.
	if _, err := domain.CompileRecordFilter(target, catalog, def.Filter); err != nil {
		return domain.NewAppError(http.StatusBadRequest, err.Error())
	}
	return nil
}

// filterCatalog resolves the object's filterable-field catalog and physical
// table — the same registry + reportCatalogForDef path recordService.
// filterCatalog uses, so a filter valid here is valid at replay. The table
// switch keys on the slug directly rather than a systemAdapters map (which
// lives on recordService): EnsureSystemObjects seeds exactly these three
// system defs, so the sets are the same by construction.
func (uc *listViewUseCase) filterCatalog(ctx context.Context, orgID uuid.UUID, slug string) ([]domain.ReportField, domain.FilterTableRef, error) {
	if err := uc.registry.EnsureSystemObjects(ctx, orgID); err != nil {
		return nil, domain.FilterTableRef{}, err
	}
	def, err := uc.registry.GetDefBySlug(ctx, orgID, slug)
	if err != nil {
		return nil, domain.FilterTableRef{}, err
	}
	if def == nil {
		return nil, domain.FilterTableRef{}, domain.NewAppError(http.StatusBadRequest, "unknown object \""+slug+"\"")
	}
	fields, err := uc.registry.ListFields(ctx, def.ID)
	if err != nil {
		return nil, domain.FilterTableRef{}, err
	}
	var target domain.FilterTableRef
	switch slug {
	case "contact":
		target = domain.FilterTableRef{Table: "contacts", JSONColumn: "custom_fields"}
	case "company":
		target = domain.FilterTableRef{Table: "companies", JSONColumn: "custom_fields"}
	case "deal":
		target = domain.FilterTableRef{Table: "deals", JSONColumn: "custom_fields"}
	default:
		target = domain.FilterTableRef{Table: "custom_object_records", JSONColumn: "data"}
	}
	return reportCatalogForDef(def, fields), target, nil
}
