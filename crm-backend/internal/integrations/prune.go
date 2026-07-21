package integrations

import (
	"context"
	"log/slog"
	"time"
)

// Ledger retention — the erasure path for deliveries no data-subject request can
// ever reach.
//
// Erasure everywhere else in this pipeline is CONTACT-KEYED: deleting a contact
// redacts the ledger rows whose result_record_id points at it. That covers every
// delivery that became a record, and it covers none of the rest. A delivery that
// FAILED or was QUARANTINED — a spam submission, a cap refusal, a lead whose form
// was never enabled, a payload we could not write — still stored the person's name,
// email and phone verbatim in raw_payload, and no contact was ever created to key
// an erasure off. Those rows were unreachable by construction and lived forever.
//
// So retention is the only honest answer for them: we cannot erase them ON REQUEST
// because we cannot find the requester, so we bound how long they exist at all.
//
// The sweep has TWO arms, because an adversarial pass showed "unreachable" has two
// causes and only one of them is age:
//
//  1. ORPHANS — a failed or quarantined delivery that never produced a record.
//     Nothing can target it on request, so retention is the only answer. Age-gated.
//  2. FAILED ERASURES — a delivery whose contact is now gone but whose redaction
//     never ran. Contact deletion calls the redactor best-effort and only LOGS on
//     failure, and a second delete cannot re-trigger it (the soft-delete finds no
//     live row and returns before reaching the redactor), so a missed redaction is
//     permanent. Scoping the sweep to orphans alone would have excluded precisely the
//     rows where erasure was promised and did not happen — the design's own
//     justification, left aspirational. This arm is a repair, so it applies the same
//     write contact deletion would have, and needs no age gate.
//
// What it deliberately does NOT do:
//
//   - It does not touch a row whose contact is still alive. Those are erasable on
//     request, and they are the only rows that can legitimately carry a consent
//     envelope — which the product promises to keep until the contact is deleted. A
//     blanket age sweep would destroy that evidence for live customers.
//   - It does not DELETE rows. What must go is the personal data, not the fact that a
//     delivery happened: the row and its status are the ops record ("how many
//     deliveries failed last quarter") and the answer to "did anything arrive at
//     all". Deleting is irreversible and destroys both, for no compliance gain.
//   - It does not blank `context` wholesale. The provider routing ids inside it are
//     what the retry path reads; erasing them would answer "this lead's form is not
//     enabled" forever, on forms that are.
const (
	// ledgerRetention is how long an unresolved delivery keeps the payload the
	// subject supplied.
	ledgerRetention = 90 * 24 * time.Hour

	// ledgerPruneInterval is how often the sweep runs. Retention is measured in
	// months, so this only has to be frequent enough that a restart-heavy deployment
	// still reaches it — not frequent enough to matter for timeliness.
	ledgerPruneInterval = 6 * time.Hour

	// ledgerPruneBatch bounds one statement's row count. The ledger is the async work
	// queue as well as the history, so a single unbounded UPDATE could hold locks on
	// a hot table for as long as it takes to rewrite every stale row in the fleet.
	ledgerPruneBatch = 500

	// ledgerPruneMaxRounds bounds one RUN. A first sweep over a long-lived
	// installation could have years of backlog; taking it in slices across ticks
	// keeps any single pass short and interruptible.
	ledgerPruneMaxRounds = 40
)

// StartLedgerPrune runs the retention sweep until ctx is cancelled.
//
// Launched like the other workers, and like them it runs on EVERY replica — but
// unlike them it is not merely idempotent-by-atomic-statement, it is a bulk
// destructive rewrite. Several replicas sweeping the same backlog simultaneously
// would multiply the write load on a hot table to no purpose, so this one takes an
// advisory lock and the losers skip the tick entirely.
func StartLedgerPrune(ctx context.Context, repo *Repository, logger *slog.Logger) {
	t := time.NewTicker(ledgerPruneInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			runLedgerPrune(ctx, repo, logger)
		}
	}
}

// runLedgerPrune performs one bounded sweep.
func runLedgerPrune(ctx context.Context, repo *Repository, logger *slog.Logger) {
	var total int64
	rounds := 0

	got, err := repo.WithLedgerPruneLock(ctx, func() error {
		cutoff := time.Now().Add(-ledgerRetention)
		for ; rounds < ledgerPruneMaxRounds; rounds++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			n, err := repo.PruneExpiredPayloads(ctx, cutoff, ledgerPruneBatch)
			if err != nil {
				return err
			}
			total += n
			if n < ledgerPruneBatch {
				return nil // backlog drained
			}
		}
		return nil
	})
	if err != nil {
		if logger != nil {
			logger.Error("integrations: ledger retention sweep failed", "error", err, "redacted", total)
		}
		return
	}
	if !got {
		return // another replica is sweeping
	}
	if total > 0 && logger != nil {
		// Loud on purpose. This is the only thing in the system that erases customer
		// data on a timer, so the count is the record that it ran and what it cost.
		logger.Info("integrations: redacted expired lead payloads",
			"redacted", total, "retention_days", int(ledgerRetention.Hours()/24))
	}
	if rounds >= ledgerPruneMaxRounds && logger != nil {
		// Never silently truncate: a run that stopped at its cap has left rows over
		// retention, and saying so is the difference between "the backlog is draining
		// across ticks" and "retention is quietly not being met".
		logger.Warn("integrations: ledger retention sweep hit its per-run cap; the remainder continues on the next tick",
			"redacted", total)
	}
}
