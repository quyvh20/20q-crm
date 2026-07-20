package integrations

import (
	"strings"
	"testing"
)

// The origin allowlist is the one piece of L4 that, done wrong, either breaks every
// customer's form or opens credentialed CORS to arbitrary sites. These pin the
// decisions rather than the implementation.

func TestNormalizeOrigin(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain https", "https://example.com", "https://example.com"},
		{"uppercased host", "HTTPS://Example.COM", "https://example.com"},
		{"trailing slash is not part of an origin", "https://example.com/", "https://example.com"},
		{"port is part of an origin", "http://localhost:3000", "http://localhost:3000"},
		{"whitespace", "  https://example.com  ", "https://example.com"},

		// Everything below must be UNUSABLE, and each for its own reason.
		{"empty", "", ""},
		{"bare hostname has no scheme to compare", "example.com", ""},
		// Browsers send "null" for sandboxed iframes and file:// pages. Allowlisting
		// it would let any such context post — a wildcard wearing a disguise.
		{"the literal null", "null", ""},
		{"NULL in any casing", "NuLl", ""},
		// A wildcard here would be matched by a naive HasSuffix, which also matches
		// evilexample.com. Refuse until it is written carefully.
		{"wildcard", "https://*.example.com", ""},
		// A pasted page URL means the admin has not understood the field; keeping
		// only the origin part would hide that from them.
		{"a page URL, not an origin", "https://example.com/contact", ""},
		{"query string", "https://example.com?a=b", ""},
		{"non-http scheme", "ftp://example.com", ""},
		{"javascript scheme", "javascript:alert(1)", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizeOrigin(c.in); got != c.want {
				t.Errorf("NormalizeOrigin(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// The single most likely way this feature could silently fail open: an empty list
// is the state of EVERY newly created source, so it must deny rather than allow.
func TestOriginAllowed_EmptyListDeniesEverything(t *testing.T) {
	for _, origin := range []string{"https://example.com", "https://evil.example", "null", ""} {
		if OriginAllowed(nil, origin) {
			t.Fatalf("an unconfigured source must accept no browser origin, but allowed %q", origin)
		}
		if OriginAllowed([]string{}, origin) {
			t.Fatalf("an empty allowlist must accept no browser origin, but allowed %q", origin)
		}
	}
}

func TestOriginAllowed(t *testing.T) {
	allowed := []string{"https://example.com", "http://localhost:3000"}

	for _, ok := range []string{
		"https://example.com",
		"https://example.com/", // a browser never sends this, but normalization must agree anyway
		"HTTPS://EXAMPLE.COM",
		"http://localhost:3000",
	} {
		if !OriginAllowed(allowed, ok) {
			t.Errorf("%q should be allowed", ok)
		}
	}

	for _, no := range []string{
		"https://evil.example",
		// Scheme is part of an origin: an http page is not the https one.
		"http://example.com",
		// A different port is a different origin.
		"http://localhost:3001",
		// The prefix/suffix traps a naive matcher falls into.
		"https://example.com.evil.test",
		"https://notexample.com",
		"https://sub.example.com",
		"null",
		"",
	} {
		if OriginAllowed(allowed, no) {
			t.Errorf("%q must NOT be allowed", no)
		}
	}
}

func TestValidateAllowedOrigins(t *testing.T) {
	got, err := ValidateAllowedOrigins([]string{"HTTPS://Example.com/", " https://a.test ", "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	// Normalized, and the duplicate collapsed rather than failing the save.
	want := []string{"https://example.com", "https://a.test"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}

	// A typo must fail CLOSED and in front of the admin — a silently dropped origin
	// would look like a working config and reject every real submission.
	if _, err := ValidateAllowedOrigins([]string{"example.com"}); err == nil {
		t.Fatal("a bare hostname must be refused with an explanation")
	}
	if _, err := ValidateAllowedOrigins([]string{"https://*.example.com"}); err == nil {
		t.Fatal("a wildcard must be refused in v1")
	}
	if _, err := ValidateAllowedOrigins([]string{"null"}); err == nil {
		t.Fatal("the literal null must never be allowlistable")
	}

	many := make([]string, maxAllowedOrigins+1)
	for i := range many {
		many[i] = "https://a.test"
	}
	if _, err := ValidateAllowedOrigins(many); err == nil {
		t.Fatal("an absurd list must be bounded")
	}
}

// The skip prefix and the routes must never drift into two spellings — a mismatch
// means the global handler 403s every submission before our handler runs, which is
// invisible to curl and to same-origin tests.
func TestFormCapturePrefixMatchesRoutes(t *testing.T) {
	if FormCapturePrefix != "/api/capture/forms" {
		t.Fatalf("prefix changed to %q — main.go's global-CORS skip must change with it", FormCapturePrefix)
	}
	if !strings.HasPrefix(FormCapturePrefix+"/:public_token", FormCapturePrefix) {
		t.Fatal("the route no longer sits under the skipped prefix")
	}
}
