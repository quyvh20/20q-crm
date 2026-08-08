package usecase

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// R9.5 — report-only objects (task, activity)
// ============================================================

// The whole feature rides on one branch in catalogFor. If it stops firing, the
// registry lookup takes over and every entry point 404s "object not found".
func TestCatalogFor_ReportOnlyObjectsResolveWithoutTheRegistry(t *testing.T) {
	env := newReportEnv()
	uc := env.uc.(*reportUseCase)

	for _, ro := range domain.ReportOnlyObjects {
		def, catalog, err := uc.catalogFor(context.Background(), env.orgID, ro.Slug)
		if err != nil {
			t.Fatalf("%s: %v", ro.Slug, err)
		}
		// The runner reads exactly these three to resolve the physical table.
		if def.Storage != "table" {
			t.Errorf("%s: storage = %q, want table", ro.Slug, def.Storage)
		}
		if def.RecordTable == nil || *def.RecordTable != ro.Table {
			t.Errorf("%s: record table mismatch", ro.Slug)
		}
		if def.Slug != ro.Slug {
			t.Errorf("%s: slug mismatch", ro.Slug)
		}
		if len(catalog) != len(ro.Fields) {
			t.Errorf("%s: catalog has %d fields, descriptor has %d", ro.Slug, len(catalog), len(ro.Fields))
		}
	}
}

// An org that created a CUSTOM object slugged "task" before R9.5 shipped still
// has one — CreateDef reserves the slug going forward, but it cannot retract
// what already exists. The registry def must win: resolving to the synthetic
// descriptor instead would give that org a report that RUNS, returns rows, and
// is about an entirely different table.
func TestCatalogFor_ExistingCustomObjectWinsOverReportOnlySlug(t *testing.T) {
	env := newReportEnv()
	uc := env.uc.(*reportUseCase)

	reg := uc.registry.(*fakeRegistryRepo)
	customID := uuid.MustParse("aaaaaaaa-0000-0000-0000-0000000075a5")
	reg.defs = append(reg.defs, domain.ObjectDef{
		ID: customID, Slug: "task", Label: "Job Ticket", LabelPlural: "Job Tickets",
		IsSystem: false, Storage: "jsonb",
	})
	reg.fields[customID] = []domain.ObjectField{
		{Key: "status", Label: "Status", Type: "select", StorageKind: "jsonb"},
	}

	def, catalog, err := uc.catalogFor(context.Background(), env.orgID, "task")
	if err != nil {
		t.Fatalf("catalogFor: %v", err)
	}
	if def.Storage != "jsonb" || def.Label != "Job Ticket" {
		t.Fatalf("the org's own custom object must win, got %+v", def)
	}
	for _, f := range catalog {
		if f.Column == "assigned_to" {
			t.Fatal("the synthetic task descriptor hijacked a real custom object")
		}
	}

	// ...and the picker must not list the slug twice.
	objects, err := env.uc.ListObjects(context.Background(), env.orgID)
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	n := 0
	for _, o := range objects {
		if o.Slug == "task" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("slug 'task' appears %d times in the object picker, want 1", n)
	}
}

// Catalog() must hand out a copy. reportCatalogForDef's callers append virtual
// fields to what they receive, and a shared backing array would let one request
// mutate the package-level descriptor for the whole process.
func TestReportOnlyObject_CatalogIsACopy(t *testing.T) {
	ro := domain.ReportOnlyObjectBySlug("task")
	if ro == nil {
		t.Fatal("task descriptor missing")
	}
	before := len(ro.Fields)
	c := ro.Catalog()
	c = append(c, domain.ReportField{Key: "injected", Type: "text", Column: "x"})
	if len(c) == before {
		t.Fatal("append did not grow the copy")
	}
	c[0] = domain.ReportField{Key: "clobbered"}
	if domain.ReportOnlyObjectBySlug("task").Fields[0].Key == "clobbered" {
		t.Error("mutating the returned catalog corrupted the shared descriptor")
	}
	if len(domain.ReportOnlyObjectBySlug("task").Fields) != before {
		t.Error("appending to the returned catalog grew the shared descriptor")
	}
}

// OLS is the ONLY gate on these objects — they have no record page, no list
// page and no create path, so if Authorize is not consulted here nothing else
// stops a denied role from reporting on the whole org's rep activity.
func TestReportOnlyObjects_RespectOLSDenial(t *testing.T) {
	env := newReportEnv()
	env.authz.deny = map[string]bool{"task:read": true}

	if _, err := env.uc.Preview(context.Background(), env.orgID, "task", domain.ReportConfig{
		Chart:     domain.ReportChartBar,
		GroupBy:   &domain.ReportGroupBy{Field: "assigned_to"},
		Aggregate: &domain.ReportAggregate{Fn: "count"},
	}); err == nil {
		t.Fatal("a role denied read on task must not be able to preview a task report")
	}
	if env.runner.called {
		t.Error("the runner ran despite the OLS denial")
	}
}

// The builder's picker must not offer an object whose preview would immediately
// 403 — and must offer the report-only ones, which the registry cannot supply.
func TestListObjects_OLSGatedAndIncludesReportOnly(t *testing.T) {
	env := newReportEnv()

	objects, err := env.uc.ListObjects(context.Background(), env.orgID)
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	got := map[string]domain.ReportObjectInfo{}
	for _, o := range objects {
		got[o.Slug] = o
	}
	if _, ok := got["deal"]; !ok {
		t.Error("registry objects must still be listed")
	}
	for _, ro := range domain.ReportOnlyObjects {
		o, ok := got[ro.Slug]
		if !ok {
			t.Fatalf("%q missing from the report object list", ro.Slug)
		}
		if !o.ReportOnly {
			t.Errorf("%q must be flagged report_only so the UI doesn't imply a record page", ro.Slug)
		}
		if o.LabelPlural == "" {
			t.Errorf("%q has no plural label — the picker renders it", ro.Slug)
		}
	}

	env2 := newReportEnv()
	env2.authz.deny = map[string]bool{"activity:read": true, "deal:read": true}
	objects2, err := env2.uc.ListObjects(context.Background(), env2.orgID)
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	for _, o := range objects2 {
		if o.Slug == "activity" || o.Slug == "deal" {
			t.Errorf("%q was denied but still offered in the picker", o.Slug)
		}
	}
}

// ListFields is what the builder renders as the field picker. It runs through
// the same catalogFor branch, so this is the check that a descriptor change
// reaches the UI.
func TestListFields_ReportOnlyObjects(t *testing.T) {
	env := newReportEnv()
	fields, err := env.uc.ListFields(context.Background(), env.orgID, "activity")
	if err != nil {
		t.Fatalf("ListFields: %v", err)
	}
	keys := map[string]string{}
	for _, f := range fields {
		keys[f.Key] = f.Type
	}
	if keys["user_id"] != "relation" {
		t.Errorf("activity must expose user_id as a relation, got %q", keys["user_id"])
	}
	if keys["duration_minutes"] != "number" {
		t.Errorf("duration_minutes must be a number so sum/avg work, got %q", keys["duration_minutes"])
	}
	// Deliberate omissions — see the note in domain/report_objects.go. body is
	// full sent-email HTML since R9.1; contact/deal would resolve names org-wide.
	for _, banned := range []string{"body", "sentiment", "contact", "deal", "contact_id", "deal_id", "owner_user_id"} {
		if _, ok := keys[banned]; ok {
			t.Errorf("activity catalog must not expose %q", banned)
		}
	}
}

func TestListFields_TaskOmitsScannerInternals(t *testing.T) {
	env := newReportEnv()
	fields, err := env.uc.ListFields(context.Background(), env.orgID, "task")
	if err != nil {
		t.Fatalf("ListFields: %v", err)
	}
	for _, f := range fields {
		switch f.Key {
		case "last_reminded_at":
			t.Error("last_reminded_at is reminder-scanner bookkeeping, not a report field")
		case "owner_user_id":
			t.Error("tasks have no owner_user_id column — this would 500 at runtime")
		case "contact", "deal", "contact_id", "deal_id":
			t.Errorf("%q resolves labels org-wide; see domain/report_objects.go", f.Key)
		}
	}
}

// A saved report over a report-only object must survive the full Create path,
// which is where validateReportConfig runs against the descriptor catalog.
func TestCreateReport_OverReportOnlyObject(t *testing.T) {
	env := newReportEnv()
	rep, err := env.uc.Create(context.Background(), env.orgID, env.orgID, domain.ReportInput{
		Name:       "Activities per rep",
		ObjectSlug: "activity",
		Visibility: domain.ReportVisibilityOrg,
		Config: domain.ReportConfig{
			Chart:     domain.ReportChartBar,
			GroupBy:   &domain.ReportGroupBy{Field: "user_id"},
			Aggregate: &domain.ReportAggregate{Fn: "count"},
			Filters: &domain.ReportFilterGroup{
				Op:    "AND",
				Rules: []domain.ReportFilterRule{{Field: "type", Operator: "neq", Value: "stage_change"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rep.ObjectSlug != "activity" {
		t.Errorf("object slug = %q", rep.ObjectSlug)
	}
}

// A config naming a field the descriptor withholds must be rejected, not
// silently dropped — otherwise "group by body" would quietly become something
// else and an operator would think the omission never happened.
func TestCreateReport_RejectsWithheldReportOnlyField(t *testing.T) {
	env := newReportEnv()
	_, err := env.uc.Create(context.Background(), env.orgID, env.orgID, domain.ReportInput{
		Name:       "Nope",
		ObjectSlug: "activity",
		Visibility: domain.ReportVisibilityPrivate,
		Config: domain.ReportConfig{
			Chart:     domain.ReportChartBar,
			GroupBy:   &domain.ReportGroupBy{Field: "body"},
			Aggregate: &domain.ReportAggregate{Fn: "count"},
		},
	})
	if err == nil {
		t.Fatal("grouping by a field outside the catalog must fail")
	}
}
