package marketing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// unsubBodyLimit bounds the public unsubscribe request body. A one-click body is the
// fixed form string `List-Unsubscribe=One-Click`; a preference-center body is a tiny
// JSON object. 4 KiB is generous for both and caps an abusive body.
const unsubBodyLimit = 4 << 10

// unsubStore is the persistence slice the public unsubscribe handler needs (the
// concrete *Repository satisfies it). Declared consumer-side so the handler is
// unit-testable with a fake — the public route is unauthenticated and internet-facing,
// so its logic (token verify → suppression write → constant 200) is worth testing in
// isolation from a database.
type unsubStore interface {
	AddSuppression(ctx context.Context, s *Suppression) (bool, error)
	SetMarketingStatus(ctx context.Context, orgID uuid.UUID, emailNorm, status string) error
	ListTopicsByOrg(ctx context.Context, orgID uuid.UUID) ([]MarketingTopic, error)
}

// ipRateLimiter is the minimal slice of integrations.RateLimiter the public route
// uses. Declared here so package marketing does not import integrations.
type ipRateLimiter interface {
	AllowN(ctx context.Context, key string, cost int) (bool, time.Duration)
}

// OrgNameResolver returns an org's display name for the preference page. Nil-tolerant
// (the page just omits the name).
type OrgNameResolver func(ctx context.Context, orgID uuid.UUID) (string, error)

// PublicUnsubHandler serves the UNAUTHENTICATED one-click unsubscribe + preference
// center (M3). It is mounted directly on the engine (NOT under the marketing.manage
// protected group) and authenticates via the opaque HMAC/AEAD token alone — a
// mailbox provider's one-click POST carries no session. It writes only the marketing
// ledger (org-scoped, resolved from the token), so it needs no RecordAuthorizer.
type PublicUnsubHandler struct {
	store   unsubStore
	tokens  *TokenService
	limiter ipRateLimiter   // nil-tolerant (no limiter in some test/dev setups)
	orgName OrgNameResolver // nil-tolerant
	logger  *slog.Logger
}

// NewPublicUnsubHandler builds the handler.
func NewPublicUnsubHandler(store unsubStore, tokens *TokenService, limiter ipRateLimiter, orgName OrgNameResolver, logger *slog.Logger) *PublicUnsubHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &PublicUnsubHandler{store: store, tokens: tokens, limiter: limiter, orgName: orgName, logger: logger}
}

// RegisterRoutes mounts the public routes on the BARE engine (no auth middleware).
// The POST is the RFC-8058 one-click target (and the preference-center form submit);
// the GET returns minimal, state-free info for the hosted preference page. The `u`
// segment is static, so `:token` lives only under /u/ and cannot collide with the
// static /api/marketing/* management routes.
func (h *PublicUnsubHandler) RegisterRoutes(router *gin.Engine) {
	router.GET("/api/marketing/u/:token", h.ipGuard, h.PreferenceInfo)
	router.POST("/api/marketing/u/:token", h.ipGuard, h.Unsubscribe)
}

// ipGuard charges a per-IP token-bucket before any work (the endpoint is
// internet-facing and a prime target for enumeration). Fail-closed: over-limit 429s.
func (h *PublicUnsubHandler) ipGuard(c *gin.Context) {
	if h.limiter != nil {
		if ok, _ := h.limiter.AllowN(c.Request.Context(), "mktunsub:ip:"+c.ClientIP(), 1); !ok {
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}
	}
	c.Next()
}

// PreferenceInfo returns the data the hosted preference page renders: a validity
// flag, the org display name, and the org's topics (names only). It DELIBERATELY
// does not reveal the recipient's current subscription/topic state — a forwarded
// token would otherwise become a subscription-state oracle for anyone holding it.
func (h *PublicUnsubHandler) PreferenceInfo(c *gin.Context) {
	tok, ok := h.verify(c)
	if !ok {
		return
	}
	resp := gin.H{"ok": true}
	if h.orgName != nil {
		if name, err := h.orgName(c.Request.Context(), tok.OrgID); err == nil && strings.TrimSpace(name) != "" {
			resp["org_name"] = name
		}
	}
	// Topics are offered for opt-DOWN; we expose only their id/name/description, never
	// which ones this address is currently subscribed to.
	topicsOut := []gin.H{}
	if topics, err := h.store.ListTopicsByOrg(c.Request.Context(), tok.OrgID); err == nil {
		for _, t := range topics {
			topicsOut = append(topicsOut, gin.H{"id": t.ID, "name": t.Name, "description": t.Description})
		}
	}
	resp["topics"] = topicsOut
	c.JSON(http.StatusOK, gin.H{"data": resp})
}

type unsubBody struct {
	TopicID *string `json:"topic_id"`
}

// Unsubscribe writes the opt-out synchronously and idempotently, returning a
// constant 200 with no redirect and no confirmation that varies by prior state. A
// body carrying a valid topic_id is a per-topic opt-down; anything else (including
// the RFC-8058 `List-Unsubscribe=One-Click` form body) is a GLOBAL marketing opt-out
// — the correct interpretation of the one-click button, which has no topic context.
func (h *PublicUnsubHandler) Unsubscribe(c *gin.Context) {
	tok, ok := h.verify(c)
	if !ok {
		return
	}

	var body unsubBody
	raw, _ := io.ReadAll(io.LimitReader(c.Request.Body, unsubBodyLimit))
	if len(raw) > 0 {
		// Best-effort: the one-click form body is not JSON, and that is fine — it just
		// leaves TopicID nil, i.e. a global opt-out.
		_ = json.Unmarshal(raw, &body)
	}

	var topicID *uuid.UUID
	if body.TopicID != nil && strings.TrimSpace(*body.TopicID) != "" {
		tid, err := uuid.Parse(strings.TrimSpace(*body.TopicID))
		if err != nil {
			abortErr(c, http.StatusBadRequest, "invalid topic_id")
			return
		}
		topicID = &tid
	}

	source := "list_unsubscribe_one_click"
	if topicID != nil {
		source = "preference_center_topic"
	}
	sup := &Suppression{
		OrgID:           tok.OrgID,
		EmailNormalized: tok.Email,
		Reason:          ReasonUnsubscribe,
		Scope:           ScopeMarketing,
		Source:          source,
		TopicID:         topicID,
	}
	if _, err := h.store.AddSuppression(c.Request.Context(), sup); err != nil {
		h.logger.Error("marketing: unsubscribe write failed", "error", err, "org_id", tok.OrgID.String())
		abortErr(c, http.StatusInternalServerError, "could not process the unsubscribe")
		return
	}

	// A GLOBAL opt-out also flips the lifecycle status (badge + HasLawfulBasis); a
	// per-topic opt-down leaves the overall status untouched. Best-effort — the
	// suppression row above is the enforcement point, so a status-write failure must
	// not fail the unsubscribe.
	if topicID == nil {
		if err := h.store.SetMarketingStatus(c.Request.Context(), tok.OrgID, tok.Email, StatusUnsubscribed); err != nil {
			h.logger.Warn("marketing: unsubscribe status write failed (suppression still applied)", "error", err, "org_id", tok.OrgID.String())
		}
	}

	h.logger.Info("marketing: unsubscribe processed", "org_id", tok.OrgID.String(), "topic_scoped", topicID != nil)
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"ok": true, "unsubscribed": true}})
}

// verify opens the path token, aborting with a uniform 400 on any failure
// (fail-closed). It never distinguishes not-configured / malformed / bad-tag to the
// caller — all are just "this link isn't valid" — but logs the not-configured case
// loudly because it is an operator misconfiguration, not a bad link.
func (h *PublicUnsubHandler) verify(c *gin.Context) (UnsubToken, bool) {
	tok, err := h.tokens.Verify(c.Param("token"))
	if err != nil {
		if errors.Is(err, ErrTokensNotConfigured) {
			h.logger.Error("marketing: unsubscribe requested but MARKETING_UNSUB_KEY is not configured — the link cannot be honored")
		}
		abortErr(c, http.StatusBadRequest, "this unsubscribe link is invalid or has expired")
		return UnsubToken{}, false
	}
	return tok, true
}
