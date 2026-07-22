package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"crm-backend/internal/domain"
)

const (
	// webhookPollInterval is how often the async processor sweeps for pending
	// provider deliveries. Short — a lead should reach the CRM within a few seconds
	// of Meta's webhook, not minutes.
	webhookPollInterval = 5 * time.Second
	// webhookClaimBatch is how many deliveries one claim grabs. FOR UPDATE SKIP
	// LOCKED lets replicas share the queue, so this is a throughput knob, not a
	// correctness one.
	webhookClaimBatch = 50
	// maxDrainRounds bounds one tick's work so a huge backlog cannot starve the
	// ticker (and the shutdown check) — the backlog drains across ticks instead.
	maxDrainRounds = 20
)

// webhookProcessor turns claimed `pending` provider deliveries into CRM records:
// resolve the connection + credentials, fetch the lead from the provider, resolve
// the per-form source, and run it through the shared ingest core. It reuses the
// ConnectionService for credential decryption and provider access (same package),
// and the LeadIngestService's IngestClaimed for the write.
type webhookProcessor struct {
	repo   *Repository
	conn   *ConnectionService
	ingest *LeadIngestService
	logger *slog.Logger
	// health announces connection transitions. Nil-tolerant.
	health *HealthReporter
}

// StartWebhookProcessor runs the async claim→fetch→ingest loop until ctx is
// cancelled — the StartReaper pattern. Launched with context.Background() so a
// request cancellation cannot kill it.
func StartWebhookProcessor(ctx context.Context, repo *Repository, conn *ConnectionService, ingest *LeadIngestService, logger *slog.Logger, health *HealthReporter) {
	p := &webhookProcessor{repo: repo, conn: conn, ingest: ingest, logger: logger, health: health}
	t := time.NewTicker(webhookPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.drain(ctx)
		}
	}
}

// drain claims and processes deliveries until the queue is empty (a claim returns
// fewer than a full batch) or the per-tick round cap is hit.
func (p *webhookProcessor) drain(ctx context.Context) {
	for round := 0; round < maxDrainRounds; round++ {
		if ctx.Err() != nil {
			return
		}
		events, err := p.repo.ClaimPendingEvents(ctx, webhookClaimBatch)
		if err != nil {
			p.logf("integrations: claiming webhook deliveries failed", "error", err)
			return
		}
		if len(events) == 0 {
			return
		}
		for i := range events {
			p.process(ctx, &events[i])
		}
		if len(events) < webhookClaimBatch {
			return // queue drained
		}
	}
}

// process handles one claimed delivery.
func (p *webhookProcessor) process(ctx context.Context, event *IntegrationEvent) {
	if event.ConnectionID == nil {
		// A claimed webhook event with no connection is a corrupt row; fail it so it
		// is not re-claimed forever.
		p.fail(ctx, event, "delivery has no connection")
		return
	}
	conn, err := p.repo.GetConnection(ctx, event.OrgID, *event.ConnectionID)
	if err != nil {
		// A read error is transient — leave it for a retry (repend if budget remains).
		p.retryOrFail(ctx, event, true, "could not load the connection")
		return
	}
	if conn == nil {
		// The connection was removed since receipt. The delivery belongs to a page no
		// longer connected here — fail it (do not process into a torn-down connection).
		p.fail(ctx, event, "the connection was disconnected before this lead could be fetched")
		return
	}

	prov, ok := p.conn.registry.Get(conn.Provider)
	if !ok {
		p.fail(ctx, event, "provider "+conn.Provider+" is no longer registered")
		return
	}
	creds, err := p.conn.openCredentials(conn)
	if err != nil {
		// Unreadable credentials: the KEK changed or the blob is corrupt. Loud — this
		// is silent lead loss otherwise — and flip the connection so the admin sees it.
		p.logf("integrations: could not open connection credentials", "connection_id", conn.ID.String(), "error", err)
		p.flipConnectionError(ctx, conn, "stored credentials could not be opened — reconnect this account")
		p.fail(ctx, event, "stored credentials could not be opened")
		return
	}

	formID := stringOf(readContext(event.Context)["form_id"])
	inbound := InboundEvent{
		ExternalAccountID: conn.ExternalAccountID,
		ProviderEventID:   derefString(event.ProviderEventID),
		FormID:            formID,
		// Rehydrated from the stored delivery, and this one line is what lets the
		// framework carry a provider whose webhook already contains the lead. Meta's
		// carries ids only, so FetchLead reads the lead back over the network; TikTok's
		// carries the answers inline and its API has no by-id read at all. A provider
		// that declares CarriesLeadData implements FetchLead as a pure function over
		// this map, and the claim/retry/health machinery around it is unchanged.
		Raw: readContext(event.RawPayload),
	}

	lead, err := prov.FetchLead(ctx, conn, creds, inbound)
	if err != nil {
		if errors.Is(err, ErrDeliveryUnusable) {
			// The DELIVERY is unusable, not the connection. Retrying cannot help and
			// the credential is not implicated, so this fails the one row and leaves the
			// health signal alone — flipping the account here would tell an admin to
			// redo OAuth over a single malformed callback.
			p.fail(ctx, event, err.Error())
			return
		}
		p.handleFetchError(ctx, conn, event, err)
		return
	}

	// The fetch worked, so the token is good — heal a connection that had tripped.
	p.healConnection(ctx, conn)

	// Resolve the per-form source. form_id from the fetched lead is authoritative;
	// fall back to the webhook's.
	fetchedForm := stringOf(lead.Context["form_id"])
	if fetchedForm == "" {
		fetchedForm = formID
	}
	source, serr := p.repo.FindConnectionFormSource(ctx, conn.ID, prov.Info().SourceKind, conn.Provider, fetchedForm)
	if serr != nil {
		p.retryOrFail(ctx, event, true, "could not resolve the form's source")
		return
	}
	if source == nil {
		// The form is not enabled (no facebook_form source). Quarantine with the
		// fetched data stored, so enabling the form later (L5.4) shows what arrived —
		// terminal, so the worker stops re-claiming it. Never pending (it would loop).
		p.quarantine(ctx, event, lead, "this lead's form ("+fetchedForm+") is not enabled — enable it in Integrations settings to start processing its leads")
		return
	}
	if !source.IsLive() {
		// An admin disabled this form's source. Every synchronous channel gates on
		// source liveness before ingesting; the async path honors the same choice —
		// a disabled source stops receiving, quarantined (not pending, which would
		// loop) so re-enabling shows what arrived.
		p.quarantine(ctx, event, lead, "this lead's form source is disabled — re-enable it in Integrations settings to process its leads")
		return
	}

	if _, ierr := p.ingest.IngestClaimed(ctx, source, lead, event); ierr != nil {
		// IngestClaimed already marked the event failed (via failEvent). But the WRITE
		// path, unlike the fetch path, has no retry of its own — and a webhook delivery
		// has no client to retry it. So distinguish: a VALIDATION refusal (a 4xx
		// AppError — unsupported target, email-required) is genuinely the lead's fault
		// and stays failed; a TRANSIENT infra error (a DB serialization/timeout, pool
		// exhaustion, the write deadline — a raw or 5xx error) is not, so repend it
		// while attempt budget remains rather than lose the lead silently.
		if !isPermanentIngestError(ierr) && event.Attempts < maxWebhookAttempts {
			if rerr := p.repo.RependEvent(ctx, event.OrgID, event.ID, "lead write failed transiently; will retry"); rerr != nil {
				p.logf("integrations: could not repend after a transient write failure", "event_id", event.ID.String(), "error", rerr)
			}
			return
		}
		p.logf("integrations: webhook ingest failed", "event_id", event.ID.String(), "error", ierr)
	}
}

// isPermanentIngestError reports whether an IngestClaimed error is a genuine
// validation refusal (a 4xx AppError) that retrying cannot fix — as opposed to a
// transient infrastructure error (a raw DB/timeout error, or a 5xx), which the
// async worker should retry because a webhook delivery has no client to retry it.
func isPermanentIngestError(err error) bool {
	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		return appErr.Code >= 400 && appErr.Code < 500
	}
	return false
}

// handleFetchError applies the retry taxonomy to a failed lead fetch.
func (p *webhookProcessor) handleFetchError(ctx context.Context, conn *IntegrationConnection, event *IntegrationEvent, err error) {
	if IsRetryableHTTP(err) {
		// Transient (5xx / 429 / network). Retry while budget remains; a persistent
		// transient failure (a Graph outage) eventually fails but does NOT flip the
		// connection — the token may be perfectly fine.
		if event.Attempts < maxWebhookAttempts {
			if rerr := p.repo.RependEvent(ctx, event.OrgID, event.ID, "lead fetch failed transiently; will retry"); rerr != nil {
				p.logf("integrations: could not repend delivery", "event_id", event.ID.String(), "error", rerr)
			}
			return
		}
		// Budget exhausted on a TRANSIENT failure: this delivery is now lost, and
		// nothing about the connection said so until L6.1. Count it toward `degraded`
		// — capped there, never `error`, because the credential is very likely fine
		// and telling an admin to reconnect during a Graph outage would be false.
		p.degradeConnection(ctx, conn, "leads could not be fetched — the provider is rate-limiting or failing our requests")
		p.fail(ctx, event, "lead fetch kept failing transiently")
		return
	}
	// Permanent (a 4xx). On a lead fetch this is almost always a dead or
	// insufficiently-scoped token, so it is LOUD: flip the connection to error (the
	// reconnect banner + L6 notification) — a webhook carries only ids, so a silent
	// fetch failure is silent lead loss.
	p.logf("integrations: lead fetch permanently failed", "connection_id", conn.ID.String(), "event_id", event.ID.String(), "error", err)
	p.flipConnectionError(ctx, conn, "leads could not be fetched (the connection's access may have been revoked) — reconnect this account")
	p.fail(ctx, event, "lead fetch failed permanently (token/permission)")
}

// flipConnectionError trips a connection to error, counting POST-fetch failures
// only (a webhook delivery reaching the fetch has already passed the signature
// check, so it is not attacker-forgeable). Best-effort.
// It takes the transition back from the statement rather than inferring it from the
// row as claimed: two replicas can hold the same connection's deliveries at once, so
// an in-memory `conn.Status == error` comparison is not evidence that THIS process is
// the one that changed it — and a notification hung off that comparison fires once per
// replica. The `prev` CTE's row lock makes exactly one caller observe the change.
func (p *webhookProcessor) flipConnectionError(ctx context.Context, conn *IntegrationConnection, reason string) {
	p.bumpConnection(ctx, conn, true, reason)
}

// degradeConnection counts a RETRYABLE fetch failure.
//
// New in L6.1, and it closes the quietest hole in the pipeline: 429 and 5xx are
// classified retryable, so they never flipped anything, and a sustained Graph outage
// or rate limit would exhaust the three-attempt budget, drop every delivery, and leave
// the connection card reading "connected". Silent lead loss behind a green badge is
// precisely what `degraded` was declared for and never used for.
func (p *webhookProcessor) degradeConnection(ctx context.Context, conn *IntegrationConnection, reason string) {
	p.bumpConnection(ctx, conn, false, reason)
}

func (p *webhookProcessor) bumpConnection(ctx context.Context, conn *IntegrationConnection, permanent bool, reason string) {
	band, err := p.repo.BumpConnectionFailure(ctx, conn.OrgID, conn.ID, permanent, reason)
	if err != nil {
		p.logf("integrations: could not record connection failure", "connection_id", conn.ID.String(), "error", err)
		return
	}
	switch band {
	case ConnStatusError:
		p.health.ConnectionError(conn.OrgID, conn.ID, conn.ExternalAccountLabel, conn.CreatedBy)
	case ConnStatusDegraded:
		p.health.ConnectionDegraded(conn.OrgID, conn.ID, conn.ExternalAccountLabel, conn.CreatedBy)
	}
}

// healConnection restores a connection to connected after a successful fetch — the
// self-healing badge, matching TouchSourceUsed for sources. Only acts when the
// connection was NOT already healthy, to avoid a write on every delivery.
func (p *webhookProcessor) healConnection(ctx context.Context, conn *IntegrationConnection) {
	healed, err := p.repo.EaseConnectionHealth(ctx, conn.OrgID, conn.ID)
	if err != nil {
		p.logf("integrations: could not heal connection", "connection_id", conn.ID.String(), "error", err)
		return
	}
	if healed {
		p.health.ConnectionRecovered(conn.OrgID, conn.ID, conn.ExternalAccountLabel, conn.CreatedBy)
	}
}

// quarantine records a fetched-but-not-ingestable delivery terminally (so the
// worker stops re-claiming it), storing the fetched data so re-enabling the form
// later shows what arrived.
func (p *webhookProcessor) quarantine(ctx context.Context, event *IntegrationEvent, lead RawLead, reason string) {
	raw, _ := json.Marshal(lead.Fields)
	ctxJSON, _ := json.Marshal(lead.Context)
	event.RawPayload = raw
	event.Context = ctxJSON
	event.Status = EventStatusQuarantined
	event.Error = reason
	if err := p.repo.FinishEvent(ctx, event); err != nil {
		p.logf("integrations: could not quarantine delivery", "event_id", event.ID.String(), "error", err)
	}
}

// retryOrFail repends a delivery when the failure is transient and budget remains,
// else fails it.
func (p *webhookProcessor) retryOrFail(ctx context.Context, event *IntegrationEvent, retryable bool, reason string) {
	if retryable && event.Attempts < maxWebhookAttempts {
		if err := p.repo.RependEvent(ctx, event.OrgID, event.ID, reason+"; will retry"); err != nil {
			p.logf("integrations: could not repend delivery", "event_id", event.ID.String(), "error", err)
		}
		return
	}
	p.fail(ctx, event, reason)
}

// fail marks a delivery terminally failed. Uses FinishEvent (sets processed_at),
// mirroring the ingest service's failEvent but from the worker, which holds the
// claimed row.
func (p *webhookProcessor) fail(ctx context.Context, event *IntegrationEvent, reason string) {
	event.Status = EventStatusFailed
	event.Error = reason
	if err := p.repo.FinishEvent(ctx, event); err != nil {
		p.logf("integrations: could not mark delivery failed", "event_id", event.ID.String(), "error", err)
	}
}

func (p *webhookProcessor) logf(msg string, args ...any) {
	if p.logger != nil {
		p.logger.Error(msg, args...)
	}
}

// readContext decodes an event's Context JSON into a map, tolerating nil/garbage.
func readContext(raw []byte) map[string]any {
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}
