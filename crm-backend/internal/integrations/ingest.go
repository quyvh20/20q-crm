package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// ingestTimeout bounds a single lead's write. The ingest context is deliberately
// detached from the request (see newIngestContext), so without this it would have
// no deadline at all.
const ingestTimeout = 30 * time.Second

// RecordWriter is the narrow slice of RecordService ingestion needs.
type RecordWriter interface {
	Create(ctx context.Context, orgID, userID uuid.UUID, slug string, in domain.RecordWriteInput) (*domain.UniformRecord, error)
	Update(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID, in domain.RecordWriteInput) (*domain.UniformRecord, error)
}

// ContactMatcher resolves an inbound lead to an existing contact. Its
// implementation is deliberately unscoped — see ContactRepository.FindByNormalizedEmail.
type ContactMatcher interface {
	FindByNormalizedEmail(ctx context.Context, orgID uuid.UUID, email string) (*domain.Contact, error)
	// FindByNormalizedPhone returns ALL matches — a shared phone is normal, so the
	// caller must see ambiguity rather than be handed one row to merge into.
	FindByNormalizedPhone(ctx context.Context, orgID uuid.UUID, digits string) ([]domain.Contact, error)
}

// RawLead is one inbound delivery, already parsed but not yet trusted.
type RawLead struct {
	Fields  map[string]any
	Context map[string]any
	Consent map[string]any
	// ProviderEventID is the delivery's stable id across retries. Empty means the
	// delivery cannot be deduped.
	ProviderEventID string
	// TestOrigin names WHO called this a test (see the TestOrigin consts), or is
	// empty for an ordinary lead. Never read off a caller's payload for the admin
	// origin — a caller who could set it would be able to file real-looking leads
	// that never page sales.
	TestOrigin string
	// DeliveryMode names WHICH ROUTE this arrived on, set by the handler from the
	// route itself and never from the body. It decides workflow enrollment for batch
	// deliveries, so a wire-settable value would let a leaked key file real leads
	// that page nobody.
	DeliveryMode string
	// EnrollAutomation lifts the source's opt-in for batch deliveries. Meaningless
	// for the direct route, which always enrols.
	EnrollAutomation bool
}

// IsTest reports whether this lead traverses the pipeline without reaching anyone.
func (l RawLead) IsTest() bool { return l.TestOrigin != TestOriginNone }

// IngestResult is what the caller reports back to the third party.
type IngestResult struct {
	EventID   uuid.UUID
	RecordID  uuid.UUID
	Outcome   string
	Duplicate bool
	// Quarantined names the payload keys that were recorded but NOT written, so an
	// integrator learns at integration time instead of from missing data weeks on.
	Quarantined []string
	// Note explains a judgement call the pipeline made — today, a refusal to merge
	// into an ambiguous phone match. Surfaced on the delivery so the decision is
	// visible rather than looking like an unexplained duplicate.
	Note string
	// Warnings are said to the CALLER, at integration time. A lead nobody owns must
	// not be discoverable only by someone reading the ledger weeks later.
	Warnings []string
	// OwnerID is who the lead was routed to (nil = unowned). Reported so a test lead
	// can name the rep, and persisted so a retry reuses it.
	OwnerID *uuid.UUID
	// DealID is the opportunity this lead also produced, when the source asked for
	// one. Reported to the caller so a Make/Zapier scenario can chain onto it.
	DealID *uuid.UUID
	// Fields are the values actually WRITTEN on the create branch, read back off the
	// persisted record. Set there and only there — it exists to name a deal after the
	// contact it points at, and the create branch is the only branch that makes one.
	// (create() synthesizes first_name for email-only leads, and the field map can
	// rename or drop keys, so the inbound payload is the wrong source for this.)
	Fields map[string]any
}

// LeadIngestService is the one path every inbound lead takes, whatever channel it
// arrived on.
type LeadIngestService struct {
	repo    *Repository
	records RecordWriter
	matcher ContactMatcher
	schema  SchemaProvider
	fields  FieldDefManager
	// members answers "may this person be handed a lead" for owner routing.
	members MemberChecker
	// stages answers "is the configured deal stage still real". Nil-tolerant: a
	// source with no deal option never consults it.
	stages StageReader
	// logger surfaces routing degradations. A lead that lands unowned, or a rotation
	// that could not be read, is invisible otherwise — the write still succeeds.
	logger *slog.Logger
	// health announces the recovery edge. Nil-tolerant on purpose: every method on
	// *HealthReporter no-ops on a nil receiver, so a deployment without notifications
	// wired keeps capturing leads exactly as before rather than panicking on the
	// success path.
	health *HealthReporter
}

// NewLeadIngestService builds the pipeline.
func NewLeadIngestService(repo *Repository, records RecordWriter, matcher ContactMatcher, schema SchemaProvider, fields FieldDefManager, members MemberChecker, stages StageReader, logger *slog.Logger) *LeadIngestService {
	return &LeadIngestService{repo: repo, records: records, matcher: matcher, schema: schema, fields: fields, members: members, stages: stages, logger: logger}
}

// WithHealthReporter wires health alerting after construction — a setter rather than
// a constructor arg so the many existing call sites (and every test) stay unchanged,
// matching Handler.WithConnections.
func (s *LeadIngestService) WithHealthReporter(h *HealthReporter) *LeadIngestService {
	s.health = h
	return s
}

// newIngestContext builds the context every ingest write runs on. THE ONLY PLACE
// this context is constructed — the trusted actor is a security decision, not an
// incidental one, so it gets exactly one definition.
//
// It starts from context.Background(), never the request context, for two reasons:
//
//  1. A root gin middleware stamps MarkHTTPTransport on EVERY request, including
//     routes mounted outside auth. A callerless HTTP context reaching Authorize
//     logs "fail-open invariant violated" — which would fire on every captured
//     lead and bury a real alarm in noise. (context.WithoutCancel does not help:
//     the mark is a context VALUE and would be carried.)
//  2. The write must not die because the third party hung up mid-request.
//
// No domain.Caller is attached: a callerless context is a trusted in-process call,
// so OLS/FLS do not constrain it. That is why the allowlist exists, and why a
// source's target_slug is re-validated here rather than trusted from the row.
func newIngestContext(source *LeadSource, lead RawLead, writeTimeout time.Duration) (context.Context, context.CancelFunc) {
	ctx := domain.WithWriteSource(context.Background(), source.WriteSource())
	// A lead is not a form submission: type-check every value it carries, but do
	// not demand the org's required fields be present. A Facebook form cannot know
	// about an org's required "Contract Value", and rejecting the lead over it
	// would be the silent loss this whole subsystem exists to prevent.
	ctx = domain.WithPartialWrite(ctx)
	// A test lead is about nobody, so nothing about it may reach a human: no
	// workflow enrollment, and no date_field timer armed to page a rep next week
	// about a contact that does not exist. Silenced rather than merely suppressed —
	// suppression would still arm the timer, and the UI's contact delete emits no
	// cancellation, so it would outlive the record.
	//
	// Derived from the LEAD, never the source. A source-level property here would be
	// the inverse failure and a far worse one: every real lead from that source
	// would land unenrolled, silently, with a ledger that looks completely normal.
	if lead.IsTest() {
		ctx = domain.WithAutomationSilenced(ctx)
	}
	// A batch is a bulk delivery: 100 recovered leads would otherwise enrol 100
	// contacts into every contact_created workflow at once, and the engine's
	// per-entity-per-minute idempotency absorbs none of it (a welcome-email blast to
	// stale leads is the failure mode). SUPPRESSED, not silenced — these are real
	// people, so their own future date_field reminders should still arm.
	//
	// An INDEPENDENT if, never an else: a test lead delivered by batch must stay
	// silenced, which is the stricter of the two.
	//
	// Backfill (L5.4) is bulk too: importing 500 historical leads must not enrol 500
	// contacts into every contact_created workflow, so it suppresses by the same rule
	// unless the admin explicitly opted to enrol.
	if isBulkDelivery(lead.DeliveryMode) && !lead.EnrollAutomation {
		ctx = domain.WithAutomationSuppressed(ctx)
	}
	return context.WithTimeout(ctx, writeTimeout)
}

// newLedgerContext returns a context for the ledger writes.
//
// The ledger must be at least as durable as the record write it describes. The
// record write is deliberately detached from the request so a client hangup cannot
// tear it in half — but if the ledger write is NOT, a third party closing the
// connection mid-request leaves the contact written and the event stranded in
// `processing` forever: attribution lost (this table is the only source-attribution
// record), the customer's ledger showing a permanently in-flight row, and the daily
// cap never incremented because it counts terminal outcomes.
//
// WithoutCancel rather than Background: this ctx never reaches Authorize, so
// carrying the request's MarkHTTPTransport value is harmless here — unlike the
// ingest write context, where it would trip the fail-open alarm.
// The ledger deadline is deliberately LONGER than the write's: it is armed FIRST
// and every ledger call hangs against it, so if it expired first a slow write would
// make post-write bookkeeping fail systematically — stranding rows at `processing`,
// which is the exact state the reaper exists to clean up.
func newLedgerContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

// writeTimeoutFor bounds one record write. A batch gives each item a shorter slice
// so a single slow write cannot eat the whole request budget.
func (s *LeadIngestService) writeTimeoutFor(lead RawLead) time.Duration {
	if isBulkDelivery(lead.DeliveryMode) {
		return batchWriteTimeout
	}
	return ingestTimeout
}

// ledgerTimeoutFor bounds one delivery's bookkeeping.
func (s *LeadIngestService) ledgerTimeoutFor(lead RawLead) time.Duration {
	if isBulkDelivery(lead.DeliveryMode) {
		return batchLedgerTimeout
	}
	return ingestTimeout
}

// Ingest runs one lead end-to-end: dedupe the delivery, filter it against the
// allowlist, match or create, and record what happened.
func (s *LeadIngestService) Ingest(ctx context.Context, source *LeadSource, lead RawLead) (*IngestResult, error) {
	// ── 1. Dedupe the delivery ───────────────────────────────────────────
	// Before any side effect: a provider redelivery must return the original
	// result, not repeat it.
	if !IsSupportedTarget(source.TargetSlug) {
		// Defence in depth against a row edited by hand — the management API already
		// rejects an unsupported target at configuration time.
		return nil, domain.NewAppError(http.StatusBadRequest, "unsupported target object: "+source.TargetSlug)
	}

	// Every ledger write runs on this, never on the request context — see
	// newLedgerContext. Established before the first insert so no exit path can
	// fall back to a context the client can cancel.
	ledgerCtx, ledgerCancel := newLedgerContext(ctx, s.ledgerTimeoutFor(lead))
	defer ledgerCancel()

	var providerID *string
	if lead.ProviderEventID != "" {
		id := lead.ProviderEventID
		providerID = &id
	}
	raw, _ := json.Marshal(lead.Fields)
	ctxJSON, _ := json.Marshal(lead.Context)

	event := &IntegrationEvent{
		OrgID:           source.OrgID,
		SourceID:        &source.ID,
		ProviderEventID: providerID,
		// Synchronous channels insert as processing, never pending: pending is the
		// async worker's claimable state, and a worker must never pick up a request
		// that is still in flight.
		Status:     EventStatusProcessing,
		Attempts:   1,
		RawPayload: datatypes.JSON(raw),
		Context:    datatypes.JSON(ctxJSON),
	}
	inserted, err := s.repo.InsertEventDeduped(ledgerCtx, event)
	if err != nil {
		return nil, err
	}
	if !inserted {
		prior, ferr := s.repo.FindEventByProviderID(ledgerCtx, source.ID, lead.ProviderEventID)
		if ferr != nil {
			return nil, ferr
		}
		if prior == nil { // lost a race with a concurrent identical delivery
			return nil, domain.NewAppError(http.StatusConflict, "a delivery with this Idempotency-Key is in flight; retry shortly")
		}
		// Replay ONLY a terminal success. Deduping on mere existence would make
		// Idempotency-Key — the documented way to make a retry SAFE — the very thing
		// that makes it a permanent no-op: a first attempt that failed before the
		// write (transient DB error, RecordService failure) would leave a `failed`
		// row, and every retry would conflict on it and be told "200, already done"
		// while no contact was ever written. A well-behaved Make/Zapier scenario
		// retrying a 500 would lose the lead and never know.
		switch prior.Status {
		case EventStatusProcessed, EventStatusTest, EventStatusDuplicate:
			if prior.ResultRecordID == nil {
				// Terminal-success status with no record is a bookkeeping bug, not a
				// success to replay; treat it as retryable rather than confirming a
				// write that may not exist.
				return nil, domain.NewAppError(http.StatusConflict, "a prior delivery with this Idempotency-Key is incomplete; retry shortly")
			}
			return &IngestResult{EventID: prior.ID, RecordID: *prior.ResultRecordID, Outcome: prior.Outcome, Duplicate: true}, nil
		case EventStatusFailed, EventStatusQuarantined:
			// The prior attempt never wrote anything, so the key is free to reuse.
			// Re-run the pipeline against the existing row instead of banking a
			// phantom success.
			//
			// `quarantined` belongs here with `failed`, and leaving it out was a live
			// lead-loss bug in the endpoint whose entire purpose is recovery: a batch
			// item refused for running out of the 40s budget is written quarantined
			// with its idempotency key, is told `retryable: true` and "resend it" —
			// and the resend then conflicted on that key, fell to the default arm, and
			// got 409 "in flight; retry shortly" forever. The reaper never released it
			// either, since it only touches `processing`. A row that is rejected
			// BEFORE any write is by definition safe to re-run.
			event = prior
			event.Status = EventStatusProcessing
			event.Attempts = prior.Attempts + 1
			event.Error = ""
			event.RawPayload = datatypes.JSON(raw)
			event.Context = datatypes.JSON(ctxJSON)
		default:
			// Still in flight (or stranded). Answer 409 so the caller retries rather
			// than recording a success that may never happen.
			return nil, domain.NewAppError(http.StatusConflict, "a delivery with this Idempotency-Key is in flight; retry shortly")
		}
	}

	return s.processEvent(ledgerCtx, source, lead, event)
}

// IngestClaimed processes a webhook delivery whose event row the async worker has
// ALREADY inserted (as pending, at receipt) and claimed (pending→processing). It
// is the async counterpart to Ingest: it never inserts or dedupes the event —
// that happened at webhook receipt and claim — it stamps the fetched payload onto
// the row and runs the shared processing core. The fetched field_data becomes the
// ledger's raw payload (the receipt only had the leadgen id), and the resolved
// source is stamped so the delivery log can name it.
func (s *LeadIngestService) IngestClaimed(ctx context.Context, source *LeadSource, lead RawLead, event *IntegrationEvent) (*IngestResult, error) {
	ledgerCtx, ledgerCancel := newLedgerContext(ctx, s.ledgerTimeoutFor(lead))
	defer ledgerCancel()
	if !IsSupportedTarget(source.TargetSlug) {
		return nil, s.failEvent(ledgerCtx, event, domain.NewAppError(http.StatusBadRequest, "unsupported target object: "+source.TargetSlug))
	}
	raw, _ := json.Marshal(lead.Fields)
	ctxJSON, _ := json.Marshal(lead.Context)
	event.RawPayload = datatypes.JSON(raw)
	event.Context = datatypes.JSON(ctxJSON)
	event.SourceID = &source.ID
	return s.processEvent(ledgerCtx, source, lead, event)
}

// processEvent runs the map → match → create/update → attribute → deal → consent
// → finish core against an event row the caller has ALREADY acquired — inserted
// as `processing` (sync Ingest) or claimed from `pending` (async IngestClaimed).
// It never inserts or dedupes; keeping the acquisition out of here is what lets
// one body serve both channels without a worker ever re-running the dedupe switch.
func (s *LeadIngestService) processEvent(ledgerCtx context.Context, source *LeadSource, lead RawLead, event *IntegrationEvent) (*IngestResult, error) {
	// ── 2. Map, then allowlist ───────────────────────────────────────────
	allow, err := BuildAllowlist(ledgerCtx, s.schema, source.OrgID, source.TargetSlug)
	if err != nil {
		return nil, s.failEvent(ledgerCtx, event, err)
	}

	// Mapping runs FIRST: a source calls it "Work Email", we call it "email". It
	// does not bypass the allowlist — a map pointing somewhere forbidden still gets
	// quarantined below — so a hand-edited row cannot become a hole.
	fmap, ferr := ParseFieldMap(source.FieldMap)
	if ferr != nil {
		return nil, s.failEvent(ledgerCtx, event, domain.NewAppError(http.StatusInternalServerError, "this source's field mapping is unreadable"))
	}
	incoming, mapFailures := fmap.Apply(lead.Fields)

	fields, quarantined := allow.Apply(incoming)
	// A mapping that could not be applied is quarantined like any other unusable
	// key: recorded, visible, and never a reason to reject the lead.
	for k, reason := range mapFailures {
		quarantined[k] = reason
	}
	quarantinedKeys := make([]string, 0, len(quarantined))
	for k := range quarantined {
		quarantinedKeys = append(quarantinedKeys, k)
	}
	sort.Strings(quarantinedKeys) // stable output; map order would churn the response
	if len(quarantined) > 0 {
		q, _ := json.Marshal(quarantined)
		event.QuarantinedFields = datatypes.JSON(q)
	}

	// A PROVIDER test (Google's "Send test data") carries a real payload we did not
	// build — sample values, a documented dummy phone, an email like
	// test@example.com. The payload has now done its one job (exercising the real
	// field map above; unmapped questions are already quarantined and observable),
	// so its IDENTITY is replaced before anything can be matched or written:
	//
	//   - email is COERCED to the synthetic address, not asserted. Without this the
	//     advertiser's SECOND test click hard-fails: click 1 creates a contact with
	//     Google's sample email, click 2 misses findTestMatch (which looks only for
	//     the synthetic address), collides with the UNIQUE (org_id, email) index,
	//     and the provenance guard then refuses the winner because test@example.com
	//     is not ours. Coerced, every click converges on one flagged test contact
	//     per source — the admin button's exact shape.
	//   - phone is STRIPPED, not asserted (the admin path 500s on a phone; a
	//     provider test must not, or every phone-bearing form fails setup with a red
	//     error in Google's UI). Google's dummy number must never become a
	//     phone-matchable value on the test contact: match_fields includes phone for
	//     these sources, so a stored dummy would be a magnet for any later lead
	//     carrying the same digits.
	coerceProviderTestIdentity(source, lead.TestOrigin, fields)

	// Normalize the match key BEFORE the write so it agrees with the unique index.
	// The index is on raw (org_id, email) — case-SENSITIVE — while matching is
	// case-insensitive. Writing the normalized form is what makes the two agree, so
	// a concurrent duplicate actually raises 23505 and the upsert loop below can
	// catch it. Skip this and "John@X.com" + "john@x.com" both insert silently.
	email := normalizeEmail(stringOf(fields["email"]))
	if email == "" && requiresEmail(source) {
		// Without another way to match, an email-less lead can neither match nor
		// conflict (the unique index is partial on email IS NOT NULL), so every
		// resubmission would insert another row. Sources that match on phone lift
		// this — that is what makes the phone-only Facebook shape safe to accept.
		return nil, s.failEvent(ledgerCtx, event, domain.NewAppError(422, "email is required for this source"))
	}
	if email != "" {
		fields["email"] = email
	} else {
		delete(fields, "email") // never write "" — it is a value, not an absence
	}

	// The synthetic identity must have survived the mapping. Checked here, after the
	// map and the allowlist have had their say and before anything is matched or
	// written, because those are exactly the steps that can delete it.
	if err := assertTestIdentity(source, lead.TestOrigin, email); err != nil {
		return nil, s.failEvent(ledgerCtx, event, err)
	}
	if lead.IsTest() {
		if err := assertNoPhone(fields); err != nil {
			return nil, s.failEvent(ledgerCtx, event, err)
		}
	}

	// Parsed before the write so a malformed envelope is warned about on the same
	// response as the lead itself. It never gates the lead — see parseConsent.
	consent := parseConsent(lead.Consent)

	// ── 3/4. Match, then create or update ────────────────────────────────
	ictx, cancel := newIngestContext(source, lead, s.writeTimeoutFor(lead))
	defer cancel()

	// Attribution: which channel produced this lead. Resolved through the org's
	// field map because a collision may have pushed a key under the crm_ prefix.
	// A failure here must not cost the lead — attribution is worth a lot, but not
	// as much as the lead itself.
	attr := attributionValues(source, parseLeadContext(lead.Context))
	amap, aerr := SeedAttributionFields(ictx, s.fields, source.OrgID, source.TargetSlug)
	if aerr != nil {
		amap = nil
	}

	// A prior attempt on this same delivery already picked a rep. Reuse it rather
	// than taking a second ticket: the ticket belongs to the DELIVERY, not the
	// attempt. Without this, a form where every other lead fails would hand one rep
	// 100% of the contacts while the ledger looked green.
	priorOwner, perr := s.repo.GetEventAssignedOwner(ledgerCtx, event.ID)
	if perr != nil {
		s.logf("integrations: could not read prior routing", "event_id", event.ID.String(), "error", perr)
	}

	result, err := s.upsert(ictx, source, lead, fields, email, amap, attr, priorOwner)
	if err != nil {
		return nil, s.failEvent(ledgerCtx, event, err)
	}
	// Persisted BEFORE the outcome, so a crash between here and FinishEvent still
	// leaves the retry able to reuse the same rep.
	if serr := s.repo.SetEventAssignedOwner(ledgerCtx, event.ID, result.OwnerID); serr != nil {
		s.logf("integrations: could not record routing", "event_id", event.ID.String(), "error", serr)
	}

	// The lead may also be an opportunity. Runs HERE — after the contact exists (the
	// deal links it) and before FinishEvent (so the delivery records what it made) —
	// and on ictx, never ledgerCtx: the deal write must inherit the same silencing
	// and suppression marks the contact write ran under, or a batch of 100 recovered
	// leads fires 100 deal_created workflows the contact half deliberately did not.
	s.maybeCreateDeal(ictx, source, lead, result, attr)

	// Consent is written HERE — after the record exists — and nowhere else.
	//
	// Erasure is contact-keyed, so an envelope stored before the write would survive
	// on a failed delivery whose result_record_id is NULL, where no erasure request
	// could ever reach it. Writing it here means every consent record in the table is
	// reachable by the person it describes. The cost is deliberate: a lead that fails
	// to write loses its envelope, which is right — consent about someone who never
	// became a contact proves nothing and would be unerasable.
	if len(consent.Envelope) > 0 {
		if n, cerr := s.repo.SetEventConsent(ledgerCtx, event.ID, consent.Envelope); cerr != nil || n == 0 {
			s.logf("integrations: could not record consent", "event_id", event.ID.String(), "error", cerr)
			consent.Warnings = append(consent.Warnings, "consent was sent but could not be recorded on this delivery")
		}
	}
	result.Warnings = append(result.Warnings, consent.Warnings...)

	// ── 7. Record the outcome ────────────────────────────────────────────
	// On the ledger context: the record is already written, so losing this to a
	// client hangup would strand the row and lose the attribution permanently.
	//
	// Clear any error a PRIOR attempt left on this row: the async worker re-claims a
	// re-pended event whose Error was stamped by RependEvent/the reaper (Error is a
	// mapped column, hydrated by ClaimPendingEvents' RETURNING *), and a `processed`
	// row carrying a stale error reads as a failed delivery in the UI's red box.
	// Error means failure, Note means success — a success must own neither the other's.
	event.Error = ""
	event.Status = EventStatusProcessed
	if lead.IsTest() {
		event.Status = EventStatusTest
	}
	event.ResultSlug = source.TargetSlug
	event.ResultRecordID = &result.RecordID
	event.Outcome = result.Outcome
	// A judgement call the pipeline made (e.g. refusing to merge into an ambiguous
	// phone match) belongs on the delivery, or the resulting duplicate looks like a
	// bug rather than a decision. Note, not Error: this delivery SUCCEEDED.
	event.Note = result.Note
	if err := s.repo.FinishEvent(ledgerCtx, event); err != nil {
		return nil, err
	}
	// A test lead is not usage. TouchSourceUsed stamps last_used_at AND resets
	// consecutive_failures, so letting the button call it would make a source that has
	// never seen a real lead look live, and — worse, since a broken source is exactly
	// when someone reaches for this button — would clear the error state L6 alerts on,
	// on evidence from the one layer the test never touches. A diagnostic that
	// silences its own alarm is worse than no diagnostic.
	if !lead.IsTest() {
		// best-effort; the lead is already written. `healed` is the recovery edge:
		// true only on the delivery that actually un-flipped an `error` badge, so the
		// "working again" notification cannot fire on every subsequent success.
		healed, err := s.repo.TouchSourceUsed(ledgerCtx, source.ID)
		if err == nil && healed {
			s.health.SourceRecovered(source.OrgID, source.ID, source.Name, source.CreatedBy)
		}
		// last_synced_at is stamped HERE and nowhere else — this is the only point at
		// which a provider delivery has demonstrably become a record. The tempting
		// place is the connection heal in the webhook processor, but that fires right
		// after FetchLead and before the form is resolved, so a connection whose every
		// delivery is quarantined would report a fresh sync time forever.
		if event.ConnectionID != nil {
			_ = s.repo.MarkConnectionSynced(ledgerCtx, *event.ConnectionID)
		}
	}

	result.EventID = event.ID
	result.Quarantined = quarantinedKeys
	return result, nil
}

// upsert is the create-or-update loop.
//
// The match-then-create sequence has an unavoidable race: two first-time
// deliveries for the same email can both miss the match and both create. The loser
// hits the unique index. Rather than fail that lead (the legacy webhook's bug —
// it returned 200 with a phantom id), we recognise the conflict and fall through
// to the update branch, which is what the winner's row now needs anyway.
//
// This depends on contactUseCase.Create surfacing the conflict as
// domain.ErrContactEmailExists instead of a blanket 500 — landed as a prerequisite.
//
// An AMBIGUOUS match (several contacts share the lead's phone) arrives here as
// no match at all, by design: findMatch refuses to guess, so the lead becomes a new
// contact and the reason is recorded on the delivery so a human can merge it.
func (s *LeadIngestService) upsert(ctx context.Context, source *LeadSource, lead RawLead, fields map[string]any, email string, amap AttributionMap, attr map[string]string, priorOwner *uuid.UUID) (*IngestResult, error) {
	match, err := s.resolveMatch(ctx, source, lead, fields, email)
	if err != nil {
		return nil, err
	}

	if match.Contact == nil {
		// The rotation ticket is taken HERE, at the only place ownership is ever
		// stamped — not in Ingest. Resolving earlier would be this feature's worst
		// bug: a source whose traffic is 90% resubmissions would burn 90% of its
		// turns on leads that never get an owner, and the rotation would degenerate
		// into noise while looking perfectly healthy.
		own := s.resolveOwner(ctx, source, lead, priorOwner)
		rec, cerr := s.create(ctx, source, fields, amap, attr, own.OwnerID)
		if cerr == nil {
			return &IngestResult{
				RecordID: rec.ID,
				Outcome:  OutcomeCreated,
				Fields:   rec.Fields,
				// Both notes can be real at once: an ambiguous-phone lead arrives as
				// no-match WITH a note and then creates. Overwriting either way deletes a
				// disclosure someone needs.
				Note:     joinNotes(match.AmbiguityNote, own.Note),
				Warnings: warningsOf(own),
				OwnerID:  own.OwnerID,
			}, nil
		}
		if !errors.Is(cerr, domain.ErrContactEmailExists) {
			return nil, cerr
		}
		// Lost an email race — re-read the winner and update it instead. Note this
		// guard exists only for email: the phone index cannot be UNIQUE (shared
		// numbers are legitimate), so no 23505 is raised and two concurrent
		// phone-only leads both insert. Documented, not silently pretended away.
		existing, ferr := s.matcher.FindByNormalizedEmail(ctx, source.OrgID, email)
		if ferr != nil {
			return nil, ferr
		}
		if existing == nil {
			return nil, cerr // conflicted but unfindable: report the original error
		}
		match = &MatchResult{Contact: existing, MatchedOn: MatchEmail}
	}

	existing := match.Contact
	// The one line where the pipeline commits to a pre-existing row — so the test
	// lead's provenance is checked HERE, covering both the matched branch and the
	// lost-email-race branch above (a 23505 proves only that SOME live row holds the
	// address, not whose).
	//
	// Before the create_only return, not after: create_only writes nothing, but it
	// still hands back the matched record's id as the test's result, and the UI
	// deep-links it. "Here is your test lead" pointing at a real customer is the same
	// failure as editing them, minus the edit.
	if lead.IsTest() {
		if err := assertTestProvenance(source, existing); err != nil {
			return nil, err
		}
	}
	if source.UpdatePolicy == UpdatePolicyCreateOnly {
		return &IngestResult{RecordID: existing.ID, Outcome: OutcomeUpdated, Note: match.AmbiguityNote}, nil
	}
	upd := s.updateFields(source, fields, existing)
	// Last touch only: how this person FIRST reached us is a fact about the past,
	// and rewriting it on every resubmission is how a CRM ends up reporting that
	// every customer came from "newsletter".
	applyAttribution(upd, amap, attr, false)
	if len(upd) == 0 {
		return &IngestResult{RecordID: existing.ID, Outcome: OutcomeUpdated, Note: match.AmbiguityNote}, nil
	}
	rec, err := s.records.Update(ctx, source.OrgID, source.TargetSlug, existing.ID, domain.RecordWriteInput{Fields: upd})
	if err != nil {
		return nil, err
	}
	return &IngestResult{RecordID: rec.ID, Outcome: OutcomeUpdated, Note: match.AmbiguityNote}, nil
}

// resolveMatch picks the matching strategy for this lead.
//
// A test lead does NOT use the source's match_fields — see findTestMatch. The
// dispatch lives here, on the one path into matching, so there is no second route a
// test lead could take into the real predicates.
func (s *LeadIngestService) resolveMatch(ctx context.Context, source *LeadSource, lead RawLead, fields map[string]any, email string) (*MatchResult, error) {
	if lead.IsTest() {
		return s.findTestMatch(ctx, source)
	}
	return s.findMatch(ctx, source, fields, email)
}

// create writes a new record. first_name is synthesized HERE, on the create branch
// only — the adapter 400s a blank one, but synthesizing on update would overwrite a
// real name with "Lead" every time an email-only lead resubmitted.
func (s *LeadIngestService) create(ctx context.Context, source *LeadSource, fields map[string]any, amap AttributionMap, attr map[string]string, owner *uuid.UUID) (*domain.UniformRecord, error) {
	out := copyFields(fields)
	// First touch: stamp everything, once.
	applyAttribution(out, amap, attr, true)
	if strings.TrimSpace(stringOf(out["first_name"])) == "" {
		out["first_name"] = synthesizeFirstName(stringOf(fields["email"]))
	}
	// Ownership comes from config, never the wire, and is resolved by the caller
	// (see resolveOwner). Stamped only here: on update the adapter reads a present
	// owner_user_id as an instruction, and a null one as UNASSIGN. An unowned contact
	// is invisible to own-scoped reps.
	if owner != nil {
		out["owner_user_id"] = owner.String()
	}
	// userID is uuid.Nil: the contact path never reads it (only the custom-object
	// path does, which is why target_slug is restricted to contact).
	return s.records.Create(ctx, source.OrgID, uuid.Nil, source.TargetSlug, domain.RecordWriteInput{Fields: out})
}

// updateFields applies the source's update policy to a matched record.
func (s *LeadIngestService) updateFields(source *LeadSource, fields map[string]any, existing *domain.Contact) map[string]any {
	out := map[string]any{}
	for k, v := range fields {
		if k == "owner_user_id" || k == "company" {
			continue // never from the wire (belt and braces — the allowlist drops these)
		}
		if source.UpdatePolicy == UpdatePolicyOverwrite {
			out[k] = v
			continue
		}
		if strings.TrimSpace(existingValue(existing, k)) == "" {
			out[k] = v // fill_blank_only: only where there is nothing to destroy
		}
	}
	return out
}

// existingValue reads a contact's current value for an allowlisted native key.
// Unknown keys report "" so fill_blank_only errs toward writing rather than
// silently skipping.
func existingValue(c *domain.Contact, key string) string {
	switch key {
	case "first_name":
		return c.FirstName
	case "last_name":
		return c.LastName
	case "email":
		return derefString(c.Email)
	case "phone":
		return derefString(c.Phone)
	default:
		return ""
	}
}

// failEvent records a failure on the ledger and returns the original error.
func (s *LeadIngestService) failEvent(ctx context.Context, e *IntegrationEvent, cause error) error {
	e.Status = EventStatusFailed
	e.Error = cause.Error()
	if ferr := s.repo.FinishEvent(ctx, e); ferr != nil {
		return cause // the cause matters more than the bookkeeping failure
	}
	return cause
}

// ── helpers ──────────────────────────────────────────────────────────────────

func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func copyFields(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// synthesizeFirstName produces a non-blank name for a lead that carried none. The
// adapter rejects a blank (and a whitespace-only) first_name, so an email-only
// lead — the common shape — would otherwise 400. The email local-part beats
// "Lead" because it is at least recognisable to a rep.
func synthesizeFirstName(email string) string {
	local := email
	// i >= 0, not i > 0: an address starting with "@" has an EMPTY local part, and
	// treating index 0 as "no @ found" would name the contact "@nolocal.com".
	if i := strings.Index(local, "@"); i >= 0 {
		local = local[:i]
	}
	local = strings.TrimSpace(local)
	if local == "" {
		return "Lead"
	}
	return local
}
