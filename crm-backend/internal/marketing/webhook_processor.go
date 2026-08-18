package marketing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"crm-backend/internal/automation"

	"github.com/google/uuid"
)

// Consumer / reaper cadence, mirroring the integrations webhook processor.
const (
	resendPollInterval   = 5 * time.Second
	resendClaimBatch     = 50
	resendMaxDrainRounds = 20
	resendMaxAttempts    = 3
	resendReapInterval   = 5 * time.Minute
	resendReapGrace      = 10 * time.Minute
)

// Complaint/bounce circuit-breaker thresholds (Gmail/Yahoo convention: rates over a
// rolling window). A minimum-volume floor prevents a low-volume org (e.g. 2 of 3)
// from tripping the pause instantly.
const (
	breakerWindow         = 7 * 24 * time.Hour
	breakerWarnRate       = 0.0010 // 0.10% — warn
	breakerPauseRate      = 0.0030 // 0.30% — auto-pause
	breakerPauseMinVolume = 500
	breakerWarnMinVolume  = 100
	breakerAlertDedupe    = time.Hour // don't re-log the same band for an org within this
)

// resendConsumerStore is the persistence slice the async consumer needs (the
// concrete *Repository satisfies it). Declared consumer-side for testability.
type resendConsumerStore interface {
	ClaimPendingEvents(ctx context.Context, limit int) ([]MarketingEmailEvent, error)
	RependEvent(ctx context.Context, orgID, eventID uuid.UUID, note string) error
	FinishEvent(ctx context.Context, orgID, eventID uuid.UUID, status, errMsg string) error
	AddSuppression(ctx context.Context, s *Suppression) (bool, error)
	RecordSoftBounce(ctx context.Context, orgID uuid.UUID, emailNorm, source string) (int, error)
	SetMarketingStatus(ctx context.Context, orgID uuid.UUID, emailNorm, status string) error
	SetMarketingPaused(ctx context.Context, orgID uuid.UUID, paused bool) error
	DeliverabilityRates(ctx context.Context, orgID uuid.UUID, window time.Duration) (DeliverabilityRates, error)
	// DeferEvent returns a claimed event to pending WITHOUT consuming a retry
	// attempt — for waiting on the clock rather than recovering from an error.
	DeferEvent(ctx context.Context, orgID, eventID uuid.UUID, note string) error
	// Engagement-trigger support (arc G): the campaign gate + machine-open check.
	CampaignExists(ctx context.Context, orgID, campaignID uuid.UUID) (bool, error)
	HadDeliveredWithin(ctx context.Context, orgID uuid.UUID, emailNorm string, campaignID uuid.UUID, at time.Time, window time.Duration) (bool, error)
}

// machineOpenWindow near-mirrors the analytics layer's Apple-MPP heuristic: a
// delivery within this window of the open (EITHER side — the two timestamps
// come from different Resend subsystems and sub-second inversion is ordinary
// jitter) marks the open as a proxy prefetch, not a human, and it must not
// start automations.
const machineOpenWindow = 10 * time.Second

// engagementGrace defers the machine-open check until the delivered event has
// had time to arrive: it lands on a DIFFERENT webhook with no cross-event
// ordering guarantee, and evaluating too early reads "no delivered row yet" as
// "human open" (fail-open). 75s covers Svix's immediate + short retries; a
// delivered delayed further (e.g. a long backoff after an endpoint outage) is
// accepted residual risk — the alternative is minutes of latency on every
// open-triggered workflow.
const engagementGrace = 75 * time.Second

// EngagementBridge hands the processor the automation-engine entry points it
// needs to start "email opened" workflows (wired in main.go; marketing →
// automation is the allowed import direction, but func fields keep this
// consumer testable without an engine). nil bridge = no emission (the house
// fail-closed wiring convention).
type EngagementBridge struct {
	// TriggerEvent is automation Engine.TriggerEvent (fire-and-forget).
	TriggerEvent func(ctx context.Context, orgID uuid.UUID, eventType string, payload map[string]any)
	// LoadContact is automation Engine.LoadContactForTrigger: resolve by the
	// send's contact_id tag first, else by normalized email; nil map = no match.
	LoadContact func(ctx context.Context, orgID, contactID uuid.UUID, email string) (map[string]any, uuid.UUID, error)
	// FrontendURL is the SPA origin used to recognise this product's own
	// unsubscribe / preference-centre link in a click payload. Empty still
	// matches the one-click /api/marketing/u/ form.
	FrontendURL string
}

// SetEngagementTrigger wires the engagement bridge (nil disables emission).
func (p *ResendProcessor) SetEngagementTrigger(b *EngagementBridge) {
	p.engagement = b
}

// ResendProcessor drains the marketing_email_events queue and applies suppression +
// the deliverability breaker off the request path. Callerless: it reads org scope off
// each claimed row (persisted at enqueue), never from an actor.
type ResendProcessor struct {
	store      resendConsumerStore
	logger     *slog.Logger
	dedupe     *alertDedupe
	engagement *EngagementBridge // nil = engagement triggers disabled
}

// NewResendProcessor builds the processor.
func NewResendProcessor(store resendConsumerStore, logger *slog.Logger) *ResendProcessor {
	if logger == nil {
		logger = slog.Default()
	}
	return &ResendProcessor{store: store, logger: logger, dedupe: newAlertDedupe()}
}

// StartResendWebhookProcessor runs the poll/drain loop until ctx is cancelled.
// Launch with context.Background() so a request cancellation cannot kill it.
func StartResendWebhookProcessor(ctx context.Context, p *ResendProcessor) {
	if p == nil {
		return
	}
	ticker := time.NewTicker(resendPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.drain(ctx)
		}
	}
}

// drain claims and processes up to maxDrainRounds batches, stopping early when the
// queue is empty (a short claim) or ctx is cancelled.
func (p *ResendProcessor) drain(ctx context.Context) {
	for round := 0; round < resendMaxDrainRounds; round++ {
		if ctx.Err() != nil {
			return
		}
		events, err := p.store.ClaimPendingEvents(ctx, resendClaimBatch)
		if err != nil {
			p.logger.Error("marketing: claim pending resend events failed", "error", err)
			return
		}
		deferred := 0
		for i := range events {
			if p.process(ctx, events[i]) {
				deferred++
			}
		}
		// A deferred event is pending again immediately, so continuing to drain
		// would re-claim the very rows we just parked and spin this tick against
		// the clock. Stop once the batch was entirely waiting.
		if deferred == len(events) {
			return
		}
		if len(events) < resendClaimBatch {
			return
		}
	}
}

// process applies one event and marks it terminal, or repends it on a transient
// error while retry budget remains.
// process returns true when the event was DEFERRED rather than handled, so the
// drain loop can stop re-claiming a batch that is only waiting on the clock.
func (p *ResendProcessor) process(ctx context.Context, evt MarketingEmailEvent) (deferred bool) {
	// Campaign opens/clicks sit out a grace period before the machine check runs
	// (see engagementGrace). Deferring is NOT a retry: DeferEvent gives back the
	// attempt the claim consumed, because a claim-increments/repend-never-resets
	// pair would otherwise push a waiting event past resendMaxAttempts and let
	// the stranded-event reaper mark it permanently failed for doing nothing
	// wrong.
	if p.shouldDeferEngagement(evt) {
		_ = p.store.DeferEvent(ctx, evt.OrgID, evt.ID, "engagement grace: waiting for the delivered event")
		return true
	}
	if err := p.apply(ctx, evt); err != nil {
		if evt.Attempts >= resendMaxAttempts {
			_ = p.store.FinishEvent(ctx, evt.OrgID, evt.ID, EventStatusFailed, err.Error())
			p.logger.Error("marketing: resend event permanently failed", "error", err, "svix_id", evt.SvixID, "type", evt.EventType)
			return false
		}
		_ = p.store.RependEvent(ctx, evt.OrgID, evt.ID, err.Error())
		return false
	}
	_ = p.store.FinishEvent(ctx, evt.OrgID, evt.ID, EventStatusDone, "")
	return false
}

// apply performs the side effects for one event. Delivered/sent/opened/clicked etc.
// (Reason == "") are ledger-only and do nothing here (they still feed the breaker's
// denominator via the stored row). A returned error is transient → the event repends.
func (p *ResendProcessor) apply(ctx context.Context, evt MarketingEmailEvent) error {
	// A suppression-bearing event with no recipient can never be applied — ack it as
	// done (not a retry) rather than looping forever.
	if evt.Reason != "" && evt.EmailNormalized == "" {
		p.logger.Warn("marketing: suppression event has no recipient — skipping", "svix_id", evt.SvixID, "type", evt.EventType)
		return nil
	}

	switch evt.Reason {
	case ReasonComplaint:
		if _, err := p.store.AddSuppression(ctx, &Suppression{OrgID: evt.OrgID, EmailNormalized: evt.EmailNormalized, Reason: ReasonComplaint, Source: "resend_webhook"}); err != nil {
			return err
		}
		p.checkBreaker(ctx, evt.OrgID)
	case ReasonHardBounce:
		if _, err := p.store.AddSuppression(ctx, &Suppression{OrgID: evt.OrgID, EmailNormalized: evt.EmailNormalized, Reason: ReasonHardBounce, Source: "resend_webhook"}); err != nil {
			return err
		}
		p.checkBreaker(ctx, evt.OrgID)
	case ReasonSoftBounce:
		if _, err := p.store.RecordSoftBounce(ctx, evt.OrgID, evt.EmailNormalized, "resend_webhook"); err != nil {
			return err
		}
	case ReasonUnsubscribe:
		if _, err := p.store.AddSuppression(ctx, &Suppression{OrgID: evt.OrgID, EmailNormalized: evt.EmailNormalized, Reason: ReasonUnsubscribe, Source: "resend_webhook"}); err != nil {
			return err
		}
		// Best-effort lifecycle flip — the suppression row is the real enforcement.
		if err := p.store.SetMarketingStatus(ctx, evt.OrgID, evt.EmailNormalized, StatusUnsubscribed); err != nil {
			p.logger.Warn("marketing: unsubscribe status write failed (suppression still applied)", "error", err, "org_id", evt.OrgID.String())
		}
	default:
		// Ledger-only event (delivered/sent/opened/clicked/delivery_delayed/unknown).
		// Opens additionally feed the email_opened automation trigger —
		// best-effort: a failed emit must never repend a ledger-only event
		// (redelivery is already absorbed by the engine's per-message run key).
		switch evt.EventType {
		case ResendTypeOpened:
			p.emitEngagementTrigger(ctx, evt, automation.TriggerEmailOpened)
		case ResendTypeClicked:
			p.emitEngagementTrigger(ctx, evt, automation.TriggerEmailClicked)
		}
	}
	return nil
}

// shouldDeferEngagement reports whether an engagement event is still inside the
// delivered-event grace window (only events that could EMIT are deferred —
// uncampaigned ones finish immediately).
func (p *ResendProcessor) shouldDeferEngagement(evt MarketingEmailEvent) bool {
	if p.engagement == nil || evt.CampaignID == nil {
		return false
	}
	if evt.EventType != ResendTypeOpened && evt.EventType != ResendTypeClicked {
		return false
	}
	return time.Since(engagementAt(evt)) < engagementGrace
}

// engagementAt is when the interaction happened per Resend, falling back to
// ingest time when the payload carried no usable timestamp.
func engagementAt(evt MarketingEmailEvent) time.Time {
	if evt.OccurredAt != nil {
		return *evt.OccurredAt
	}
	return evt.CreatedAt
}

// emitEngagementTrigger starts email_opened / email_clicked workflows for a
// HUMAN interaction with a real campaign email.
//
// v1 scope (engagement_and_split_plan.md arc G, locked): CAMPAIGN mail only —
// the event's campaign_id must resolve to a live marketing_campaigns row. M8
// sequence sends echo the WORKFLOW uuid in that tag and 1:1 sends carry none;
// skipping both structurally prevents the interaction → send → interaction
// trigger loop that no other engine guard covers.
//
// Automated interactions are filtered by the same delivered-within-window
// heuristic for both event types. For opens that is Apple MPP and friends
// prefetching the pixel; for clicks it is corporate link scanners (Outlook
// Safe Links, Proofpoint, Barracuda) fetching every URL in the message
// moments after delivery. Both fire within seconds of delivery, which is what
// the window keys on, and both would otherwise enrol someone who never
// touched the email.
func (p *ResendProcessor) emitEngagementTrigger(ctx context.Context, evt MarketingEmailEvent, triggerType string) {
	if p.engagement == nil || p.engagement.TriggerEvent == nil || p.engagement.LoadContact == nil {
		return
	}
	if evt.EmailNormalized == "" || evt.CampaignID == nil {
		return
	}

	// A click on the unsubscribe / preference-centre link must NEVER start a
	// workflow. Every marketing email carries that link and click tracking
	// rewrites it like any other, so without this an "on click" automation
	// would enrol someone at the exact moment they asked to hear less from us.
	// Checked before anything else so no lookup can change the outcome.
	link := ""
	if triggerType == automation.TriggerEmailClicked {
		link = clickedLink(evt)
		if p.isOptOutClick(link, evt) {
			return
		}
	}

	isCampaign, err := p.store.CampaignExists(ctx, evt.OrgID, *evt.CampaignID)
	if err != nil {
		p.logger.Warn("marketing: engagement-trigger campaign lookup failed", "error", err, "org_id", evt.OrgID.String(), "type", evt.EventType)
		return
	}
	if !isCampaign {
		return // sequence (workflow-uuid tag) or unknown attribution — out of v1 scope
	}

	machine, err := p.store.HadDeliveredWithin(ctx, evt.OrgID, evt.EmailNormalized, *evt.CampaignID, engagementAt(evt), machineOpenWindow)
	if err != nil {
		p.logger.Warn("marketing: engagement-trigger delivery lookup failed", "error", err, "org_id", evt.OrgID.String(), "type", evt.EventType)
		return
	}
	if machine {
		return // mailbox prefetch or link scanner, not a person
	}

	taggedContactID, emailID := engagementIdentity(evt)
	fields, contactID, err := p.engagement.LoadContact(ctx, evt.OrgID, taggedContactID, evt.EmailNormalized)
	if err != nil {
		p.logger.Warn("marketing: engagement-trigger contact resolution failed", "error", err, "org_id", evt.OrgID.String(), "type", evt.EventType)
		return
	}
	if fields == nil {
		p.logger.Warn("marketing: engagement event with no matching contact — skipping trigger",
			"org_id", evt.OrgID.String(), "svix_id", evt.SvixID, "type", evt.EventType)
		return
	}

	trigger := map[string]any{
		"type":        triggerType,
		"source":      "resend_webhook",
		"campaign_id": evt.CampaignID.String(),
		"email_id":    emailID,
	}
	payload := map[string]any{
		"contact":     fields,
		"entity_id":   contactID.String(),
		"email_id":    emailID, // keys the engine's per-message run dedupe
		"campaign_id": evt.CampaignID.String(),
		"trigger":     trigger,
	}
	// The clicked URL rides in the payload so a workflow can interpolate it
	// ({{trigger.link}}) even though v1 offers no link FILTER — filtering needs
	// the payload shape confirmed against a real click first.
	if link != "" {
		payload["link"] = link
		trigger["link"] = link
	}
	// context.Background(): TriggerEvent's goroutine inherits the caller's ctx,
	// and this must outlive any drain-loop cancellation (the house idiom).
	p.engagement.TriggerEvent(context.Background(), evt.OrgID, triggerType, payload)
}

// clickedLink pulls the clicked URL out of the raw webhook payload. Resend's
// click shape is not pinned by any fixture in this repo and has not been
// verified against a real payload, so every plausible key is tried rather than
// trusting one — a miss here only costs the {{trigger.link}} value, while
// isOptOutClick has its own shape-independent fallback.
func clickedLink(evt MarketingEmailEvent) string {
	var env resendEnvelope
	if err := json.Unmarshal(evt.RawPayload, &env); err != nil {
		return ""
	}
	return env.Data.clickedLink()
}

// isOptOutClick reports whether a clicked URL is this product's unsubscribe or
// preference-centre link (render.go's PreferenceCenterURL / OneClickUnsubURL).
//
// Matching is by PATH, not by origin. The link was baked into the email at SEND
// time from whatever FRONTEND_URL held then, and mail sits in inboxes for weeks
// — an origin change (preview domain to custom domain, apex to www, a rebrand)
// would silently un-recognise every unsubscribe link still in flight if we
// compared against the current value. The configured origin is still consulted
// as a positive signal, but never as a requirement.
//
// When the URL could not be extracted at all, the whole raw payload is scanned
// for the same markers: the click shape is unverified, and an exclusion that
// fails open would enrol people at the worst possible moment. Erring toward
// suppressing a genuine click is the correct direction here.
func (p *ResendProcessor) isOptOutClick(link string, evt MarketingEmailEvent) bool {
	if link == "" {
		return optOutMarkerIn(string(evt.RawPayload))
	}
	if u, err := url.Parse(link); err == nil && u.Path != "" {
		// "/u/<token>" (preference centre) or "/api/marketing/u/<token>"
		// (one-click), whatever host they now resolve through.
		if strings.HasPrefix(u.Path, "/u/") || strings.Contains(u.Path, "/api/marketing/u/") {
			return true
		}
	}
	// Unparseable, or a tracking wrapper carrying the real URL inside a query
	// parameter — fall back to substring markers over the whole value.
	if optOutMarkerIn(link) {
		return true
	}
	if p.engagement != nil {
		if base := strings.TrimRight(p.engagement.FrontendURL, "/"); base != "" && strings.Contains(link, base+"/u/") {
			return true
		}
	}
	return false
}

// optOutMarkerIn spots either unsubscribe URL form inside an arbitrary string.
func optOutMarkerIn(s string) bool {
	return strings.Contains(s, "/api/marketing/u/") || strings.Contains(s, "/u/")
}

// engagementIdentity re-reads the raw webhook payload for the send's contact_id
// attribution tag and the Resend message id (neither is a ledger column).
func engagementIdentity(evt MarketingEmailEvent) (contactID uuid.UUID, emailID string) {
	var env resendEnvelope
	if err := json.Unmarshal(evt.RawPayload, &env); err != nil {
		return uuid.Nil, ""
	}
	emailID = env.Data.EmailID
	if s := env.Data.tag("contact_id"); s != "" {
		if id, err := uuid.Parse(s); err == nil {
			contactID = id
		}
	}
	return contactID, emailID
}

// checkBreaker recomputes the org's rolling-window complaint/bounce rates from the
// deduped ledger (so it is idempotent to redeliveries and consumer repends) and
// auto-pauses marketing at >=0.30% (transactional keeps flowing), warn-logging at
// >=0.10%. Both legs require a minimum delivered/sent volume so a low-volume org is
// not falsely paused. A rollup error is logged, never fatal to the event.
func (p *ResendProcessor) checkBreaker(ctx context.Context, orgID uuid.UUID) {
	rates, err := p.store.DeliverabilityRates(ctx, orgID, breakerWindow)
	if err != nil {
		p.logger.Warn("marketing: deliverability rollup failed", "error", err, "org_id", orgID.String())
		return
	}

	pauseComplaint := rates.Delivered >= breakerPauseMinVolume && rates.ComplaintRate >= breakerPauseRate
	pauseBounce := rates.Sent >= breakerPauseMinVolume && rates.BounceRate >= breakerPauseRate
	if pauseComplaint || pauseBounce {
		if err := p.store.SetMarketingPaused(ctx, orgID, true); err != nil {
			p.logger.Error("marketing: auto-pause failed", "error", err, "org_id", orgID.String())
			return
		}
		if p.dedupe.claim("pause:"+orgID.String(), time.Now()) {
			p.logger.Error("marketing: MARKETING AUTO-PAUSED — deliverability breaker tripped",
				"org_id", orgID.String(),
				"complaint_rate", rates.ComplaintRate, "bounce_rate", rates.BounceRate,
				"delivered", rates.Delivered, "sent", rates.Sent)
		}
		return
	}

	warnComplaint := rates.Delivered >= breakerWarnMinVolume && rates.ComplaintRate >= breakerWarnRate
	warnBounce := rates.Sent >= breakerWarnMinVolume && rates.BounceRate >= breakerWarnRate
	if (warnComplaint || warnBounce) && p.dedupe.claim("warn:"+orgID.String(), time.Now()) {
		p.logger.Warn("marketing: deliverability warning — complaint/bounce rate crossed 0.10%",
			"org_id", orgID.String(),
			"complaint_rate", rates.ComplaintRate, "bounce_rate", rates.BounceRate,
			"delivered", rates.Delivered, "sent", rates.Sent)
	}
}

// StartResendReaper recovers events stranded in `processing` by a crashed worker,
// mirroring the integrations reaper.
func StartResendReaper(ctx context.Context, store interface {
	ReapStrandedEvents(ctx context.Context, grace time.Duration, maxAttempts int) (int64, error)
}, logger *slog.Logger) {
	if store == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(resendReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := store.ReapStrandedEvents(ctx, resendReapGrace, resendMaxAttempts)
			if err != nil {
				logger.Error("marketing: reap stranded resend events failed", "error", err)
			} else if n > 0 {
				logger.Info("marketing: reaped stranded resend events", "count", n)
			}
		}
	}
}

// alertDedupe suppresses repeat breaker log lines for the same org+band within a
// window (mirrors the integrations healthDedupe), so a complaint storm does not
// spam one line per event.
type alertDedupe struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newAlertDedupe() *alertDedupe { return &alertDedupe{seen: make(map[string]time.Time)} }

// claim returns true if key has not fired within breakerAlertDedupe of now.
func (d *alertDedupe) claim(key string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if last, ok := d.seen[key]; ok && now.Sub(last) < breakerAlertDedupe {
		return false
	}
	d.seen[key] = now
	return true
}
