// Package safedial builds HTTP clients that refuse to reach private network space.
//
// It exists for one caller shape: an outbound request whose URL comes from a user.
// The automation send_webhook action is the live example — its URL is
// template-interpolated at RUN time, so nothing checked at save time constrains the
// address finally dialled, and the response body is handed back to the workflow.
// That combination is not blind SSRF; it is a read primitive pointed at whatever
// the platform's network can reach.
//
// The guard therefore runs at CONNECT time, on the resolved address, not on the
// hostname. Validating a hostname is defeated by a name that resolves to a public
// address on the first lookup and a private one on the second (DNS rebinding), and
// by any redirect. net.Dialer.ControlContext is invoked once per resolved address
// immediately before the socket is opened, which is the only place the check cannot
// be raced.
package safedial

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrBlockedAddress is returned when a dial targets non-public address space.
// Callers should treat it as PERMANENT: retrying reaches the same address, so a
// retry only burns the schedule and re-runs the request.
var ErrBlockedAddress = errors.New("destination address is not permitted")

// ErrBlockedScheme is returned for a URL this package will not fetch at all.
var ErrBlockedScheme = errors.New("destination URL is not permitted")

// blockedPrefix is one denied range and the reason to report for it.
type blockedPrefix struct {
	prefix netip.Prefix
	reason string
}

// blocked is an EXPLICIT deny table rather than a chain of netip predicates.
//
// The predicate chain that used to be here was wrong in a way that read as
// correct. netip.Addr.IsPrivate covers only RFC1918 and IPv6 ULA, so
// 100.64.0.0/10 — carrier-grade NAT, which is the default pod/service CIDR on
// many Kubernetes installs, the whole Tailscale address space, and the internal
// fabric of several hosting providers — returned false from every predicate and
// was dialled. So did 240/4, 198.18/15, the TEST-NET blocks, and 6to4/NAT64
// embeddings. Listing the ranges makes the coverage auditable instead of
// depending on what a standard-library helper happens to mean.
var blocked = []blockedPrefix{
	// IPv4
	{netip.MustParsePrefix("0.0.0.0/8"), "unspecified / this-network"},
	{netip.MustParsePrefix("10.0.0.0/8"), "private network"},
	{netip.MustParsePrefix("100.64.0.0/10"), "carrier-grade NAT (also Kubernetes and Tailscale space)"},
	{netip.MustParsePrefix("127.0.0.0/8"), "loopback"},
	{netip.MustParsePrefix("169.254.0.0/16"), "link-local (includes cloud instance metadata)"},
	{netip.MustParsePrefix("172.16.0.0/12"), "private network"},
	{netip.MustParsePrefix("192.0.0.0/24"), "IETF protocol assignments"},
	{netip.MustParsePrefix("192.0.2.0/24"), "TEST-NET-1"},
	{netip.MustParsePrefix("192.168.0.0/16"), "private network"},
	{netip.MustParsePrefix("198.18.0.0/15"), "benchmark network"},
	{netip.MustParsePrefix("198.51.100.0/24"), "TEST-NET-2"},
	{netip.MustParsePrefix("203.0.113.0/24"), "TEST-NET-3"},
	{netip.MustParsePrefix("224.0.0.0/4"), "multicast"},
	{netip.MustParsePrefix("240.0.0.0/4"), "reserved (includes 255.255.255.255 broadcast)"},
	// IPv6
	{netip.MustParsePrefix("::/128"), "unspecified"},
	{netip.MustParsePrefix("::1/128"), "loopback"},
	// ::ffff:0:0/96 is checked post-Unmap, so a mapped address is judged as its
	// IPv4 self; this entry catches a mapped address that somehow survives unmapping.
	{netip.MustParsePrefix("::ffff:0:0/96"), "IPv4-mapped"},
	{netip.MustParsePrefix("64:ff9b::/96"), "NAT64"},
	{netip.MustParsePrefix("64:ff9b:1::/48"), "local-use NAT64"},
	{netip.MustParsePrefix("2001::/32"), "Teredo"},
	{netip.MustParsePrefix("2001:db8::/32"), "documentation"},
	{netip.MustParsePrefix("2002::/16"), "6to4"},
	{netip.MustParsePrefix("fc00::/7"), "unique-local"},
	{netip.MustParsePrefix("fe80::/10"), "link-local"},
	{netip.MustParsePrefix("ff00::/8"), "multicast"},
}

// IsBlocked reports whether an address is outside the public internet, along with a
// human-readable reason.
//
// IPv4-mapped and 6to4/Teredo-style embeddings are unwrapped first: ::ffff:127.0.0.1
// is loopback however it is spelled, and a check that only looks at the outer form
// is a check with a documented bypass.
func IsBlocked(a netip.Addr) (bool, string) {
	if !a.IsValid() {
		return true, "unparseable address"
	}
	if a.Is4In6() {
		a = a.Unmap()
	}
	for _, b := range blocked {
		// Prefix.Contains is family-strict — an IPv4 addr never matches an IPv6
		// prefix — so the two families can share one table safely.
		if b.prefix.Contains(a) {
			return true, b.reason
		}
	}
	// Backstop for anything the table misses: these predicates are a superset of
	// nothing above, but a future address class we have not enumerated is more
	// likely to be caught here than not at all.
	switch {
	case a.IsUnspecified():
		return true, "unspecified address"
	case a.IsLoopback():
		return true, "loopback"
	case a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast():
		return true, "link-local"
	case a.IsMulticast(), a.IsInterfaceLocalMulticast():
		return true, "multicast"
	case a.IsPrivate():
		return true, "private network"
	}
	return false, ""
}

// control is the ControlContext hook. It runs after DNS resolution, with the
// concrete address about to be connected to.
func control(_ context.Context, _, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: unparseable destination %q", ErrBlockedAddress, address)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		// ControlContext always receives a literal IP, never a name. Anything else
		// means an assumption here is wrong, so refuse rather than guess.
		return fmt.Errorf("%w: %q is not an IP address", ErrBlockedAddress, host)
	}
	if blocked, reason := IsBlocked(addr); blocked {
		return fmt.Errorf("%w: %s is %s", ErrBlockedAddress, addr, reason)
	}
	return nil
}

// ValidateURL applies the checks that can be made before any connection: the scheme
// allowlist, and the absence of embedded credentials.
//
// This is defence in depth rather than the control. It cannot constrain the address
// actually reached — that is the dialer's job — but it rejects the obviously wrong
// before a socket is opened, and it produces a much clearer error than a dial
// failure.
func ValidateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBlockedScheme, err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		// file://, gopher://, ftp:// and friends. Go's client does not speak most of
		// them, but naming the allowlist beats relying on that.
		return fmt.Errorf("%w: scheme %q is not http or https", ErrBlockedScheme, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("%w: no host", ErrBlockedScheme)
	}
	if u.User != nil {
		// Credentials in a webhook URL end up in logs and in the action log. They are
		// also a classic way to disguise the real host from a human reviewer.
		return fmt.Errorf("%w: URLs with embedded credentials are not accepted", ErrBlockedScheme)
	}
	return nil
}

// Options tunes a client. The zero value is the safe configuration.
type Options struct {
	// AllowPrivate disables the address guard entirely. It exists for two callers
	// and no others: tests, which drive real servers on 127.0.0.1, and a
	// self-hosted deployment whose webhook targets genuinely live on a private
	// network. Never enable it on a multi-tenant deployment — it hands every
	// workflow author a read primitive against the internal network.
	AllowPrivate bool
	// Timeout bounds the whole request. Zero leaves it to the caller.
	Timeout time.Duration
}

// NewTransport returns a transport whose dialer refuses non-public addresses.
func NewTransport(opts Options) *http.Transport {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !opts.AllowPrivate {
		d.ControlContext = control
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = d.DialContext
	// Clone() inherits Proxy: ProxyFromEnvironment. A proxy would make the dialer
	// connect to the PROXY's address instead of the destination, so ControlContext
	// would inspect the proxy and wave through whatever the proxy was then asked to
	// fetch — the guard would still appear to run while checking the wrong thing.
	// Nobody sets HTTP_PROXY on this deployment today, which is exactly why it must
	// be pinned rather than left to the environment.
	t.Proxy = nil
	return t
}

// NewClient returns an http.Client that will not reach private address space.
//
// Redirects are REFUSED rather than re-validated. Re-validating each hop is
// correct too, but refusing is simpler to reason about and to prove, and a webhook
// receiver that answers a POST with a redirect is already misbehaving. The cost of
// the simpler rule is a clear error; the cost of a subtle one is a bypass.
func NewClient(opts Options) *http.Client {
	return &http.Client{
		Timeout:   opts.Timeout,
		Transport: NewTransport(opts),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("%w: destination redirected to %s, which is not followed", ErrBlockedScheme, req.URL.Redacted())
		},
	}
}

// IsBlockedErr reports whether err came from this package's guards, at any depth.
// Callers use it to classify the failure as permanent rather than retryable.
func IsBlockedErr(err error) bool {
	return errors.Is(err, ErrBlockedAddress) || errors.Is(err, ErrBlockedScheme)
}
