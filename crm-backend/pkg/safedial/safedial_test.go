package safedial

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestIsBlocked(t *testing.T) {
	blocked := []struct{ addr, why string }{
		{"127.0.0.1", "loopback"},
		{"127.1.2.3", "the whole 127/8 block, not just .0.1"},
		{"::1", "IPv6 loopback"},
		{"10.0.0.5", "RFC1918"},
		{"172.16.0.1", "RFC1918 (the 172.16/12 block people forget)"},
		{"172.31.255.254", "top of the 172.16/12 block"},
		{"192.168.1.1", "RFC1918"},
		{"169.254.169.254", "cloud instance metadata — the prize target"},
		{"169.254.0.1", "link-local generally"},
		{"fe80::1", "IPv6 link-local"},
		{"0.0.0.0", "unspecified"},
		{"::", "IPv6 unspecified"},
		{"224.0.0.1", "multicast"},
		{"ff02::1", "IPv6 multicast"},
		{"fd00::1", "IPv6 unique-local"},
		// The bypass a naive check misses: the same private addresses spelled as
		// IPv4-mapped IPv6.
		{"::ffff:127.0.0.1", "IPv4-mapped loopback"},
		{"::ffff:169.254.169.254", "IPv4-mapped metadata address"},
		{"::ffff:10.0.0.1", "IPv4-mapped RFC1918"},
	}
	for _, tc := range blocked {
		t.Run(tc.addr, func(t *testing.T) {
			a, err := netip.ParseAddr(tc.addr)
			if err != nil {
				t.Fatalf("bad fixture: %v", err)
			}
			got, reason := IsBlocked(a)
			if !got {
				t.Fatalf("%s must be blocked (%s)", tc.addr, tc.why)
			}
			if reason == "" {
				t.Fatal("a blocked address must carry a reason the user can act on")
			}
		})
	}

	// Public addresses must still work — a guard that blocks everything is not a
	// guard, it is an outage.
	for _, addr := range []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700::1111"} {
		t.Run("allowed/"+addr, func(t *testing.T) {
			a, _ := netip.ParseAddr(addr)
			if blocked, reason := IsBlocked(a); blocked {
				t.Fatalf("%s must be reachable, got blocked as %q", addr, reason)
			}
		})
	}
}

func TestValidateURL(t *testing.T) {
	bad := map[string]string{
		"file:///etc/passwd":            "file scheme",
		"gopher://x/":                   "gopher scheme",
		"ftp://example.com/":            "ftp scheme",
		"http://user:pw@example.com/":   "embedded credentials",
		"https://:secret@example.com/":  "embedded credentials, password only",
		"http://":                       "no host",
		"://nonsense":                   "unparseable",
	}
	for raw, why := range bad {
		t.Run(raw, func(t *testing.T) {
			if err := ValidateURL(raw); err == nil {
				t.Fatalf("%s must be refused (%s)", raw, why)
			} else if !IsBlockedErr(err) {
				t.Fatalf("refusal must be classifiable via IsBlockedErr, got %v", err)
			}
		})
	}

	// A private HOST is deliberately allowed through here: the string check cannot
	// be the control (DNS rebinding, redirects), so the dialer is what refuses it.
	// Rejecting here too would only give a false sense of where the guard lives.
	for _, raw := range []string{
		"https://example.com/hook",
		"http://example.com:8080/hook?token=abc",
		"http://127.0.0.1/",
	} {
		t.Run("passes-url-check/"+raw, func(t *testing.T) {
			if err := ValidateURL(raw); err != nil {
				t.Fatalf("%s should pass the URL-shape check, got %v", raw, err)
			}
		})
	}
}

// The end-to-end proof: a real client aimed at a real server on loopback.
func TestClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"secret":"internal"}`))
	}))
	defer srv.Close()

	resp, err := NewClient(Options{}).Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("the guard let a loopback request through")
	}
	if !IsBlockedErr(err) {
		t.Fatalf("want a classifiable block error, got %v", err)
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("the error should say why: %v", err)
	}
}

// The escape hatch must actually work, or tests and self-hosted deployments have
// no way through and the constant gets deleted by the next person.
func TestAllowPrivateReachesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	resp, err := NewClient(Options{AllowPrivate: true}).Get(srv.URL)
	if err != nil {
		t.Fatalf("AllowPrivate must reach loopback: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// A public host that redirects to loopback is the classic bypass: the URL passes
// every string check, and only the hop is dangerous. Refusing redirects outright
// closes it without needing per-hop revalidation.
func TestRedirectsAreRefused(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("internal"))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	// AllowPrivate so the FIRST hop succeeds; the redirect must still be refused,
	// proving the refusal comes from the redirect policy and not from the dialer.
	resp, err := NewClient(Options{AllowPrivate: true}).Get(redirector.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("a redirect was followed")
	}
	if !IsBlockedErr(err) {
		t.Fatalf("want a classifiable block error, got %v", err)
	}
}
