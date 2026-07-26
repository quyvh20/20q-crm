package marketing

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// resendWebhookBodyLimit bounds the webhook body. Resend delivery events are small;
// 1MB matches the provider-webhook cap.
const resendWebhookBodyLimit = 1 << 20

// resendWebhookStore is the persistence slice the public webhook handler needs (the
// concrete *Repository satisfies it). Declared consumer-side so the handler is
// unit-testable with a fake — the route is unauthenticated and internet-facing.
type resendWebhookStore interface {
	DomainOwnerOrg(ctx context.Context, domain string) (uuid.UUID, bool, error)
	InsertEvent(ctx context.Context, e *MarketingEmailEvent) (bool, error)
}

// ResendWebhookHandler serves the PUBLIC, unauthenticated Resend delivery webhook
// (M4). It is mounted on the bare gin engine like the provider webhooks, IP-limited,
// and authenticates via the Svix signature alone (no session). It is a PURE ENQUEUE:
// verify → parse → resolve org → dedup-insert a pending row → ack. All suppression /
// breaker work happens off the request path in StartResendWebhookProcessor.
type ResendWebhookHandler struct {
	store   resendWebhookStore
	limiter ipRateLimiter // nil-tolerant
	secret  string        // whsec_ Svix secret; empty => every event 401s (fail-closed)
	appEnv  string        // gates the signature skip hatch (default-closed allowlist)
	logger  *slog.Logger
	now     func() time.Time
}

// NewResendWebhookHandler builds the handler.
func NewResendWebhookHandler(store resendWebhookStore, limiter ipRateLimiter, secret, appEnv string, logger *slog.Logger) *ResendWebhookHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ResendWebhookHandler{store: store, limiter: limiter, secret: secret, appEnv: appEnv, logger: logger, now: time.Now}
}

// RegisterRoutes mounts the webhook on the BARE engine (no auth middleware).
func (h *ResendWebhookHandler) RegisterRoutes(router *gin.Engine) {
	router.POST("/api/marketing/webhooks/resend", h.ipGuard, h.Receive)
}

// skipSignatureAllowed mirrors the automation gate: default-CLOSED via an exact
// allowlist (the appEnv zero value / any non-dev-test value is production, so a
// negative match on "production" — which would fail OPEN when APP_ENV is unset — is
// deliberately NOT used). Only then is the explicit escape-hatch env consulted.
func (h *ResendWebhookHandler) skipSignatureAllowed() bool {
	if h.appEnv != "development" && h.appEnv != "test" {
		return false
	}
	return os.Getenv("RESEND_WEBHOOK_SKIP_SIGNATURE") == "true"
}

func (h *ResendWebhookHandler) ipGuard(c *gin.Context) {
	if h.limiter != nil {
		if ok, _ := h.limiter.AllowN(c.Request.Context(), "resendwh:ip:"+c.ClientIP(), 1); !ok {
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}
	}
	c.Next()
}

// Receive verifies + enqueues one Resend delivery event.
//
// Ack taxonomy (mirrors the provider webhooks): 401 bad/absent signature (never a
// DB write); 200 durably received OR authentically-received-but-unownable (dropped);
// 503 transient DB failure so Resend redelivers; 429 over the IP limit; 400 unreadable
// body.
func (h *ResendWebhookHandler) Receive(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, resendWebhookBodyLimit))
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	hdr := readSvixHeaders(c.GetHeader)
	if !h.skipSignatureAllowed() {
		if verr := verifySvix(hdr, body, h.secret, h.now()); verr != nil {
			if errors.Is(verr, ErrWebhookNotConfigured) {
				h.logger.Error("marketing: resend webhook received but RESEND_WEBHOOK_SECRET is not configured — rejecting")
			} else {
				h.logger.Warn("marketing: resend webhook signature verification failed", "error", verr)
			}
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
	}

	env, err := parseResendEnvelope(body)
	if err != nil {
		// Authentic (signed) but unparseable — never retryable. Ack + drop.
		h.logger.Warn("marketing: resend webhook body did not parse", "error", err, "svix_id", hdr.id)
		c.Status(http.StatusOK)
		return
	}

	// Resolve org DEFENSIVELY from the sending domain — there is no caller on a public
	// route. from-domain, then its parent (aligned subdomains like send.acme.com).
	fromDomain := env.Data.fromDomain()
	orgID, resolved, rerr := h.resolveOrg(c.Request.Context(), fromDomain)
	if rerr != nil {
		// DB blip resolving the domain → transient; Resend redelivers.
		h.logger.Error("marketing: resend webhook org resolution failed", "error", rerr, "from_domain", fromDomain)
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if !resolved {
		// Authentic event we cannot attribute to any org (e.g. a transactional send from
		// the platform's own domain, which no org owns). Drop + loud-log + ACK 200 —
		// never 5xx, or Resend retries forever. Expected until M7 sends from org domains.
		h.logger.Warn("marketing: resend webhook event not attributable to any org — dropped",
			"from_domain", fromDomain, "email_id", env.Data.EmailID, "svix_id", hdr.id, "type", env.Type)
		c.Status(http.StatusOK)
		return
	}

	action := classify(env)
	svixID := hdr.id
	if svixID == "" {
		// Only reachable with the dev skip hatch (Svix always sends svix-id). Fall back
		// to a stable per-event key so dedupe still holds locally.
		svixID = "local:" + env.Data.EmailID + ":" + env.Type
	}
	// M9: attribute the event to its campaign (or M8 sequence workflow) + mark the lane
	// from the send-time tags Resend echoes back, so engagement rolls up per campaign.
	campaignID := env.Data.tagCampaignID()
	channel := ""
	if campaignID != nil {
		channel = string(ChannelMarketing)
	}
	evt := &MarketingEmailEvent{
		OrgID:           orgID,
		SvixID:          svixID,
		EventType:       env.Type,
		EmailNormalized: env.Data.recipient(),
		FromDomain:      fromDomain,
		Reason:          action.Reason,
		BounceType:      bounceTypeOf(env, action),
		Channel:         channel,
		CampaignID:      campaignID,
		RawPayload:      datatypes.JSON(body),
		OccurredAt:      parseOccurredAt(env.CreatedAt),
		Status:          EventStatusPending,
	}
	if _, err := h.store.InsertEvent(c.Request.Context(), evt); err != nil {
		h.logger.Error("marketing: resend webhook enqueue failed", "error", err, "svix_id", svixID)
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	c.Status(http.StatusOK)
}

// resolveOrg maps a sending domain to its owning org, trying the exact domain then
// its parent (one label up) to cover aligned subdomains (send.acme.com → acme.com).
func (h *ResendWebhookHandler) resolveOrg(ctx context.Context, domain string) (uuid.UUID, bool, error) {
	if domain == "" {
		return uuid.Nil, false, nil
	}
	if orgID, ok, err := h.store.DomainOwnerOrg(ctx, domain); err != nil {
		return uuid.Nil, false, err
	} else if ok {
		return orgID, true, nil
	}
	if parent := parentDomain(domain); parent != "" {
		if orgID, ok, err := h.store.DomainOwnerOrg(ctx, parent); err != nil {
			return uuid.Nil, false, err
		} else if ok {
			return orgID, true, nil
		}
	}
	return uuid.Nil, false, nil
}

// bounceTypeOf records the bounce classification on the ledger row (only for a
// bounce event, so the rate rollup can distinguish hard from soft).
func bounceTypeOf(env resendEnvelope, action suppressionAction) string {
	if env.Type != ResendTypeBounced {
		return ""
	}
	if action.SoftBounce {
		return "transient"
	}
	return "permanent"
}
