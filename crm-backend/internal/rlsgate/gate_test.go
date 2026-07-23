package rlsgate

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// moduleRoot returns the crm-backend module root (the dir containing go.mod). It
// tries two start points and walks each up to go.mod: the test's working
// directory (which `go test` sets to the package dir — always absolute) and the
// compiler-recorded source path from runtime.Caller. Using cwd first keeps the
// gate robust under -trimpath, where runtime.Caller yields a module-relative path
// that can't be stat-resolved. (Kept in-package, mirroring internal/authzgate, so
// each grep gate is self-contained.)
func moduleRoot(t *testing.T) string {
	t.Helper()
	var starts []string
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		starts = append(starts, filepath.Dir(file))
	}
	for _, dir := range starts {
		if !filepath.IsAbs(dir) {
			continue // a trimmed/relative path can't be walked reliably — skip it
		}
		for i := 0; i < 10; i++ {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	t.Fatal("rlsgate: go.mod not found walking up from the test cwd or source file")
	return ""
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("rlsgate: reading %s: %v", path, err)
	}
	return string(data)
}

func mainGoPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(moduleRoot(t), "cmd", "server", "main.go")
}

// sweepMarker is the distinctive fragment of the pg_class RLS backfill sweep in
// cmd/server/main.go (the `AND NOT c.relrowsecurity` catalog predicate). It
// appears nowhere else, so its presence is a reliable proxy for "the sweep is
// still installed".
const sweepMarker = "NOT c.relrowsecurity"

// TestRLSSweepBootGuardPresent fails if the RLS backfill sweep is removed from
// main.go. That sweep is the single thing standing between production and the
// rls_disabled_in_public alert: without it, every table whose RLS lived only in
// a dead migration ships with RLS off and is reachable through Supabase's
// PostgREST via the public anon key.
func TestRLSSweepBootGuardPresent(t *testing.T) {
	src := readFile(t, mainGoPath(t))
	if !strings.Contains(src, sweepMarker) {
		t.Fatalf("rlsgate: the RLS backfill sweep is missing from cmd/server/main.go " +
			"(no %q found).\nMigrations do not run on prod (golang-migrate dirty at v2), so " +
			"this boot guard is the ONLY thing that enables Row Level Security on the tables " +
			"whose RLS was stranded in migrations/000008 and /000013 — users, refresh_tokens, " +
			"org_users, contacts, chat_messages and the rest. Without it those tables are " +
			"readable and writable by anyone holding the Supabase anon key. Restore the sweep, " +
			"or — if you deliberately replaced it with an explicit per-table list — update this " +
			"gate and make sure TestEveryBootGuardTableHasRLS covers every created table.",
			sweepMarker)
	}
	if !strings.Contains(src, "ENABLE ROW LEVEL SECURITY") {
		t.Fatal("rlsgate: main.go references the RLS catalog predicate but no longer runs " +
			"ENABLE ROW LEVEL SECURITY — the sweep has been gutted.")
	}
}

// forceRLS matches the DDL that turns on FORCE ROW LEVEL SECURITY. The zero-policy
// RLS this codebase relies on is safe ONLY because the app connects as the table
// owner, and an owner keeps full access unless FORCE is set. Adding FORCE with no
// policies present locks the backend out of its own tables — a total outage. This
// is the one way the RLS remediation itself could take prod down, so it is
// forbidden outright.
var forceRLS = regexp.MustCompile(`(?i)FORCE ROW LEVEL SECURITY`)

// sourceFiles returns every non-test .go and .sql file under the given top-level
// dirs, skipping this gate's own package (its doc/test necessarily name the
// forbidden phrase).
func sourceFiles(t *testing.T, root string, dirs ...string) []string {
	t.Helper()
	var out []string
	for _, d := range dirs {
		base := filepath.Join(root, d)
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if filepath.Base(filepath.Dir(path)) == "rlsgate" {
				return nil
			}
			isGo := strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
			isSQL := strings.HasSuffix(path, ".sql")
			if isGo || isSQL {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("rlsgate: walking %s: %v", base, err)
		}
	}
	return out
}

// stripComments removes trailing line comments (`//` for Go, `--` for SQL) so a
// comment that merely mentions FORCE ROW LEVEL SECURITY — like the explanatory
// note next to the sweep in main.go — cannot trip the guard. Crude (ignores the
// delimiter inside string literals), which is fine here: no FORCE-bearing DDL in
// this tree carries `//` or `--` on the same line inside a string.
func stripComments(src, path string) string {
	delim := "//"
	if strings.HasSuffix(path, ".sql") {
		delim = "--"
	}
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if j := strings.Index(line, delim); j >= 0 {
			lines[i] = line[:j]
		}
	}
	return strings.Join(lines, "\n")
}

// TestNoForceRowLevelSecurity fails if any Go or SQL source under cmd/, internal/
// or migrations/ enables FORCE ROW LEVEL SECURITY.
func TestNoForceRowLevelSecurity(t *testing.T) {
	root := moduleRoot(t)
	files := sourceFiles(t, root, "cmd", "internal", "migrations")
	if len(files) == 0 {
		t.Fatal("rlsgate: found no source files to scan — the walker is misconfigured")
	}
	for _, path := range files {
		content := stripComments(readFile(t, path), path)
		if forceRLS.MatchString(content) {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("rlsgate: %s enables FORCE ROW LEVEL SECURITY. The app connects as the "+
				"table owner and relies on the owner's RLS bypass; with zero policies present, "+
				"FORCE locks the backend out of its own tables (total outage). Remove it.",
				filepath.ToSlash(rel))
		}
	}
}

var (
	reCreate    = regexp.MustCompile(`(?i)CREATE TABLE IF NOT EXISTS\s+(\w+)`)
	reEnableRLS = regexp.MustCompile(`(?i)ALTER TABLE\s+(?:IF EXISTS\s+)?(?:public\.)?(\w+)\s+ENABLE ROW LEVEL SECURITY`)
)

func tableSet(re *regexp.Regexp, src string) map[string]bool {
	out := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		out[strings.ToLower(m[1])] = true
	}
	return out
}

// TestEveryBootGuardTableHasRLS is the fallback for the per-table world. While the
// pg_class sweep is installed it secures every public table at boot, so the
// per-table requirement holds by construction and this test skips (the sweep
// itself is pinned by TestRLSSweepBootGuardPresent). If a future change replaces
// the sweep with an explicit list, this activates and fails for any CREATE TABLE
// boot guard that lacks a matching ENABLE ROW LEVEL SECURITY.
func TestEveryBootGuardTableHasRLS(t *testing.T) {
	path := mainGoPath(t)
	src := readFile(t, path)
	if strings.Contains(src, sweepMarker) {
		t.Skip("rlsgate: pg_class sweep present — every boot-guard table is secured at boot")
	}
	// Parse the DDL, not the prose: main.go's comments quote "CREATE TABLE IF NOT
	// EXISTS" and "ENABLE ROW LEVEL SECURITY" when explaining the guards, which
	// would otherwise show up as phantom tables named "here"/"above"/"never".
	code := stripComments(src, path)
	created := tableSet(reCreate, code)
	if len(created) == 0 {
		t.Fatal("rlsgate: no CREATE TABLE IF NOT EXISTS boot guards found in main.go — the " +
			"parser is stale or the file was restructured")
	}
	secured := tableSet(reEnableRLS, code)
	for tbl := range created {
		if !secured[tbl] {
			t.Errorf("rlsgate: main.go boot-guards CREATE TABLE %q but never enables RLS on it. "+
				"Add `ALTER TABLE %s ENABLE ROW LEVEL SECURITY` alongside it (or restore the "+
				"pg_class sweep). A public table with RLS off is readable and writable by anyone "+
				"holding the Supabase anon key.", tbl, tbl)
		}
	}
}
