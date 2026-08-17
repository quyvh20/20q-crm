package schemaaudit

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"crm-backend/internal/domain"

	"gorm.io/gorm/schema"
)

// ============================================================
// Fixture drift — hand-rolled CREATE TABLEs vs the models
// ============================================================
//
// DB-backed tests build their own schemas with literal CREATE TABLE strings
// instead of running the migrations. gorm names EVERY column of a model in the
// SELECT it generates, so the moment a migration adds a column, every fixture
// that omits it starts failing with 42703 — and only in CI, because those tests
// are Docker-gated and Docker does not run on the maintainer's machine. That
// class has already bitten this repo three times: tasks.last_reminded_at (000074),
// deals.pipeline_id and the whole pipelines table (000075), and
// contacts.lead_score/lead_score_at (000076), each discovered from a red CI run
// rather than from anything local.
//
// This sweep finds them BEFORE they are reached. It is deliberately static — no
// database, no Docker, no container — so it runs in the ordinary unit shard and
// on any laptop, which is the whole point: the thing that made this class
// expensive was that it could only be observed somewhere inconvenient.
//
// It compares each fixture's declared columns against schema.DBNames, which is
// precisely the list gorm would name, so a finding here is exactly the query
// that would fail.
//
// A gap is NOT automatically a bug. Plenty of fixtures create a deliberately
// minimal table only to satisfy a foreign key or a row-scope join, and widening
// them would be noise. Those are recorded in knownMinimalFixtures with the
// reason; anything else is a finding, so this is a ratchet — today's state is
// frozen and new drift fails.
func TestFixtureDrift_HandRolledTablesMatchTheirModels(t *testing.T) {
	root := backendRoot(t)
	models := modelColumns(t)

	type finding struct {
		file, table string
		missing     []string
	}
	var findings []finding
	tablesSeen := map[string]bool{}
	fixtureCount := 0

	for _, file := range goTestFiles(t, root) {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		rel, _ := filepath.Rel(root, file)
		rel = filepath.ToSlash(rel)

		for _, fx := range parseCreateTables(string(src)) {
			cols, known := models[fx.table]
			if !known {
				// A table with no model (join tables like contact_tags, or a
				// fixture-only scratch table) has no authority to compare with.
				continue
			}
			fixtureCount++
			tablesSeen[fx.table] = true

			var missing []string
			for _, want := range cols {
				if !fx.columns[want] {
					missing = append(missing, want)
				}
			}
			if len(missing) > 0 {
				findings = append(findings, finding{file: rel, table: fx.table, missing: missing})
			}
		}
	}

	if fixtureCount == 0 {
		t.Fatal("no hand-rolled fixtures matched a known model — the parser or the model registry " +
			"is broken, and a sweep that inspects nothing would pass forever")
	}
	t.Logf("swept %d fixture tables across %d distinct tables", fixtureCount, len(tablesSeen))

	// Aggregate per (file, table): a file often builds the same table in several
	// helpers, and the count is kept so fixing four of six is still visible.
	type key struct{ file, table string }
	agg := map[key]map[string]bool{}
	counts := map[key]int{}
	for _, f := range findings {
		k := key{f.file, f.table}
		if agg[k] == nil {
			agg[k] = map[string]bool{}
		}
		for _, c := range f.missing {
			agg[k][c] = true
		}
		counts[k]++
	}
	var current []string
	for k, cols := range agg {
		names := make([]string, 0, len(cols))
		for c := range cols {
			names = append(names, c)
		}
		sort.Strings(names)
		current = append(current, k.file+"\t"+k.table+"\t"+itoa(counts[k])+"\t"+strings.Join(names, ","))
	}
	sort.Strings(current)

	// A RATCHET, not a wall. Widening all of these would be churn: most are FK
	// or join targets a test inserts one bare row into and never selects, and
	// several models carry NOT NULL columns a fixture insert would then have to
	// satisfy. What matters is that the set never GROWS silently — because it
	// grows exactly when a migration adds a column, which is the moment the
	// fixtures behind it become a CI failure nobody can reproduce locally.
	baselinePath := filepath.Join("testdata", "fixture_drift_baseline.txt")
	if os.Getenv("UPDATE_FIXTURE_BASELINE") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(baselinePath, []byte(strings.Join(current, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
		t.Logf("baseline rewritten with %d entries", len(current))
		return
	}

	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read %s: %v\nGenerate it with: UPDATE_FIXTURE_BASELINE=1 go test ./internal/schemaaudit/ -run TestFixtureDrift", baselinePath, err)
	}
	var baseline []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			baseline = append(baseline, line)
		}
	}
	sort.Strings(baseline)

	inBaseline := map[string]bool{}
	for _, l := range baseline {
		inBaseline[l] = true
	}
	inCurrent := map[string]bool{}
	for _, l := range current {
		inCurrent[l] = true
	}

	var grown, shrunk []string
	for _, l := range current {
		if !inBaseline[l] {
			grown = append(grown, l)
		}
	}
	for _, l := range baseline {
		if !inCurrent[l] {
			shrunk = append(shrunk, l)
		}
	}

	if len(grown) > 0 {
		t.Errorf("fixture drift GREW — %d entr(y|ies) are new or now miss more columns:\n  %s\n\n"+
			"This is the failure mode that has bitten three times (tasks.last_reminded_at, "+
			"deals.pipeline_id, contacts.lead_score): a column landed and the hand-rolled fixtures "+
			"behind it now fail in CI only, where Docker exists.\n"+
			"Add the column to those fixtures. Re-baseline "+
			"(UPDATE_FIXTURE_BASELINE=1) ONLY when the table is a deliberate stub the tests never "+
			"select.\n\nAlso removed (fixtures that improved — re-baseline to record):\n  %s",
			len(grown), strings.Join(grown, "\n  "), strings.Join(shrunk, "\n  "))
		return
	}
	if len(shrunk) > 0 {
		t.Errorf("%d fixture(s) improved — re-baseline to lock the gain in "+
			"(UPDATE_FIXTURE_BASELINE=1 go test ./internal/schemaaudit/ -run TestFixtureDrift):\n  %s",
			len(shrunk), strings.Join(shrunk, "\n  "))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// modelColumns maps table name → the columns gorm would name for that model.
// schema.Parse derives the table name itself, so no hand-maintained table→model
// mapping can drift out of date here.
func modelColumns(t *testing.T) map[string][]string {
	t.Helper()
	candidates := []any{
		&domain.Organization{}, &domain.User{}, &domain.OrgUser{}, &domain.OrgSettings{},
		&domain.Contact{}, &domain.Deal{}, &domain.Company{}, &domain.Task{}, &domain.Activity{},
		&domain.Tag{}, &domain.Role{}, &domain.RolePermission{},
		&domain.Pipeline{}, &domain.PipelineStage{},
		&domain.CustomObjectDef{}, &domain.CustomObjectRecord{},
		&domain.UserGroup{}, &domain.UserGroupMember{},
		&domain.RecordShare{}, &domain.ObjectDef{}, &domain.ObjectField{}, &domain.ObjectLink{},
		&domain.ObjectPermission{}, &domain.FieldPermission{},
		&domain.Report{}, &domain.ReportShare{}, &domain.Notification{},
		&domain.ListView{},
	}
	out := make(map[string][]string, len(candidates))
	cache := &sync.Map{}
	for _, m := range candidates {
		s, err := schema.Parse(m, cache, schema.NamingStrategy{})
		if err != nil {
			t.Fatalf("parse model %T: %v", m, err)
		}
		cols := append([]string(nil), s.DBNames...)
		sort.Strings(cols)
		out[s.Table] = cols
	}
	return out
}

type fixtureTable struct {
	table   string
	columns map[string]bool
}

// parseCreateTables pulls every literal CREATE TABLE out of a Go source file and
// returns the column names each one declares. Written by hand rather than with a
// regex because the body contains nested parens — NUMERIC(10,6), CHECK (...) —
// that only a depth scan gets right.
func parseCreateTables(src string) []fixtureTable {
	var out []fixtureTable
	upper := strings.ToUpper(src)
	const marker = "CREATE TABLE "

	for i := 0; ; {
		idx := strings.Index(upper[i:], marker)
		if idx < 0 {
			return out
		}
		pos := i + idx + len(marker)
		i = pos

		rest := src[pos:]
		restUpper := upper[pos:]
		if strings.HasPrefix(restUpper, "IF NOT EXISTS ") {
			skip := len("IF NOT EXISTS ")
			rest = rest[skip:]
			pos += skip
		}

		open := strings.IndexByte(rest, '(')
		if open < 0 {
			continue
		}
		name := strings.TrimSpace(rest[:open])
		name = strings.Trim(name, "\"`")
		// Prose in a comment ("a CREATE TABLE would ..."), not a statement.
		if name == "" || strings.ContainsAny(name, " \t\n") {
			continue
		}

		body, end := balancedBody(rest[open:])
		if end < 0 {
			continue
		}
		i = pos + open + end

		out = append(out, fixtureTable{table: name, columns: columnNames(body)})
	}
}

// balancedBody returns the contents between s[0]=='(' and its matching ')',
// plus the index just past that ')'. Quoted strings are skipped so a paren
// inside a default value cannot unbalance the scan.
func balancedBody(s string) (string, int) {
	depth := 0
	inQuote := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			inQuote = !inQuote
		case '(':
			if !inQuote {
				depth++
			}
		case ')':
			if !inQuote {
				depth--
				if depth == 0 {
					return s[1:i], i + 1
				}
			}
		}
	}
	return "", -1
}

// columnNames extracts the declared column names from a CREATE TABLE body:
// the first token of each top-level comma-separated item that is not a
// table-level constraint.
func columnNames(body string) map[string]bool {
	cols := map[string]bool{}
	for _, item := range splitTopLevel(body) {
		// Strip SQL line comments before looking for the name.
		var cleaned []string
		for _, line := range strings.Split(item, "\n") {
			if c := strings.Index(line, "--"); c >= 0 {
				line = line[:c]
			}
			cleaned = append(cleaned, line)
		}
		fields := strings.Fields(strings.Join(cleaned, " "))
		if len(fields) == 0 {
			continue
		}
		name := strings.Trim(fields[0], "\"`")
		switch strings.ToUpper(name) {
		case "PRIMARY", "UNIQUE", "CONSTRAINT", "FOREIGN", "CHECK", "EXCLUDE", "LIKE":
			continue
		}
		cols[strings.ToLower(name)] = true
	}
	return cols
}

func splitTopLevel(body string) []string {
	var items []string
	depth, inQuote, start := 0, false, 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\'':
			inQuote = !inQuote
		case '(':
			if !inQuote {
				depth++
			}
		case ')':
			if !inQuote {
				depth--
			}
		case ',':
			if !inQuote && depth == 0 {
				items = append(items, body[start:i])
				start = i + 1
			}
		}
	}
	return append(items, body[start:])
}

// backendRoot resolves crm-backend/ from this package's directory, so the sweep
// covers every package rather than only its own.
func backendRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve backend root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		t.Fatalf("crm-backend root not found at %s: %v", abs, err)
	}
	return abs
}

func goTestFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "vendor", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(info.Name(), "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}
