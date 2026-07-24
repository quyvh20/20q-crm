package integrations

import (
	"context"
	"time"
)

// The reconciliation poller — the leadgen webhook's backstop.
//
// A Facebook leadgen webhook delivery can be DROPPED and never redelivered: a
// token-death window, a Meta outage that exhausts the async worker's 3-attempt budget,
// a delivery that lands while the connection is briefly `error`. When that happens the
// lead exists on Facebook but never in the CRM, and nothing notices — the worst failure
// mode this connector has. So on a slow tick we re-pull each live form's RECENT leads
// and import any the webhook missed.
//
// Exactly-once is free: importBackfillLead inserts connection-scoped-deduped on the
// leadgen id, so a lead the webhook already delivered is a no-op. Suppressed by default
// (enroll=false) — a lead recovered 30 minutes late must not fire a burst of welcome
// emails; the webhook is the real-time path and this is a data-recovery net. Bounded by
// stopWhenCaughtUp: the walk ends as soon as a page yields nothing new, so a healthy
// form costs one Graph page per tick and nothing else.
const (
	// reconcileInterval is how often the backstop sweeps. It is a backstop, not the
	// primary path, so this only has to be frequent enough to shrink the window a
	// dropped lead sits unrecovered — not frequent enough to matter for timeliness.
	reconcileInterval = 30 * time.Minute
	// reconcileMaxPages bounds one form's walk per tick. Small on purpose: the poller
	// catches RECENT drops (a short token blip); a long outage's backlog is the
	// reconnect-then-backfill recovery, not this. stopWhenCaughtUp usually ends the walk
	// on the first page.
	reconcileMaxPages = 5
	// reconcileTimeout bounds one whole SWEEP, and with it the advisory lock's hold. The
	// sweep walks the fleet's forms sequentially under the lock; without a ceiling, one
	// wedged provider (or a very large fleet of failing forms) would pin the lock past
	// the next tick and starve every replica. A cut-off tick just leaves later forms for
	// the next one — fine for a backstop.
	reconcileTimeout = 10 * time.Minute
)

// StartReconciliationPoller runs the backstop sweep until ctx is cancelled. Like the
// other fleet workers it runs on every replica but takes an advisory lock so only one
// actually sweeps a given tick. Dormant without a ConnectionService — there is no
// provider to poll.
func (h *Handler) StartReconciliationPoller(ctx context.Context) {
	if h.connections == nil {
		return
	}
	t := time.NewTicker(reconcileInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.runReconcile(ctx)
		}
	}
}

// runReconcile performs one bounded sweep across the fleet's live Facebook forms,
// under the singleton lock.
func (h *Handler) runReconcile(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()
	_, err := h.repo.WithReconcileLock(ctx, func() error {
		sources, lerr := h.repo.ListReconcilableFacebookForms(ctx)
		if lerr != nil {
			return lerr
		}
		for i := range sources {
			if ctx.Err() != nil {
				return nil // shutdown, not a failure
			}
			h.reconcileForm(ctx, &sources[i])
		}
		return nil
	})
	// A replica that lost the lock got (false, nil) and simply did nothing this tick.
	if err != nil {
		h.logf("integrations: reconciliation sweep failed", "error", err)
	}
}

// reconcileForm re-pulls one form's recent leads, importing any the webhook missed.
func (h *Handler) reconcileForm(ctx context.Context, src *LeadSource) {
	conn, prov, creds, err := h.resolveBackfillConnection(ctx, src)
	if err != nil {
		h.logf("integrations: reconcile could not resolve the connection", "source_id", src.ID.String(), "error", err)
		return
	}
	formID := connectionFormID(src, conn.Provider)
	if formID == "" {
		return
	}
	imported, _, werr := h.walkFormLeads(ctx, src, conn, prov, creds, formID, reconcileMaxPages, true, false)
	if werr != nil {
		// The webhook path owns connection failure-counting; the poller only LOGS, so it
		// cannot double-flag a connection the webhook is already tracking as failing.
		h.logf("integrations: reconcile hit a provider error", "source_id", src.ID.String(), "error", werr)
		return
	}
	if imported > 0 {
		// A recovered lead is a terminal delivery success like any other, so it stamps
		// last_synced_at (whose documented meaning is "a lead arrived") and earns a line:
		// a non-zero count is direct evidence the webhook dropped something.
		if serr := h.repo.MarkConnectionSynced(ctx, conn.ID); serr != nil {
			h.logf("integrations: reconcile could not stamp last_synced_at", "connection_id", conn.ID.String(), "error", serr)
		}
		h.logf("integrations: reconciliation recovered leads the webhook missed", "source_id", src.ID.String(), "recovered", imported)
	}
}
