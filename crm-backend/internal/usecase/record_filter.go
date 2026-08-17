package usecase

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// Record-list filter engine (filtering overhaul F2/F3)
// ============================================================
//
// RecordService.List accepts two filter inputs: the structured `filter` AST
// (the same JSON grammar reports and automation conditions speak) and the
// legacy flat ?field=value params. Both resolve here, against the SAME
// registry+virtual-fields catalog reports compile against, and both come out
// as ONE CompiledFilter fragment the repositories AND onto their chains.
//
// This kills the silent-empty bug class (sort_by pre-R7.3, pipeline_id R9.3,
// lead_score R9.4): a filter key that resolves nowhere is now a 400, and a
// NATIVE key (contact email, deal title/value…) that used to fall through to a
// `custom_fields ->> key` test that matched nothing now compiles against its
// real column and actually works.

// typedListFilterKeys names the in.Filters keys each system adapter binds to a
// typed column itself (record_service_system.go). They are excluded from
// catalog compilation so the adapters' existing behaviour — including
// filterUUID's tolerant unparseable-value drop, which the kanban board and
// automation find_records rely on — is preserved byte-for-byte. Everything
// else is resolved through the object's field catalog.
var typedListFilterKeys = map[string]map[string]bool{
	"contact": {"company": true, "owner_user_id": true},
	"deal":    {"stage": true, "contact": true, "company": true, "owner_user_id": true, "pipeline_id": true},
	"company": {},
}

// SetFilterRegistry wires the registry the filter engine builds field catalogs
// from. Called once at startup (mirrors SetNumberRepo/SetEventEmitter). Until
// set — nil in unit tests that don't exercise filtering — legacy ?field=value
// params keep their historical pass-through behaviour and structured ASTs are
// rejected, so no test silently changes meaning.
func (s *recordService) SetFilterRegistry(reg domain.ObjectRegistryRepository) {
	s.filterRegistry = reg
}

// compileListFilter validates and compiles in.Filter plus every non-typed
// legacy filter param into in.Compiled, removing the consumed keys from
// in.Filters. It enforces, in order:
//
//  1. FLS — a filter on a hidden field is a 403 BEFORE any catalog work.
//     Filter keys used to bypass the field mask entirely, which made result
//     membership an equality oracle on hidden values; richer operators
//     (ranges, contains) would have upgraded that to efficient bisection.
//  2. catalog resolution — unknown field or operator, bad value, over-deep
//     nesting: 400, never a silently empty list.
func (s *recordService) compileListFilter(ctx context.Context, orgID uuid.UUID, slug string, in *domain.RecordListInput) error {
	// FLS gate FIRST, over EVERY filter key — the AST's fields and every
	// non-blank legacy param INCLUDING the typed adapter keys. The typed keys
	// must not skip this: "company" on contact and stage/contact/company on
	// deal are registry fields an admin can FLS-hide, and ?company=<uuid> is a
	// membership oracle on the hidden relation even though the response masks
	// the value on each row. Gating only the catalog-compiled keys would leave
	// the two filter languages inconsistent — 403 through the AST, 200 through
	// the legacy spelling of the same predicate. Needs no catalog, so it holds
	// even when the registry is unwired.
	touched := map[string]bool{}
	collectReportFilterFields(in.Filter, touched)
	for k, v := range in.Filters {
		if k != "" && strings.TrimSpace(v) != "" {
			touched[k] = true
		}
	}
	if len(touched) == 0 {
		return nil
	}
	if mask := s.fieldMask(ctx, orgID, slug); !mask.Empty() {
		for key := range touched {
			if mask.IsHidden(key) {
				return domain.NewAppError(http.StatusForbidden, "the \""+key+"\" field is restricted for your role and cannot be filtered on")
			}
		}
	}

	typed := typedListFilterKeys[slug]
	var leftovers map[string]string
	for k, v := range in.Filters {
		if k == "" || strings.TrimSpace(v) == "" || (typed != nil && typed[k]) {
			continue
		}
		if leftovers == nil {
			leftovers = map[string]string{}
		}
		leftovers[k] = v
	}
	if in.Filter == nil && len(leftovers) == 0 {
		return nil
	}

	if s.filterRegistry == nil {
		if in.Filter != nil {
			return domain.NewAppError(http.StatusBadRequest, "structured filters are not available")
		}
		// Legacy pass-through: the adapters' CustomFilters path handles the
		// leftovers exactly as before this engine existed.
		return nil
	}

	catalog, target, err := s.filterCatalog(ctx, orgID, slug)
	if err != nil {
		return err
	}

	// Merge: AND( original AST, eq-leaves for the legacy params ). The original
	// group nests as one rule so its own AND/OR semantics are preserved.
	group := in.Filter
	if len(leftovers) > 0 {
		merged := domain.ReportFilterGroup{Op: "AND"}
		if group != nil {
			merged.Rules = append(merged.Rules, domain.ReportFilterRule{
				Field: group.Field, Operator: group.Operator, Value: group.Value,
				Op: group.Op, Rules: group.Rules,
			})
		}
		keys := make([]string, 0, len(leftovers))
		for k := range leftovers {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic SQL for logs, tests and plan caching
		for _, k := range keys {
			merged.Rules = append(merged.Rules, domain.ReportFilterRule{Field: k, Operator: "eq", Value: leftovers[k]})
		}
		group = &merged
	}

	compiled, err := domain.CompileRecordFilter(target, catalog, group)
	if err != nil {
		return domain.NewAppError(http.StatusBadRequest, err.Error())
	}
	in.Compiled = compiled
	// The engine owns the leftover keys now; leaving them in Filters would ALSO
	// run the legacy `custom_fields ->> key` test and empty the result for
	// native fields. Rebuild rather than delete: the map belongs to the caller
	// (ExportCSV reuses one input across pages), so mutating it would strip the
	// filters from every page after the first.
	if len(leftovers) > 0 {
		kept := make(map[string]string, len(in.Filters))
		for k, v := range in.Filters {
			if _, consumed := leftovers[k]; !consumed {
				kept[k] = v
			}
		}
		in.Filters = kept
	}
	return nil
}

// filterCatalog resolves an object's filterable-field catalog (registry fields
// + virtual fields, via the same reportCatalogForDef reports use) and the
// physical table the filter compiles against.
func (s *recordService) filterCatalog(ctx context.Context, orgID uuid.UUID, slug string) ([]domain.ReportField, domain.FilterTableRef, error) {
	reg := s.filterRegistry
	if err := reg.EnsureSystemObjects(ctx, orgID); err != nil {
		return nil, domain.FilterTableRef{}, err
	}
	def, err := reg.GetDefBySlug(ctx, orgID, slug)
	if err != nil {
		return nil, domain.FilterTableRef{}, err
	}
	if def == nil {
		return nil, domain.FilterTableRef{}, domain.NewAppError(http.StatusBadRequest, "unknown object \""+slug+"\"")
	}
	fields, err := reg.ListFields(ctx, def.ID)
	if err != nil {
		return nil, domain.FilterTableRef{}, err
	}

	var target domain.FilterTableRef
	if _, isSystem := s.systemAdapters[slug]; isSystem {
		switch slug {
		case "contact":
			target = domain.FilterTableRef{Table: "contacts", JSONColumn: "custom_fields"}
		case "company":
			target = domain.FilterTableRef{Table: "companies", JSONColumn: "custom_fields"}
		case "deal":
			target = domain.FilterTableRef{Table: "deals", JSONColumn: "custom_fields"}
		}
	} else {
		target = domain.FilterTableRef{Table: "custom_object_records", JSONColumn: "data"}
	}
	return reportCatalogForDef(def, fields), target, nil
}
