package domain

import (
	"context"
	"testing"
)

// These two functions are a matched pair: WithRequestID writes under an
// unexported key type and RequestIDFromContext is the only thing that can read
// it back. Nothing else in the build can catch a mismatch between them — `go
// vet` cannot, and the failure is SILENT rather than loud: the id degrades to
// the caller's placeholder ("unknown" in the AI budget guard's llm_call
// accounting), no error is returned, and request correlation just quietly stops
// working. Hence a round-trip test.
func TestRequestID_RoundTrips(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req-abc-123")

	if got := RequestIDFromContext(ctx); got != "req-abc-123" {
		t.Fatalf("RequestIDFromContext = %q, want %q", got, "req-abc-123")
	}
}

func TestRequestIDFromContext_EmptyWhenAbsent(t *testing.T) {
	// A context that never passed through the HTTP router — automation runs,
	// workers and seed scripts all look like this. Callers substitute their own
	// placeholder, so this must return the zero value rather than a fabricated id.
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Fatalf("RequestIDFromContext on a bare context = %q, want empty", got)
	}
}

func TestWithRequestID_EmptyIDDoesNotBlankAnExistingOne(t *testing.T) {
	// Documented contract: an empty id is ignored. Without this, a middleware
	// that computed an empty id would erase the real one already on the context.
	ctx := WithRequestID(context.Background(), "req-original")
	ctx = WithRequestID(ctx, "")

	if got := RequestIDFromContext(ctx); got != "req-original" {
		t.Fatalf("RequestIDFromContext = %q, want the original id to survive", got)
	}
}

// The whole point of the unexported struct key (staticcheck SA1029). If the key
// were the bare string "request_id" that the router used before R3, this would
// read the foreign value and pass.
func TestRequestID_IgnoresABareStringKeyInTheSameContext(t *testing.T) {
	//nolint:staticcheck // SA1029 deliberately: this is the collision being tested.
	ctx := context.WithValue(context.Background(), "request_id", "planted-by-another-package")

	if got := RequestIDFromContext(ctx); got != "" {
		t.Fatalf("RequestIDFromContext read a bare-string key (%q) — the typed key is not isolating the namespace", got)
	}
}
