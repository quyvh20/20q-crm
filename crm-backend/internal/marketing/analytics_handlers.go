package marketing

import (
	"log/slog"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AnalyticsHandler serves the M9 read-only engagement analytics (per-campaign rollups +
// the per-org deliverability health card). Route-gated by marketing.manage; org-aggregate
// data so there is no per-viewer scope to apply (owner-less, companies-like).
type AnalyticsHandler struct {
	repo   *Repository
	logger *slog.Logger
}

// NewAnalyticsHandler builds the handler.
func NewAnalyticsHandler(repo *Repository, logger *slog.Logger) *AnalyticsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AnalyticsHandler{repo: repo, logger: logger}
}

// RegisterRoutes mounts the analytics reads under /api/marketing, gated by the protected
// stack + marketing.manage. The per-campaign route reuses the campaigns :id param name so
// it coexists with the campaign handler's /campaigns/:id tree.
func (h *AnalyticsHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	g := router.Group("/api/marketing")
	g.Use(protected...)
	g.Use(requireCap(domain.CapMarketingManage))
	{
		g.GET("/campaigns/:id/analytics", h.CampaignAnalytics)
		g.GET("/campaigns/:id/ab", h.ABResults)
		g.GET("/deliverability", h.Deliverability)
	}
}

// CampaignAnalytics returns the engagement rollup for one campaign (404 for a foreign or
// unknown campaign so a caller can't probe another org's ids).
func (h *AnalyticsHandler) CampaignAnalytics(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid campaign id")
		return
	}
	camp, err := h.repo.GetCampaignByID(c.Request.Context(), orgID, id)
	if err != nil {
		h.logger.Error("marketing: analytics campaign lookup failed", "error", err, "org_id", orgID.String())
		abortErr(c, http.StatusInternalServerError, "internal error")
		return
	}
	if camp == nil {
		abortErr(c, http.StatusNotFound, "campaign not found")
		return
	}
	a, err := h.repo.CampaignAnalytics(c.Request.Context(), orgID, id)
	if err != nil {
		h.logger.Error("marketing: campaign analytics failed", "error", err, "org_id", orgID.String())
		abortErr(c, http.StatusInternalServerError, "could not load analytics")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": a})
}

// ABResults returns a campaign's A/B test state: per-variant unique engagement, the
// decided winner (empty until the decider runs), and — while undecided — the projected
// winner + whether the difference is significant yet (for the live significance readout).
func (h *AnalyticsHandler) ABResults(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid campaign id")
		return
	}
	camp, err := h.repo.GetCampaignByID(c.Request.Context(), orgID, id)
	if err != nil {
		h.logger.Error("marketing: ab results campaign lookup failed", "error", err, "org_id", orgID.String())
		abortErr(c, http.StatusInternalServerError, "internal error")
		return
	}
	if camp == nil {
		abortErr(c, http.StatusNotFound, "campaign not found")
		return
	}
	if camp.ABTestPct == 0 {
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"enabled": false}})
		return
	}
	metrics, err := h.repo.ABVariantMetrics(c.Request.Context(), id)
	if err != nil {
		h.logger.Error("marketing: ab variant metrics failed", "error", err, "org_id", orgID.String())
		abortErr(c, http.StatusInternalServerError, "could not load A/B results")
		return
	}
	projected, sig, reason := decideABWinner(metrics["A"], metrics["B"])
	a, b := metrics["A"], metrics["B"]
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"enabled":          true,
		"test_pct":         camp.ABTestPct,
		"subject_b":        camp.ABSubjectB,
		"variant_a":        a,
		"variant_b":        b,
		"winner":           camp.ABWinnerVariant, // "" until the decider runs
		"decided_at":       camp.ABDecidedAt,
		"projected_winner": projected,
		"significant":      sig,
		"reason":           reason,
	}})
}

// Deliverability returns the org's rolling complaint/bounce rates alongside the SAME
// breaker thresholds + min-volume floors the auto-pause uses, so the health card and the
// breaker can never drift.
func (h *AnalyticsHandler) Deliverability(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	rates, err := h.repo.DeliverabilityRates(c.Request.Context(), orgID, breakerWindow)
	if err != nil {
		h.logger.Error("marketing: deliverability rates failed", "error", err, "org_id", orgID.String())
		abortErr(c, http.StatusInternalServerError, "could not load deliverability")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"rates": rates,
		"thresholds": gin.H{
			"warn_rate":        breakerWarnRate,
			"pause_rate":       breakerPauseRate,
			"warn_min_volume":  breakerWarnMinVolume,
			"pause_min_volume": breakerPauseMinVolume,
			"window_days":      int(breakerWindow.Hours() / 24),
		},
	}})
}
