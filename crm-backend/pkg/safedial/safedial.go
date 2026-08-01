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

// blockedReason names why an address is refused, for an error a user can act on.
func blockedReason(a netip.Addr) string {
	switch {
	case !a.IsValid():
		return "unparseable address"
	case a.IsUnspecified():
		return "unspecified address (0.0.0.0 / ::)"
	case a.IsLoopback():
		return "loopback"
	case a.IsLinkLocalUnicast():
		// Includes 169.254.169.254, the cloud instance-metadata endpoint, which is
		// the single most valuable target for this class of bug.
		return "link-local (includes cloud instance metadata)"
	case a.IsLinkLocalMulticast(), a.IsMulticast(), a.IsInterfaceLocalMulticast():
		return "multicast"
	case a.IsPrivate():
		return "private network"
	}
	return ""
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
	if r := blockedReason(a); r != "" {
		return true, r
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
