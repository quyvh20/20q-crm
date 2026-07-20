package integrations

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
)

// webhookBodyLimit caps a provider webhook body. Meta batches up to ~1000 changes
// per POST, so this is looser than the 256KB capture cap but still bounded — an
// unbounded read on a PUBLIC endpoint is a memory-amplification bomb.
const webhookBodyLimit = 1 << 20 // 1MB

// WebhookHandler serves the app-level Facebook leadgen webhook (L5.3): the GET
// verification handshake and the POST receipt that signature-checks, parses, and
// durably enqueues each delivery as a `pending` event for the async processor —
// then acks 200 immediately (Meta retries any non-200 for ~36h, so the ack must
// mean "durably received", which enqueuing guarantees).
//
// The fetch of the actual lead data happens OFF this request, in the async
// processor: the webhook carries only ids, and blocking the ack on a Graph fetch
// would make a slow Graph turn Meta's retries into duplicate work.
type WebhookHandler struct {
	repo        *Repository
	conn        *ConnectionService // registry access (verify/parse via the provider)
	verifyToken string             // FACEBOOK_WEBHOOK_VERIFY_TOKEN — the GET handshake secret
	ipLimiter   *RateLimiter
	logger      *slog.Logger
}

// NewWebhookHandler builds the handler.
func NewWebhookHandler(repo *Repository, conn *ConnectionService, verifyToken string, ipLimiter *RateLimiter, logger *slog.Logger) *WebhookHandler {
	return &WebhookHandler{repo: repo, conn: conn, verifyToken: verifyToken, ipLimiter: ipLimiter, logger: logger}
}

// RegisterRoutes mounts the PUBLIC webhook routes (no auth middleware — Meta
// authenticates via the GET verify token and the POST X-Hub-Signature-256).
func (h *WebhookHandler) RegisterRoutes(router *gin.Engine) {
	router.GET("/api/integrations/facebook/webhook", h.Verify)
	router.POST("/api/integrations/facebook/webhook", h.Receive)
}

// Verify answers Meta's subscription handshake: echo hub.challenge iff hub.mode is
// `subscribe` and hub.verify_token matches the configured token (constant-time).
func (h *WebhookHandler) Verify(c *gin.Context) {
	mode := c.Query("hub.mode")
	token := c.Query("hub.verify_token")
	challenge := c.Query("hub.challenge")
	if mode != "subscribe" || h.verifyToken == "" ||
		subtle.ConstantTimeCompare([]byte(token), []byte(h.verifyToken)) != 1 {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	// Meta expects the raw challenge string echoed back with a 200.
	c.String(http.StatusOK, challenge)
}

// Receive verifies, parses, and enqueues a leadgen delivery batch.
func (h *WebhookHandler) Receive(c *gin.Context) {
	// A per-IP limit bounds a flood; the signature check below is the real gate (an
	// unsigned request costs only an HMAC + a bounded body read, never a DB write).
	if h.ipLimiter != nil {
		if ok, _ := h.ipLimiter.AllowN(c.Request.Context(), "fbwh:ip:"+c.ClientIP(), 1); !ok {
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, webhookBodyLimit))
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	prov, ok := h.conn.registry.Get(ProviderKeyFacebook)
	if !ok {
		// The provider is not configured on this deployment. Ack so Meta stops
		// retrying — there is nothing we can do with the delivery, and a 5xx would
		// just churn Meta's retry budget for ~36h.
		c.Status(http.StatusOK)
		return
	}

	// Signature over the RAW bytes — never a re-marshalled struct.
	if err := prov.VerifyWebhook(c.Request, body); err != nil {
		// Not authentic (bad/absent signature). 401, not 200: this is not Meta, so
		// there is no retry budget to protect, and answering 200 would make the
		// endpoint a silent sink for forged deliveries.
		if h.logger != nil {
			h.logger.Warn("integrations: facebook webhook signature rejected", "error", err, "ip", c.ClientIP())
		}
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	events, err := prov.ParseWebhook(body)
	if err != nil {
		// Signed but unparseable — a Graph shape we do not understand. Ack (a retry
		// of the same bytes will not parse either) and log so it is visible.
		if h.logger != nil {
			h.logger.Error("integrations: facebook webhook parse failed", "error", err)
		}
		c.Status(http.StatusOK)
		return
	}

	anyTransient := false
	for i := range events {
		if h.enqueue(c.Request.Context(), &events[i]) {
			anyTransient = true
		}
	}
	if anyTransient {
		// A TRANSIENT failure (a DB blip) enqueued nothing for at least one delivery.
		// Acking 200 here would tell Meta "durably received" — its ~36h retries would
		// stop and the lead would be lost, the exact silent loss this subsystem exists
		// to prevent. Answer 503 so Meta redelivers the WHOLE batch; the
		// (connection_id, leadgen_id) dedupe index makes the siblings that DID enqueue
		// a no-op on redelivery, so no duplicate contact or double enrollment results.
		// A PERMANENT drop (a page no live workspace holds) does NOT set this — that
		// delivery is unprocessable no matter how many times Meta resends it, so we ack.
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	// Ack once every delivery is durably enqueued (or permanently, knowingly dropped).
	// The async processor does the fetch; Meta's job is done.
	c.Status(http.StatusOK)
}

// enqueue routes one delivery to its connection and inserts a pending event. It
// returns transient=true ONLY when a delivery could not be enqueued because of a
// recoverable error (a DB failure) — the signal Receive uses to withhold the ack
// so Meta redelivers.
//
// A delivery for a page no LIVE workspace holds (revoked/disconnected/deleted/
// never-connected) is dropped with a loud log and transient=false: there is no org
// to attribute it to, writing it into the workspace that used to hold the page is
// the "never the old workspace" failure the routing filter prevents, and Meta
// resending it would never help — so we ack that one.
func (h *WebhookHandler) enqueue(ctx context.Context, ev *InboundEvent) (transient bool) {
	conn, err := h.repo.FindConnectionForWebhook(ctx, ProviderKeyFacebook, ev.ExternalAccountID)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("integrations: webhook connection lookup failed", "error", err, "page_id", ev.ExternalAccountID)
		}
		return true // recoverable — let Meta redeliver
	}
	if conn == nil {
		if h.logger != nil {
			// Loud: a real lead arrived for a page that no live workspace holds. The
			// leadgen id is logged so it can be recovered manually if the page is
			// reconnected. We do NOT write it anywhere — there is no org to own it.
			h.logger.Warn("integrations: dropping leadgen for a page held by no live workspace",
				"page_id", ev.ExternalAccountID, "leadgen_id", ev.ProviderEventID)
		}
		return false // permanent — acking is correct; a redelivery would drop the same way
	}

	raw, _ := json.Marshal(ev.Raw)
	ctxJSON, _ := json.Marshal(map[string]any{"form_id": ev.FormID, "page_id": ev.ExternalAccountID})
	leadgenID := ev.ProviderEventID
	event := &IntegrationEvent{
		OrgID:        conn.OrgID,
		ConnectionID: &conn.ID,
		// leadgen_id — the stable delivery id. Connection-scoped dedupe: the
		// (connection_id, provider_event_id) partial unique index makes a Meta
		// redelivery (they retry for ~36h) a no-op.
		ProviderEventID: &leadgenID,
		// pending, NOT processing: this is the async worker's claimable state. Attempts
		// stays 0 until ClaimPendingEvents bumps it.
		Status:     EventStatusPending,
		RawPayload: datatypes.JSON(raw),
		Context:    datatypes.JSON(ctxJSON),
	}
	if _, err := h.repo.InsertEventDeduped(ctx, event); err != nil {
		if h.logger != nil {
			h.logger.Error("integrations: could not enqueue webhook delivery", "error", err, "leadgen_id", leadgenID)
		}
		return true // recoverable — let Meta redeliver (dedupe protects the retry)
	}
	return false
}
