package integrations

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// LegacyCapture lends the lead platform's ADDITIVE capabilities to the legacy
// automation webhook (POST /api/webhooks/inbound/:org_token) without taking over its
// write.
//
// The write deliberately stays where it is. A four-angle design pass established that
// routing it through LeadIngestService changes the automation trigger payload — the
// legacy handler emits the caller's submitted JSON, RecordService emits a projection
// of the stored row, and the two share only `id`. Every difference between them
// (`company` vanishing, a synthesized first_name, custom_fields becoming the union of
// every past delivery) is invisible at the HTTP layer and silent in the engine, which
// renders an unresolved template as "" and answers false for every operator except
// is_empty on an absent path. So the payload is left alone, byte for byte.
//
// What is borrowed is the part no workflow can observe:
//
//   - a delivery ledger row, so legacy traffic finally appears in the delivery log
//     and its raw payload is recoverable;
//   - owner routing, which is the original complaint this whole platform opens with —
//     legacy writes no owner at all, so own-scoped reps have never been able to see a
//     single lead this endpoint produced. `owner_user_id` is a COLUMN, not a payload
//     key, so stamping it changes who can SEE the lead without touching one template;
//   - the health signal, so a legacy source that starts failing raises the same badge
//     and notification every other channel does.
//
// Attribution is deliberately NOT borrowed. A legacy payload carries no `context`
// envelope, so the only attribution available is lead_source/lead_source_detail —
// which collides with a key legacy senders already use, is not reportable without
// seeding field definitions, and seeding those definitions is precisely the mechanism
// that turns a previously-accepted numeric value into a 400 that loses the lead
// (`validateCustomValuesOnly` type-checks any custom key that HAS a def).
type LegacyCapture struct {
	repo   *Repository
	ingest *LeadIngestService
	logger *slog.Logger
}

// NewLegacyCapture builds the bridge. ingest is used only for its owner-routing
// ladder — no lead is written through it.
func NewLegacyCapture(repo *Repository, ingest *LeadIngestService, logger *slog.Logger) *LegacyCapture {
	return &LegacyCapture{repo: repo, ingest: ingest, logger: logger}
}

// legacySourceName is the display name of an auto-created legacy source. It says
// where the thing is configured, because its URL and secret live in the workflow
// builder rather than on the Integrations page.
const legacySourceName = "Workflow webhook (legacy)"

// BeginDelivery opens the ledger row for an inbound legacy delivery and resolves who
// the lead should land on.
//
// It returns the delivery id (to close later) and the owner, and it NEVER fails the
// caller's lead: every degraded path — no source row, an unwritable ledger, a dead
// owner pool — returns a nil owner and a nil delivery id, and the endpoint proceeds
// to write the contact exactly as it did before this existed. The legacy contract is
// "the lead gets recorded"; bookkeeping that cannot complete must not be able to
// revoke that.
func (c *LegacyCapture) BeginDelivery(ctx context.Context, orgID uuid.UUID, payload map[string]any) (uuid.UUID, error) {
	src, err := c.resolveSource(ctx, orgID)
	if err != nil || src == nil {
		c.logf("integrations: legacy capture has no source row; delivery not recorded", "org_id", orgID.String(), "error", err)
		return uuid.Nil, err
	}

	raw := marshalJSONB(payload)
	event := &IntegrationEvent{
		OrgID:    orgID,
		SourceID: &src.ID,
		// processing, never pending: this is a SYNCHRONOUS channel like the capture
		// API, so the async claim loop must never pick it up (it would re-run a
		// delivery whose write already happened in the request).
		Status:     EventStatusProcessing,
		Attempts:   1,
		RawPayload: datatypes.JSON(raw),
	}
	// No ProviderEventID, deliberately: the legacy wire has no idempotency key, so
	// there is nothing to dedupe ON. Synthesizing one from the body would collapse two
	// genuinely distinct submissions from the same person into one delivery — the
	// legacy contract has always accepted repeats, and quietly discarding one would
	// lose a real lead.
	if _, err := c.repo.InsertEventDeduped(ctx, event); err != nil {
		c.logf("integrations: could not open a legacy delivery row", "org_id", orgID.String(), "error", err)
		return uuid.Nil, err
	}
	return event.ID, nil
}

// ResolveOwner picks the owner for a lead that is about to be CREATED, and is called
// from nowhere else.
//
// The lateness is the entire point, and getting it wrong here was caught in review.
// The rotation ticket is CONSUMING, so resolving it before the create/update decision
// burns a turn on every resubmission — and a legacy webhook's ordinary traffic is
// resubmissions of addresses it has already seen. ingest.go:563 calls that "this
// feature's worst bug": with a steady one-new-in-three mix and a three-person
// rotation, every new lead lands on the same rep while the Integrations page shows a
// perfectly healthy three-way split. So the ticket is taken at the one place
// ownership is actually stamped, exactly as the ingest path takes it.
//
// Returns nil for "nobody", which is a real answer: an unconfigured source, a pool
// whose members are all suspended, and a routing failure all mean the lead is written
// unowned rather than not written.
func (c *LegacyCapture) ResolveOwner(ctx context.Context, orgID, deliveryID uuid.UUID) *uuid.UUID {
	src, err := c.resolveSource(ctx, orgID)
	if err != nil || src == nil {
		return nil
	}
	// priorOwner is nil: with no idempotency key there is no prior attempt whose
	// ticket this delivery could inherit.
	decision := c.ingest.resolveOwner(ctx, src, RawLead{}, nil)
	if deliveryID != uuid.Nil {
		// Both recorded in one statement. The note is the ONLY disclosure an admin ever
		// gets that a lead landed unowned — this endpoint's 200 body is frozen, so
		// unlike the capture API there is no warnings channel to say it on.
		if err := c.repo.SetEventRouting(ctx, deliveryID, decision.OwnerID, decision.Note); err != nil {
			c.logf("integrations: could not record the routing decision", "event_id", deliveryID.String(), "error", err)
		}
	}
	return decision.OwnerID
}

// FinishDelivery closes the ledger row and moves the source's health signal.
//
// deliveryID may be uuid.Nil (BeginDelivery degraded), in which case there is nothing
// to close and the health signal is left alone rather than guessed at — a source
// whose bookkeeping never opened has produced no evidence either way.
func (c *LegacyCapture) FinishDelivery(ctx context.Context, orgID, deliveryID, contactID uuid.UUID, created bool, cause error) {
	if deliveryID == uuid.Nil {
		return
	}
	status, outcome, errText := EventStatusProcessed, OutcomeUpdated, ""
	var recordID *uuid.UUID
	if cause != nil {
		status, errText = EventStatusFailed, truncate(cause.Error(), 1000)
	} else {
		if created {
			outcome = OutcomeCreated
		}
		recordID = &contactID
	}
	if err := c.repo.FinishLegacyEvent(ctx, deliveryID, status, outcome, errText, recordID); err != nil {
		c.logf("integrations: could not close a legacy delivery row", "event_id", deliveryID.String(), "error", err)
	}

	// The health signal. Counting here is safe under the rule the counter documents —
	// only POST-authentication failures may move it — because the endpoint has already
	// verified the HMAC (or refused) by the time a delivery is opened. A stranger who
	// reads an org token off a proxy log still cannot flip this source red: an unsigned
	// body never reaches BeginDelivery.
	src, err := c.resolveSource(ctx, orgID)
	if err != nil || src == nil {
		return
	}
	if cause != nil {
		// The same two steps Handler.countSourceFailure takes, inlined because that one
		// hangs off the HTTP handler rather than the service. Kept deliberately
		// identical: one atomic increment that reports whether THIS call caused the
		// flip, and an announcement only on the transition.
		flipped, ferr := c.repo.IncrementSourceFailure(ctx, src.ID)
		if ferr != nil {
			c.logf("integrations: could not count a legacy source failure", "source_id", src.ID.String(), "error", ferr)
			return
		}
		if flipped && c.ingest.health != nil {
			c.ingest.health.SourceFailing(src.OrgID, src.ID, src.Name, src.CreatedBy)
		}
		return
	}
	healed, err := c.repo.TouchSourceUsed(ctx, src.ID)
	if err != nil {
		c.logf("integrations: could not stamp legacy source usage", "source_id", src.ID.String(), "error", err)
		return
	}
	if healed && c.ingest.health != nil {
		c.ingest.health.SourceRecovered(src.OrgID, src.ID, src.Name, src.CreatedBy)
	}
}

// resolveSource returns the org's single legacy source, creating it if it is missing.
//
// Creation goes through CreateLegacySource, which OMITS token_hash: an empty
// string there would collide on the UNIQUE index across the second such source in the
// fleet, and the column must stay NULL because this kind has no bearer key by
// construction — FindSourceByTokenHash has no kind filter, so a token_hash here would
// silently open a second capture-API ingress into the org.
//
// ON CONFLICT DO NOTHING with no target is safe with OR without the uniqueness guard:
// with it, a concurrent creator loses and we re-read; without it, the statement simply
// inserts. The write path therefore never depends on an index whose boot guard is
// allowed to fail.
func (c *LegacyCapture) resolveSource(ctx context.Context, orgID uuid.UUID) (*LeadSource, error) {
	if orgID == uuid.Nil {
		return nil, nil
	}
	if src, err := c.repo.FindSourceByKind(ctx, orgID, KindWebhookInbound); err != nil || src != nil {
		return src, err
	}
	src := &LeadSource{
		OrgID:      orgID,
		Kind:       KindWebhookInbound,
		Name:       legacySourceName,
		TargetSlug: "contact",
		// email only: the legacy handler has always required an email and matched on
		// it exactly, and this row must not start changing which contact a delivery
		// resolves to.
		MatchFields: datatypes.JSON([]byte(`["email"]`)),
		FieldMap:    datatypes.JSON([]byte(`{}`)),
		// overwrite, because that is what the legacy UPDATE branch does: it sets every
		// direct field present in the payload, unconditionally. fill_blank_only would
		// silently stop a resubmission correcting a phone number.
		UpdatePolicy: UpdatePolicyOverwrite,
		Config:       datatypes.JSON([]byte(`{}`)),
		Status:       SourceStatusActive,
		// Uncapped on purpose. The legacy endpoint has never had a daily cap, and
		// introducing one here would start silently refusing a live integration's leads
		// at a threshold nobody chose.
		DailyCap: 0,
	}
	if err := c.repo.CreateLegacySource(ctx, src); err != nil {
		return nil, err
	}
	if src.ID == uuid.Nil {
		// The insert conflicted with a concurrent creator; theirs is the live row.
		return c.repo.FindSourceByKind(ctx, orgID, KindWebhookInbound)
	}
	return src, nil
}

func (c *LegacyCapture) logf(msg string, args ...any) {
	if c.logger != nil {
		c.logger.Error(msg, args...)
	}
}
