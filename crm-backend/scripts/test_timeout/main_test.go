// Package test_timeout verifies CF AI timeout → HTTP 503 + Retry-After header.
//
// Strategy: spin up httptest.Servers simulating slow/fast upstreams,
// call through our own ai.AIGateway, and assert the correct behaviour.
//
// Run: go test -v -count=1 ./scripts/test_timeout/
package test_timeout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"crm-backend/internal/ai"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fake upstream servers
// ─────────────────────────────────────────────────────────────────────────────

// newSlowServer simulates a CF gateway that takes longer than the timeout.
func newSlowServer(delay time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// newFastServer returns an immediate valid SSE stream.
func newFastServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"response\":\"hello\"}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func newGW(url string, timeout time.Duration) *ai.AIGateway {
	return ai.NewAIGatewayForTest(url, "dummy-token", nil, timeout, "dummy-gw-token")
}

// mockChatHandler is a minimal stand-in for /api/ai/chat that exercises the
// exact same header-deferral + ErrAITimeout pattern as the real Gin handler.
func mockChatHandler(gw *ai.AIGateway) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
			http.Error(w, "bad request", 400)
			return
		}

		msgs := []ai.Message{
			{Role: "system", Content: "You are a CRM assistant."},
			{Role: "user", Content: req.Message},
		}

		headerWritten := false
		writeSSEHeaders := func() {
			if headerWritten {
				return
			}
			headerWritten = true
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
		}
		flush := func() {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}

		err := gw.StreamChat(
			r.Context(),
			uuid.New(), uuid.New(),
			ai.TaskAssistantChat,
			msgs, w, writeSSEHeaders, flush,
		)
		if err == nil {
			return
		}

		var timeoutErr ai.ErrAITimeout
		if errors.As(err, &timeoutErr) && !headerWritten {
			// Deadline missed before any bytes — emit proper HTTP 503.
			w.Header().Set("Retry-After", strconv.Itoa(timeoutErr.After))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
				"error": timeoutErr.Error(),
				"code":  "ai_timeout",
			})
			return
		}

		// Generic fallback
		if !headerWritten {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
		} else {
			fmt.Fprintf(w, "event: error\ndata: {\"code\":\"error\"}\n\n")
			flush()
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  1. ErrAITimeout type contract
// ─────────────────────────────────────────────────────────────────────────────

func TestErrAITimeout_ErrorString(t *testing.T) {
	err := ai.ErrAITimeout{Provider: "cloudflare", After: 5}
	got := err.Error()

	for _, want := range []string{"cloudflare", "timed out", "5"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in ErrAITimeout.Error(), got: %q", want, got)
		}
	}
	t.Logf("✓ ErrAITimeout.Error() = %q", got)
}

// ─────────────────────────────────────────────────────────────────────────────
//  2. StreamChat → ErrAITimeout when upstream is slow
// ─────────────────────────────────────────────────────────────────────────────

func TestStreamChat_SlowUpstream_ReturnsErrAITimeout(t *testing.T) {
	slow := newSlowServer(2 * time.Second) // server sleeps 2s
	defer slow.Close()

	gw := newGW(slow.URL, 200*time.Millisecond) // client times out at 200ms

	var buf strings.Builder
	noop := func() {}
	err := gw.StreamChat(
		context.Background(),
		uuid.New(), uuid.New(),
		ai.TaskAssistantChat,
		[]ai.Message{{Role: "user", Content: "ping"}},
		&buf, noop, noop,
	)
	if err == nil {
		t.Fatal("expected non-nil error")
	}

	var timeoutErr ai.ErrAITimeout
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("expected ErrAITimeout, got %T: %v", err, err)
	}
	if timeoutErr.After <= 0 {
		t.Errorf("expected After > 0, got %d", timeoutErr.After)
	}
	t.Logf("✓ StreamChat → ErrAITimeout{After:%d} as expected", timeoutErr.After)
}

// ─────────────────────────────────────────────────────────────────────────────
//  3. /api/ai/chat → 503 + Retry-After on timeout  ← PRIMARY VERIFICATION
// ─────────────────────────────────────────────────────────────────────────────

func TestHTTPHandler_Timeout_Returns503WithRetryAfter(t *testing.T) {
	slow := newSlowServer(3 * time.Second) // CF mock never responds in time
	defer slow.Close()

	gw := newGW(slow.URL, 300*time.Millisecond)
	ts := httptest.NewServer(mockChatHandler(gw))
	defer ts.Close()

	resp, err := http.Post(
		ts.URL+"/api/ai/chat",
		"application/json",
		strings.NewReader(`{"message":"will this timeout?"}`),
	)
	if err != nil {
		t.Fatalf("request transport error: %v", err)
	}
	defer resp.Body.Close()

	// 1. HTTP status must be 503
	if resp.StatusCode != http.StatusServiceUnavailable {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected HTTP 503, got %d (%s)", resp.StatusCode, raw)
	}
	t.Logf("✓ HTTP %d Service Unavailable", resp.StatusCode)

	// 2. Retry-After header must be present and a positive integer
	ra := resp.Header.Get("Retry-After")
	if ra == "" {
		t.Fatal("expected Retry-After header, got empty string")
	}
	n, parseErr := strconv.Atoi(ra)
	if parseErr != nil || n <= 0 {
		t.Fatalf("Retry-After must be a positive integer, got %q", ra)
	}
	t.Logf("✓ Retry-After: %s", ra)

	// 3. JSON body must reference timeout
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON body: %v", err)
	}
	msg := strings.ToLower(body["error"])
	if !strings.Contains(msg, "timeout") && !strings.Contains(msg, "timed out") {
		t.Fatalf("expected timeout message, got: %q", body["error"])
	}
	if body["code"] != "ai_timeout" {
		t.Errorf("expected code=ai_timeout, got %q", body["code"])
	}
	t.Logf("✓ error body: %v", body)
}

// ─────────────────────────────────────────────────────────────────────────────
//  4. Normal SSE flow is unaffected by the timeout changes
// ─────────────────────────────────────────────────────────────────────────────

func TestHTTPHandler_FastUpstream_Returns200SSE(t *testing.T) {
	fast := newFastServer()
	defer fast.Close()

	gw := newGW(fast.URL, 5*time.Second)
	ts := httptest.NewServer(mockChatHandler(gw))
	defer ts.Close()

	resp, err := http.Post(
		ts.URL+"/api/ai/chat",
		"application/json",
		strings.NewReader(`{"message":"hello"}`),
	)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, raw)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}

	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if !strings.Contains(body, "hello") || !strings.Contains(body, "[DONE]") {
		t.Errorf("unexpected SSE body: %q", body)
	}
	t.Logf("✓ Normal SSE 200: %q", body)
}
