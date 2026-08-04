package worker

import (
	"context"
	"time"

	"crm-backend/internal/domain"

	"go.uber.org/zap"
)

// Search-index reconciliation (R6.3)
// ============================================================
//
// Both indexes are maintained by fire-and-forget enqueues that DROP the job when
// the queue is full (Enqueue / EnqueueRecord), and a pod restart discards whatever
// is still buffered. Nothing ever noticed, because nothing ever looked: a record
// that missed its enqueue is simply absent from search forever.
//
// The far bigger hole is not the queue at all. custom_object_usecase.UpdateDef
// flips `searchable` on and busts the schema cache, but backfills NOTHING, and
// indexRecord only fires on create/update — so the moment an admin ticks
// "searchable" on an object that already has records, every one of those records is
// permanently invisible to search until somebody edits it one by one. The same
// sweep closes both, because to a reconciler "never enqueued" and "enqueued before
// the object was searchable" are the same observable state: eligible, not indexed.
//
// THREE arms, not one, because "re-embed rows with a NULL embedding" only describes
// one of the three ways a row goes missing — see domain.RecordIndexBacklog for the
// full argument. Briefly: contacts embed IN PLACE so a drop leaves a NULL column;
// custom records embed into a SEPARATE table so a drop leaves NO ROW AT ALL and
// only an anti-join can see it; and a failed embed CALL leaves a row that exists
// with a NULL vector (deliberately — content-only keeps fulltext alive).

const (
	// reconcileInterval matches the notification digest ticker's cadence. The sweep
	// is a safety net for dropped work, not a delivery path, so hourly is enough for
	// every case except the searchable-flip backfill — and an admin who just ticked
	// the box waits at most an hour, against "never" today.
	reconcileInterval = 1 * time.Hour

	// reconcileStartupDelay staggers the first pass past boot so a rolling deploy
	// isn't racing a sweep while it is still opening connections and warming caches.
	reconcileStartupDelay = 2 * time.Minute

	// reconcileMaxPerPass / reconcileMaxPerOrg are the COST ceiling, and the reason
	// this sweep is bounded at all: every candidate it processes is one paid call
	// through the Cloudflare AI gateway. Discovering a 200k-row backlog and firing
	// all of it would be a slow, expensive surprise arriving in one burst.
	//
	// 100 per arm per pass × 3 arms = at most 300 embedding calls per hour, ~7.2k a
	// day, and only while a backlog actually exists — a drained backlog costs three
	// cheap queries an hour and nothing else. 100 is also half the embedding worker's
	// 200-slot queue, but that is a coincidence, not a dependency: the sweep does NOT
	// enqueue (see reconcileRecords).
	//
	// The per-org cap is the fairness half. Without it a single org backfilling a
	// large object would consume every pass for days and no other tenant's records
	// would be indexed at all.
	reconcileMaxPerPass = 100
	reconcileMaxPerOrg  = 25
)

// ReconcileStats is one pass's outcome. Truncated names the arms that hit their cap
// — the project rule is no silent caps, so a pass that left work behind says so in
// the log rather than looking identical to a pass that found nothing.
type ReconcileStats struct {
	MissingRecords    int
	UnvectoredRecords int
	UnvectoredContact int
	Truncated         []string
}

func (s ReconcileStats) total() int {
	return s.MissingRecords + s.UnvectoredRecords + s.UnvectoredContact
}

// SetBacklog wires the reconciliation source. Called once at startup from main.go
// (mirrors how RecordService gets its indexer). A nil backlog leaves the sweep
// disabled, which is what unit tests that only exercise the queues get.
func (w *EmbeddingWorker) SetBacklog(b domain.RecordIndexBacklog) {
	w.backlog = b
}

// StartReconciler runs the sweep on a ticker until ctx is cancelled. Fire-and-
// forget from main.go with context.Background(), following the notification
// retention/digest goroutines' shape:
//
//	go func() { ... ticker := time.NewTicker(24 * time.Hour); for range ticker.C { sweep() } }()
//
// A failed pass is logged and retried on the next tick; nothing here can fail boot,
// because nothing here is on the boot path.
func (w *EmbeddingWorker) StartReconciler(ctx context.Context) {
	if w.backlog == nil {
		w.logger.Info("search index reconciler disabled (no backlog source wired)")
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconcileStartupDelay):
		}
		w.reconcileTick(ctx)

		ticker := time.NewTicker(reconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.reconcileTick(ctx)
			}
		}
	}()
}

func (w *EmbeddingWorker) reconcileTick(ctx context.Context) {
	stats, ran, err := w.ReconcileOnce(ctx)
	switch {
	case err != nil:
		w.logger.Warn("search index reconcile failed", zap.Error(err))
	case !ran:
		w.logger.Debug("search index reconcile skipped — another instance holds the lock")
	case stats.total() > 0:
		w.logger.Info("search index reconciled",
			zap.Int("records_missing", stats.MissingRecords),
			zap.Int("records_unvectored", stats.UnvectoredRecords),
			zap.Int("contacts_unvectored", stats.UnvectoredContact),
			zap.Strings("truncated_arms", stats.Truncated))
	}
}

// ReconcileOnce runs a single bounded pass under the fleet-wide lock. The bool
// reports whether this instance actually ran (false = another pod holds the lock).
// Exported so an operator-triggered pass and the tests share one code path.
func (w *EmbeddingWorker) ReconcileOnce(ctx context.Context) (ReconcileStats, bool, error) {
	var stats ReconcileStats
	if w.backlog == nil {
		return stats, false, nil
	}
	ran, err := w.backlog.WithReconcileLock(ctx, func() error {
		return w.reconcilePass(ctx, &stats)
	})
	return stats, ran, err
}

func (w *EmbeddingWorker) reconcilePass(ctx context.Context, stats *ReconcileStats) error {
	// Arm 1 first, and unconditionally: a missing record_embeddings row is invisible
	// to BOTH fulltext and semantic search, whereas the other two arms are only
	// missing a vector. This arm is also the only one that does useful work when
	// embedding is unavailable — processRecord still writes content, which restores
	// fulltext immediately.
	missing, err := w.backlog.MissingRecords(ctx, reconcileMaxPerOrg, reconcileMaxPerPass)
	if err != nil {
		return err
	}
	stats.MissingRecords = w.reconcileRecords(ctx, missing)
	if len(missing) >= reconcileMaxPerPass {
		stats.Truncated = append(stats.Truncated, "records_missing")
	}

	// Arms 2 and 3 exist ONLY to fill in a vector, so they are pure waste — and pure
	// cost — with no embed service. Arm 1 has already restored their fulltext.
	if w.embedSvc == nil {
		return nil
	}

	unvectored, err := w.backlog.UnvectoredRecords(ctx, reconcileMaxPerOrg, reconcileMaxPerPass)
	if err != nil {
		return err
	}
	stats.UnvectoredRecords = w.reconcileRecords(ctx, unvectored)
	if len(unvectored) >= reconcileMaxPerPass {
		stats.Truncated = append(stats.Truncated, "records_unvectored")
	}

	contacts, err := w.backlog.UnvectoredContacts(ctx, reconcileMaxPerOrg, reconcileMaxPerPass)
	if err != nil {
		return err
	}
	for i := range contacts {
		if ctx.Err() != nil {
			break
		}
		// process, not Enqueue: same reason as reconcileRecords below.
		w.process(ctx, contactEmbedJob(&contacts[i]))
		stats.UnvectoredContact++
	}
	if len(contacts) >= reconcileMaxPerPass {
		stats.Truncated = append(stats.Truncated, "contacts_unvectored")
	}
	return nil
}

// reconcileRecords indexes a batch SYNCHRONOUSLY rather than pushing it onto
// recordQueue. Enqueueing would be wrong three times over: a 100-row batch against
// a 200-slot queue that live writes are also using would silently DROP part of the
// backlog it just went to the trouble of finding (the exact failure this sweep
// exists to repair); the drops would land as per-job warnings instead of one honest
// truncation line; and the pass would report work it never did. Running inline also
// serializes the embed calls, which is the gentlest possible shape against the
// gateway's rate limit — and it costs nothing, because this whole pass is already
// on its own background goroutine.
func (w *EmbeddingWorker) reconcileRecords(ctx context.Context, cands []domain.RecordIndexCandidate) int {
	n := 0
	for _, c := range cands {
		if ctx.Err() != nil {
			break
		}
		w.processRecord(ctx, recordEmbedJob{
			OrgID:    c.OrgID,
			Slug:     c.ObjectSlug,
			RecordID: c.RecordID,
			Content:  c.Content(),
		})
		n++
	}
	return n
}

// contactEmbedJob mirrors EnqueueContact's adaptation without the queue hop, so a
// reconciled contact embeds exactly the text a freshly-written one would.
func contactEmbedJob(c *domain.Contact) EmbedJob {
	var compName *string
	if c.Company != nil {
		compName = &c.Company.Name
	}
	job := EmbedJob{
		ContactID:   c.ID,
		OrgID:       c.OrgID,
		FirstName:   c.FirstName,
		LastName:    c.LastName,
		Email:       c.Email,
		Phone:       c.Phone,
		CompanyName: compName,
	}
	if len(c.CustomFields) > 0 {
		job.CustomFields = []byte(c.CustomFields)
	}
	return job
}
