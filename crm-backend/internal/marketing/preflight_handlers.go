package marketing

import (
	"log/slog"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
)

// PreflightHandler serves the org-level marketing go-live report.
type PreflightHandler struct {
	svc    *PreflightService
	logger *slog.Logger
}

func NewPreflightHandler(svc *PreflightService, logger *slog.Logger) *PreflightHandler {
	return &PreflightHandler{svc: svc, logger: logger}
}

func (h *PreflightHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	g := router.Group("/api/marketing")
	g.Use(protected...)
	g.Use(requireCap(domain.CapMarketingManage))
	{
		g.GET("/preflight", h.Preflight)
	}
}

// Preflight always returns 200. A red row is the payload, not an error — returning
// 503 for "not configured yet" would make the one screen built to explain the
// problem look like the problem.
func (h *PreflightHandler) Preflight(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	report, err := h.svc.Evaluate(c.Request.Context(), orgID)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("marketing: preflight failed", "error", err, "org_id", orgID.String())
		}
		abortErr(c, http.StatusInternalServerError, "could not evaluate marketing readiness")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": report})
}
