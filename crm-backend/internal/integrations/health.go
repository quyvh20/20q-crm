package integrations

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"crm-backend/internal/domain"
)

// Health alerting: turning the status transitions the pipeline already computes into
// something that reaches a human.
//
// The whole of L6.1's alerting hangs off edges the existing atomic statements ALREADY
// produce — IncrementSourceFailure has returned a genuine once-only `flipped` since L3
// and it was being thrown into a log line. Nothing here adds a column, and that is a
// deliberate design position rather than an accident of scope:
//
// The obvious design is a notified-watermark pair (health_notified_status /
// health_notified_at) written beside the status. An adversarial pass found EIGHT
// separate high-severity failures that all trace to those two columns and to nothing
// else — a mapped watermark whose boot guard failed 500s every capture request in every
// org; the same columns added inside integration_connections' `CREATE TABLE IF NOT
// EXISTS` guard are a silent no-op on prod, so the OAuth callback 500s AFTER the
// single-use state is burned and connecting a page becomes permanently impossible;
// folding them into TouchSourceUsed regresses L3/L5 behaviour that works today
// (last_used_at freezes fleet-wide, `error` can never self-heal) instead of merely
// disabling the new feature; and because a watermark commits with the UPDATE while
// dispatch happens later, a rolling deploy loses the escalation permanently — while
// deploys are themselves among the likeliest causes of the failures being escalated.
//
// A transition the statement hands back cannot be lost by a schema that did not
// migrate, cannot be desynchronised by an admin's disable/re-enable, and cannot claim
// to have announced something it did not. So: no watermark, no new columns.
//
// The counterpart cost is that de-duplication has to live in memory (see healthDedupe),
// which is bounded and documented rather than durable. That is the right trade for an
// ALARM: at-least-once is nearly free here, and at-most-once is the wrong default.

// Notification types. Registered in domain.NotificationEventTypes so a member can
// control them independently — a type absent from that catalog still delivers, but is
// invisible in the preference centre and its overrides are silently dropped, leaving
// mute-all (which also kills workflow notifications) as the only way to quieten it.
const (
	// NotifyTypeIntegrationHealth carries lead-source and connection health.
	NotifyTypeIntegrationHealth = "integration_health"
)

// Notifier is the narrow notification port.
//
// Declared HERE rather than imported, mirroring automation's NotificationCreator: the
// integrations package depends only on ports it declares, and internal/usecase must not
// be imported from here.
type Notifier interface {
	Create(ctx context.Context, in domain.NotificationCreateInput) (*domain.Notification, error)
}

// HealthAudience resolves who hears about an org's integration health.
//
// It is a port because the answer needs role→capability data that lives in the
// permission repository — the domain.RecordAuthorizer already injected into this
// package answers only about the CONTEXT caller and cannot enumerate users.
type HealthAudience interface {
	// IntegrationAdmins returns the LIVE members of an org who should be told when a
	// lead pipe breaks: holders of integrations.manage, plus the workspace owner.
	//
	// The owner leg is not belt-and-braces. The owner role holds no capability rows at
	// all by design (it bypasses capability checks so an empty permission table cannot
	// lock the owner out), so a recipient query written as "roles granting
	// integrations.manage" silently excludes the one person who always cares.
	IntegrationAdmins(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error)
}

const (
	// healthDedupeWindow is how long the same (entity, band) transition stays quiet.
	//
	// Sized for the failure this exists to stop: a Facebook token that alternates
	// working and failing flips the connection on EVERY alternating delivery today
	// (webhook_processor's flip/heal have no hysteresis at all), which at webhook rates
	// is a notification per second per recipient. The badge is still allowed to flap —
	// it is telling the truth each time — but the paging is not.
	healthDedupeWindow = 30 * time.Minute

	// healthFleetWindow / healthFleetOrgLimit bound a CORRELATED storm.
	//
	// Every kind now counts an Ingest 5xx, so a single RecordService or database fault
	// is a fleet-wide event: every source in every org crosses the threshold at once,
	// and the fan-out would then mail every admin on the platform a message blaming
	// their own integration — while hammering the database that is already failing.
	// Above this many distinct orgs in a window we stop notifying and log once. The
	// badges still turn red, because that part is true; only the accusation is withheld.
	healthFleetWindow   = 15 * time.Minute
	healthFleetOrgLimit = 5

	// healthQueueDepth bounds the dispatch buffer. A drop is logged, never silent.
	healthQueueDepth = 256
)

// healthEvent is one transition worth telling someone about.
type healthEvent struct {
	OrgID uuid.UUID
	// EntityID is the source or connection the notification deep-links to.
	EntityID uuid.UUID
	// Band is the state being announced. It is half the dedupe key, so an escalation
	// and a recovery for the same row never suppress each other.
	Band string
	// Creator is the source/connection author, notified alongside the admins when they
	// are still a live member. Nullable by design — a lead pipe outlives its author.
	Creator *uuid.UUID
	Title   string
	Body    string
	Link    string
	// Window overrides healthDedupeWindow for bands that need a longer silence.
	// Zero means the default.
	Window time.Duration
}

// Health bands. These name what is being announced, NOT a column — no status column
// gains a value in this slice.
const (
	bandSourceFailing     = "source_failing"
	bandSourceRecovered   = "source_recovered"
	bandConnDegraded      = "connection_degraded"
	bandConnError         = "connection_error"
	bandConnRecovered     = "connection_recovered"
	bandSourceKeyMismatch = "source_key_mismatch"
)

// healthDedupe is an in-process, TTL'd "have we just said this" set.
//
// In-process is a deliberate limitation with a known cost: on N replicas a flapping
// entity can produce up to N notifications per window instead of one. That is bounded,
// it degrades toward NOISIER rather than SILENT, and it needs no Redis — so a Redis
// outage cannot turn an alarm off. A durable watermark would fix the N, and would cost
// the eight failure modes documented at the top of this file.
type healthDedupe struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newHealthDedupe() *healthDedupe { return &healthDedupe{seen: map[string]time.Time{}} }

// claim reports whether this (entity, band) may be announced now, and records it.
func (d *healthDedupe) claim(key string, now time.Time, window time.Duration) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if at, ok := d.seen[key]; ok && now.Sub(at) < window {
		return false
	}
	// Opportunistic eviction: this map only ever holds entities that recently changed
	// state, so it is small, but an unbounded map in a long-lived process is a leak.
	if len(d.seen) > 4096 {
		for k, at := range d.seen {
			if now.Sub(at) >= window {
				delete(d.seen, k)
			}
		}
	}
	d.seen[key] = now
	return true
}

// HealthReporter turns transitions into notifications, off the request path.
//
// Off the request path because a fan-out is one preference read + insert + unread count
// + SSE publish PER RECIPIENT, and a lead delivery must not pay for it. Best-effort
// throughout: a notification that cannot be sent is logged, and never fails the lead
// that revealed the problem.
type HealthReporter struct {
	notifier Notifier
	audience HealthAudience
	members  MemberChecker
	logger   *slog.Logger

	queue  chan healthEvent
	dedupe *healthDedupe

	// fleet tracking for the correlated-storm breaker.
	fleetMu    sync.Mutex
	fleetOrgs  map[uuid.UUID]time.Time
	fleetSince time.Time

	stopOnce sync.Once
	done     chan struct{}
}

// NewHealthReporter builds a reporter. A nil notifier or audience yields a nil
// reporter, and every call site is nil-tolerant — health alerting is an addition to the
// pipeline, so a workspace without it configured must keep capturing leads normally.
func NewHealthReporter(n Notifier, a HealthAudience, m MemberChecker, logger *slog.Logger) *HealthReporter {
	if n == nil || a == nil {
		return nil
	}
	return &HealthReporter{
		notifier:  n,
		audience:  a,
		members:   m,
		logger:    logger,
		queue:     make(chan healthEvent, healthQueueDepth),
		dedupe:    newHealthDedupe(),
		fleetOrgs: map[uuid.UUID]time.Time{},
		done:      make(chan struct{}),
	}
}

// Start runs the dispatch loop until ctx is cancelled or Stop is called.
func (r *HealthReporter) Start(ctx context.Context) {
	if r == nil {
		return
	}
	defer close(r.done)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-r.queue:
			if !ok {
				return
			}
			r.dispatch(ctx, ev)
		}
	}
}

// Stop closes the queue and waits for the in-flight buffer to drain.
//
// Called from main.go's shutdown block beside autoEngine.Stop(). Without it a rolling
// deploy discards whatever is buffered — and a deploy is one of the likeliest causes of
// the very failures being reported, so the alarm would be lost exactly when it fires.
func (r *HealthReporter) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		close(r.queue)
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
		}
	})
}

// enqueue offers an event to the dispatcher, dropping (loudly) rather than blocking.
//
// Never blocks: the caller is a lead delivery, and a slow notification consumer must
// not become backpressure on lead capture.
func (r *HealthReporter) enqueue(ev healthEvent) {
	if r == nil {
		return
	}
	window := ev.Window
	if window <= 0 {
		window = healthDedupeWindow
	}
	if !r.dedupe.claim(ev.EntityID.String()+"|"+ev.Band, time.Now(), window) {
		return
	}
	select {
	case r.queue <- ev:
	default:
		if r.logger != nil {
			r.logger.Error("integrations: health notification dropped, queue full",
				"org_id", ev.OrgID.String(), "band", ev.Band)
		}
	}
}

// fleetAllows implements the correlated-storm breaker.
func (r *HealthReporter) fleetAllows(orgID uuid.UUID, now time.Time) bool {
	r.fleetMu.Lock()
	defer r.fleetMu.Unlock()
	if now.Sub(r.fleetSince) >= healthFleetWindow {
		r.fleetOrgs = map[uuid.UUID]time.Time{}
		r.fleetSince = now
	}
	if _, ok := r.fleetOrgs[orgID]; ok {
		return true // already counted this org in the window
	}
	if len(r.fleetOrgs) >= healthFleetOrgLimit {
		return false
	}
	r.fleetOrgs[orgID] = now
	return true
}

// dispatch resolves recipients and creates one notification each.
func (r *HealthReporter) dispatch(ctx context.Context, ev healthEvent) {
	if !r.fleetAllows(ev.OrgID, time.Now()) {
		// Deliberately a log line and not a notification. When this trips, the cause is
		// almost certainly ours, and telling a customer their integration is broken
		// during our own outage is worse than telling them nothing.
		if r.logger != nil {
			r.logger.Error("integrations: health notifications suppressed — many orgs failing at once, this looks like a platform fault",
				"org_id", ev.OrgID.String(), "band", ev.Band)
		}
		return
	}

	recipients, err := r.audience.IntegrationAdmins(ctx, ev.OrgID)
	if err != nil {
		if r.logger != nil {
			r.logger.Error("integrations: could not resolve health notification recipients",
				"org_id", ev.OrgID.String(), "error", err)
		}
		return
	}
	recipients = r.withCreator(ctx, ev, recipients)

	for _, uid := range recipients {
		in := domain.NotificationCreateInput{
			OrgID: ev.OrgID,
			UserID: uid,
			// Always set explicitly: an empty Type is silently coerced to "automation",
			// which would put integration health under the member's Workflow
			// notifications toggle and make the two indistinguishable in the bell.
			Type:       NotifyTypeIntegrationHealth,
			Title:      ev.Title,
			Body:       ev.Body,
			Link:       ev.Link,
			EntityType: "lead_source",
			EntityID:   &ev.EntityID,
		}
		n, err := r.notifier.Create(ctx, in)
		if err != nil {
			if r.logger != nil {
				r.logger.Error("integrations: health notification failed",
					"org_id", ev.OrgID.String(), "user_id", uid.String(), "error", err)
			}
			continue
		}
		// A nil notification with a nil error means the recipient's own preferences
		// suppressed it. That is a success, not a failure — and checking only err here
		// would nil-panic on any muted admin.
		if n == nil && r.logger != nil {
			r.logger.Info("integrations: health notification suppressed by recipient preferences",
				"org_id", ev.OrgID.String(), "user_id", uid.String())
		}
	}
}

// withCreator appends the source's author when they are still a live member.
//
// CreatedBy is nullable on purpose (ON DELETE SET NULL — a lead pipe must outlive the
// admin who made it), and offboarding does NOT null it, so a present id is not evidence
// of a current member. Liveness is re-checked; an unknown answer drops the creator
// rather than mailing someone who left.
func (r *HealthReporter) withCreator(ctx context.Context, ev healthEvent, recipients []uuid.UUID) []uuid.UUID {
	if ev.Creator == nil || r.members == nil {
		return recipients
	}
	for _, uid := range recipients {
		if uid == *ev.Creator {
			return recipients // already an admin
		}
	}
	live, err := r.members.ActiveMemberIDs(ctx, ev.OrgID, []uuid.UUID{*ev.Creator})
	if err != nil || !live[*ev.Creator] {
		return recipients
	}
	return append(recipients, *ev.Creator)
}

// countSourceFailure records one post-authentication processing failure and
// announces the flip if this call caused it.
//
// The one place every channel's failure counting now goes. Before L6.1 only
// google_ads and form_embed called it, which made health alerting structurally blind
// on the L1 capture API — the highest-traffic channel — where consecutive_failures
// was permanently 0 and `status` could never become 'error' at all. Alerting off a
// counter that the busiest pipe never touches is alerting off nothing.
//
// The rule that stayed: callers must invoke this ONLY after the source's own
// credential has been verified. Pre-auth failures (unknown token, bad key, rate
// limit, daily cap) are forgeable by anyone who read a public token off a landing
// page, and since the response a red badge asks for is "disable this source", a
// forgeable alarm is a remote kill switch on the customer's lead flow rather than a
// merely annoying false positive.
func (h *Handler) countSourceFailure(ctx context.Context, source *LeadSource) {
	flipped, err := h.repo.IncrementSourceFailure(ctx, source.ID)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("integrations: could not count source failure", "error", err, "source_id", source.ID.String())
		}
		return
	}
	if flipped {
		if h.logger != nil {
			h.logger.Error("integrations: source flipped to error after consecutive failures",
				"source_id", source.ID.String(), "org_id", source.OrgID.String())
		}
		h.health.SourceFailing(source.OrgID, source.ID, source.Name, source.CreatedBy)
	}
}

// ---------------------------------------------------------------------------
// The copy.
//
// Three rules, each of which exists because the obvious wording is FALSE here:
//
//  1. Never "stopped receiving leads". `error` is a self-healing BADGE, not a gate —
//     IsLive() is still true, the endpoint still accepts, and the next success un-flips
//     it. A source in `error` is still writing leads whenever it can.
//  2. Never render the counter as a rate ("7 of your last 20 failed"). consecutive_failures
//     counts unbroken RUNS; it cannot honestly carry a rate, and inventing one is how a
//     status page stops being believed.
//  3. Never point CONNECTION copy at the delivery log. A connection-side failure happens
//     before ingest stamps source_id, so those rows keep source_id NULL — and the only
//     events route filters on source_id. That log structurally cannot contain them until
//     L6.3 adds a connection-scoped view.
// ---------------------------------------------------------------------------

// SourceFailing announces a source that has crossed the consecutive-failure threshold.
func (r *HealthReporter) SourceFailing(orgID, sourceID uuid.UUID, name string, creator *uuid.UUID) {
	r.enqueue(healthEvent{
		OrgID: orgID, EntityID: sourceID, Band: bandSourceFailing, Creator: creator,
		Title: "Lead source \"" + name + "\" is failing",
		Body: "The last several deliveries to this source could not be turned into records. " +
			"It is still accepting deliveries and every one of them is recorded, so nothing is being " +
			"discarded — but leads are not reaching your CRM until this is fixed. Open the delivery " +
			"log to see what each failure said.",
		Link: "/settings/integrations/" + sourceID.String(),
	})
}

// SourceRecovered announces a source that has processed a lead successfully again.
func (r *HealthReporter) SourceRecovered(orgID, sourceID uuid.UUID, name string, creator *uuid.UUID) {
	r.enqueue(healthEvent{
		OrgID: orgID, EntityID: sourceID, Band: bandSourceRecovered, Creator: creator,
		Title: "Lead source \"" + name + "\" is working again",
		Body:  "A lead was received and written successfully, so this source is no longer flagged.",
		Link:  "/settings/integrations/" + sourceID.String(),
	})
}

// SourceKeyMismatch announces a run of deliveries rejected for a bad key.
//
// This is the ONLY alert here that an anonymous caller can trigger — anyone who reads a
// public_token off a landing page can post a wrong key. It ships anyway, because the
// threat model is the REMEDY, not the trigger: the action this asks for is "check your
// key", which is harmless, whereas the action a red badge asks for is "disable the
// source", which is why a forgeable signal must never move status. So this one writes
// no status, touches no counter, and says both things that could be true.
func (r *HealthReporter) SourceKeyMismatch(orgID, sourceID uuid.UUID, name string, creator *uuid.UUID) {
	r.enqueue(healthEvent{
		OrgID: orgID, EntityID: sourceID, Band: bandSourceKeyMismatch, Creator: creator,
		// A full day of silence, not the usual half hour. This is the one band a
		// stranger can drive, so its rate must be bounded by US rather than by them:
		// without it, anyone who read the URL could mail the org's admins on a loop.
		Window: 24 * time.Hour,
		Title:  "Deliveries to \"" + name + "\" are being rejected for a bad key",
		Body: "Something is posting to this source's URL with a key that does not match. " +
			"Either the key was rotated here and not updated in Google Ads, or someone is probing " +
			"the URL. No leads were lost to this: deliveries carrying the right key are unaffected, " +
			"and every rejected attempt is in the delivery log.",
		Link: "/settings/integrations/" + sourceID.String(),
	})
}

// ConnectionDegraded announces sustained throttling or provider trouble.
//
// Deliberately NOT worded as a credential problem, and deliberately capped short of
// `error` at the call site: a Graph outage and a revoked token are different events with
// different remedies, and letting them share one band would mean telling an admin to
// redo OAuth because Meta was rate-limiting us.
//
// "Meta", not "Facebook": the same page connection carries Instagram lead ads (L7.1),
// and naming one placement in a message about a platform-wide throttle invites the
// admin to go looking for a Facebook-specific fault that does not exist.
func (r *HealthReporter) ConnectionDegraded(orgID, connID uuid.UUID, label string, creator *uuid.UUID) {
	r.enqueue(healthEvent{
		OrgID: orgID, EntityID: connID, Band: bandConnDegraded, Creator: creator,
		Title: "Meta is throttling or failing requests for \"" + label + "\"",
		Body: "We keep being turned away when fetching leads for this page, which usually means a " +
			"platform-side rate limit or outage rather than a problem with your connection. " +
			"Deliveries are retried a limited number of times, so some may not arrive. " +
			"Your credentials look fine and reconnecting will not help.",
		Link: "/settings/integrations",
	})
}

// ConnectionError announces a credential that no longer works.
func (r *HealthReporter) ConnectionError(orgID, connID uuid.UUID, label string, creator *uuid.UUID) {
	r.enqueue(healthEvent{
		OrgID: orgID, EntityID: connID, Band: bandConnError, Creator: creator,
		Title: "Reconnect \"" + label + "\" — its access has stopped working",
		Body: "Leads for this page cannot be fetched: the access token was rejected. This usually " +
			"means the app was removed from the page's business integrations, or the permission " +
			"was withdrawn. New leads for this page will not reach your CRM until you reconnect it.",
		Link: "/settings/integrations",
	})
}

// ConnectionRecovered announces a connection fetching leads again.
func (r *HealthReporter) ConnectionRecovered(orgID, connID uuid.UUID, label string, creator *uuid.UUID) {
	r.enqueue(healthEvent{
		OrgID: orgID, EntityID: connID, Band: bandConnRecovered, Creator: creator,
		Title: "\"" + label + "\" is fetching leads again",
		Body:  "A lead was fetched successfully, so this connection is no longer flagged.",
		Link:  "/settings/integrations",
	})
}
