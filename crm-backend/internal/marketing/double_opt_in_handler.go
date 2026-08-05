package marketing

import (
	"errors"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DoubleOptInRequestHandler is the AUTHED admin-facing trigger:
// "invite this contact to confirm their subscription". Gated on
// marketing.manage, same as the rest of the consent surface.
type DoubleOptInRequestHandler struct {
	uc *DoubleOptInUseCase
}

func NewDoubleOptInRequestHandler(uc *DoubleOptInUseCase) *DoubleOptInRequestHandler {
	return &DoubleOptInRequestHandler{uc: uc}
}

func (h *DoubleOptInRequestHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	g := router.Group("/api/marketing/consent/contacts")
	g.Use(protected...)
	g.Use(requireCap(domain.CapMarketingManage))
	g.POST("/:contactId/request-confirmation", h.Request)
}

func (h *DoubleOptInRequestHandler) Request(c *gin.Context) {
	orgID, userID, ok := actorFromCtx(c)
	if !ok {
		return
	}

	contactID, err := uuid.Parse(c.Param("contactId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid contact id"))
		return
	}

	if err := h.uc.RequestForContact(c.Request.Context(), orgID, userID, contactID); err != nil {
		var appErr *domain.AppError
		if errors.As(err, &appErr) {
			c.JSON(appErr.Code, domain.Err(appErr.Message))
			return
		}
		c.JSON(http.StatusInternalServerError, domain.Err("failed to send confirmation"))
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"ok": true}))
}

// PublicConfirmHandler is the UNAUTHENTICATED double-opt-in confirm surface —
// mirrors PublicUnsubHandler exactly (opaque token IS the credential).
type PublicConfirmHandler struct {
	uc      *DoubleOptInUseCase
	limiter ipRateLimiter
	orgName OrgNameResolver
}

func NewPublicConfirmHandler(uc *DoubleOptInUseCase, limiter ipRateLimiter, orgName OrgNameResolver) *PublicConfirmHandler {
	return &PublicConfirmHandler{uc: uc, limiter: limiter, orgName: orgName}
}

func (h *PublicConfirmHandler) RegisterRoutes(router *gin.Engine) {
	router.GET("/api/marketing/confirm/:token", h.ipGuard, h.Info)
	router.POST("/api/marketing/confirm/:token", h.ipGuard, h.Confirm)
}

func (h *PublicConfirmHandler) ipGuard(c *gin.Context) {
	if h.limiter != nil {
		if ok, _ := h.limiter.AllowN(c.Request.Context(), "mktconfirm:ip:"+c.ClientIP(), 1); !ok {
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}
	}
	c.Next()
}

// Info verifies the token and returns just enough for the landing page to
// render a "confirm your subscription to <org>" screen — never the current
// subscription state (same reasoning as PublicUnsubHandler.PreferenceInfo: a
// forwarded token must not become a state oracle).
func (h *PublicConfirmHandler) Info(c *gin.Context) {
	tok, err := h.uc.tokens.Verify(c.Param("token"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "this confirmation link is invalid or has expired")
		return
	}
	resp := gin.H{"ok": true}
	if h.orgName != nil {
		if name, err := h.orgName(c.Request.Context(), tok.OrgID); err == nil && name != "" {
			resp["org_name"] = name
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": resp})
}

func (h *PublicConfirmHandler) Confirm(c *gin.Context) {
	tok, err := h.uc.Confirm(c.Request.Context(), c.Param("token"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "this confirmation link is invalid or has expired")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"ok": true, "confirmed": true, "org_id": tok.OrgID}})
}
