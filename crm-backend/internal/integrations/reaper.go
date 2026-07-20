package integrations

import (
	"context"
	"log/slog"
	"time"
)

// The stranded-delivery reaper.
//
// A delivery is inserted as `processing` before any work happens, and only moves to
// a terminal status when the pipeline finishes. If the process dies in between — a
// redeploy, a crash, a killed request — the row stays `processing` forever. Nothing
// repairs it on its own: Ingest's status assignment lives in Go memory until
// FinishEvent runs.
//
// That is not merely untidy. The replay switch treats a non-terminal prior delivery
// as "still in flight" and answers 409, so the Idempotency-Key — the documented way
// to make a retry SAFE — becomes the thing that makes the lead permanently
// unrecoverable. The batch endpoint's entire contract is "retry exactly the failed
// rows", so it cannot ship over a path that can poison a key forever.
//
// Marking the row `failed` is what makes it retryable: Ingest's failed-row branch
// re-runs the pipeline against it instead of banking a phantom success.
const (
	// reapGrace must exceed the longest a live delivery can legitimately hold a
	// `processing` row. The batch ledger timeout is the real ceiling, so this is set
	// well above it — reaping a delivery that is still running would let a concurrent
	// retry write the same lead twice.
	reapGrace = 10 * time.Minute
	// reapInterval is how often we look. Cheap: a partial index covers the scan.
	reapInterval = 5 * time.Minute

	// oauthSweepInterval is how often expired OAuth custody rows are purged, and
	// oauthSweepRetain is how long past expiry/consumption a row is kept — a small
	// grace so a diagnosis can still read a just-expired attempt.
	oauthSweepInterval = 30 * time.Minute
	oauthSweepRetain   = 1 * time.Hour

	// maxWebhookAttempts bounds how many times an async provider delivery (L5) is
	// tried before it is given up as failed — the plan's "≤3 attempts". Shared by
	// the async webhook processor (which fails a delivery whose fetch keeps erroring)
	// and the reaper (which stops re-claiming a delivery that keeps stranding).
	maxWebhookAttempts = 3
)

// StartReaper runs the stranded-delivery sweep until ctx is cancelled.
func StartReaper(ctx context.Context, repo *Repository, logger *slog.Logger) {
	t := time.NewTicker(reapInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := repo.ReapStrandedEvents(ctx, reapGrace)
			if err != nil {
				if logger != nil {
					logger.Error("integrations: reaping stranded deliveries failed", "error", err)
				}
				continue
			}
			if n > 0 && logger != nil {
				// Loud on purpose: a stranded delivery means a process died mid-write, and
				// the count is the only signal anyone gets that it happened.
				logger.Warn("integrations: released stranded deliveries for retry", "count", n)
			}
		}
	}
}

// StartOAuthArtifactSweeper purges consumed/expired OAuth state and pending
// connection rows until ctx is cancelled. Advisory-only (every consume re-checks
// expiry), so a failed sweep is logged and retried on the next tick, never fatal.
func StartOAuthArtifactSweeper(ctx context.Context, repo *Repository, logger *slog.Logger) {
	t := time.NewTicker(oauthSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := repo.PurgeExpiredOAuthArtifacts(ctx, oauthSweepRetain); err != nil && logger != nil {
				logger.Error("integrations: purging expired OAuth artifacts failed", "error", err)
			}
		}
	}
}
