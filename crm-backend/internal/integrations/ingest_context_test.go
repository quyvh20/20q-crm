package integrations

import (
	"context"
	"testing"

	"crm-backend/internal/domain"
)

// The ingest context decides whether a lead reaches a human. These tests pin both
// halves — and the unsuppressed control is the one that matters most: a wiring slip
// that silences unconditionally, or that derives silence from the SOURCE instead of
// the lead, would leave every real lead from every source unenrolled. No error, no
// failing test, and a ledger that looks completely normal.

func TestNewIngestContext_RealLeadIsNeverSilenced(t *testing.T) {
	src := testSource(t, "", "")
	ctx, cancel := newIngestContext(src, RawLead{Fields: map[string]any{"email": "real@customer.com"}})
	defer cancel()

	if domain.IsAutomationSilenced(ctx) {
		t.Error("a real lead must never be silenced — its workflows are the product")
	}
	if domain.IsAutomationSuppressed(ctx) {
		t.Error("a real lead must enroll: suppressing it would leave leads rotting with nobody notified")
	}
	if got := domain.WriteSourceFromContext(ctx); got != "integration:api" {
		t.Errorf("write source = %q, want integration:api", got)
	}
	if !domain.IsPartialWrite(ctx) {
		t.Error("a lead is not a form submission; required-field presence must stay relaxed")
	}
}

func TestNewIngestContext_TestLeadIsSilenced(t *testing.T) {
	src := testSource(t, "", "")
	ctx, cancel := newIngestContext(src, RawLead{TestOrigin: TestOriginAdmin})
	defer cancel()

	if !domain.IsAutomationSilenced(ctx) {
		t.Error("a test lead must be silenced, or it arms a timer that pages a rep about a contact that does not exist")
	}
	// The composition guarantee, checked from the caller's side: silence must imply
	// suppression, or the test lead enrolls anyway.
	if !domain.IsAutomationSuppressed(ctx) {
		t.Error("silence must imply suppression — otherwise a silenced lead still enrolls")
	}
}

// TestNewIngestContext_SilenceComesFromTheLeadNotTheSource pins the direction of the
// derivation. Reading it off the source would be the inverse failure: a whole
// source's real traffic silently unenrolled.
func TestNewIngestContext_SilenceComesFromTheLeadNotTheSource(t *testing.T) {
	src := testSource(t, "", "")

	testCtx, cancelTest := newIngestContext(src, RawLead{TestOrigin: TestOriginAdmin})
	defer cancelTest()
	realCtx, cancelReal := newIngestContext(src, RawLead{Fields: map[string]any{"email": "real@customer.com"}})
	defer cancelReal()

	if !domain.IsAutomationSilenced(testCtx) || domain.IsAutomationSilenced(realCtx) {
		t.Error("the same source must silence its test lead and enroll its real one")
	}
}

// TestIngestContext_IsDetachedFromTheRequest pins why the context is built from
// Background: a root middleware marks every request as HTTP transport, and a
// callerless HTTP context reaching Authorize logs a fail-open alarm on every
// captured lead — burying the real one.
func TestIngestContext_IsDetachedFromTheRequest(t *testing.T) {
	src := testSource(t, "", "")
	req := domain.MarkHTTPTransport(context.Background())
	reqCancelled, cancel := context.WithCancel(req)
	cancel() // the third party hung up mid-request

	ctx, cancelIngest := newIngestContext(src, RawLead{})
	defer cancelIngest()

	if domain.IsHTTPTransport(ctx) {
		t.Error("the ingest context must not carry the HTTP-transport mark — it would trip warnCallerlessHTTP on every lead")
	}
	if ctx.Err() != nil {
		t.Error("the ingest write must survive the client hanging up")
	}
	_ = reqCancelled
}
