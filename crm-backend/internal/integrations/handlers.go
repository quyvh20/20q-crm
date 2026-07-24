package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// captureBodyLimit caps an inbound lead body. A lead is a handful of form fields;
// anything larger is a mistake or an attack.
const captureBodyLimit = 256 * 1024

// MemberChecker resolves membership in an org. Narrow port: integrations needs only
// "may this person be handed a lead".
type MemberChecker interface {
	GetOrgUser(ctx context.Context, userID, orgID uuid.UUID) (*domain.OrgUser, error)
	// ActiveMemberIDs answers the same question for MANY users in one round trip —
	// owner routing checks a whole pool per lead, on the public capture path, where
	// N point lookups (each of which Preloads Role) would be the wrong shape.
	//
	// An error means UNKNOWN, never "nobody is active": callers must fail OPEN, or a
	// DB blip unowns real leads.
	ActiveMemberIDs(ctx context.Context, orgID uuid.UUID, userIDs []uuid.UUID) (map[uuid.UUID]bool, error)
}

// Handler serves the integrations module's own routes — both the capability-gated
// management API and the public capture endpoint.
type Handler struct {
	repo    *Repository
	ingest  *LeadIngestService
	authz   domain.RecordAuthorizer
	members MemberChecker
	schema  SchemaProvider
	limiter *RateLimiter
	// ipLimiter is a SEPARATE bucket with its own ceiling. Both are charged the full
	// batch cost, and shared-egress callers (a corporate NAT, Make/Zapier's outbound
	// IPs) need more headroom than a single credential does — so the difference is a
	// number someone chose, not one bought by undercharging.
	ipLimiter *RateLimiter
	// stages validates the deal option's stage at SAVE time. Required whenever that
	// option can be enabled — the ingest side re-checks too, but only save time can
	// tell the admin.
	stages StageReader
	// http is the outbound client for provider verification calls (Turnstile).
	// Nil-tolerant: httpClient() supplies a bounded default, and a test injects one.
	http *http.Client
	// connections gives the backfill action access to a provider connection's
	// decrypted credentials + adapter (L5.4). Nil-tolerant: the backfill route
	// answers 503 when provider connections are not wired.
	connections *ConnectionService
	// backfillInFlight guards against two concurrent backfills of the same source —
	// dedupe already makes a re-run correct, this just avoids doubling the Graph calls.
	backfillInFlight sync.Map
	// health announces status transitions. Nil-tolerant (every method no-ops on a nil
	// receiver): capture must keep working in a deployment without notifications.
	health *HealthReporter
	logger *slog.Logger
}

// WithHealthReporter wires health alerting after construction.
func (h *Handler) WithHealthReporter(r *HealthReporter) *Handler { h.health = r; return h }

// WithHTTPClient overrides the outbound client used for provider verification.
func (h *Handler) WithHTTPClient(c *http.Client) *Handler { h.http = c; return h }

// WithConnections wires the provider-connection service used by the backfill
// action. Set after both are built (main.go), so it is a setter rather than a
// constructor arg — keeping NewHandler's signature (and its many test call sites)
// unchanged.
func (h *Handler) WithConnections(cs *ConnectionService) *Handler { h.connections = cs; return h }

// NewHandler builds the handler. A nil authorizer panics rather than degrading:
// the source-save OLS re-check is the only thing stopping integrations.manage from
// becoming an org-wide write primitive, so it must not be optional.
func NewHandler(repo *Repository, ingest *LeadIngestService, authz domain.RecordAuthorizer, members MemberChecker, schema SchemaProvider, stages StageReader, limiter, ipLimiter *RateLimiter, logger *slog.Logger) *Handler {
	if authz == nil {
		panic("integrations: authorizer is required — the source-save OLS check is a security control")
	}
	if members == nil {
		panic("integrations: member checker is required — default_owner_id validation is a security control")
	}
	if ipLimiter == nil {
		ipLimiter = limiter
	}
	return &Handler{repo: repo, ingest: ingest, authz: authz, members: members, schema: schema, stages: stages, limiter: limiter, ipLimiter: ipLimiter, logger: logger}
}

// RegisterRoutes mounts the module's routes.
//
// `protected` is the FULL protected-group stack, taken as a slice rather than a
// single middleware on purpose. The automation precedent takes one gin.HandlerFunc
// and main.go hands it the plain session middleware — so automation's management
// routes reject PATs and skip the workspace 2FA policy. That signature makes the
// bug unfixable without changing it; a slice makes the correct stack one value
// main.go builds once and hands over whole.
func (h *Handler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	// Public: no auth middleware. Authenticates itself with the source credential.
	router.POST("/api/capture/leads", h.CaptureLead)
	// The recovery port: up to 100 leads, one result each.
	router.POST("/api/capture/leads/batch", h.CaptureLeadBatch)
	// Google Ads lead-form webhook (L3): the URL token names the source, the
	// google_key in the body corroborates it.
	router.POST("/api/capture/google-ads/:public_token", h.CaptureGoogleAds)
	// Form embeds (L4): posted by a visitor's BROWSER from the customer's own site.
	// Both routes carry formCORS, which is where the origin check, the rate-limit
	// charge and the CORS headers all live — main.go skips the global CORS handler
	// for this prefix, so nothing else supplies them.
	//
	// The OPTIONS route is not optional: Content-Type: application/json is not a
	// CORS-safelisted value, so every submit preflights, and gin answers an
	// unregistered OPTIONS with a bare 404 carrying no CORS headers — invisible to
	// curl and to same-origin tests, and a total failure in a real browser.
	router.POST(FormCapturePrefix+"/:public_token", h.formCORS, h.CaptureForm)
	router.OPTIONS(FormCapturePrefix+"/:public_token", h.formCORS, func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	g := router.Group("/api/integrations")
	g.Use(protected...)
	g.Use(requireCap(domain.CapIntegrationsManage))
	{
		g.GET("/sources", h.ListSources)
		g.POST("/sources", h.CreateSource)
		g.GET("/sources/:id", h.GetSource)
		g.PATCH("/sources/:id", h.UpdateSource)
		g.DELETE("/sources/:id", h.DeleteSource)
		g.POST("/sources/:id/rotate-key", h.RotateKey)
		g.POST("/sources/:id/rotate-google-key", h.RotateGoogleKey)
		// The per-source log. Left EXACTLY as it was, response shape included: the
		// frontend coerces an unexpected envelope to an empty array rather than
		// erroring, so changing this would render a full ledger as "no deliveries" for
		// anyone whose bundle and backend are one deploy apart. L6.2's filters live on
		// the org-wide route instead.
		g.GET("/sources/:id/events", h.ListEvents)
		g.GET("/sources/:id/mapping", h.GetMapping)
		// Per-day delivery counts for the source sparkline (L6.6).
		g.GET("/sources/:id/stats", h.SourceStats)
		g.POST("/sources/:id/test-lead", h.SendTestLead)
		// Import a facebook_form source's historical leads (L5.4).
		g.POST("/sources/:id/backfill", h.Backfill)
		// The org-wide ledger + per-delivery retry (L6.2).
		h.RegisterEventRoutes(g)
	}
}

// ── Public capture ───────────────────────────────────────────────────────────

type captureRequest struct {
	Fields  map[string]any `json:"fields"`
	Context map[string]any `json:"context"`
	Consent map[string]any `json:"consent"`
}

type captureResponse struct {
	RecordID string `json:"record_id"`
	Outcome  string `json:"outcome"`
	EventID  string `json:"event_id"`
	// Quarantined names keys the payload sent that were not written. Returned so an
	// integrator finds out at integration time, not from missing data weeks later.
	Quarantined []string `json:"quarantined,omitempty"`
	// Warnings say what the pipeline could not do, at integration time. This route
	// discarded IngestResult.Warnings entirely until now — so the unowned-lead warning
	// and every consent parse problem went nowhere on the endpoint carrying all the
	// traffic. A warning nobody receives is the same defect as not raising one.
	Warnings []string `json:"warnings,omitempty"`
	// ConsentRecorded reports whether a consent envelope was stored on this delivery.
	// Absent when none was sent.
	ConsentRecorded bool `json:"consent_recorded,omitempty"`
	// DealID is the opportunity this lead also produced, when the source asks for one.
	// Returned so a Make/Zapier scenario can chain onto the deal without a second
	// lookup. Absent when the source makes no deals, or when this lead matched an
	// existing contact (see the note on the delivery).
	DealID string `json:"deal_id,omitempty"`
}

// CaptureLead is the public port: POST /api/capture/leads, Bearer crm_lead_…
func (h *Handler) CaptureLead(c *gin.Context) {
	key := bearerToken(c)
	if !IsLeadKey(key) {
		h.captureError(c, http.StatusUnauthorized, "invalid capture key")
		return
	}
	hash := HashLeadKey(key)

	// Throttle BEFORE the DB probe. WebhookInbound looks up its token first, which
	// makes every unauthenticated request a free query — an invalid-key flood is
	// then a DB amplifier. The key hash is derivable without touching the DB, so
	// the limit can precede the lookup; the client IP is limited too, or an
	// attacker rotating random keys gets a fresh bucket per request.
	if !h.allow(c, "k:"+hash) || !h.allow(c, "ip:"+c.ClientIP()) {
		return // allow() has already written 429 + Retry-After
	}

	source, err := h.repo.FindSourceByTokenHash(c.Request.Context(), hash)
	if err != nil {
		h.logger.Error("integrations: source lookup failed", "error", err)
		h.captureError(c, http.StatusInternalServerError, "lookup failed")
		return
	}
	// One message for unknown/revoked/disabled alike: which of the three it is, is
	// not the caller's business.
	if source == nil || !source.IsLive() {
		h.captureError(c, http.StatusUnauthorized, "invalid capture key")
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, captureBodyLimit))
	if err != nil {
		h.captureError(c, http.StatusBadRequest, "could not read body")
		return
	}
	var req captureRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.captureError(c, http.StatusBadRequest, "body must be a JSON object")
		return
	}
	if len(req.Fields) == 0 {
		h.captureError(c, http.StatusUnprocessableEntity, "fields is required")
		return
	}

	// The daily cap is the backstop that survives both limiters being wrong, so it
	// fails CLOSED: a count we could not take is not evidence of headroom. Skipping
	// the cap on error would make a DB blip the one moment the backstop is absent.
	if source.DailyCap > 0 {
		n, err := h.repo.CountCreatedToday(c.Request.Context(), source.ID, time.Now())
		if err != nil {
			h.logger.Error("integrations: daily cap check failed", "error", err, "source_id", source.ID.String())
			c.Header("Retry-After", "60")
			h.captureError(c, http.StatusServiceUnavailable, "could not verify capture limit; retry")
			return
		}
		if n >= int64(source.DailyCap) {
			// Evidence first, THEN the refusal. google_ads and form_embed have always
			// stored a row here; this route did not, so a source that hit its cap went
			// silent in its own delivery log — the admin saw leads stop arriving and a
			// ledger that showed nothing at all, which reads as "the integrator stopped
			// sending" rather than "we refused them". The one question the ledger exists
			// to answer was the one it could not.
			//
			// The 429 is UNCHANGED and stays correct: unlike Google (which drops a 4xx
			// permanently, hence its accept-and-quarantine), a bearer-authenticated
			// integrator retries, and Retry-After tells them when. This row is the
			// record, not the recovery.
			h.ledgerCappedLead(c, source, req)
			c.Header("Retry-After", "3600")
			h.captureError(c, http.StatusTooManyRequests, "daily capture limit reached for this source")
			return
		}
	}

	lead := RawLead{
		Fields:  req.Fields,
		Context: req.Context,
		Consent: req.Consent,
		// Namespaced per source by the dedupe index. IsTest is NOT read from the
		// payload: a caller who could set it would be able to file real-looking
		// leads that never page sales.
		ProviderEventID: strings.TrimSpace(c.GetHeader("Idempotency-Key")),
	}

	res, err := h.ingest.Ingest(c.Request.Context(), source, lead)
	if err != nil {
		if appErr, ok := err.(*domain.AppError); ok {
			// Only the 5xx class counts, matching google.go's split: a payload-shaped
			// 4xx (a 422 on a lead missing an email, say) is permanent for THIS lead
			// and says nothing about the source, which may be fine for every other one.
			// The bearer key was verified above, so this is post-authentication and not
			// forgeable — the same bar google_key verification sets.
			if appErr.Code >= http.StatusInternalServerError {
				h.countSourceFailure(c.Request.Context(), source)
			}
			h.captureError(c, appErr.Code, appErr.Message)
			return
		}
		h.logger.Error("integrations: ingest failed", "error", err, "source_id", source.ID.String())
		h.countSourceFailure(c.Request.Context(), source)
		// 500 is safe to retry: the delivery is deduped by Idempotency-Key.
		h.captureError(c, http.StatusInternalServerError, "could not process lead")
		return
	}

	// Never report success without a record. A 200 carrying an empty record_id is
	// the response shape that turns any bug on this path into SILENT lead loss: the
	// integrator's Make/Zapier scenario marks the lead delivered and moves on, and
	// nobody finds out until someone asks where the leads went.
	if res.RecordID == uuid.Nil {
		h.logger.Error("integrations: ingest returned no record", "source_id", source.ID.String(), "event_id", res.EventID.String())
		h.countSourceFailure(c.Request.Context(), source)
		h.captureError(c, http.StatusInternalServerError, "lead was not written; retry")
		return
	}

	out := captureResponse{
		RecordID:        res.RecordID.String(),
		Outcome:         res.Outcome,
		EventID:         res.EventID.String(),
		Quarantined:     res.Quarantined,
		Warnings:        res.Warnings,
		ConsentRecorded: len(req.Consent) > 0 && !hasConsentFailure(res.Warnings),
	}
	if res.DealID != nil {
		out.DealID = res.DealID.String()
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// CaptureLeadBatch is the recovery port: POST /api/capture/leads/batch.
//
// Up to 100 leads, each with its own result, and a batch of N costs exactly what N
// single requests cost on every existing bound.
func (h *Handler) CaptureLeadBatch(c *gin.Context) {
	key := bearerToken(c)
	if !IsLeadKey(key) {
		h.captureError(c, http.StatusUnauthorized, "invalid capture key")
		return
	}
	hash := HashLeadKey(key)

	// STAGE ONE of the charge, before the DB is touched — byte-identical to the
	// single endpoint's reasoning. N is unknowable until the body is parsed, so
	// deferring the ENTIRE charge until after the lookup would hand back the
	// DB-amplifier hole that ordering exists to close.
	if !h.allowN(c, "k:"+hash, 1) || !h.allowN(c, "ip:"+c.ClientIP(), 1) {
		return
	}

	source, err := h.repo.FindSourceByTokenHash(c.Request.Context(), hash)
	if err != nil {
		h.logger.Error("integrations: source lookup failed", "error", err)
		h.captureError(c, http.StatusInternalServerError, "lookup failed")
		return
	}
	if source == nil || !source.IsLive() {
		h.captureError(c, http.StatusUnauthorized, "invalid capture key")
		return
	}

	// Read one byte past the cap so an oversized body is an explicit 413 rather than
	// surfacing as a confusing "body must be a JSON object" after silent truncation.
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, batchBodyLimit+1))
	if err != nil {
		h.captureError(c, http.StatusBadRequest, "could not read body")
		return
	}
	if len(body) > batchBodyLimit {
		h.captureError(c, http.StatusRequestEntityTooLarge, "batch body exceeds 1MB")
		return
	}
	if k := strings.TrimSpace(c.GetHeader("Idempotency-Key")); k != "" {
		// Rejected rather than ignored: silently dropping a safety header is how an
		// integrator believes their retry is safe when it is not, and finds out from
		// duplicates behind a green 200.
		h.captureError(c, http.StatusBadRequest,
			"Idempotency-Key is not honoured on the batch endpoint; give each item its own idempotency_key")
		return
	}
	var req batchRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.captureError(c, http.StatusBadRequest, "body must be a JSON object")
		return
	}
	if len(req.Items) == 0 {
		h.captureError(c, http.StatusUnprocessableEntity, "items is required")
		return
	}
	// Clamped by the limiter's own window: admitting a batch no window could ever
	// charge would guarantee a 429 after the caller had already paid to serialize it.
	maxItems := maxBatchItems
	if lim := h.limiter.Limit(); lim < maxItems {
		maxItems = lim
	}
	if len(req.Items) > maxItems {
		h.captureError(c, http.StatusUnprocessableEntity,
			"a batch may carry at most "+strconv.Itoa(maxItems)+" items (this request carried "+strconv.Itoa(len(req.Items))+")")
		return
	}

	// STAGE TWO: charge the remaining N-1. Over the limit refuses the WHOLE batch —
	// a prefix-accept splits on state the caller cannot see, so the same batch
	// retried would stop at a different place every time and no client could tell
	// where to resume.
	if rest := len(req.Items) - 1; rest > 0 {
		if !h.allowN(c, "k:"+hash, rest) || !h.allowN(c, "ip:"+c.ClientIP(), rest) {
			return
		}
	}

	h.runBatch(c, source, req.Items)
}

// runBatch processes items strictly in order.
//
// Sequential, not a worker pool, and that is load-bearing: item k must see item
// k-1's write, or two rows sharing an email create two contacts. The same ordering
// is what makes the per-item daily-cap headroom meaningful.
func (h *Handler) runBatch(c *gin.Context, source *LeadSource, items []batchItem) {
	batchID := newBatchID()
	results := prescan(items)
	deadline := time.Now().Add(batchBudget)

	enroll, err := h.repo.GetBatchEnrollAutomation(c.Request.Context(), source.OrgID, source.ID)
	if err != nil {
		// Unreadable config must not decide to page 100 reps. Fall back to the safe
		// value, which is the column's own default.
		h.logger.Error("integrations: could not read batch enrollment setting", "error", err, "source_id", source.ID.String())
		enroll = false
	}

	// Headroom is read ONCE up front and decremented as records are actually
	// created; a cap we could not read fails the whole batch closed, mirroring the
	// single endpoint ("a count we could not take is not evidence of headroom").
	remaining := int64(-1) // -1 = uncapped
	if source.DailyCap > 0 {
		used, cerr := h.repo.CountCreatedToday(c.Request.Context(), source.ID, time.Now())
		if cerr != nil {
			h.logger.Error("integrations: daily cap check failed", "error", cerr, "source_id", source.ID.String())
			c.Header("Retry-After", "60")
			h.captureError(c, http.StatusServiceUnavailable, "could not verify capture limit; retry")
			return
		}
		remaining = int64(source.DailyCap) - used
		if remaining <= 0 {
			c.Header("Retry-After", "3600")
			h.captureError(c, http.StatusTooManyRequests, "daily capture limit reached for this source")
			return
		}
	}

	var refused []*IntegrationEvent
	processed := 0

	for i := range items {
		if !pending(results[i]) {
			continue // prescan already decided this row
		}
		switch {
		case c.Request.Context().Err() != nil:
			refuse(&results[i], CodeClientDisconnect, "the connection closed before this item was attempted")
			refused = append(refused, refusedEvent(source, items[i], results[i], batchID))
			continue
		case time.Now().After(deadline):
			refuse(&results[i], CodeBatchDeadline, "this batch ran out of time before reaching this item; resend it")
			refused = append(refused, refusedEvent(source, items[i], results[i], batchID))
			continue
		case remaining == 0:
			refuse(&results[i], CodeDailyCapReached, "this source's daily capture limit was reached partway through the batch")
			refused = append(refused, refusedEvent(source, items[i], results[i], batchID))
			continue
		}

		// Re-read the cap periodically so concurrent batches converge on the truth
		// rather than each spending the same headroom.
		if remaining > 0 && processed > 0 && processed%capRecheckInterval == 0 {
			if used, cerr := h.repo.CountCreatedToday(c.Request.Context(), source.ID, time.Now()); cerr == nil {
				remaining = int64(source.DailyCap) - used
			} else {
				refuse(&results[i], CodeCapUnverified, "could not re-verify this source's daily limit; resend this item")
				refused = append(refused, refusedEvent(source, items[i], results[i], batchID))
				continue
			}
			if remaining <= 0 {
				refuse(&results[i], CodeDailyCapReached, "this source's daily capture limit was reached partway through the batch")
				refused = append(refused, refusedEvent(source, items[i], results[i], batchID))
				continue
			}
		}

		lead := RawLead{
			Fields:           items[i].Fields,
			Context:          batchContext(items[i].Context, batchID),
			Consent:          items[i].Consent,
			ProviderEventID:  results[i].IdempotencyKey,
			DeliveryMode:     DeliveryBatch,
			EnrollAutomation: enroll,
		}
		res, ierr := h.ingest.Ingest(c.Request.Context(), source, lead)
		processed++

		if ierr != nil {
			results[i].Status, results[i].Retryable = ItemError, true
			results[i].Code = CodeRejected
			results[i].Message = ierr.Error()
			if appErr, ok := ierr.(*domain.AppError); ok {
				// A validation refusal is the caller's to fix; retrying it unchanged
				// just fails again.
				results[i].Retryable = appErr.Code >= 500 || appErr.Code == http.StatusConflict
			}
			continue
		}
		applyResult(&results[i], res)
		if remaining > 0 && consumedHeadroom(res) {
			remaining--
		}
	}

	// Evidence for every refusal, in one insert. Best-effort: the caller already has
	// the truth in their envelope, so a bookkeeping failure must not fail the batch.
	if err := h.repo.InsertRefusedEvents(c.Request.Context(), refused); err != nil {
		h.logger.Error("integrations: could not record refused items", "error", err, "source_id", source.ID.String())
	}

	succeeded, failed := 0, 0
	for _, r := range results {
		if r.Status == ItemOK || r.Status == ItemDuplicate {
			succeeded++
		} else {
			failed++
		}
	}
	// Only stamp usage when the batch was clean: a run that failed 99 of 100 must not
	// reset this source's failure signal and report itself healthy.
	if failed == 0 {
		healed, err := h.repo.TouchSourceUsed(c.Request.Context(), source.ID)
		if err == nil && healed {
			h.health.SourceRecovered(source.OrgID, source.ID, source.Name, source.CreatedBy)
		}
	} else if succeeded == 0 {
		// A batch is ONE delivery envelope, so it counts as at most one failure — never
		// one per item. Per-item weighting would let a single authenticated request
		// cross the threshold on its own, on the endpoint documented as the recovery
		// path for exactly the rows that accumulate during an outage: the act of
		// recovering would flip the source it was recovering.
		h.countSourceFailure(c.Request.Context(), source)
	}

	status := http.StatusOK
	if failed > 0 {
		// 207, never a flat 200 over failures. This codebase already holds that a 200
		// carrying an empty record_id is "the response shape that turns any bug on this
		// path into SILENT lead loss" — a 2xx over 99 failures is that at scale. The
		// header exists because Make and Zapier sail past any 2xx without reading the
		// body.
		status = http.StatusMultiStatus
		c.Header("X-Lead-Batch-Failed", strconv.Itoa(failed))
	}
	c.JSON(status, gin.H{"data": batchResponse{
		BatchID:   batchID,
		Received:  len(items),
		Succeeded: succeeded,
		Failed:    failed,
		Results:   results,
	}})
}

// allowN applies one weighted rate-limit key, writing 429 + Retry-After when
// exceeded.
func (h *Handler) allowN(c *gin.Context, key string, cost int) bool {
	lim := h.limiter
	if strings.HasPrefix(key, "ip:") {
		lim = h.ipLimiter
	}
	ok, retry := lim.AllowN(c.Request.Context(), key, cost)
	if ok {
		return true
	}
	secs := int(retry.Seconds())
	if secs < 1 {
		secs = 1
	}
	c.Header("Retry-After", strconv.Itoa(secs))
	h.captureError(c, http.StatusTooManyRequests, "rate limit exceeded")
	return false
}

// allow applies one rate-limit key, writing 429 + Retry-After when exceeded.
func (h *Handler) allow(c *gin.Context, key string) bool {
	return h.allowN(c, key, 1)
}

// captureError writes the public endpoint's error envelope.
//
// A local shape rather than domain.Err: handleAppError is unexported in
// delivery/http and unreachable from here, and this endpoint's audience is curl,
// Make and Zapier — never the frontend's parseJsonSafe. It also must never share
// the package-level *AppError sentinels, since mutating one (e.g. RetryAfter)
// races across every request in the process.
func (h *Handler) captureError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

// hasConsentFailure reports whether consent was sent but could not be stored, so the
// response never claims a record that does not exist.
func hasConsentFailure(warnings []string) bool {
	for _, w := range warnings {
		if strings.Contains(w, "could not be recorded") || strings.Contains(w, "too large to store") ||
			strings.Contains(w, "could not be stored") {
			return true
		}
	}
	return false
}

func bearerToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// ── Management ───────────────────────────────────────────────────────────────

type sourceRequest struct {
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	TargetSlug     string  `json:"target_slug"`
	UpdatePolicy   string  `json:"update_policy"`
	DefaultOwnerID *string `json:"default_owner_id"`
	// DailyCap is a POINTER so an explicit 0 can CLEAR the cap. A plain int makes
	// "cleared" and "not mentioned" the same value, which is why this setting was
	// one-way until now.
	DailyCap    *int      `json:"daily_cap"`
	Status      *string   `json:"status"`
	FieldMap    *FieldMap `json:"field_map"`
	MatchFields []string  `json:"match_fields"`
	// OwnerPool is a POINTER so an explicit [] can clear a rotation. A plain slice
	// makes "cleared" and "not mentioned" the same value, which is why match_fields
	// above cannot be emptied once set.
	OwnerPool *[]string `json:"owner_pool"`
	// BatchEnrollAutomation was MISSING from this struct until now, which made the
	// toggle decorative in both directions: gin silently dropped the key the UI sent,
	// the setter had no callers, and viewOf never hydrated it, so a GET always said
	// false. Batch suppression was therefore permanently on and the documented opt-in
	// unreachable. A pointer, so absent means "leave it alone".
	BatchEnrollAutomation *bool `json:"batch_enroll_automation"`
	// Deal is a POINTER for the same tri-state reason as OwnerPool: absent leaves the
	// setting alone, present replaces it wholesale.
	Deal *dealConfigRequest `json:"deal"`
	// Form is the form_embed definition. Same tri-state rule.
	Form *FormConfig `json:"form"`
	// AllowedOrigins is the browser origin allowlist. A POINTER so an explicit []
	// can clear it (which denies every browser) and an absent key leaves it alone.
	AllowedOrigins *[]string `json:"allowed_origins"`
	// TurnstileSecret is WRITE-ONLY: it is never returned by any endpoint, so the
	// only way it can be set is here, and an empty string clears it.
	TurnstileSecret *string `json:"turnstile_secret"`
}

// dealConfigRequest is the wire shape of the "also create a deal" option.
type dealConfigRequest struct {
	Enabled      bool    `json:"enabled"`
	StageID      *string `json:"stage_id"`
	NameTemplate string  `json:"name_template"`
}

// parseDealConfig validates the deal option and returns what to store.
//
// Fails CLOSED, the opposite polarity to ingest — the rule parseOwnerPool states:
// here an admin is watching and can retry, so a bad stage or a misspelled token is
// worth a 400; at ingest, refusing would cost a lead.
func (h *Handler) parseDealConfig(c *gin.Context, orgID uuid.UUID, targetSlug string, req dealConfigRequest) (DealConfig, bool) {
	if !req.Enabled {
		// Storing the disabled shape rather than deleting the key keeps the admin's
		// stage and template so re-enabling does not make them retype it.
		cfg := DealConfig{NameTemplate: strings.TrimSpace(req.NameTemplate)}
		if req.StageID != nil {
			if id, err := uuid.Parse(strings.TrimSpace(*req.StageID)); err == nil {
				cfg.StageID = &id
			}
		}
		return cfg, true
	}

	// Enabling this makes the source write DEALS, callerless, forever. Ingest never
	// runs OLS, so without this check `integrations.manage` alone would become a
	// standing permission to create records on an object the admin may not be allowed
	// to touch — the exact hole the target_slug re-check exists to close, one object
	// over.
	if !h.authorizeTarget(c, orgID, dealSlug) {
		return DealConfig{}, false
	}
	// A deal inherits the contact's rep, so configuring it is an ownership write too.
	if !h.authorizeOwnerWrite(c, orgID, dealSlug) {
		return DealConfig{}, false
	}

	if req.StageID == nil || strings.TrimSpace(*req.StageID) == "" {
		h.mgmtError(c, http.StatusBadRequest, "choose the stage new deals should start in")
		return DealConfig{}, false
	}
	stageID, err := uuid.Parse(strings.TrimSpace(*req.StageID))
	if err != nil {
		h.mgmtError(c, http.StatusBadRequest, "that stage id is not valid")
		return DealConfig{}, false
	}
	stage, ok := h.liveStage(c, orgID, stageID)
	if !ok {
		return DealConfig{}, false
	}
	// A won/lost stage is refused rather than allowed-with-a-warning because deal
	// creation does NOT derive is_won/is_lost/closed_at — only ChangeStage does — so
	// a deal created into "Closed Won" sits in the won column reporting is_won=false.
	// Wrong in the board and wrong in the forecast, in opposite directions.
	if stage.IsWon || stage.IsLost {
		h.mgmtError(c, http.StatusBadRequest, "new deals cannot start in a won or lost stage")
		return DealConfig{}, false
	}

	tpl := strings.TrimSpace(req.NameTemplate)
	if tpl == "" {
		tpl = DefaultDealNameTemplate
	}
	if err := ValidateDealNameTemplate(tpl); err != nil {
		h.mgmtError(c, http.StatusBadRequest, err.Error())
		return DealConfig{}, false
	}
	_ = targetSlug // the deal is a second write beside the target, never the target itself
	return DealConfig{Enabled: true, StageID: &stageID, NameTemplate: tpl}, true
}

// liveStage resolves a stage id within the org, rejecting one that is gone.
func (h *Handler) liveStage(c *gin.Context, orgID, stageID uuid.UUID) (*domain.PipelineStage, bool) {
	if h.stages == nil {
		h.mgmtError(c, http.StatusInternalServerError, "the pipeline is unavailable")
		return nil, false
	}
	stages, err := h.stages.List(c.Request.Context(), orgID)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not read the pipeline")
		return nil, false
	}
	for i := range stages {
		if stages[i].ID == stageID {
			return &stages[i], true
		}
	}
	h.mgmtError(c, http.StatusBadRequest, "that stage no longer exists")
	return nil, false
}

// sourceView is a LeadSource plus the routing config that deliberately does not
// live on the model (see the note on LeadSource). The API shape stays stable; the
// storage decision stays invisible to the frontend.
type sourceView struct {
	*LeadSource
	OwnerPool []string `json:"owner_pool"`
	// OwnerPoolInactive names pool members who can no longer receive leads, computed
	// server-side. The UI must not derive this by intersecting with a member list: a
	// failed member fetch would then badge a healthy rotation as dead.
	OwnerPoolInactive []string `json:"owner_pool_inactive,omitempty"`
	// DealStageMissing reports that the configured deal stage has been deleted since
	// it was chosen. Server-computed for the same reason as OwnerPoolInactive: a UI
	// that derived it from a stage list would badge a healthy source dead whenever
	// that fetch failed. Deals still land (in the first stage) — this is the badge
	// that stops that from being a silent re-filing.
	DealStageMissing bool `json:"deal_stage_missing,omitempty"`
	// TurnstileConfigured reports whether a bot-check secret is set, WITHOUT
	// revealing it. The secret goes to Cloudflare in plaintext so it cannot be
	// hashed; never returning it is the whole of its protection.
	TurnstileConfigured bool `json:"turnstile_configured,omitempty"`
}

// viewOf decorates a source with its rotation. Best-effort: routing config that
// cannot be read must not deny the whole management screen.
func (h *Handler) viewOf(c *gin.Context, src *LeadSource, withLiveness bool) sourceView {
	v := sourceView{LeadSource: src, OwnerPool: []string{}}
	// Hydrated here because the column is unmapped. Omitting this is what made the
	// toggle read as permanently off no matter what was stored.
	if enroll, err := h.repo.GetBatchEnrollAutomation(c.Request.Context(), src.OrgID, src.ID); err == nil {
		v.BatchEnrollAutomation = enroll
	}
	// Same rule for the URL token — without this, the setup panel would render a
	// webhook URL (or a form snippet) with a blank where the token goes.
	if (src.Kind == KindGoogleAds || src.Kind == KindFormEmbed) && src.PublicToken == "" {
		if tok, err := h.repo.GetPublicToken(c.Request.Context(), src.OrgID, src.ID); err == nil {
			src.PublicToken = tok
		}
	}
	if src.Kind == KindFormEmbed {
		if origins, err := h.repo.GetAllowedOrigins(c.Request.Context(), src.OrgID, src.ID); err == nil {
			src.AllowedOrigins = origins
		}
		// Whether a bot check is configured, never the key itself — it is sent
		// verbatim to Cloudflare, so it cannot be hashed, and "never serialize it" is
		// the only protection it has.
		if on, err := h.repo.HasTurnstileSecret(c.Request.Context(), src.OrgID, src.ID); err == nil {
			v.TurnstileConfigured = on
		}
	}
	v.DealStageMissing = h.dealStageMissing(c, src, withLiveness)
	raw, err := h.repo.GetOwnerPool(c.Request.Context(), src.OrgID, src.ID)
	if err != nil {
		h.logger.Error("integrations: could not read owner pool", "error", err, "source_id", src.ID.String())
		return v
	}
	ids := parsePoolUUIDs(raw)
	for _, id := range ids {
		v.OwnerPool = append(v.OwnerPool, id.String())
	}
	if !withLiveness || len(ids) == 0 {
		return v
	}
	live, err := h.members.ActiveMemberIDs(c.Request.Context(), src.OrgID, ids)
	if err != nil {
		return v // unknown, not "all dead" — never badge on a failed lookup
	}
	for _, id := range ids {
		if !live[id] {
			v.OwnerPoolInactive = append(v.OwnerPoolInactive, id.String())
		}
	}
	return v
}

// dealStageMissing reports whether this source's configured deal stage is gone.
//
// Never badges on a failed lookup — the owner_pool_inactive rule, one setting over:
// a stage list we could not read is not evidence the stage was deleted, and telling
// an admin their pipeline is broken because of a DB blip sends them to fix a
// healthy source.
func (h *Handler) dealStageMissing(c *gin.Context, src *LeadSource, withLiveness bool) bool {
	if !withLiveness || h.stages == nil {
		return false
	}
	cfg := ParseDealConfig(src.Config)
	if !cfg.Enabled || cfg.StageID == nil {
		return false
	}
	stages, err := h.stages.List(c.Request.Context(), src.OrgID)
	if err != nil {
		return false
	}
	for i := range stages {
		if stages[i].ID == *cfg.StageID {
			return false
		}
	}
	return true
}

// mappingView is everything the mapping UI needs, in one call: what this source
// has actually SENT, what it can be mapped INTO, and the current map.
type mappingView struct {
	// Observed are the real keys seen in this source's recent deliveries. An admin
	// maps from this list rather than recalling what their provider calls a field.
	Observed []string `json:"observed"`
	// TargetFields are the keys a lead may be written into (the allowlist — so
	// ownership and relations never appear as options).
	TargetFields []mappingTarget `json:"target_fields"`
	FieldMap     FieldMap        `json:"field_map"`
}

type mappingTarget struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

type sourceWithKey struct {
	sourceView
	// PlaintextKey is populated exactly once, at creation or rotation.
	PlaintextKey string `json:"plaintext_key,omitempty"`
	// GoogleKey is the second one-time secret a google_ads source carries — the
	// value the advertiser pastes beside the webhook URL. Shown at creation and
	// google-key rotation only; the bearer PlaintextKey above still exists for
	// these sources because the batch recovery endpoint authenticates with it.
	GoogleKey string `json:"google_key,omitempty"`
}

// ownerString renders an optional owner id for a response.
func ownerString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// orEmpty lets a nil pointer-slice be measured without a branch at each call site.
func orEmpty(p *[]string) *[]string {
	if p == nil {
		return &[]string{}
	}
	return p
}

// validateSeedFieldMap runs a kind's seeded map through the same validation an
// admin-saved one gets. Returns an error rather than writing a response shape —
// the caller decides how to say "our own seed is broken".
func (h *Handler) validateSeedFieldMap(c *gin.Context, orgID uuid.UUID, targetSlug string, m FieldMap) error {
	allow, err := BuildAllowlist(c.Request.Context(), h.schema, orgID, targetSlug)
	if err != nil {
		return err
	}
	if problems := ValidateFieldMap(m, allow); len(problems) > 0 {
		keys := make([]string, 0, len(problems))
		for k, v := range problems {
			keys = append(keys, k+": "+v)
		}
		sort.Strings(keys)
		return domain.NewAppError(http.StatusInternalServerError, "seed map invalid: "+strings.Join(keys, "; "))
	}
	return nil
}

// CreateSource mints a source and returns its key once.
func (h *Handler) CreateSource(c *gin.Context) {
	orgID, userID, ok := h.actor(c)
	if !ok {
		return
	}
	var req sourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.mgmtError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		h.mgmtError(c, http.StatusBadRequest, "name is required")
		return
	}
	if req.Kind == "" {
		req.Kind = KindAPI
	}
	if !IsValidKind(req.Kind) {
		h.mgmtError(c, http.StatusBadRequest, "unknown source kind: "+req.Kind)
		return
	}
	// EVERY keyless kind, not an enumeration of two. A keyless source is credentialed
	// by something other than a bearer key, so minting one here would hand it a second
	// capture-API ingress that FindSourceByTokenHash — which has no kind filter —
	// happily authenticates, on a source whose UI hides the key and whose Rotate button
	// is refused. The literal-by-literal version of this guard missed tiktok_form the
	// day it was added, which is why it is a predicate now.
	if IsKeylessKind(req.Kind) && req.Kind != KindWebhookInbound {
		h.mgmtError(c, http.StatusBadRequest, "forms are added from their connection, not here")
		return
	}
	if req.Kind == KindWebhookInbound {
		// There is exactly ONE of these per org and the system creates it, because its
		// credential is the org's automation webhook token — a second row would be a
		// source with no way to authenticate anything.
		h.mgmtError(c, http.StatusBadRequest, "the workflow webhook source is created automatically, not here")
		return
	}
	if req.TargetSlug == "" {
		req.TargetSlug = "contact"
	}
	if !IsSupportedTarget(req.TargetSlug) {
		// Reject at CONFIGURATION time, not at ingest. Accepting a source that can
		// never work means the admin's leads 4xx silently at 3am with no UI feedback,
		// and the ingest-side guard exists only as defence in depth against a row
		// edited by hand.
		h.mgmtError(c, http.StatusBadRequest, "unsupported target object: "+req.TargetSlug+" (only contact is supported)")
		return
	}
	if req.UpdatePolicy == "" {
		req.UpdatePolicy = UpdatePolicyFillBlankOnly
	}
	if !IsValidUpdatePolicy(req.UpdatePolicy) {
		h.mgmtError(c, http.StatusBadRequest, "unknown update policy: "+req.UpdatePolicy)
		return
	}
	if !h.authorizeTarget(c, orgID, req.TargetSlug) {
		return
	}

	owner, ok := h.parseOwner(c, orgID, req.DefaultOwnerID)
	if !ok {
		return
	}
	var pool datatypes.JSON = datatypes.JSON(`[]`)
	if req.OwnerPool != nil {
		if pool, ok = h.parseOwnerPool(c, orgID, *req.OwnerPool); !ok {
			return
		}
	}
	// Configuring who owns captured records is an ownership write, so it needs the
	// caller's own permission to make one — ingest writes callerless and checks nothing.
	if (owner != nil || len(*orEmpty(req.OwnerPool)) > 0) && !h.authorizeOwnerWrite(c, orgID, req.TargetSlug) {
		return
	}

	matchFields := req.MatchFields
	if len(matchFields) == 0 {
		matchFields = []string{MatchEmail}
		// google_ads defaults to email+phone: phone-only lead forms are common, and
		// under email-only matching requiresEmail would 422 every such lead —
		// permanently, because Google never retries a 4xx. Phone matching is the L2
		// machinery built for exactly this provider shape. An explicit request
		// still wins.
		if req.Kind == KindGoogleAds {
			matchFields = []string{MatchEmail, MatchPhone}
		}
	}
	if err := ValidateMatchFields(matchFields); err != nil {
		h.mgmtError(c, http.StatusBadRequest, err.Error())
		return
	}
	matchFieldsJSON, _ := json.Marshal(matchFields)

	// Resolved BEFORE the key is minted so a rejected deal option cannot leave a
	// source (and a live credential) behind that the admin did not mean to create.
	dealCfg := DealConfig{}
	if req.Deal != nil {
		if dealCfg, ok = h.parseDealConfig(c, orgID, req.TargetSlug, *req.Deal); !ok {
			return
		}
	}
	configJSON, cerr := MergeDealConfig(datatypes.JSON(`{}`), dealCfg)
	if cerr != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not save the deal option")
		return
	}

	plaintext, hash, prefix, err := GenerateLeadKey()
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not mint key")
		return
	}
	// A google_ads source starts with Google's standard columns pre-mapped, so the
	// advertiser's first test exercises a real mapping instead of the identity
	// passthrough. Validated like any admin-saved map — a seed that broke the rules
	// would be a bug worth failing loudly on, not shipping.
	fieldMapJSON := datatypes.JSON(`{}`)
	if req.Kind == KindGoogleAds {
		seed := googleSeedFieldMap()
		if err := h.validateSeedFieldMap(c, orgID, req.TargetSlug, seed); err != nil {
			h.logger.Error("integrations: google seed field map invalid", "error", err)
			h.mgmtError(c, http.StatusInternalServerError, "could not seed the field mapping")
			return
		}
		raw, _ := json.Marshal(seed)
		fieldMapJSON = datatypes.JSON(raw)
	}
	src := &LeadSource{
		OrgID:        orgID,
		Kind:         req.Kind,
		Name:         strings.TrimSpace(req.Name),
		TokenHash:    hash,
		TokenPrefix:  prefix,
		TargetSlug:   req.TargetSlug,
		UpdatePolicy: req.UpdatePolicy,
		// Set explicitly: GORM sends these columns on INSERT, so leaving them nil
		// persists NULL/[] and silently defeats the column DEFAULT.
		MatchFields:    matchFieldsJSON,
		FieldMap:       fieldMapJSON,
		Config:         configJSON,
		DefaultOwnerID: owner,
		DailyCap:       defaultDailyCap,
		Status:         SourceStatusActive,
	}
	if req.DailyCap != nil && *req.DailyCap >= 0 {
		src.DailyCap = *req.DailyCap
	}
	if userID != uuid.Nil {
		src.CreatedBy = &userID
	}
	if err := h.repo.CreateSource(c.Request.Context(), src); err != nil {
		h.logger.Error("integrations: create source failed", "error", err)
		h.mgmtError(c, http.StatusInternalServerError, "could not create source")
		return
	}
	// The rotation is written separately — it is not a column on the model, and a
	// failure here must not orphan the source that was just created with its key.
	if err := h.repo.SetOwnerPool(c.Request.Context(), orgID, src.ID, pool); err != nil {
		h.logger.Error("integrations: could not save the rotation", "error", err, "source_id", src.ID.String())
	}
	// The google_ads credentials are written separately too (unmapped columns) —
	// but UNLIKE the rotation, a failure here is fatal: a google_ads source with no
	// public_token is a webhook nobody can ever call, handed to the admin with a
	// green 201. Delete the half-made source and say so.
	// A form embed with no token is a form nobody can ever post to — the same fatal
	// shape as a google_ads source with no webhook URL, so the same handling.
	if req.Kind == KindFormEmbed {
		pub, perr := GeneratePublicToken()
		if perr == nil {
			perr = h.repo.SetPublicToken(c.Request.Context(), orgID, src.ID, pub)
		}
		if perr != nil {
			h.logger.Error("integrations: could not mint form token", "error", perr, "source_id", src.ID.String())
			_ = h.repo.SoftDeleteSource(c.Request.Context(), orgID, src.ID)
			h.mgmtError(c, http.StatusInternalServerError, "could not create source")
			return
		}
		src.PublicToken = pub
		// The allowlist starts EMPTY, which denies every browser origin. That is the
		// deliberate default: a form that accepted any site the moment it was created
		// would be the fail-open this feature most easily produces. The UI shows the
		// source as not-ready until an origin is added.
	}

	googleKey := ""
	if req.Kind == KindGoogleAds {
		pub, perr := GeneratePublicToken()
		gk, ghash, kerr := GenerateGoogleKey()
		if perr == nil && kerr == nil {
			perr = h.repo.SetGoogleCredentials(c.Request.Context(), orgID, src.ID, pub, ghash)
		}
		if perr != nil || kerr != nil {
			h.logger.Error("integrations: could not mint google credentials", "error", errors.Join(perr, kerr), "source_id", src.ID.String())
			_ = h.repo.SoftDeleteSource(c.Request.Context(), orgID, src.ID)
			h.mgmtError(c, http.StatusInternalServerError, "could not create source")
			return
		}
		src.PublicToken = pub
		googleKey = gk
	}
	c.JSON(http.StatusCreated, gin.H{"data": sourceWithKey{
		sourceView:   h.viewOf(c, src, false),
		PlaintextKey: plaintext,
		GoogleKey:    googleKey,
	}})
}

// ListSources returns the org's sources.
func (h *Handler) ListSources(c *gin.Context) {
	orgID, _, ok := h.actor(c)
	if !ok {
		return
	}
	out, err := h.repo.ListSources(c.Request.Context(), orgID)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not list sources")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// GetSource returns one source, including its rotation and which of its members
// can no longer receive leads.
func (h *Handler) GetSource(c *gin.Context) {
	src, ok := h.loadSource(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": h.viewOf(c, src, true)})
}

// UpdateSource edits a source's mutable config.
func (h *Handler) UpdateSource(c *gin.Context) {
	src, ok := h.loadSource(c)
	if !ok {
		return
	}
	var req sourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.mgmtError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		src.Name = strings.TrimSpace(req.Name)
	}
	if req.UpdatePolicy != "" {
		if !IsValidUpdatePolicy(req.UpdatePolicy) {
			h.mgmtError(c, http.StatusBadRequest, "unknown update policy: "+req.UpdatePolicy)
			return
		}
		src.UpdatePolicy = req.UpdatePolicy
	}
	if req.TargetSlug != "" && req.TargetSlug != src.TargetSlug {
		if !IsSupportedTarget(req.TargetSlug) {
			h.mgmtError(c, http.StatusBadRequest, "unsupported target object: "+req.TargetSlug+" (only contact is supported)")
			return
		}
		// Re-authorize on every target change, not just at create: otherwise a source
		// created against a permitted object could be repointed at a forbidden one.
		if !h.authorizeTarget(c, src.OrgID, req.TargetSlug) {
			return
		}
		src.TargetSlug = req.TargetSlug
	}
	if req.DefaultOwnerID != nil {
		if !h.authorizeOwnerWrite(c, src.OrgID, src.TargetSlug) {
			return
		}
		owner, ok := h.parseOwner(c, src.OrgID, req.DefaultOwnerID)
		if !ok {
			return
		}
		src.DefaultOwnerID = owner
	}
	// Resolved before the model save so a rejected rotation does not leave the other
	// edits half-applied.
	var newPool *datatypes.JSON
	if req.OwnerPool != nil {
		if !h.authorizeOwnerWrite(c, src.OrgID, src.TargetSlug) {
			return
		}
		pool, ok := h.parseOwnerPool(c, src.OrgID, *req.OwnerPool)
		if !ok {
			return
		}
		newPool = &pool
	}
	if req.DailyCap != nil && *req.DailyCap >= 0 {
		src.DailyCap = *req.DailyCap
	}
	// Resolved before the save, applied after it — the same shape as the rotation,
	// and for the same reason: config lives outside the model's Save (see
	// UpdateSource in the repository) so a rejected option must not half-apply.
	var newDeal *DealConfig
	if req.Deal != nil {
		cfg, ok := h.parseDealConfig(c, src.OrgID, src.TargetSlug, *req.Deal)
		if !ok {
			return
		}
		newDeal = &cfg
	}
	// Resolved before the save, applied after it — the same shape as the rotation
	// and the deal option, and for the same reason: these live outside the model's
	// Save, so a rejected one must not leave the rest half-applied.
	var newForm *FormConfig
	if req.Form != nil {
		if err := ValidateFormConfig(*req.Form); err != nil {
			h.mgmtError(c, http.StatusBadRequest, err.Error())
			return
		}
		newForm = req.Form
	}
	var newOrigins *[]string
	if req.AllowedOrigins != nil {
		origins, err := ValidateAllowedOrigins(*req.AllowedOrigins)
		if err != nil {
			h.mgmtError(c, http.StatusBadRequest, err.Error())
			return
		}
		newOrigins = &origins
	}
	if req.FieldMap != nil {
		if !h.applyFieldMapUpdate(c, src, *req.FieldMap) {
			return
		}
	}
	if len(req.MatchFields) > 0 {
		if err := ValidateMatchFields(req.MatchFields); err != nil {
			h.mgmtError(c, http.StatusBadRequest, err.Error())
			return
		}
		raw, _ := json.Marshal(req.MatchFields)
		src.MatchFields = datatypes.JSON(raw)
	}
	if src.Kind == KindWebhookInbound &&
		(req.DailyCap != nil || req.UpdatePolicy != "" || req.Deal != nil ||
			req.FieldMap != nil || len(req.MatchFields) > 0) {
		// The legacy write path reads NONE of these: it has its own upsert, its own
		// email match, its own overwrite semantics, and no cap. Accepting them would
		// store a setting the product then ignores — the same "switch that silently
		// does nothing" the status and delete guards exist to prevent, and worse here
		// because these ones look like they took effect.
		//
		// default_owner_id and owner_pool are deliberately NOT in this list: routing is
		// the one platform capability the legacy path really does honour.
		h.mgmtError(c, http.StatusBadRequest, "the workflow webhook source routes leads to an owner; its mapping, cap and update policy are handled by the webhook itself and cannot be set here")
		return
	}
	if req.Status != nil && src.Kind == KindWebhookInbound {
		// Same reason as the delete guard: disabling this row would not refuse a single
		// delivery, because the endpoint authenticates on the org token and never
		// consults it. A switch that silently does nothing is worse than no switch.
		h.mgmtError(c, http.StatusBadRequest, "this source mirrors the workflow webhook and cannot be disabled here — rotate its secret in the workflow builder to stop accepting deliveries")
		return
	}
	if req.Status != nil {
		switch *req.Status {
		case SourceStatusActive:
			// Mutating the struct keeps the response honest; the actual write is the
			// targeted SetSourceStatus below — Save omits these columns because the
			// capture path writes them concurrently (see UpdateSource in the repo).
			src.Status = SourceStatusActive
			src.DisabledAt = nil
			src.ConsecutiveFailures = 0
		case SourceStatusDisabled:
			now := time.Now()
			src.Status = SourceStatusDisabled
			src.DisabledAt = &now
		default:
			h.mgmtError(c, http.StatusBadRequest, "status must be active or disabled")
			return
		}
	}
	if err := h.repo.UpdateSource(c.Request.Context(), src); err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not update source")
		return
	}
	if req.Status != nil {
		if err := h.repo.SetSourceStatus(c.Request.Context(), src.OrgID, src.ID, src.Status); err != nil {
			h.mgmtError(c, http.StatusInternalServerError, "could not update source status")
			return
		}
	}
	if newPool != nil {
		if err := h.repo.SetOwnerPool(c.Request.Context(), src.OrgID, src.ID, *newPool); err != nil {
			h.mgmtError(c, http.StatusInternalServerError, "could not save the rotation")
			return
		}
	}
	if newDeal != nil {
		if err := h.repo.SetDealConfig(c.Request.Context(), src.OrgID, src.ID, *newDeal); err != nil {
			h.mgmtError(c, http.StatusInternalServerError, "could not save the deal option")
			return
		}
		if merged, err := MergeDealConfig(src.Config, *newDeal); err == nil {
			src.Config = merged // so the response shows what was just stored
		}
	}
	if req.BatchEnrollAutomation != nil {
		if err := h.repo.SetBatchEnrollAutomation(c.Request.Context(), src.OrgID, src.ID, *req.BatchEnrollAutomation); err != nil {
			h.mgmtError(c, http.StatusInternalServerError, "could not save the bulk-delivery setting")
			return
		}
	}
	if newForm != nil {
		if err := h.repo.SetFormConfig(c.Request.Context(), src.OrgID, src.ID, *newForm); err != nil {
			h.mgmtError(c, http.StatusInternalServerError, "could not save the form")
			return
		}
		if merged, err := MergeFormConfig(src.Config, *newForm); err == nil {
			src.Config = merged
		}
	}
	if newOrigins != nil {
		if err := h.repo.SetAllowedOrigins(c.Request.Context(), src.OrgID, src.ID, *newOrigins); err != nil {
			h.mgmtError(c, http.StatusInternalServerError, "could not save the allowed websites")
			return
		}
		src.AllowedOrigins = *newOrigins
	}
	if req.TurnstileSecret != nil {
		if err := h.repo.SetTurnstileSecret(c.Request.Context(), src.OrgID, src.ID, *req.TurnstileSecret); err != nil {
			h.mgmtError(c, http.StatusInternalServerError, "could not save the bot-check key")
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": h.viewOf(c, src, true)})
}

// DeleteSource retires a source (soft — the ledger outlives it).
func (h *Handler) DeleteSource(c *gin.Context) {
	src, ok := h.loadSource(c)
	if !ok {
		return
	}
	if src.Kind == KindWebhookInbound {
		// Refused, and the alternative is worse than a missing button. This row is a
		// VIEW onto an endpoint that lives elsewhere: the URL and secret belong to the
		// org's automation webhook token, so deleting the row stops none of the traffic
		// it describes — the next delivery would simply recreate it, and an admin who
		// "deleted" their webhook would watch it come back. Turn the endpoint off where
		// it actually lives, by rotating the secret in the workflow builder.
		h.mgmtError(c, http.StatusBadRequest, "this source mirrors the workflow webhook and cannot be deleted here — rotate its secret in the workflow builder to stop accepting deliveries")
		return
	}
	if err := h.repo.SoftDeleteSource(c.Request.Context(), src.OrgID, src.ID); err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not delete source")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
}

// RotateKey mints a new credential, invalidating the old one immediately.
func (h *Handler) RotateKey(c *gin.Context) {
	src, ok := h.loadSource(c)
	if !ok {
		return
	}
	// A facebook_form source has NO bearer key — its credential is the connection,
	// and it is resolved only by (connection_id, form_id). Minting a token_hash for
	// it would give it a second, unintended capture-API ingress (FindSourceByToken
	// Hash has no kind filter). Reject, symmetric to RotateGoogleKey's kind guard.
	// One predicate, two messages. The hazard is identical for both kinds — minting a
	// token_hash opens a capture-API ingress that bypasses the credential the source
	// actually authenticates with, because FindSourceByTokenHash has no kind filter —
	// but the remedy differs, so the copy has to.
	if IsKeylessKind(src.Kind) {
		remedy := "its credential is the connection it was enabled from"
		switch src.Kind {
		case KindFacebookForm:
			remedy = "its credential is the Facebook connection"
		case KindTikTokForm:
			remedy = "its credential is the TikTok connection"
		case KindWebhookInbound:
			remedy = "its secret is the workflow webhook signing secret, rotated in the workflow builder"
		}
		h.mgmtError(c, http.StatusBadRequest, "this source has no bearer key to rotate — "+remedy)
		return
	}
	plaintext, hash, prefix, err := GenerateLeadKey()
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not mint key")
		return
	}
	src.TokenHash = hash
	src.TokenPrefix = prefix
	if err := h.repo.UpdateSource(c.Request.Context(), src); err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not rotate key")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": sourceWithKey{
		sourceView:   h.viewOf(c, src, false),
		PlaintextKey: plaintext,
	}})
}

// RotateGoogleKey mints a new google_key for a google_ads source, invalidating
// the old one immediately. The public_token — the pasted URL — deliberately does
// NOT rotate with it: the point of a key rotation is that the advertiser swaps
// one field in Google's editor, not two.
//
// The loss window is real and stated to the admin by the UI: every real lead
// arriving between "rotate here" and "paste there" is 401d, and Google never
// retries a 4xx. Those leads land as failed ledger rows (the mismatch handler
// writes them), replayable through the batch endpoint once the new key is in.
func (h *Handler) RotateGoogleKey(c *gin.Context) {
	src, ok := h.loadSource(c)
	if !ok {
		return
	}
	if src.Kind != KindGoogleAds {
		h.mgmtError(c, http.StatusBadRequest, "this source has no Google webhook key")
		return
	}
	plaintext, hash, err := GenerateGoogleKey()
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not mint key")
		return
	}
	if err := h.repo.SetGoogleKeyHash(c.Request.Context(), src.OrgID, src.ID, hash); err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not rotate key")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": sourceWithKey{
		sourceView: h.viewOf(c, src, false),
		GoogleKey:  plaintext,
	}})
}

// ListEvents returns a source's recent deliveries.
func (h *Handler) ListEvents(c *gin.Context) {
	src, ok := h.loadSource(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	out, err := h.repo.ListEvents(c.Request.Context(), src.OrgID, &src.ID, limit)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not list events")
		return
	}
	// Consent rides a second targeted read so a boot guard that never ran degrades the
	// consent display rather than the ledger — the delivery log is how a customer
	// answers "what happened to this lead" and must survive a missing column.
	ids := make([]uuid.UUID, 0, len(out))
	for i := range out {
		ids = append(ids, out[i].ID)
	}
	if consents, cerr := h.repo.ConsentForEvents(c.Request.Context(), ids); cerr != nil {
		h.logger.Error("integrations: could not read consent", "error", cerr, "source_id", src.ID.String())
	} else {
		for i := range out {
			if env, ok := consents[out[i].ID]; ok {
				out[i].Consent = env
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// capQuarantineError is the fixed ledger text for a lead refused by the daily cap.
//
// Fixed, and it names the remedy: the admin's question on seeing this row is always
// "did I lose the lead?", and the honest answer is no — the integrator got a 429 with
// a Retry-After, so their own retry is the recovery, and raising the cap is the fix.
const capQuarantineError = "daily capture limit reached — this delivery was refused (429) and NOT written as a contact. " +
	"The sender was told to retry later; raise this source's daily limit to accept it."

// ledgerCappedLead records a delivery the daily cap refused.
//
// Best-effort by construction: the caller already holds a correct answer for the
// integrator (429 + Retry-After), so a ledger failure must not change it. Losing the
// evidence is worse than nothing but far better than converting a clean, retryable
// refusal into a 500 the integrator has to interpret.
//
// Dedupe-aware via InsertEventDeduped: a caller retrying with the same
// Idempotency-Key against a still-capped source gets one row, not one per attempt.
func (h *Handler) ledgerCappedLead(c *gin.Context, source *LeadSource, req captureRequest) {
	raw := marshalJSONB(req.Fields)
	ctxJSON := marshalJSONB(req.Context)
	var providerID *string
	if id := strings.TrimSpace(c.GetHeader("Idempotency-Key")); id != "" {
		providerID = &id
	}
	event := &IntegrationEvent{
		OrgID:           source.OrgID,
		SourceID:        &source.ID,
		ProviderEventID: providerID,
		// quarantined, not failed: nothing was attempted and nothing broke. It is also
		// the status Ingest's replay switch re-runs in place, so if the caller does
		// retry that key once the cap resets, the row is reused rather than orphaned.
		Status:     EventStatusQuarantined,
		Attempts:   1,
		RawPayload: datatypes.JSON(raw),
		Context:    datatypes.JSON(ctxJSON),
		Error:      capQuarantineError,
	}
	if _, err := h.repo.InsertEventDeduped(c.Request.Context(), event); err != nil {
		h.logger.Error("integrations: could not ledger capped lead", "error", err, "source_id", source.ID.String())
	}
}

// testLeadResponse is what the admin learns from one click.
//
// Uncovered is as important as the rest: the test cannot exercise every field, and
// a result that lists only successes reads as "everything works".
type testLeadResponse struct {
	RecordID    string   `json:"record_id"`
	EventID     string   `json:"event_id"`
	Outcome     string   `json:"outcome"`
	Quarantined []string `json:"quarantined,omitempty"`
	Note        string   `json:"note,omitempty"`
	// Uncovered names fields this test could not exercise (a number/select target we
	// will not guess a value for; phone, which a test lead never sends).
	Uncovered []string `json:"uncovered,omitempty"`
	// SourceStatus lets the UI warn that a disabled source is rejecting real traffic
	// right now, even though this test just succeeded.
	SourceStatus string `json:"source_status"`
	// AssignedOwnerID names the rep this lead landed on — the panel claims the test
	// proves owner assignment, so it has to show the answer rather than assert the
	// category. Nil means the lead is unowned, which is itself the finding.
	AssignedOwnerID string `json:"assigned_owner_id,omitempty"`
	// Warnings carries routing problems (an unowned lead) to the admin who clicked.
	Warnings []string `json:"warnings,omitempty"`
}

// SendTestLead drives a synthetic lead through the real pipeline.
//
// It takes NO request body, deliberately: there must be no wire on which a caller
// could hand us `is_test`, a payload, or an identity. Everything is server-built.
func (h *Handler) SendTestLead(c *gin.Context) {
	_, userID, ok := h.actor(c)
	if !ok {
		return
	}
	src, ok := h.loadSource(c)
	if !ok {
		return // soft-deleted sources 404 here for free (GORM's soft-delete scope)
	}

	// Re-authorize the CLICKING admin's own permission to write the target, with
	// their real caller, before anything else happens.
	//
	// This is the control that stops integrations.manage from being a contact-write
	// primitive. authorizeTarget otherwise runs only at source create and on a target
	// change, and a test click is neither — so without this, a role granted
	// "configure integrations" and no contact access could write contacts on demand,
	// through a callerless actor that no OLS check ever sees.
	//
	// Before the ledger insert, too: a 403 afterwards would strand a `processing` row
	// describing a delivery that never happened.
	if !h.authorizeTarget(c, src.OrgID, src.TargetSlug) {
		return
	}

	// Limited per source AND per user, so one admin cannot fan out across every
	// source in the org. Amplification is already bounded by the stable test identity
	// (one contact per source, forever), so this reuses the existing limiter rather
	// than growing a second one.
	if !h.allow(c, "test:s:"+src.ID.String()) || !h.allow(c, "test:u:"+userID.String()) {
		return
	}

	fmap, err := ParseFieldMap(src.FieldMap)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "this source's field mapping is unreadable")
		return
	}
	allow, err := BuildAllowlist(c.Request.Context(), h.schema, src.OrgID, src.TargetSlug)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not load this object's fields")
		return
	}
	desc, err := h.schema.GetSchema(c.Request.Context(), src.OrgID, src.TargetSlug)
	if err != nil || desc == nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not load this object's fields")
		return
	}

	fields, uncovered, err := buildTestPayload(src, fmap, allow, desc)
	if err != nil {
		if appErr, ok := err.(*domain.AppError); ok {
			h.mgmtError(c, appErr.Code, appErr.Message)
			return
		}
		h.mgmtError(c, http.StatusInternalServerError, "could not build a test lead")
		return
	}

	// The one human-initiated path into the callerless writer, so it is the one worth
	// naming a user on: the ledger records the source but never a person.
	h.logger.Info("integrations: test lead sent",
		"source_id", src.ID.String(), "org_id", src.OrgID.String(), "user_id", userID.String())

	lead := RawLead{
		Fields:  fields,
		Context: testLeadContext(),
		// No ProviderEventID: a stable one would make the second click conflict on the
		// dedupe index and replay click one's result — returning green having run none
		// of the pipeline. Each click is an honest, separate delivery.
		TestOrigin: TestOriginAdmin,
	}
	res, err := h.ingest.Ingest(c.Request.Context(), src, lead)
	if err != nil {
		if appErr, ok := err.(*domain.AppError); ok {
			h.mgmtError(c, appErr.Code, appErr.Message)
			return
		}
		h.logger.Error("integrations: test lead failed", "error", err, "source_id", src.ID.String())
		h.mgmtError(c, http.StatusInternalServerError, "the test lead could not be processed")
		return
	}
	if res.RecordID == uuid.Nil {
		h.logger.Error("integrations: test lead returned no record", "source_id", src.ID.String())
		h.mgmtError(c, http.StatusInternalServerError, "the test lead was not written")
		return
	}

	// A deal-creating source makes NO deal for a test lead, and the panel says so
	// here rather than letting a green result imply otherwise. `uncovered` is the
	// right channel: it already exists to stop a test from reading as "everything
	// works" when parts of the pipeline were deliberately not exercised.
	if ParseDealConfig(src.Config).Enabled {
		uncovered = append(uncovered, "deal creation — a real lead would also open a deal; test leads never do, because a test deal would count in your forecast and could not be told apart from a real one")
	}

	c.JSON(http.StatusOK, gin.H{"data": testLeadResponse{
		RecordID:        res.RecordID.String(),
		EventID:         res.EventID.String(),
		Outcome:         res.Outcome,
		Quarantined:     res.Quarantined,
		Note:            res.Note,
		Uncovered:       uncovered,
		SourceStatus:    src.Status,
		AssignedOwnerID: ownerString(res.OwnerID),
		Warnings:        res.Warnings,
	}})
}

// ── helpers ──────────────────────────────────────────────────────────────────

// actor reads the caller's org/user off the gin context. Copied rather than
// imported: internal/delivery/http is imported only by main, and importing it here
// would invert this package's dependency direction.
func (h *Handler) actor(c *gin.Context) (orgID, userID uuid.UUID, ok bool) {
	o, exists := c.Get("org_id")
	if !exists {
		h.mgmtError(c, http.StatusUnauthorized, "unauthorized")
		return uuid.Nil, uuid.Nil, false
	}
	u, _ := c.Get("user_id")
	orgID, _ = o.(uuid.UUID)
	userID, _ = u.(uuid.UUID)
	if orgID == uuid.Nil {
		h.mgmtError(c, http.StatusUnauthorized, "unauthorized")
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, true
}

// loadSource resolves :id within the caller's org.
func (h *Handler) loadSource(c *gin.Context) (*LeadSource, bool) {
	orgID, _, ok := h.actor(c)
	if !ok {
		return nil, false
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.mgmtError(c, http.StatusBadRequest, "invalid source id")
		return nil, false
	}
	src, err := h.repo.GetSource(c.Request.Context(), orgID, id)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not load source")
		return nil, false
	}
	if src == nil {
		h.mgmtError(c, http.StatusNotFound, "source not found")
		return nil, false
	}
	return src, true
}

// authorizeTarget re-checks the CONFIGURING ADMIN's own object permission on the
// target, using their real caller from the request context.
//
// This is a security control, not a formality. Ingest writes callerless, so OLS
// never runs at write time — meaning without this check `integrations.manage`
// alone would let an admin point a source at any object in the org (HR, finance)
// and write to it, regardless of their role. The capability says "may configure
// integrations"; it must not silently mean "may write anything".
func (h *Handler) authorizeTarget(c *gin.Context, orgID uuid.UUID, slug string) bool {
	ctx := c.Request.Context()
	if err := h.authz.Authorize(ctx, orgID, slug, domain.ActionCreate); err != nil {
		h.mgmtError(c, http.StatusForbidden, "you do not have permission to create "+slug+" records")
		return false
	}
	if err := h.authz.Authorize(ctx, orgID, slug, domain.ActionEdit); err != nil {
		h.mgmtError(c, http.StatusForbidden, "you do not have permission to edit "+slug+" records")
		return false
	}
	return true
}

// parseOwner validates the source's default owner.
//
// Membership is checked, not assumed. Nothing downstream does it: contactUseCase
// assigns OwnerUserID blindly, and the ingest write is callerless so no
// authorization layer sees it either. An unchecked id means every lead from this
// source lands owned by a stranger, a departed employee, or nobody — and an
// unowned or foreign-owned contact is invisible to own-scoped reps, which is
// exactly the silent lead-loss this platform exists to prevent.
func (h *Handler) parseOwner(c *gin.Context, orgID uuid.UUID, raw *string) (*uuid.UUID, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, true
	}
	id, err := uuid.Parse(*raw)
	if err != nil {
		h.mgmtError(c, http.StatusBadRequest, "invalid default_owner_id")
		return nil, false
	}
	ou, err := h.members.GetOrgUser(c.Request.Context(), id, orgID)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not verify the owner")
		return nil, false
	}
	if !ou.IsLive() {
		h.mgmtError(c, http.StatusBadRequest, "default_owner_id must be an active member of this workspace")
		return nil, false
	}
	return &id, true
}

// parseOwnerPool validates a rotation at save time.
//
// Same liveness bar as parseOwner, plus shape rules a pool needs and a single owner
// does not. Errors NAME the offending id: a rotation that silently drops a member is
// how leads quietly stop reaching someone.
func (h *Handler) parseOwnerPool(c *gin.Context, orgID uuid.UUID, raw []string) (datatypes.JSON, bool) {
	if len(raw) == 0 {
		return datatypes.JSON(`[]`), true // an explicit empty list turns rotation off
	}
	if len(raw) > maxOwnerPool {
		h.mgmtError(c, http.StatusBadRequest,
			"a rotation can hold at most "+strconv.Itoa(maxOwnerPool)+" people")
		return nil, false
	}
	seen := map[uuid.UUID]bool{}
	ids := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil {
			h.mgmtError(c, http.StatusBadRequest, "invalid user id in the rotation: "+s)
			return nil, false
		}
		if seen[id] {
			// A duplicate is not harmless: it would silently double that person's share
			// of every cycle, which is never what an admin meant by adding them twice.
			h.mgmtError(c, http.StatusBadRequest, "the same person appears twice in the rotation")
			return nil, false
		}
		seen[id] = true
		ids = append(ids, id)
	}

	live, err := h.members.ActiveMemberIDs(c.Request.Context(), orgID, ids)
	if err != nil {
		// Save time fails CLOSED — the opposite of ingest. Here an admin is watching and
		// can retry; there, refusing would cost a lead.
		h.mgmtError(c, http.StatusInternalServerError, "could not verify the rotation's members")
		return nil, false
	}
	for _, id := range ids {
		if !live[id] {
			h.mgmtError(c, http.StatusBadRequest,
				"everyone in the rotation must be an active member of this workspace ("+id.String()+" is not)")
			return nil, false
		}
	}
	out, _ := json.Marshal(raw)
	return datatypes.JSON(out), true
}

// authorizeOwnerWrite gates configuring ownership on the caller's own permission to
// write the owner field.
//
// Without it, integrations.manage would be an ownership-assignment primitive: ingest
// writes callerless, so nothing downstream checks whether this admin may set
// owner_user_id on the target object. automation's assign_user gates the identical
// write on the same mask; integrations gated nothing until now.
func (h *Handler) authorizeOwnerWrite(c *gin.Context, orgID uuid.UUID, slug string) bool {
	mask := h.authz.FieldMask(c.Request.Context(), orgID, slug)
	if !mask.CanWrite("owner_user_id") {
		h.mgmtError(c, http.StatusForbidden, "you do not have permission to assign the owner of "+slug+" records")
		return false
	}
	return true
}

// mgmtError writes the management API's error envelope, matching what the
// frontend's apiError expects ({error: "..."}).
func (h *Handler) mgmtError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

// compile-time assertion that the ingest service satisfies what the handler needs.
var _ = context.Background

// GetMapping returns everything the mapping UI needs in one call.
//
// The observed keys are the point. Without them an admin has to remember what
// their provider calls a field and type it exactly — which is a guessing game they
// lose silently, because a wrong source key simply never matches and the lead
// quarantines. The ledger already knows the real names.
func (h *Handler) GetMapping(c *gin.Context) {
	src, ok := h.loadSource(c)
	if !ok {
		return
	}
	observed, err := h.repo.ObservedKeys(c.Request.Context(), src.OrgID, src.ID, 50)
	if err != nil {
		h.logger.Error("integrations: observed keys failed", "error", err, "source_id", src.ID.String())
		observed = nil // a missing hint list must not deny the mapping screen
	}
	allow, err := BuildAllowlist(c.Request.Context(), h.schema, src.OrgID, src.TargetSlug)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not load this object's fields")
		return
	}
	desc, err := h.schema.GetSchema(c.Request.Context(), src.OrgID, src.TargetSlug)
	if err != nil || desc == nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not load this object's fields")
		return
	}
	targets := make([]mappingTarget, 0, len(desc.Fields))
	for _, f := range desc.Fields {
		// Only what a lead may actually be written into: the allowlist already
		// excludes ownership and relations, so they can never be offered as options.
		if !allow.Permits(f.Key) {
			continue
		}
		targets = append(targets, mappingTarget{Key: f.Key, Label: f.Label, Type: f.Type})
	}
	fmap, err := ParseFieldMap(src.FieldMap)
	if err != nil {
		fmap = FieldMap{}
	}
	c.JSON(http.StatusOK, gin.H{"data": mappingView{Observed: observed, TargetFields: targets, FieldMap: fmap}})
}

// applyFieldMapUpdate validates and stores a source's mapping.
//
// Validated HERE, at save time, against the target object's writable keys — a
// mapping that can never work must fail in front of the admin who wrote it, not
// quarantine every lead at 3am with nobody watching.
func (h *Handler) applyFieldMapUpdate(c *gin.Context, src *LeadSource, m FieldMap) bool {
	allow, err := BuildAllowlist(c.Request.Context(), h.schema, src.OrgID, src.TargetSlug)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not load this object's fields")
		return false
	}
	if problems := ValidateFieldMap(m, allow); len(problems) > 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error":   "this mapping has problems",
			"details": problems,
		})
		return false
	}
	raw, err := json.Marshal(m)
	if err != nil {
		h.mgmtError(c, http.StatusBadRequest, "invalid field map")
		return false
	}
	src.FieldMap = datatypes.JSON(raw)
	return true
}

// SourceStats serves the per-day delivery counts behind the source sparkline (L6.6).
//
// Display only. The split it applies (test deliveries out of `written`) is one
// CountCreatedToday refuses, because that function backs the daily CAP and excluding a
// wire-settable status there would be cap-free record creation. A chart gates nothing.
func (h *Handler) SourceStats(c *gin.Context) {
	src, ok := h.loadSource(c)
	if !ok {
		return
	}
	days, _ := strconv.Atoi(c.Query("days"))
	rows, err := h.repo.DailyIngestCounts(c.Request.Context(), src.OrgID, src.ID, days)
	if err != nil {
		h.logger.Error("integrations: could not read source stats", "error", err, "source_id", src.ID.String())
		h.mgmtError(c, http.StatusInternalServerError, "could not load delivery counts")
		return
	}
	if rows == nil {
		rows = []DailyIngestCount{}
	}
	c.JSON(http.StatusOK, gin.H{"data": rows})
}
