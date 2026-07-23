package marketing

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DomainHandler serves the per-org sending-domain API (M2), mounted under
// /api/marketing/domains and gated by marketing.manage. Mirrors the
// integrations Handler/ConnectionHandler split — a separate handler on the same
// capability + protected stack.
type DomainHandler struct {
	svc    *DomainService
	logger *slog.Logger
}

// NewDomainHandler builds the handler.
func NewDomainHandler(svc *DomainService, logger *slog.Logger) *DomainHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &DomainHandler{svc: svc, logger: logger}
}

// RegisterRoutes mounts the domain routes. `protected` is the FULL protected stack
// (PAT auth + 2FA), matching the integrations shape.
func (h *DomainHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	g := router.Group("/api/marketing/domains")
	g.Use(protected...)
	g.Use(requireCap(domain.CapMarketingManage))
	{
		g.GET("", h.List)
		g.POST("", h.Add)
		g.GET("/:id", h.Get)
		g.POST("/:id/verify", h.Verify)
		g.POST("/:id/refresh", h.Refresh)
		g.DELETE("/:id", h.Remove)
	}
}

type addDomainRequest struct {
	Domain            string `json:"domain"`
	TrackingSubdomain string `json:"tracking_subdomain"`
}

// List returns the org's sending domains plus a bulk-send readiness summary (drives
// the "marketing blocked until verified" banner).
func (h *DomainHandler) List(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	domains, err := h.svc.ListDomains(c.Request.Context(), orgID)
	if err != nil {
		h.fail(c, err, "could not list domains")
		return
	}
	if domains == nil {
		domains = []EmailDomain{}
	}
	canSend, reason, err := h.svc.CanBulkSend(c.Request.Context(), orgID)
	if err != nil {
		h.fail(c, err, "could not compute sending status")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data": domains,
		"meta": gin.H{"can_bulk_send": canSend, "reason": reason},
	})
}

// Add registers a new domain with Resend.
func (h *DomainHandler) Add(c *gin.Context) {
	orgID, userID, ok := actorFromCtx(c)
	if !ok {
		return
	}
	var req addDomainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	d, err := h.svc.AddDomain(c.Request.Context(), orgID, userID, req.Domain, req.TrackingSubdomain)
	if err != nil {
		h.fail(c, err, "could not add domain")
		return
	}
	h.logger.Info("marketing: sending domain added",
		"org_id", orgID.String(), "actor", userID.String(), "domain", d.Domain)
	c.JSON(http.StatusCreated, gin.H{"data": d})
}

// Get returns one domain with its DNS records.
func (h *DomainHandler) Get(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid domain id")
		return
	}
	d, err := h.svc.GetDomain(c.Request.Context(), orgID, id)
	if err != nil {
		h.fail(c, err, "could not load domain")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": d})
}

// Verify triggers Resend's async DNS re-check and returns the refreshed domain.
func (h *DomainHandler) Verify(c *gin.Context) {
	h.mutate(c, func(orgID, id uuid.UUID) (*EmailDomain, error) {
		return h.svc.TriggerVerify(c.Request.Context(), orgID, id)
	})
}

// Refresh re-reads status + records from Resend and re-checks DMARC.
func (h *DomainHandler) Refresh(c *gin.Context) {
	h.mutate(c, func(orgID, id uuid.UUID) (*EmailDomain, error) {
		return h.svc.RefreshDomain(c.Request.Context(), orgID, id)
	})
}

// Remove deletes the domain from Resend and soft-deletes the row.
func (h *DomainHandler) Remove(c *gin.Context) {
	orgID, userID, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid domain id")
		return
	}
	if err := h.svc.RemoveDomain(c.Request.Context(), orgID, id); err != nil {
		h.fail(c, err, "could not remove domain")
		return
	}
	h.logger.Info("marketing: sending domain removed",
		"org_id", orgID.String(), "actor", userID.String(), "domain_id", id.String())
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"removed": true}})
}

// mutate is the shared shape for the verify/refresh routes (parse id → call → 200).
func (h *DomainHandler) mutate(c *gin.Context, fn func(orgID, id uuid.UUID) (*EmailDomain, error)) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid domain id")
		return
	}
	d, err := fn(orgID, id)
	if err != nil {
		h.fail(c, err, "could not update domain")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": d})
}

// fail maps service/sentinel errors to HTTP status codes.
func (h *DomainHandler) fail(c *gin.Context, err error, fallback string) {
	switch {
	case errors.Is(err, ErrDomainInvalid):
		abortErr(c, http.StatusBadRequest, "that doesn't look like a valid domain")
	case errors.Is(err, ErrDomainExists):
		abortErr(c, http.StatusConflict, "this domain is already added to your workspace")
	case errors.Is(err, ErrDomainTakenOtherOrg):
		abortErr(c, http.StatusConflict, "this domain is already registered by another workspace")
	case errors.Is(err, ErrDomainNotFound):
		abortErr(c, http.StatusNotFound, "domain not found")
	case errors.Is(err, ErrSendingNotConfigured):
		abortErr(c, http.StatusServiceUnavailable, "email sending isn't configured on this deployment")
	default:
		var apiErr *ResendAPIError
		if errors.As(err, &apiErr) {
			// Surface Resend's own message (it's operator-facing, not secret) but as a 502.
			msg := "the email provider rejected the request"
			if m := resendMessage(apiErr.Body); m != "" {
				msg = "email provider: " + m
			}
			h.logger.Warn("marketing: resend domains API error", "status", apiErr.Status, "body", apiErr.Body)
			abortErr(c, http.StatusBadGateway, msg)
			return
		}
		h.logger.Error("marketing: domain handler error", "error", err)
		abortErr(c, http.StatusInternalServerError, fallback)
	}
}

// resendMessage best-effort extracts the {"message":...} field from a Resend error body.
func resendMessage(body string) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(body), &e) == nil {
		return e.Message
	}
	return ""
}
