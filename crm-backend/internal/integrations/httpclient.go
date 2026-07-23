package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The one outbound HTTP client every provider adapter uses (Facebook now,
// TikTok/Instagram later). It generalizes the taxonomy that was hand-inlined at
// six call sites (executor_webhook, executor_email, the AI gateway, turnstile,
// the embedding client, the resend mailer) rather than becoming a seventh:
//
//   - a bounded body read, so a provider that starts streaming cannot hold a
//     goroutine and memory open (io.LimitReader, the executor_webhook idiom);
//   - a retry taxonomy — network error / 5xx / 429 are RETRYABLE, other 4xx are
//     PERMANENT (the executor_webhook classification, which is the one the plan
//     specifies);
//   - honoring Retry-After on 429/503, which nothing in the codebase parsed
//     before, and FULL-JITTER backoff, which nothing used before (every existing
//     backoff was a deterministic 1<<attempt, i.e. a thundering-herd generator);
//   - a cancelable sleep (select on ctx.Done), so a caller deadline aborts a
//     backoff immediately instead of burning it.
//
// It is NOT a migration of the six existing clients — that is a separate, risky
// change. This is the client the connector framework's own calls use.

const (
	// providerBodyLimit caps a provider response read. 1MB matches the established
	// executor_webhook/turnstile cap: a lead payload is small, and anything larger
	// is a mistake or an attack that must not be buffered whole.
	providerBodyLimit = 1 << 20
	// providerTimeout bounds a single attempt when the caller's context carries no
	// deadline of its own.
	providerTimeout = 15 * time.Second
	// providerMaxAttempts is total tries including the first. Kept small: the async
	// webhook worker (L5.3) has its own durable retry budget on top of this, so the
	// in-request loop only needs to ride out a transient blip, not a real outage.
	providerMaxAttempts = 3
	// providerBaseBackoff / providerMaxBackoff bound the jittered sleep between
	// attempts.
	providerBaseBackoff = 500 * time.Millisecond
	providerMaxBackoff  = 8 * time.Second
	// retryAfterCap clamps a provider's Retry-After. A misbehaving or hostile header
	// (Retry-After: 86400) must not be able to park an in-request attempt for a day.
	retryAfterCap = 30 * time.Second
)

// HTTPError classifies a non-2xx response or a transport failure.
//
// Retryable is the load-bearing field: the async worker keys its durable
// reschedule off it, and a 4xx (token revoked, bad request) must read as
// permanent so a dead credential is not retried forever. StatusCode is 0 for a
// transport error (no response arrived). Body is the capped response body, kept
// so a caller can log a provider's error detail without a second read.
type HTTPError struct {
	StatusCode int
	Body       []byte
	Retryable  bool
	Err        error
}

func (e *HTTPError) Error() string {
	if e.StatusCode == 0 {
		return fmt.Sprintf("provider request failed: %v", e.Err)
	}
	// Include the wrapped reason when a caller (e.g. the Facebook adapter) has
	// enriched a status error with the provider's own message — otherwise the
	// message is silently dropped and every error reads as a bare status code.
	if e.Err != nil {
		return fmt.Sprintf("provider responded %d: %v", e.StatusCode, e.Err)
	}
	return fmt.Sprintf("provider responded %d", e.StatusCode)
}

func (e *HTTPError) Unwrap() error { return e.Err }

// IsRetryableHTTP reports whether an error from HTTPClient.Do is worth retrying
// later. Used by the async worker to tell "reschedule" from "give up".
func IsRetryableHTTP(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Retryable
}

// Response is a completed provider call. Body is already read and capped.
type Response struct {
	StatusCode int
	Body       []byte
	Header     http.Header
	// Truncated reports that the body hit the client's cap and was cut. A JSON
	// caller can ignore it (the decode fails anyway); a caller reading a bulk export
	// MUST check it, because a short read there looks exactly like a short export.
	Truncated bool
}

// DecodeJSON unmarshals the (already-capped) body into v.
func (r *Response) DecodeJSON(v any) error { return json.Unmarshal(r.Body, v) }

// OutboundRequest is a provider call. The body is held as bytes, not a Reader,
// so a retry can re-send it — a streamed Reader would be drained after the first
// attempt (the exact bug that makes a naive retry loop silently POST an empty
// body on attempt two).
type OutboundRequest struct {
	Method string
	URL    string
	Body   []byte
	Header http.Header
	// BodyLimit overrides the client's response cap for this ONE request. Zero means
	// the default.
	//
	// Per-request rather than a client variant, because the client carries a mutex and
	// copying it to vary one field is a data race the compiler will not catch (go vet
	// does). The default bounds a JSON API response, where a megabyte is generous; a
	// bulk export is a different shape — TikTok's lead download is a CSV that only
	// becomes a zip above 10MB, so the default would cut a real advertiser's history
	// in half and the truncated file would still parse.
	BodyLimit int64
}

// HTTPClient is the retrying, bounded outbound client.
type HTTPClient struct {
	client      *http.Client
	maxAttempts int
	baseBackoff time.Duration
	maxBackoff  time.Duration
	bodyLimit   int64
	// rng is the jitter source, held on the struct so a test can seed it for a
	// deterministic backoff. math/rand's Rand is not concurrency-safe, and this
	// client IS designed to be shared as a singleton across the async worker's
	// goroutines (L5.3), so every read of rng is guarded by rngMu. Without the
	// guard, concurrent retries would race the generator's internal state — a data
	// race the race detector flags and, worse, a silent source of corruption.
	rng   *rand.Rand
	rngMu sync.Mutex
	sleep func(context.Context, time.Duration) error
}

// NewHTTPClient builds the client with production defaults. A nil inner client
// gets a bounded default.
func NewHTTPClient(inner *http.Client) *HTTPClient {
	if inner == nil {
		inner = &http.Client{Timeout: providerTimeout}
	}
	c := &HTTPClient{
		client:      inner,
		maxAttempts: providerMaxAttempts,
		baseBackoff: providerBaseBackoff,
		maxBackoff:  providerMaxBackoff,
		bodyLimit:   providerBodyLimit,
		rng:         rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	c.sleep = sleepWithContext
	return c
}

// Do executes the request, retrying transient failures with jittered backoff,
// and returns the response or an *HTTPError.
//
// On a 2xx: (resp, nil). On a non-2xx that is exhausted or permanent: (resp,
// *HTTPError) — resp is still returned so the caller can read status/body. On a
// transport failure: (nil, *HTTPError{StatusCode:0}).
func (c *HTTPClient) Do(ctx context.Context, r OutboundRequest) (*Response, error) {
	attempts := c.maxAttempts
	if attempts < 1 {
		attempts = 1
	}

	var last *HTTPError
	var lastResp *Response
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			// A caller deadline already blown means every remaining attempt would
			// fail the same way — stop rather than sleep into a certain timeout.
			if ctx.Err() != nil {
				return lastResp, wrapCtx(ctx, last)
			}
			delay := c.backoff(attempt, retryAfterOf(lastResp))
			if err := c.sleep(ctx, delay); err != nil {
				return lastResp, wrapCtx(ctx, last)
			}
		}

		resp, herr := c.attempt(ctx, r)
		if herr == nil {
			return resp, nil
		}
		last = herr
		lastResp = resp
		if !herr.Retryable {
			return resp, herr
		}
		// Retryable but the caller's context is done — do not loop into a guaranteed
		// failure.
		if ctx.Err() != nil {
			return resp, wrapCtx(ctx, herr)
		}
	}
	return lastResp, last
}

// attempt performs one request and classifies the result.
func (c *HTTPClient) attempt(ctx context.Context, r OutboundRequest) (*Response, *HTTPError) {
	var body io.Reader
	if len(r.Body) > 0 {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		// A malformed method/URL is the caller's bug, not a transient condition.
		return nil, &HTTPError{Err: err, Retryable: false}
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	res, err := c.client.Do(req)
	if err != nil {
		// SECURITY: a transport error is a *url.Error whose string embeds the full
		// request URL, and Go redacts only userinfo passwords — never query params.
		// Provider adapters put access tokens, app secrets and appsecret_proof in the
		// query string, so an un-redacted transport error carries live credentials
		// straight into any log line or ledger note that renders it. Strip the query
		// HERE, at the one choke point every provider call funnels through, so no
		// caller can leak a secret by logging an error.
		err = redactURLError(err)
		// A cancelled/deadline-exceeded context is the caller's decision, not a
		// transient network fault — do not mark it retryable, or the worker would
		// reschedule work the caller explicitly abandoned.
		if ctx.Err() != nil {
			return nil, &HTTPError{Err: err, Retryable: false}
		}
		// Any other transport error (dial failure, connection reset, TLS, a
		// client-timeout that is not the caller's ctx) is retryable.
		return nil, &HTTPError{Err: err, Retryable: true}
	}
	defer res.Body.Close()

	// Bounded read: a body cut at the cap fails a later JSON decode, which is the
	// right outcome — an unreadable response is not a usable one.
	//
	// One extra byte is requested so a body that exactly fills the cap is
	// DISTINGUISHABLE from one that was cut. That matters for a caller reading a
	// non-JSON body (a CSV export), where truncation does not fail a decode — it
	// silently returns fewer rows, and an import that drops most of its history
	// while reporting success is the worst outcome available.
	limit := c.bodyLimit
	if r.BodyLimit > 0 {
		limit = r.BodyLimit
	}
	raw, readErr := io.ReadAll(io.LimitReader(res.Body, limit+1))
	// A read error mid-body (a reset connection, a TLS teardown) is NOT a complete
	// response — but io.ReadAll returns the bytes it got ALONGSIDE the error, so
	// discarding it would hand a caller a half a body that looks whole. For a JSON
	// caller the later decode fails anyway; for one reading a bulk export a short read
	// is indistinguishable from a short export, so this is where it has to be caught.
	// Retryable, because a re-fetch may complete.
	if readErr != nil {
		return nil, &HTTPError{StatusCode: res.StatusCode, Err: fmt.Errorf("response body read failed: %w", readErr), Retryable: true}
	}
	truncated := int64(len(raw)) > limit
	if truncated {
		raw = raw[:limit]
	}
	resp := &Response{StatusCode: res.StatusCode, Body: raw, Header: res.Header, Truncated: truncated}

	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return resp, nil
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
		// 429 and 5xx are transient by contract. Retry-After (if any) is honored by
		// the backoff computation on the next loop.
		return resp, &HTTPError{StatusCode: res.StatusCode, Body: raw, Retryable: true}
	default:
		// Every other 4xx is permanent: a bad token, a revoked grant, a malformed
		// request. Retrying only wastes the credential's rate budget and delays the
		// reconnect banner the admin actually needs.
		return resp, &HTTPError{StatusCode: res.StatusCode, Body: raw, Retryable: false}
	}
}

// backoff computes the sleep before an attempt, honoring a provider Retry-After
// when present, else full-jitter exponential.
//
// Full jitter (sleep uniformly in [0, cap]) rather than the codebase's existing
// deterministic 1<<attempt: identical retry schedules across many connections
// hitting the same provider after a shared outage is a self-inflicted thundering
// herd, and jitter is the standard fix.
func (c *HTTPClient) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > retryAfterCap {
			return retryAfterCap
		}
		return retryAfter
	}
	// base * 2^(attempt-1), capped, then jittered into [0, window].
	window := c.baseBackoff << (attempt - 1)
	if window <= 0 || window > c.maxBackoff {
		window = c.maxBackoff
	}
	if c.rng == nil {
		return window
	}
	c.rngMu.Lock()
	defer c.rngMu.Unlock()
	return time.Duration(c.rng.Int63n(int64(window) + 1))
}

// retryAfterOf reads a Retry-After header off the last response, in both the
// delta-seconds and the HTTP-date forms the RFC allows. Nothing in the codebase
// parsed this before.
func retryAfterOf(resp *Response) time.Duration {
	if resp == nil || resp.Header == nil {
		return 0
	}
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// redactURLError strips the query string (and fragment) from the URL inside a
// *url.Error, so a transport failure cannot carry a token or secret we passed as
// a query param into an error string. The path is kept (it names which endpoint
// failed, and our paths carry no secrets). A non-url.Error is returned unchanged
// — it never contains our request URL.
func redactURLError(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	safe := ue.URL
	if u, perr := url.Parse(ue.URL); perr == nil {
		u.RawQuery = ""
		u.Fragment = ""
		safe = u.String()
	} else {
		// Unparseable — drop everything from the first '?' rather than risk keeping a
		// secret we could not structurally remove.
		if i := strings.IndexByte(safe, '?'); i >= 0 {
			safe = safe[:i]
		}
	}
	return &url.Error{Op: ue.Op, URL: safe, Err: ue.Err}
}

// sleepWithContext sleeps for d, returning early if ctx is cancelled — the
// gateway.go select idiom, which beats time.Sleep because a cancelled context
// aborts the wait instead of blocking through it.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// wrapCtx prefers the context's own error when the loop stopped because the
// caller went away, falling back to the last HTTP error otherwise.
func wrapCtx(ctx context.Context, last *HTTPError) error {
	if ctx.Err() != nil {
		return &HTTPError{Err: ctx.Err(), Retryable: false}
	}
	if last != nil {
		return last
	}
	return &HTTPError{Err: errors.New("request failed"), Retryable: true}
}
