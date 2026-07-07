package authzgate

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
// that can't be stat-resolved.
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
	t.Fatal("authzgate: go.mod not found walking up from the test cwd or source file")
	return ""
}

// appGoFiles returns every non-test .go file under the given top-level dirs.
func appGoFiles(t *testing.T, root string, dirs ...string) []string {
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
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			// Skip this gate's own package: it necessarily names the forbidden
			// artifacts (in the test file, and describing them in doc.go), so scanning
			// it would flag the guard's own documentation.
			if filepath.Base(filepath.Dir(path)) == "authzgate" {
				return nil
			}
			out = append(out, path)
			return nil
		})
		if err != nil {
			t.Fatalf("authzgate: walking %s: %v", base, err)
		}
	}
	return out
}

// forbiddenArtifacts are the exact identifiers/log-keys of the name-keyed
// authorization bridges deleted in P9. Their reappearance in any app source file
// (outside tests) means a bridge was resurrected — fail the build with a pointer
// to the plan phase that removed it.
var forbiddenArtifacts = []struct {
	needle string
	why    string
}{
	{"func RequireRole(", "the hardcoded role-name middleware was deleted in P9 — gate routes with RequireCapability/RequireObjectAccess instead"},
	{"roleIDByName", "the P5 permission-cache name→id bridge index was deleted in P9 — authorize by caller.RoleID"},
	{"authz_name_fallback", "the P5 name-fallback WARN was deleted in P9 — a caller with no RoleID is default-denied, not name-resolved"},
	{"logAuthzDivergence", "the P7/P8 authorization-divergence shadow was deleted in P9 — enforce the single (capability/OLS) decision"},
	{"ai.authz.divergence", "the P7 AI name-switch shadow log was deleted in P9"},
	{"automation.authz.divergence", "the P8 automation actor shadow log was deleted in P9"},
}

// TestNoResurrectedAuthzBridges fails if any P9-removed name-keyed authorization
// artifact reappears in the app packages (internal/ + cmd/, excluding tests).
func TestNoResurrectedAuthzBridges(t *testing.T) {
	root := moduleRoot(t)
	files := appGoFiles(t, root, "internal", "cmd")
	if len(files) == 0 {
		t.Fatal("authzgate: found no app .go files to scan — the walker is misconfigured")
	}

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("authzgate: reading %s: %v", path, err)
		}
		content := string(data)
		rel, _ := filepath.Rel(root, path)
		for _, f := range forbiddenArtifacts {
			if strings.Contains(content, f.needle) {
				t.Errorf("authzgate: %s contains removed authorization artifact %q — %s", filepath.ToSlash(rel), f.needle, f.why)
			}
		}
	}
}

// enforcementFiles are the identity-enforcement sites that must authorize purely
// by role id / owner flag. After P9 none of them may compare a role NAME. Kept
// as an explicit, reviewed allowlist (relative to the module root) rather than a
// tree-wide sweep, because display/persona helpers and seeders legitimately name
// roles — this pins the load-bearing decision points, not prose.
var enforcementFiles = []string{
	"internal/usecase/permission_usecase.go",
	"internal/usecase/object_layout_usecase.go",
	"internal/repository/object_layout_repository.go",
	"internal/domain/caller.go",
	"internal/delivery/http/middleware.go",
}

// roleNameComparisons matches raw role-name equality/inequality. Each requires a
// role-name-shaped right-hand side (a string literal or a Role* constant) so it
// flags name authorization, NOT a `*domain.Role` pointer nil-check like
// `ou.Role != nil`. The `.Role`/`UserRole` selectors are word-bounded so
// `.RoleID`/`.RoleName` don't trip them. The `domain.` qualifier is optional so an
// in-package comparison inside package domain itself (e.g. caller.go writing
// `== RoleOwner`) is caught too.
var roleNameComparisons = []*regexp.Regexp{
	regexp.MustCompile(`\.Role\b\s*(==|!=)\s*"`),
	regexp.MustCompile(`\bUserRole\b\s*(==|!=)\s*"`),
	regexp.MustCompile(`(==|!=)\s*(domain\.)?Role(Owner|Admin|Manager|Sales|Viewer)\b`),
	regexp.MustCompile(`(==|!=)\s*"(owner|admin|manager|sales_rep|viewer)"`),
}

// stripComment removes a line's trailing // comment so a comment that mentions a
// role name can't trip the regex. Crude (ignores // inside string literals), but
// the enforcement files carry no such literals on comparison lines.
func stripComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

// TestEnforcementFilesHaveNoRoleNameComparisons fails if a core enforcement file
// authorizes by role NAME instead of role id / owner flag (plan §3.1 grep gate).
func TestEnforcementFilesHaveNoRoleNameComparisons(t *testing.T) {
	root := moduleRoot(t)
	for _, rel := range enforcementFiles {
		path := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("authzgate: enforcement file %s is missing or unreadable (%v) — was it renamed? update enforcementFiles", rel, err)
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			code := stripComment(line)
			for _, re := range roleNameComparisons {
				if re.MatchString(code) {
					t.Errorf("authzgate: %s:%d compares a role NAME (%q) — authorize by caller.RoleID / caller.IsOwner instead:\n    %s",
						rel, i+1, re.String(), strings.TrimSpace(line))
				}
			}
		}
	}
}
