package repository

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"crm-backend/internal/domain"
)

// report_templates_contract_test.go checks the SHIPPED frontend report templates
// against the backend catalogs they query.
//
// Why read a .ts file from a Go test: the templates are a frontend-only artifact
// (picking one just prefills the builder), but every field key in them is a
// backend contract. Nothing else enforces that. A Go test that reconstructs the
// templates as Go literals proves only that the transcription is self-consistent
// — rename `user_id` to `logged_by` in templates.ts and the Go copy still says
// `user_id`, vitest asserts nothing about template contents, and both suites go
// green while "Activities per Rep" 400s the moment anyone clicks it.
//
// Limited to the report-only objects on purpose: their catalogs are static
// descriptors this package can see, whereas contact/deal catalogs are assembled
// per-org from the registry in the usecase layer and cannot be resolved here.

var (
	tmplEntryRe  = regexp.MustCompile(`(?m)^\s*id:\s*'([a-z0-9-]+)',`)
	tmplSlugRe   = regexp.MustCompile(`objectSlug:\s*'([a-z_]+)'`)
	tmplFieldRe  = regexp.MustCompile(`field:\s*'([a-z0-9_]+)'`)
	tmplRuleRe   = regexp.MustCompile(`field:\s*'([a-z0-9_]+)',\s*operator:\s*'([a-z_]+)'`)
	tmplAggFnRe  = regexp.MustCompile(`fn:\s*'([a-z_]+)'`)
	tmplBucketRe = regexp.MustCompile(`bucket:\s*'([a-z]+)'`)
)

func TestFrontendReportTemplates_MatchBackendCatalogs(t *testing.T) {
	// Relative to this package directory (crm-backend/internal/repository). CI
	// checks out the whole monorepo, so a missing file is a real failure — never
	// a skip, which would silently retire this check.
	path := filepath.Join("..", "..", "..", "crm-frontend", "src", "features", "reports", "templates.ts")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read the shipped templates at %s: %v", path, err)
	}
	src := string(raw)

	// Split into per-template blocks: each runs from its `id:` line to the next.
	idx := tmplEntryRe.FindAllStringSubmatchIndex(src, -1)
	if len(idx) == 0 {
		t.Fatal("parsed zero templates — the file's shape changed and this test is no longer checking anything")
	}

	checked := map[string]int{} // report-only slug → templates validated
	for i, m := range idx {
		id := src[m[2]:m[3]]
		end := len(src)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		block := src[m[0]:end]

		slugMatch := tmplSlugRe.FindStringSubmatch(block)
		if slugMatch == nil {
			t.Errorf("template %q declares no objectSlug", id)
			continue
		}
		slug := slugMatch[1]
		ro := domain.ReportOnlyObjectBySlug(slug)
		if ro == nil {
			continue // a registry object — its catalog is not resolvable here
		}
		checked[slug]++

		catalog := make(map[string]domain.ReportField, len(ro.Fields))
		for _, f := range ro.Fields {
			catalog[f.Key] = f
		}

		// Every field key the template names must exist in the catalog.
		for _, fm := range tmplFieldRe.FindAllStringSubmatch(block, -1) {
			if _, ok := catalog[fm[1]]; !ok {
				t.Errorf("template %q (%s) references field %q, which is not in the backend catalog — this report would 400 on preview",
					id, slug, fm[1])
			}
		}

		// Every (field, operator) pair must be allowed for that field's type.
		for _, rm := range tmplRuleRe.FindAllStringSubmatch(block, -1) {
			f, ok := catalog[rm[1]]
			if !ok {
				continue // already reported above
			}
			if !reportOperatorsByType[f.Type][rm[2]] {
				t.Errorf("template %q (%s) uses operator %q on %q, which is a %s field — rejected by the builder",
					id, slug, rm[2], rm[1], f.Type)
			}
		}

		// Aggregate functions other than count need a number field.
		if am := tmplAggFnRe.FindStringSubmatch(block); am != nil {
			switch am[1] {
			case "sum", "avg", "min", "max":
				// The aggregate's own field is the last `field:` inside the
				// aggregate object; find it explicitly rather than guessing.
				if ai := strings.Index(block, "aggregate:"); ai >= 0 {
					if fm := tmplFieldRe.FindStringSubmatch(block[ai:]); fm != nil {
						if f := catalog[fm[1]]; f.Type != "number" {
							t.Errorf("template %q (%s) applies %s to %q, which is a %s field",
								id, slug, am[1], fm[1], f.Type)
						}
					}
				}
			}
		}

		// Date buckets only apply to date fields, and only to known buckets.
		for _, bm := range tmplBucketRe.FindAllStringSubmatch(block, -1) {
			if !reportBuckets[bm[1]] {
				t.Errorf("template %q (%s) uses unknown date bucket %q", id, slug, bm[1])
			}
		}
	}

	// Guard against a vacuous pass: if the parser stops matching, the loop above
	// silently validates nothing.
	for _, ro := range domain.ReportOnlyObjects {
		if checked[ro.Slug] == 0 {
			t.Errorf("no shipped template targets %q — either the R9.5 templates were removed, or this test stopped parsing them", ro.Slug)
		}
	}
}
