package integrations

import (
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fastClient builds a client whose backoff sleep is a no-op, so retry tests do
// not actually wait.
func fastClient(inner *http.Client) *HTTPClient {
	c := NewHTTPClient(inner)
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestHTTPClient_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	resp, err := fastClient(nil).Do(context.Background(), OutboundRequest{Method: "GET", URL: srv.URL})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != 200 || string(resp.Body) != "ok" {
		t.Fatalf("resp = %d %q", resp.StatusCode, resp.Body)
	}
}

func TestHTTPClient_Permanent4xxNotRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "bad", 400)
	}))
	defer srv.Close()

	resp, err := fastClient(nil).Do(context.Background(), OutboundRequest{Method: "GET", URL: srv.URL})
	if err == nil {
		t.Fatal("expected an error for 400")
	}
	if IsRetryableHTTP(err) {
		t.Error("a 400 must not be retryable")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("a permanent 4xx must be tried exactly once, got %d", got)
	}
	// The response is still returned so the caller can read status/body.
	if resp == nil || resp.StatusCode != 400 {
		t.Errorf("resp = %+v, want status 400", resp)
	}
}

func TestHTTPClient_Retries5xxThenGivesUp(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "boom", 503)
	}))
	defer srv.Close()

	_, err := fastClient(nil).Do(context.Background(), OutboundRequest{Method: "GET", URL: srv.URL})
	if err == nil || !IsRetryableHTTP(err) {
		t.Fatalf("a persistent 5xx must exhaust as retryable, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != providerMaxAttempts {
		t.Errorf("want %d attempts, got %d", providerMaxAttempts, got)
	}
}

func TestHTTPClient_RecoversAfter5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			http.Error(w, "boom", 500)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("recovered"))
	}))
	defer srv.Close()

	resp, err := fastClient(nil).Do(context.Background(), OutboundRequest{Method: "POST", URL: srv.URL, Body: []byte(`{}`)})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != 200 || string(resp.Body) != "recovered" {
		t.Fatalf("resp = %d %q", resp.StatusCode, resp.Body)
	}
}

func TestHTTPClient_ResendsBodyOnRetry(t *testing.T) {
	var seen []string
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		seen = append(seen, string(buf[:n]))
		if atomic.AddInt32(&calls, 1) == 1 {
			http.Error(w, "boom", 503)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	_, err := fastClient(nil).Do(context.Background(), OutboundRequest{Method: "POST", URL: srv.URL, Body: []byte("payload")})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	// The body must survive into the retry — a streamed Reader would be empty on the
	// second attempt, silently POSTing nothing.
	for i, s := range seen {
		if s != "payload" {
			t.Errorf("attempt %d saw body %q, want %q", i, s, "payload")
		}
	}
}

func TestHTTPClient_TransportErrorRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // now nothing is listening

	resp, err := fastClient(nil).Do(context.Background(), OutboundRequest{Method: "GET", URL: url})
	if err == nil || !IsRetryableHTTP(err) {
		t.Fatalf("a connection refusal must be retryable, got %v", err)
	}
	if resp != nil {
		t.Errorf("no response should be returned on a transport error, got %+v", resp)
	}
}

func TestHTTPClient_BodyCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(strings.Repeat("x", 1000)))
	}))
	defer srv.Close()

	c := fastClient(nil)
	c.bodyLimit = 10
	resp, err := c.Do(context.Background(), OutboundRequest{Method: "GET", URL: srv.URL})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(resp.Body) != 10 {
		t.Errorf("body must be capped at 10, got %d", len(resp.Body))
	}
}

func TestHTTPClient_CanceledContextNotRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fastClient(nil).Do(ctx, OutboundRequest{Method: "GET", URL: srv.URL})
	if err == nil {
		t.Fatal("expected an error for a canceled context")
	}
	if IsRetryableHTTP(err) {
		t.Error("a caller-canceled context must not be retryable — the worker abandoned the work on purpose")
	}
}

func TestRetryAfterOf(t *testing.T) {
	mk := func(v string) *Response {
		h := http.Header{}
		if v != "" {
			h.Set("Retry-After", v)
		}
		return &Response{Header: h}
	}
	if got := retryAfterOf(mk("5")); got != 5*time.Second {
		t.Errorf("delta-seconds: got %v, want 5s", got)
	}
	if got := retryAfterOf(mk("")); got != 0 {
		t.Errorf("absent: got %v, want 0", got)
	}
	if got := retryAfterOf(mk("-3")); got != 0 {
		t.Errorf("negative: got %v, want 0", got)
	}
	if got := retryAfterOf(nil); got != 0 {
		t.Errorf("nil resp: got %v, want 0", got)
	}
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if got := retryAfterOf(mk(future)); got <= 0 || got > 31*time.Second {
		t.Errorf("http-date future: got %v, want ~30s", got)
	}
	past := time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)
	if got := retryAfterOf(mk(past)); got != 0 {
		t.Errorf("http-date past: got %v, want 0", got)
	}
}

func TestBackoff(t *testing.T) {
	c := NewHTTPClient(nil)
	c.rng = rand.New(rand.NewSource(1))

	// Retry-After is honored and clamped.
	if got := c.backoff(1, 5*time.Second); got != 5*time.Second {
		t.Errorf("retry-after honored: got %v, want 5s", got)
	}
	if got := c.backoff(1, time.Hour); got != retryAfterCap {
		t.Errorf("retry-after clamped: got %v, want %v", got, retryAfterCap)
	}
	// Full jitter: within [0, window] where window = base<<(attempt-1), capped.
	for attempt := 1; attempt <= 6; attempt++ {
		got := c.backoff(attempt, 0)
		if got < 0 || got > c.maxBackoff {
			t.Errorf("attempt %d backoff %v out of [0, %v]", attempt, got, c.maxBackoff)
		}
	}
}
