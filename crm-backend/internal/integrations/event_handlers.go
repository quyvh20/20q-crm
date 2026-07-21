package integrations

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The org-wide delivery ledger and the per-delivery retry action.
//
// Why a second read route rather than filters on the existing one: the shipped
// per-source route answers "what happened to this source's leads", and it
// structurally CANNOT answer the question an operator actually arrives with when
// something is broken. A provider delivery is inserted with a NULL source_id — the
// source is resolved later, inside the write — so every failure that happens BEFORE
// that point (a dead token, an unfetched lead, a form nobody enabled yet) has no
// source_id, forever. Those rows are invisible to a `source_id = ?` query. So is the
// entire ledger of a soft-deleted source, because the per-source route resolves
// through loadSource and a soft-deleted source 404s — even though the soft delete
// exists precisely so its history survives.
//
// This route scopes on org_id and treats source/connection as FILTERS, which makes
// all three reachable through one surface.

// eventStatusFilter is the set a caller may filter on.
//
// `duplicate` is deliberately absent even though it is a declared status and the
// frontend badges it: NOTHING in the codebase ever writes it (a redelivery returns
// the prior row's result and leaves its status alone). Offering it would be a filter
// that always answers empty, which teaches an operator that the log is broken at the
// exact moment they are relying on it.
var eventStatusFilter = map[string]bool{
	EventStatusPending:     true,
	EventStatusProcessing:  true,
	EventStatusProcessed:   true,
	EventStatusFailed:      true,
	EventStatusQuarantined: true,
	EventStatusTest:        true,
}

// RegisterEventRoutes mounts the ledger routes on the already-gated group.
func (h *Handler) RegisterEventRoutes(g *gin.RouterGroup) {
	g.GET("/events", h.ListOrgEvents)
	g.POST("/events/:event_id/retry", h.RetryEvent)
}

// ListOrgEvents serves one keyset page of the org's ledger.
func (h *Handler) ListOrgEvents(c *gin.Context) {
	orgID, _, ok := h.actor(c)
	if !ok {
		return
	}
	f := EventFilter{Limit: 50}
	if n, err := strconv.Atoi(c.Query("limit")); err == nil {
		f.Limit = n
	}
	if raw := strings.TrimSpace(c.Query("source_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			h.mgmtError(c, http.StatusBadRequest, "source_id must be a uuid")
			return
		}
		f.SourceID = &id
	}
	if raw := strings.TrimSpace(c.Query("connection_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			h.mgmtError(c, http.StatusBadRequest, "connection_id must be a uuid")
			return
		}
		f.ConnectionID = &id
	}
	for _, s := range c.QueryArray("status") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		// Rejected rather than ignored. A silently-dropped filter returns MORE rows
		// than asked for, and a log that quietly widens its own question is worse than
		// one that refuses it.
		if !eventStatusFilter[s] {
			h.mgmtError(c, http.StatusBadRequest, "unknown delivery status: "+s)
			return
		}
		f.Statuses = append(f.Statuses, s)
	}
	f.Unresolved = c.Query("unresolved") == "1"
	if cur := strings.TrimSpace(c.Query("cursor")); cur != "" {
		at, id, err := decodeEventCursor(cur)
		if err != nil {
			h.mgmtError(c, http.StatusBadRequest, "invalid cursor")
			return
		}
		f.CursorAt, f.CursorID = &at, &id
	}

	rows, err := h.repo.ListEventsFiltered(c.Request.Context(), orgID, f)
	if err != nil {
		h.logger.Error("integrations: could not list org events", "error", err, "org_id", orgID.String())
		h.mgmtError(c, http.StatusInternalServerError, "could not list deliveries")
		return
	}

	// The repo fetched limit+1 to detect a further page without a COUNT.
	next := ""
	if len(rows) > f.Limit {
		last := rows[f.Limit-1]
		next = encodeEventCursor(last.CreatedAt, last.ID)
		rows = rows[:f.Limit]
	}

	h.hydrateConsent(c, rows)

	// A second targeted read, like consent: the column is ALTER-added and unmapped, so
	// a boot guard that never ran must cost this one marker rather than the whole log.
	ids := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	redacted, rerr := h.repo.RedactedAtForEvents(c.Request.Context(), ids)
	if rerr != nil {
		h.logger.Error("integrations: could not read redaction markers", "error", rerr)
		redacted = nil
	}

	page := eventPage{
		Events:     make([]eventView, 0, len(rows)),
		NextCursor: next,
		Sources:    h.sourceLabels(c, orgID, rows),
	}
	for _, ev := range rows {
		var at *time.Time
		if t, ok := redacted[ev.ID]; ok {
			at = &t
		}
		page.Events = append(page.Events, viewOfEvent(ev, at))
	}
	c.JSON(http.StatusOK, gin.H{"data": page})
}

// hydrateConsent fills the unmapped consent envelope, degrading rather than failing.
// Same contract as the per-source route: the ledger must survive a boot guard that
// never ran, because it is how a customer answers "what happened to this lead".
func (h *Handler) hydrateConsent(c *gin.Context, rows []IntegrationEvent) {
	if len(rows) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	consents, err := h.repo.ConsentForEvents(c.Request.Context(), ids)
	if err != nil {
		h.logger.Error("integrations: could not read consent", "error", err)
		return
	}
	for i := range rows {
		if env, ok := consents[rows[i].ID]; ok {
			rows[i].Consent = env
		}
	}
}

// sourceLabels names the sources a page references, INCLUDING soft-deleted ones.
//
// Unscoped on purpose and only here: a deleted source's rows are the reason this
// route exists, and labelling them with a bare uuid would make the one view that can
// show them unreadable. It exposes a name and a kind, nothing that was not already on
// the source list.
func (h *Handler) sourceLabels(c *gin.Context, orgID uuid.UUID, rows []IntegrationEvent) map[string]eventSourceLabel {
	out := map[string]eventSourceLabel{}
	seen := map[uuid.UUID]bool{}
	ids := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		if rows[i].SourceID == nil || seen[*rows[i].SourceID] {
			continue
		}
		seen[*rows[i].SourceID] = true
		ids = append(ids, *rows[i].SourceID)
	}
	if len(ids) == 0 {
		return out
	}
	labels, err := h.repo.SourceLabels(c.Request.Context(), orgID, ids)
	if err != nil {
		h.logger.Error("integrations: could not label sources", "error", err)
		return out
	}
	for id, l := range labels {
		out[id.String()] = l
	}
	return out
}

// RetryEvent hands one failed provider delivery back to the async worker.
func (h *Handler) RetryEvent(c *gin.Context) {
	orgID, _, ok := h.actor(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("event_id"))
	if err != nil {
		h.mgmtError(c, http.StatusBadRequest, "invalid delivery id")
		return
	}
	ev, err := h.repo.GetEvent(c.Request.Context(), orgID, id)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not load the delivery")
		return
	}
	if ev == nil {
		h.mgmtError(c, http.StatusNotFound, "delivery not found")
		return
	}

	// THE AUTHORIZATION THAT MAKES THIS SAFE TO EXPOSE.
	//
	// Ingest writes CALLERLESS — no domain.Caller on the context, so OLS and FLS never
	// run on the write. That is fine for a lead arriving on a credentialed endpoint,
	// but it means any authenticated button that can cause an ingest turns
	// `integrations.manage` into a standing contact-write primitive for whoever holds
	// it. The test-lead button closed exactly this hole by re-checking the CLICKING
	// admin's own create+edit on the target object, with their real caller, and this
	// is the second button in the same position — so it takes the same guard rather
	// than relying on the capability alone.
	if !h.authorizeTarget(c, orgID, "contact") {
		return
	}

	if plan := classifyRetry(ev); plan.Mode != RetryModeRefetch {
		// 409 rather than 400: the row is real and the request was well-formed, it is
		// the delivery's STATE that refuses. The reason is machine-readable so the UI
		// renders its own copy instead of echoing ours.
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error":  "this delivery cannot be retried",
			"reason": plan.Reason,
		})
		return
	}

	// Re-check that the retry can actually SUCCEED before spending anything on it.
	//
	// The commonest retryable row is a lead quarantined because its form was not
	// enabled. If the admin has not enabled it yet, re-queueing achieves nothing and
	// is not free: the claim increments `attempts`, and after maxWebhookAttempts the
	// row is terminal — so a few hopeful clicks would permanently destroy the only
	// recovery path those leads have, while the ledger note still says "enable it in
	// Integrations settings". Refusing here costs the admin one honest message.
	if ev.ConnectionID != nil {
		formID := stringOf(readContext(ev.Context)["form_id"])
		src, serr := h.repo.FindFacebookFormSource(c.Request.Context(), *ev.ConnectionID, formID)
		if serr != nil {
			h.mgmtError(c, http.StatusInternalServerError, "could not check this lead's form")
			return
		}
		if src == nil || !src.IsLive() {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{
				"error":  "this lead's form is not enabled, so retrying it now would fail again",
				"reason": RetryReasonFormClosed,
			})
			return
		}
	}

	requeued, err := h.repo.RequeueEventForRetry(c.Request.Context(), orgID, id)
	if err != nil {
		h.logger.Error("integrations: could not requeue delivery", "error", err, "event_id", id.String())
		h.mgmtError(c, http.StatusInternalServerError, "could not queue the retry")
		return
	}
	if !requeued {
		// The atomic guard matched nothing, so something changed between the read and
		// the write — the worker claimed it, the reaper moved it, or a concurrent click
		// won. All of them mean "not yours to retry now", and none of them is an error
		// worth alarming about.
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error":  "this delivery changed while you were looking at it — reload the log",
			"reason": RetryReasonInFlight,
		})
		return
	}
	// 202, not 200: the worker picks it up on its next tick, so nothing has happened
	// yet and claiming otherwise would be the "retried successfully" lie.
	c.JSON(http.StatusAccepted, gin.H{"data": gin.H{"status": EventStatusPending}})
}

// encodeEventCursor packs a keyset position opaquely.
//
// Opaque because it is a POSITION, not a page number: a client that could build one
// could make the two halves of the row-value comparison disagree and silently skip
// rows. Base64 of a tiny JSON object keeps it inspectable in a bug report without
// inviting anyone to construct it.
func encodeEventCursor(at time.Time, id uuid.UUID) string {
	b, err := json.Marshal(eventCursor{At: at.UTC().Format(time.RFC3339Nano), ID: id})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeEventCursor(s string) (time.Time, uuid.UUID, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	var cur eventCursor
	if err := json.Unmarshal(b, &cur); err != nil {
		return time.Time{}, uuid.Nil, err
	}
	at, err := time.Parse(time.RFC3339Nano, cur.At)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return at, cur.ID, nil
}
