package domain

import (
	"context"

	"github.com/google/uuid"
)

// Caller is the authenticated principal behind a request: their role name (the
// same string the auth middleware resolves from the JWT / org_users.role_id) and
// user id. RecordService reads it from the context to enforce Object-Level
// Security (which role) and to stamp the audit actor (which user).
//
// It is carried on the context — not threaded through every method signature —
// because the existing data-scope plumbing already works that way
// (repository.WithDataScope) and RecordService already receives the context. The
// auth middleware sets it for every protected route, so any user-facing record
// request has a caller. A request WITHOUT a caller is therefore an in-process,
// trusted call (automation, AI, a seed/migration) and OLS treats it as such.
type Caller struct {
	// Role is the role NAME — display/audit vocabulary only. Authorization keys
	// off RoleID exclusively (the P5 name-fallback bridge was deleted in P9); a
	// caller with no RoleID resolves to no grants and is default-denied.
	Role   string
	UserID uuid.UUID
	// RoleID is the caller's role identity (R1 re-key). Names are tenant-editable
	// vocabulary — a rename or duplicate must never change what a role can do, so
	// every authorization layer (OLS/FLS/capabilities/layouts) looks up grants by
	// this id. uuid.Nil means "not resolved": since the P9 bridge removal there is
	// no name fallback, so an unresolved role is default-denied everywhere.
	RoleID uuid.UUID
	// IsOwner marks the god-mode principal, resolved from roles.is_owner (never
	// the name string) by the auth middleware. Every owner bypass keys off this.
	IsOwner bool
	// DataScope is the caller's row visibility ('own' | 'all'), resolved from the
	// role's data_scope by the auth middleware (same value threaded to the repos via
	// repository.WithDataScope). Empty on a name-only bridge caller. The AI reads it
	// to fork own-scope and shape its persona (P7); REST relies on the repo-layer
	// scope, so it doesn't need to read this.
	DataScope string
	// IsAPIToken marks a request authenticated by a personal access token rather
	// than a session (U6.5). The token resolves to the SAME Caller its owner would
	// get — identical role, row scope and audit actor — with one addition:
	// TokenScopes narrows what it may do. Nothing downstream branches on this; only
	// the capability/OLS middleware does, to apply the intersection.
	IsAPIToken bool
	// TokenScopes are the scope codes the token was granted. Meaningful only when
	// IsAPIToken. The gate is an INTERSECTION with the role's own capabilities, and
	// it is applied BEFORE the owner-role bypass — otherwise a leaked owner token
	// would ignore the scopes its creator chose for it.
	TokenScopes []string
}

type callerCtxKey struct{}

// WithCallerIdentity returns a context carrying the full caller identity —
// role id + owner flag included (P5). Set once by the auth middleware after the
// membership is resolved.
func WithCallerIdentity(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerCtxKey{}, c)
}

// CallerFromContext returns the caller and true when one is present. A missing
// caller (ok=false) marks a trusted in-process call — see Caller's doc.
func CallerFromContext(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerCtxKey{}).(Caller)
	return c, ok
}

type requestMetaCtxKey struct{}

// WithRequestMeta carries the request's transport detail (IP, User-Agent) on the
// context so the admin usecases (workspace/role/permission) can stamp an
// auth_events row (P4) without threading RequestMeta through every method — the
// same pattern WithCallerIdentity uses. Set once by the auth middleware.
func WithRequestMeta(ctx context.Context, meta RequestMeta) context.Context {
	return context.WithValue(ctx, requestMetaCtxKey{}, meta)
}

// RequestMetaFromContext returns the request meta and true when present.
func RequestMetaFromContext(ctx context.Context) (RequestMeta, bool) {
	m, ok := ctx.Value(requestMetaCtxKey{}).(RequestMeta)
	return m, ok
}

type httpTransportCtxKey struct{}

// MarkHTTPTransport tags a context as HTTP-originated. Set by a root gin
// middleware on EVERY request — including ones on routes mounted outside
// AuthMiddleware — so the permission engine can distinguish "trusted in-process
// call" (no caller, no mark) from "HTTP request that somehow reached
// authorization without a caller" (no caller, marked), which is a wiring bug
// worth logging rather than a silent god-mode pass (U0.10).
func MarkHTTPTransport(ctx context.Context) context.Context {
	return context.WithValue(ctx, httpTransportCtxKey{}, true)
}

// IsHTTPTransport reports whether the context originated from an HTTP request.
func IsHTTPTransport(ctx context.Context) bool {
	v, _ := ctx.Value(httpTransportCtxKey{}).(bool)
	return v
}

type writeSourceCtxKey struct{}

// DefaultWriteSource is the origin stamped on a write that never declared one —
// i.e. every user-facing write through the app. It is the literal the record
// service's event emitters hardcoded before the write source became configurable,
// so an un-plumbed caller keeps producing byte-identical automation payloads.
const DefaultWriteSource = "crm_ui"

// AutomationSuppressedPayloadKey is the trigger-payload key the record service's
// emitters stamp when a write asked for suppression, and the automation engine
// reads to skip enrollment. It lives here because the two sides can't share a
// package (usecase must not import automation — the emitter is injected), and
// because the flag cannot ride the context all the way: the emitters deliberately
// detach onto context.Background() before handing off, so the payload is the only
// channel. Mirrors the engine's existing _internal_update convention.
const AutomationSuppressedPayloadKey = "_suppressed"

// AutomationSilencedPayloadKey is the stricter sibling of the suppression key: the
// emitters stamp it when a write asked for silence, and the automation engine reads
// it to skip date_field timer ARMING as well as enrollment. It lives here for the
// same reasons as the suppression key — usecase must not import automation, and the
// emitters detach onto context.Background() before handing off.
const AutomationSilencedPayloadKey = "_silenced"

// WithWriteSource names the channel a record write came from ("crm_ui",
// "webhook_inbound", "integration:google_ads", …). RecordService's emitters stamp
// it into the automation trigger payload as trigger.source, so a workflow can
// condition on where a record originated — the uniform write path is shared by the
// UI, the AI, automation actions and the lead-ingest pipeline, which are
// otherwise indistinguishable downstream.
//
// Naming a source is OPT-IN per entry point: one that never calls this keeps
// reporting DefaultWriteSource, which is why introducing this changed no existing
// behavior. Today no caller sets it, so UI, AI and automation-action writes all
// still report "crm_ui" — each becomes distinguishable only once its own entry
// point is plumbed.
//
// It rides on the context for the same reason Caller does: the write path already
// threads ctx end-to-end and the emitters sit several layers below the entry
// point. An empty source is ignored so a caller can't accidentally blank the
// default.
func WithWriteSource(ctx context.Context, source string) context.Context {
	if source == "" {
		return ctx
	}
	return context.WithValue(ctx, writeSourceCtxKey{}, source)
}

// WriteSourceFromContext returns the write's origin channel, falling back to
// DefaultWriteSource when unset.
func WriteSourceFromContext(ctx context.Context) string {
	s, ok := ctx.Value(writeSourceCtxKey{}).(string)
	if !ok || s == "" {
		return DefaultWriteSource
	}
	return s
}

type partialWriteCtxKey struct{}

// WithPartialWrite marks a write that is NOT a form submission, so form
// completeness rules must not be applied to it.
//
// Concretely: an admin who marks a custom field "required" means "a human filling
// this record in must provide it". They cannot plausibly mean "silently reject
// inbound leads that lack it" — and that is what a required-field check does to an
// ingested lead, because the check ranges over the org's DEFINITIONS, not over the
// payload. A lead from a Facebook form cannot know about the org's required
// "Contract Value" field, and rejecting it would be the one failure this whole
// subsystem exists to prevent: a lead lost with nobody watching.
//
// It relaxes PRESENCE only. Every value the write DOES carry is still type-checked,
// and the field allowlist still governs which keys may be written at all — so this
// is not a validation bypass, it is the difference between "this value is wrong"
// (still rejected) and "this record is incomplete" (not this write's problem).
//
// Server-side only. It is set by the lead-ingest service and by nothing reachable
// from an HTTP payload; a caller who could set it could file records that skip the
// org's own completeness rules.
func WithPartialWrite(ctx context.Context) context.Context {
	return context.WithValue(ctx, partialWriteCtxKey{}, true)
}

// IsPartialWrite reports whether required-field presence should be relaxed.
func IsPartialWrite(ctx context.Context) bool {
	v, _ := ctx.Value(partialWriteCtxKey{}).(bool)
	return v
}

type automationSuppressedCtxKey struct{}

// WithAutomationSuppressed marks a write whose automation events must not enroll
// workflow runs. Bulk and synthetic writes need this: importing 500 historical
// leads, or injecting an admin's test lead, would otherwise enroll every one of
// them into every contact_created workflow — the engine's idempotency key is
// per-entity-per-minute, so it absorbs none of that (a welcome-email blast to
// stale leads is the failure mode).
//
// Suppression stops RUN CREATION only. date_field timers still materialize from a
// suppressed write: a backfilled record's future schedule (its close-date
// reminder) is not the thing being prevented — the enrollment storm is. So a
// suppressed write is quiet, NOT silent: if the org has a date_field workflow on
// the object, a timer arms now and fires a real run later.
//
// Use this for a write about a REAL record that must not stampede (backfill,
// import). A write about a record that describes NOBODY — a synthetic test lead —
// wants WithAutomationSilenced instead.
//
// SCOPE — honored only on writes through RecordService, whose three emitters
// consult it. The legacy delivery/http handlers (contact/deal/custom-object)
// build their own trigger payloads and would enroll normally. That is not a live
// gap: those are user-facing HTTP routes, and nothing sets this flag on a request
// context — it is set by server-side bulk writers, which all go through
// RecordService. Any NEW server-side writer must route through RecordService to
// inherit suppression, rather than emitting its own payload.
//
// It is deliberately distinct from the engine's _internal_update loop guard: that
// marks writes CAUSED BY automation, this is chosen by the caller of the write.
func WithAutomationSuppressed(ctx context.Context) context.Context {
	return context.WithValue(ctx, automationSuppressedCtxKey{}, true)
}

// IsAutomationSuppressed reports whether this write's events should skip workflow
// enrollment. Read by the record service's emitters, which translate it into
// AutomationSuppressedPayloadKey on the payload.
//
// Silence DERIVES suppression rather than duplicating it, and that direction is
// deliberate. The two failures are not equally expensive: forgetting to honor
// silence costs one stray timer, while forgetting to honor suppression costs an
// enrollment storm. Deriving here means a caller cannot ask for silence and get a
// stampede — the cheap mistake is the only one still available.
func IsAutomationSuppressed(ctx context.Context) bool {
	v, _ := ctx.Value(automationSuppressedCtxKey{}).(bool)
	return v || IsAutomationSilenced(ctx)
}

// automationSilencedCtxKey is its own key, never a second value under the
// suppression key: that reader discards the comma-ok, so a non-bool stored there
// would read false and silently un-suppress the write.
type automationSilencedCtxKey struct{}

// WithAutomationSilenced marks a write that nothing may ever reach a human about.
// Stricter than WithAutomationSuppressed: it skips workflow enrollment AND stops
// date_field timers from arming.
//
// The distinction is about what the record IS, not about volume. A backfilled lead
// is a real person whose close-date reminder should still fire, so it wants mere
// suppression. A test lead describes nobody: there is no future to schedule, and a
// timer armed on it pages a real rep about a person who does not exist.
//
// That failure is unrecoverable through the product, which is why this flag exists
// rather than leaning on "just delete the test record". Cancellation is emitted by
// RecordService.Delete alone; the UI's own contact delete
// (DELETE /api/contacts/:id → contactUseCase.Delete) emits nothing at all, so the
// timer outlives the record it describes and fires anyway. Nor can it be fixed
// downstream: fireTimerRun creates the run straight from the stored timer payload
// and consults no suppression predicate, so a flag propagated into that payload is
// a no-op that reads like a fix. The only moment silence can be honored is before
// the timer is armed.
//
// SCOPE — like suppression, honored only on writes through RecordService, whose
// three emitters consult it. A new server-side writer must route through
// RecordService to inherit it.
func WithAutomationSilenced(ctx context.Context) context.Context {
	return context.WithValue(ctx, automationSilencedCtxKey{}, true)
}

// IsAutomationSilenced reports whether this write's events should skip both
// enrollment and date_field arming.
func IsAutomationSilenced(ctx context.Context) bool {
	v, _ := ctx.Value(automationSilencedCtxKey{}).(bool)
	return v
}
