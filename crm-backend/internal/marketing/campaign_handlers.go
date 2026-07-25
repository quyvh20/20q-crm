package marketing

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CampaignHandler serves the M7 bulk-send API. Route-gated by marketing.manage;
// M7.1 exposes CRUD + audience estimate + the pre-send launch checklist + a
// roster snapshot (which sends no email).
type CampaignHandler struct {
	uc     *CampaignUseCase
	logger *slog.Logger
}

// NewCampaignHandler builds the handler.
func NewCampaignHandler(uc *CampaignUseCase, logger *slog.Logger) *CampaignHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &CampaignHandler{uc: uc, logger: logger}
}

// RegisterRoutes mounts the campaign routes under /api/marketing, gated by the full
// protected stack + marketing.manage (mirroring the other marketing handlers). The
// dry audience estimate lives at a distinct top path so it never collides with the
// /campaigns/:id param route (the M5 segment-fields/segment-preview precedent).
func (h *CampaignHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	capMW := requireCap(domain.CapMarketingManage)

	g := router.Group("/api/marketing/campaigns")
	g.Use(protected...)
	g.Use(capMW)
	{
		g.GET("", h.List)
		g.POST("", h.Create)
		g.GET("/:id", h.Get)
		g.PUT("/:id", h.Update)
		g.DELETE("/:id", h.Remove)
		g.GET("/:id/readiness", h.Readiness)
		g.POST("/:id/snapshot", h.Snapshot)
		g.POST("/:id/launch", h.Launch)
		g.POST("/:id/pause", h.Pause)
		g.POST("/:id/resume", h.Resume)
		g.POST("/:id/cancel", h.Cancel)
		g.GET("/:id/progress", h.Progress)
	}

	ge := router.Group("/api/marketing/campaign-estimate")
	ge.Use(protected...)
	ge.Use(capMW)
	ge.POST("", h.EstimateDry)
}

func (h *CampaignHandler) fail(c *gin.Context, err error) {
	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		abortErr(c, appErr.Code, appErr.Message)
		return
	}
	h.logger.Error("marketing: campaign request failed", "error", err)
	abortErr(c, http.StatusInternalServerError, "internal error")
}

func campaignID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid campaign id")
		return uuid.Nil, false
	}
	return id, true
}

func (h *CampaignHandler) List(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	rows, err := h.uc.List(c.Request.Context(), orgID)
	if err != nil {
		h.fail(c, err)
		return
	}
	if rows == nil {
		rows = []domain.Campaign{}
	}
	c.JSON(http.StatusOK, gin.H{"data": rows})
}

func (h *CampaignHandler) Get(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, ok := campaignID(c)
	if !ok {
		return
	}
	camp, err := h.uc.Get(c.Request.Context(), orgID, id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": camp})
}

func (h *CampaignHandler) Create(c *gin.Context) {
	orgID, userID, ok := actorFromCtx(c)
	if !ok {
		return
	}
	var in domain.CampaignInput
	if err := c.ShouldBindJSON(&in); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	camp, err := h.uc.Create(c.Request.Context(), orgID, userID, in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": camp})
}

func (h *CampaignHandler) Update(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, ok := campaignID(c)
	if !ok {
		return
	}
	var in domain.CampaignInput
	if err := c.ShouldBindJSON(&in); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	camp, err := h.uc.Update(c.Request.Context(), orgID, id, in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": camp})
}

func (h *CampaignHandler) Remove(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, ok := campaignID(c)
	if !ok {
		return
	}
	if err := h.uc.Delete(c.Request.Context(), orgID, id); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"removed": true}})
}

func (h *CampaignHandler) Readiness(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, ok := campaignID(c)
	if !ok {
		return
	}
	r, err := h.uc.Readiness(c.Request.Context(), orgID, id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": r})
}

func (h *CampaignHandler) Snapshot(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, ok := campaignID(c)
	if !ok {
		return
	}
	res, err := h.uc.Snapshot(c.Request.Context(), orgID, id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}

func (h *CampaignHandler) Launch(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, ok := campaignID(c)
	if !ok {
		return
	}
	camp, err := h.uc.Launch(c.Request.Context(), orgID, id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": camp})
}

func (h *CampaignHandler) lifecycle(c *gin.Context, fn func(ctx context.Context, orgID, id uuid.UUID) error) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, ok := campaignID(c)
	if !ok {
		return
	}
	if err := fn(c.Request.Context(), orgID, id); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"ok": true}})
}

func (h *CampaignHandler) Pause(c *gin.Context)  { h.lifecycle(c, h.uc.Pause) }
func (h *CampaignHandler) Resume(c *gin.Context) { h.lifecycle(c, h.uc.Resume) }
func (h *CampaignHandler) Cancel(c *gin.Context) { h.lifecycle(c, h.uc.Cancel) }

func (h *CampaignHandler) Progress(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, ok := campaignID(c)
	if !ok {
		return
	}
	p, err := h.uc.Progress(c.Request.Context(), orgID, id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": p})
}

type estimateRequest struct {
	SegmentIDs        []uuid.UUID `json:"segment_ids"`
	ExcludeSegmentIDs []uuid.UUID `json:"exclude_segment_ids"`
}

func (h *CampaignHandler) EstimateDry(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	var req estimateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	n, err := h.uc.EstimateForSegments(c.Request.Context(), orgID, req.SegmentIDs, req.ExcludeSegmentIDs)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"count": n}})
}
