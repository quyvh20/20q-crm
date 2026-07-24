package marketing

import (
	"context"
	"log/slog"
	"sync"
	"time"

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
}

// ResendProcessor drains the marketing_email_events queue and applies suppression +
// the deliverability breaker off the request path. Callerless: it reads org scope off
// each claimed row (persisted at enqueue), never from an actor.
type ResendProcessor struct {
	store  resendConsumerStore
	logger *slog.Logger
	dedupe *alertDedupe
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
		for i := range events {
			p.process(ctx, events[i])
		}
		if len(events) < resendClaimBatch {
			return
		}
	}
}

// process applies one event and marks it terminal, or repends it on a transient
// error while retry budget remains.
func (p *ResendProcessor) process(ctx context.Context, evt MarketingEmailEvent) {
	if err := p.apply(ctx, evt); err != nil {
		if evt.Attempts >= resendMaxAttempts {
			_ = p.store.FinishEvent(ctx, evt.OrgID, evt.ID, EventStatusFailed, err.Error())
			p.logger.Error("marketing: resend event permanently failed", "error", err, "svix_id", evt.SvixID, "type", evt.EventType)
			return
		}
		_ = p.store.RependEvent(ctx, evt.OrgID, evt.ID, err.Error())
		return
	}
	_ = p.store.FinishEvent(ctx, evt.OrgID, evt.ID, EventStatusDone, "")
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
	}
	return nil
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
