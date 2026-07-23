package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// backfillPageDelay throttles between pages/polls, so importing a large form's
// history does not itself trip the rate limiter the connector works so hard to
// respect. A var, not a const, only so a test can zero it — production never sets it.
var backfillPageDelay = 1 * time.Second

const (
	// backfillMaxPages bounds one run. Graph's /leads is already ~90-day-bounded; this
	// is a runaway backstop (100 x 100 leads = 10k, far beyond any real form's window).
	backfillMaxPages = 100
	// backfillTimeout bounds the whole async run so a wedged Graph cannot leak a
	// goroutine forever.
	backfillTimeout = 15 * time.Minute
	// backfillMaxRetries bounds how many consecutive retryable failures a run absorbs
	// before giving up. Small, because the ctx deadline is the real ceiling; this only
	// stops a mislabelled-permanent error from spinning against it.
	backfillMaxRetries = 5
)

// Backfill triggers a historical import for a facebook_form source. It returns
// 202 immediately; the import runs asynchronously and its leads appear in the
// delivery log as they land (deduped against anything already received by webhook).
func (h *Handler) Backfill(c *gin.Context) {
	src, ok := h.loadSource(c)
	if !ok {
		return
	}
	// Connection-backed kinds only. Not an enumeration of providers: a source with no
	// connection has no credential to import WITH, and the provider itself refuses
	// with ErrProviderCapabilityUnsupported if it has no historical export.
	if !IsKeylessKind(src.Kind) || src.Kind == KindWebhookInbound {
		h.mgmtError(c, http.StatusBadRequest, "only connected provider forms can be backfilled")
		return
	}
	if !src.IsLive() {
		// An admin disabled this form's source to pause its intake; the webhook path
		// quarantines a disabled source's leads rather than ingest them, and an
		// explicit import must honor the same pause rather than resume writing.
		h.mgmtError(c, http.StatusBadRequest, "this form's source is disabled — re-enable it before importing")
		return
	}
	if h.connections == nil {
		h.mgmtError(c, http.StatusServiceUnavailable, "provider connections are not configured on this deployment")
		return
	}
	var req struct {
		// Enroll opts the imported leads INTO automation. Off by default: importing
		// months of history must not blast a welcome email to every stale lead. The FE
		// confirm dialog makes the choice explicit.
		Enroll bool `json:"enroll"`
	}
	_ = c.ShouldBindJSON(&req)

	// One backfill per source at a time. Dedupe already makes a concurrent second run
	// correct, but it would double the Graph calls for nothing.
	//
	// The map stores the run's CANCEL FUNC, not a bare `true`. An import runs detached
	// for up to backfillTimeout holding a *LeadSource and opened credentials captured
	// at request time, and it re-checks nothing — so deleting the workspace mid-import
	// could not stop it, and it would go on writing real people's details into a
	// workspace that no longer exists (mailing them too, with enrolment on). A handle
	// is the only thing that can stop it.
	ctx, cancel := context.WithTimeout(context.Background(), backfillTimeout)
	if _, running := h.backfillInFlight.LoadOrStore(src.ID, backfillRun{orgID: src.OrgID, cancel: cancel}); running {
		cancel()
		h.mgmtError(c, http.StatusConflict, "a backfill for this form is already running")
		return
	}

	go func() {
		defer h.backfillInFlight.Delete(src.ID)
		defer cancel()
		h.runBackfill(ctx, src, req.Enroll)
	}()

	c.JSON(http.StatusAccepted, gin.H{"data": gin.H{"status": "started", "enroll": req.Enroll}})
}

// runBackfill pages the form's history and imports each lead through the shared
// ingest pipeline. Runs on a detached context (never the request's) so a client
// hangup cannot tear it in half.
func (h *Handler) runBackfill(ctx context.Context, src *LeadSource, enroll bool) {
	conn, prov, creds, err := h.resolveBackfillConnection(ctx, src)
	if err != nil {
		h.logf("integrations: backfill could not resolve the connection", "source_id", src.ID.String(), "error", err)
		return
	}
	formID := connectionFormID(src, conn.Provider)
	if formID == "" {
		h.logf("integrations: backfill source has no form id", "source_id", src.ID.String())
		return
	}

	cursor := ""
	imported := 0
	pages := 0
	retries := 0
	completed := false
	for {
		if ctx.Err() != nil {
			// The run's own 15m deadline (or a cancel) elapsed. This is an incomplete
			// walk, reported as such below — not "finished".
			break
		}
		leads, next, ferr := prov.Backfill(ctx, conn, creds, formID, cursor)
		if ferr != nil {
			// A RETRYABLE error is not a reason to abandon the walk. This is the shape
			// that bit TikTok: its throttles arrive as HTTP 200 with a code, classified
			// inside the adapter AFTER the shared client's own retry has passed, and a
			// once-per-second poll against one advertiser is exactly what provokes an
			// advertiser-level QPS limit. Breaking here would drop the remaining
			// regions and log "finished". Re-issue the SAME cursor after a backoff,
			// bounded so a mislabelled-permanent error cannot spin until the deadline.
			if IsRetryableHTTP(ferr) && retries < backfillMaxRetries {
				retries++
				h.logf("integrations: backfill hit a retryable error, retrying", "source_id", src.ID.String(), "attempt", retries, "error", ferr)
				if !sleepBackfill(ctx, backfillPageDelay*time.Duration(retries)) {
					break
				}
				continue
			}
			// Permanent, or out of retries. Stop — but LOUDLY and distinctly from the
			// success line, because a re-run is the recovery and the admin has to know
			// one is needed. Dedupe makes a re-run safe: everything already imported is
			// skipped.
			if h.logger != nil {
				h.logger.Error("integrations: backfill stopped on a provider error", "source_id", src.ID.String(), "imported", imported, "error", ferr)
			}
			return
		}
		retries = 0
		for i := range leads {
			if h.importBackfillLead(ctx, src, conn, leads[i], enroll) {
				imported++
			}
		}
		if next == "" {
			completed = true
			break
		}

		// A POLL returns the SAME cursor it was handed (a provider waiting on an async
		// export task, e.g. TikTok). It must NOT count against the page budget: that
		// budget bounds how many PAGES of leads a runaway cursor can pull, and charging
		// polls against it means a slow export exhausts it before the leads arrive —
		// which is the "budget spent on polls, history dropped" defect. A page (a real
		// advance) counts; a poll only pays the delay and is bounded by the ctx
		// deadline. Facebook never returns the same cursor, so its behaviour is
		// unchanged.
		if next != cursor {
			pages++
			if pages >= backfillMaxPages {
				h.logf("integrations: backfill hit the page cap before completing", "source_id", src.ID.String(), "imported", imported)
				break
			}
		}
		cursor = next
		if !sleepBackfill(ctx, backfillPageDelay) {
			break
		}
	}
	// Info ONLY on a genuinely complete walk. Every other exit — the deadline, the
	// page cap, a cancel — is a partial import that a re-run should finish, and it must
	// not read as success.
	if completed {
		if h.logger != nil {
			h.logger.Info("integrations: backfill finished", "source_id", src.ID.String(), "imported", imported, "enroll", enroll)
		}
		return
	}
	if h.logger != nil {
		h.logger.Warn("integrations: backfill ended incomplete — run it again to finish", "source_id", src.ID.String(), "imported", imported)
	}
}

// sleepBackfill waits between steps, returning false if the run's context ended first.
func sleepBackfill(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// resolveBackfillConnection loads the source's connection, its provider adapter,
// and its decrypted credentials.
func (h *Handler) resolveBackfillConnection(ctx context.Context, src *LeadSource) (*IntegrationConnection, Provider, Credentials, error) {
	connID, err := h.repo.SourceConnectionID(ctx, src.OrgID, src.ID)
	if err != nil {
		return nil, nil, Credentials{}, err
	}
	if connID == uuid.Nil {
		return nil, nil, Credentials{}, domain.NewAppError(http.StatusBadRequest, "this form is not linked to a connection")
	}
	conn, err := h.repo.GetConnection(ctx, src.OrgID, connID)
	if err != nil {
		return nil, nil, Credentials{}, err
	}
	if conn == nil {
		return nil, nil, Credentials{}, domain.NewAppError(http.StatusBadRequest, "the connection was removed")
	}
	prov, ok := h.connections.registry.Get(conn.Provider)
	if !ok {
		return nil, nil, Credentials{}, domain.NewAppError(http.StatusBadRequest, "provider not available")
	}
	creds, err := h.connections.openCredentials(conn)
	if err != nil {
		return nil, nil, Credentials{}, err
	}
	return conn, prov, creds, nil
}

// importBackfillLead inserts one historical lead (connection-scoped-deduped) and,
// if it is genuinely new, runs it through the ingest core suppressed-by-default.
// Returns true when a NEW delivery was ingested (not a duplicate of one already
// received by webhook or a prior backfill).
func (h *Handler) importBackfillLead(ctx context.Context, src *LeadSource, conn *IntegrationConnection, lead RawLead, enroll bool) bool {
	if lead.ProviderEventID == "" {
		return false // no leadgen id ⇒ cannot dedupe ⇒ refuse rather than risk a dup
	}
	lead.DeliveryMode = DeliveryBackfill
	lead.EnrollAutomation = enroll

	raw, _ := json.Marshal(lead.Fields)
	ctxJSON, _ := json.Marshal(lead.Context)
	leadgenID := lead.ProviderEventID
	event := &IntegrationEvent{
		OrgID:           src.OrgID,
		SourceID:        &src.ID,
		ConnectionID:    &conn.ID,
		ProviderEventID: &leadgenID,
		// processing (not pending): backfill imports synchronously in this goroutine,
		// it is not queued for the async worker. Attempts=1 so a strand is reaped like
		// any other processing row.
		Status:     EventStatusProcessing,
		Attempts:   1,
		ClaimedAt:  ptrTime(time.Now()),
		RawPayload: datatypes.JSON(raw),
		Context:    datatypes.JSON(ctxJSON),
	}
	inserted, err := h.repo.InsertEventDeduped(ctx, event)
	if err != nil {
		h.logf("integrations: backfill could not insert delivery", "leadgen_id", leadgenID, "error", err)
		return false
	}
	if !inserted {
		// A prior delivery for this leadgen id already exists — a webhook lead, or a
		// prior backfill attempt. Exactly-once means: if it produced a record, skip.
		// But a prior BACKFILL attempt that FAILED before writing (a transient DB
		// error, a write-timeout under load) left a `failed` row with no record — and
		// backfill is these historical leads' ONLY entry (they predate the webhook
		// subscription), so a re-run MUST recover it. Re-run against the existing row,
		// mirroring the sync Ingest failed-row branch, rather than silently skipping.
		prior, ferr := h.repo.FindEventByProviderID(ctx, src.ID, leadgenID)
		if ferr != nil || prior == nil {
			// No row for THIS source (e.g. the lead was already handled by webhook, whose
			// row has a NULL source_id) — genuinely nothing to do.
			return false
		}
		if prior.ResultRecordID != nil {
			return false // already produced a record — never re-run (would duplicate)
		}
		if prior.Status != EventStatusFailed && prior.Status != EventStatusQuarantined {
			// processing/pending = in flight or another path owns it; do not race it.
			return false
		}
		event = prior
		event.Status = EventStatusProcessing
		event.Attempts = prior.Attempts + 1
		event.Error = ""
	}
	if _, ierr := h.ingest.IngestClaimed(ctx, src, lead, event); ierr != nil {
		h.logf("integrations: backfill ingest failed", "leadgen_id", leadgenID, "error", ierr)
		return false
	}
	return true
}

func (h *Handler) logf(msg string, args ...any) {
	if h.logger != nil {
		h.logger.Warn(msg, args...)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// connectionFormID reads the provider form id out of the source's config.
//
// Keyed by the PROVIDER, matching the namespace EnableForm writes and the one
// FindConnectionFormSource queries — `config -> <provider> ->> 'form_id'`. It read
// only `config.facebook.form_id` until a second adapter existed.
func connectionFormID(src *LeadSource, provider string) string {
	if len(src.Config) == 0 || provider == "" {
		return ""
	}
	var cfg map[string]struct {
		FormID string `json:"form_id"`
	}
	if json.Unmarshal(src.Config, &cfg) != nil {
		return ""
	}
	return cfg[provider].FormID
}

// backfillRun is a live import: which org it belongs to, and how to stop it.
type backfillRun struct {
	orgID  uuid.UUID
	cancel context.CancelFunc
}

// CancelBackfillsForOrg stops every in-flight import for an org and reports how many
// it stopped. Used by the workspace teardown — see PurgeService.
func (h *Handler) CancelBackfillsForOrg(orgID uuid.UUID) int {
	n := 0
	h.backfillInFlight.Range(func(_, v any) bool {
		if run, ok := v.(backfillRun); ok && run.orgID == orgID {
			run.cancel()
			n++
		}
		return true
	})
	return n
}
