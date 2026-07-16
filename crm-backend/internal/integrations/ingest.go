package integrations

import (
	"context"
	"encoding/json"
	"errors"
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
}

// RawLead is one inbound delivery, already parsed but not yet trusted.
type RawLead struct {
	Fields  map[string]any
	Context map[string]any
	Consent map[string]any
	// ProviderEventID is the delivery's stable id across retries. Empty means the
	// delivery cannot be deduped.
	ProviderEventID string
	// IsTest marks a lead that must traverse the whole pipeline without paging
	// anyone. Server-derived only — never read off a caller's payload, or a leaked
	// key becomes a way to file real leads that never reach sales.
	IsTest bool
}

// IngestResult is what the caller reports back to the third party.
type IngestResult struct {
	EventID   uuid.UUID
	RecordID  uuid.UUID
	Outcome   string
	Duplicate bool
}

// LeadIngestService is the one path every inbound lead takes, whatever channel it
// arrived on.
type LeadIngestService struct {
	repo    *Repository
	records RecordWriter
	matcher ContactMatcher
	schema  SchemaProvider
}

// NewLeadIngestService builds the pipeline.
func NewLeadIngestService(repo *Repository, records RecordWriter, matcher ContactMatcher, schema SchemaProvider) *LeadIngestService {
	return &LeadIngestService{repo: repo, records: records, matcher: matcher, schema: schema}
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
func newIngestContext(source *LeadSource) (context.Context, context.CancelFunc) {
	ctx := domain.WithWriteSource(context.Background(), source.WriteSource())
	return context.WithTimeout(ctx, ingestTimeout)
}

// Ingest runs one lead end-to-end: dedupe the delivery, filter it against the
// allowlist, match or create, and record what happened.
func (s *LeadIngestService) Ingest(ctx context.Context, source *LeadSource, lead RawLead) (*IngestResult, error) {
	// ── 1. Dedupe the delivery ───────────────────────────────────────────
	// Before any side effect: a provider redelivery must return the original
	// result, not repeat it.
	if source.TargetSlug != "contact" {
		// Defence in depth against a row edited by hand: the custom-object path
		// would write CreatedBy = uuid.Nil into a users(id) FK and crash.
		return nil, domain.NewAppError(400, "unsupported target object: "+source.TargetSlug)
	}

	var providerID *string
	if lead.ProviderEventID != "" {
		id := lead.ProviderEventID
		providerID = &id
	}
	raw, _ := json.Marshal(lead.Fields)
	lctx, _ := json.Marshal(lead.Context)

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
		Context:    datatypes.JSON(lctx),
	}
	inserted, err := s.repo.InsertEventDeduped(ctx, event)
	if err != nil {
		return nil, err
	}
	if !inserted {
		prior, ferr := s.repo.FindEventByProviderID(ctx, source.ID, lead.ProviderEventID)
		if ferr != nil {
			return nil, ferr
		}
		if prior == nil { // lost a race with a concurrent identical delivery
			return &IngestResult{Duplicate: true}, nil
		}
		res := &IngestResult{EventID: prior.ID, Outcome: prior.Outcome, Duplicate: true}
		if prior.ResultRecordID != nil {
			res.RecordID = *prior.ResultRecordID
		}
		return res, nil
	}

	// ── 2. Allowlist ─────────────────────────────────────────────────────
	allow, err := BuildAllowlist(ctx, s.schema, source.OrgID, source.TargetSlug)
	if err != nil {
		return nil, s.failEvent(ctx, event, err)
	}
	fields, quarantined := allow.Apply(lead.Fields)
	if len(quarantined) > 0 {
		q, _ := json.Marshal(quarantined)
		event.QuarantinedFields = datatypes.JSON(q)
	}

	// Normalize the match key BEFORE the write so it agrees with the unique index.
	// The index is on raw (org_id, email) — case-SENSITIVE — while matching is
	// case-insensitive. Writing the normalized form is what makes the two agree, so
	// a concurrent duplicate actually raises 23505 and the upsert loop below can
	// catch it. Skip this and "John@X.com" + "john@x.com" both insert silently.
	email := normalizeEmail(stringOf(fields["email"]))
	if email == "" {
		return nil, s.failEvent(ctx, event, domain.NewAppError(422, "email is required"))
	}
	fields["email"] = email

	// ── 3/4. Match, then create or update ────────────────────────────────
	ictx, cancel := newIngestContext(source)
	defer cancel()

	result, err := s.upsert(ictx, source, fields, email)
	if err != nil {
		return nil, s.failEvent(ctx, event, err)
	}

	// ── 7. Record the outcome ────────────────────────────────────────────
	event.Status = EventStatusProcessed
	if lead.IsTest {
		event.Status = EventStatusTest
	}
	event.ResultSlug = source.TargetSlug
	event.ResultRecordID = &result.RecordID
	event.Outcome = result.Outcome
	if err := s.repo.FinishEvent(ctx, event); err != nil {
		return nil, err
	}
	_ = s.repo.TouchSourceUsed(ctx, source.ID) // best-effort; the lead is already written

	result.EventID = event.ID
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
func (s *LeadIngestService) upsert(ctx context.Context, source *LeadSource, fields map[string]any, email string) (*IngestResult, error) {
	existing, err := s.matcher.FindByNormalizedEmail(ctx, source.OrgID, email)
	if err != nil {
		return nil, err
	}

	if existing == nil {
		rec, cerr := s.create(ctx, source, fields)
		if cerr == nil {
			return &IngestResult{RecordID: rec.ID, Outcome: OutcomeCreated}, nil
		}
		if !errors.Is(cerr, domain.ErrContactEmailExists) {
			return nil, cerr
		}
		// Lost the race — re-read the winner and update it instead.
		existing, err = s.matcher.FindByNormalizedEmail(ctx, source.OrgID, email)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, cerr // conflicted but unfindable: report the original error
		}
	}

	if source.UpdatePolicy == UpdatePolicyCreateOnly {
		return &IngestResult{RecordID: existing.ID, Outcome: OutcomeUpdated}, nil
	}
	upd := s.updateFields(source, fields, existing)
	if len(upd) == 0 {
		return &IngestResult{RecordID: existing.ID, Outcome: OutcomeUpdated}, nil
	}
	rec, err := s.records.Update(ctx, source.OrgID, source.TargetSlug, existing.ID, domain.RecordWriteInput{Fields: upd})
	if err != nil {
		return nil, err
	}
	return &IngestResult{RecordID: rec.ID, Outcome: OutcomeUpdated}, nil
}

// create writes a new record. first_name is synthesized HERE, on the create branch
// only — the adapter 400s a blank one, but synthesizing on update would overwrite a
// real name with "Lead" every time an email-only lead resubmitted.
func (s *LeadIngestService) create(ctx context.Context, source *LeadSource, fields map[string]any) (*domain.UniformRecord, error) {
	out := copyFields(fields)
	if strings.TrimSpace(stringOf(out["first_name"])) == "" {
		out["first_name"] = synthesizeFirstName(stringOf(fields["email"]))
	}
	// Ownership comes from config, never the wire. Stamped only here: on update the
	// adapter reads a present owner_user_id as an instruction, and a null one as
	// UNASSIGN. An unowned contact is invisible to own-scoped reps.
	if source.DefaultOwnerID != nil {
		out["owner_user_id"] = source.DefaultOwnerID.String()
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
