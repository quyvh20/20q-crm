package marketing

import (
	"errors"
	"log/slog"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SequenceHandler serves the M8 drip-sequence enrollment API. Route-gated by
// marketing.manage. A "sequence" IS an automation workflow (built in the React Flow
// builder); this surface just wires a segment audience into one and shows progress.
type SequenceHandler struct {
	uc     *SequenceUseCase
	logger *slog.Logger
}

// NewSequenceHandler builds the handler.
func NewSequenceHandler(uc *SequenceUseCase, logger *slog.Logger) *SequenceHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SequenceHandler{uc: uc, logger: logger}
}

// RegisterRoutes mounts the sequence routes under /api/marketing/sequences, gated by the
// full protected stack + marketing.manage (mirroring the other marketing handlers).
func (h *SequenceHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	g := router.Group("/api/marketing/sequences")
	g.Use(protected...)
	g.Use(requireCap(domain.CapMarketingManage))
	{
		g.GET("", h.List)
		g.POST("", h.Create)
		g.GET("/:id", h.Get)
		g.GET("/:id/progress", h.Progress)
		g.POST("/:id/cancel", h.Cancel)
	}
}

func (h *SequenceHandler) fail(c *gin.Context, err error) {
	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		abortErr(c, appErr.Code, appErr.Message)
		return
	}
	h.logger.Error("marketing: sequence request failed", "error", err)
	abortErr(c, http.StatusInternalServerError, "internal error")
}

type createSequenceRequest struct {
	SequenceWorkflowID string `json:"sequence_workflow_id"`
	SegmentID          string `json:"segment_id"`
}

func (h *SequenceHandler) Create(c *gin.Context) {
	orgID, userID, ok := actorFromCtx(c)
	if !ok {
		return
	}
	var req createSequenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	seqWFID, err := uuid.Parse(req.SequenceWorkflowID)
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid sequence_workflow_id")
		return
	}
	segmentID, err := uuid.Parse(req.SegmentID)
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid segment_id")
		return
	}
	dto, err := h.uc.Create(c.Request.Context(), orgID, userID, seqWFID, segmentID)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": dto})
}

func (h *SequenceHandler) List(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	rows, err := h.uc.List(c.Request.Context(), orgID)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rows})
}

func (h *SequenceHandler) Get(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, ok := sequenceID(c)
	if !ok {
		return
	}
	dto, err := h.uc.Get(c.Request.Context(), orgID, id)
	if err != nil {
		h.fail(c, err)
		return
	}
	if dto == nil {
		abortErr(c, http.StatusNotFound, "enrollment not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": dto})
}

func (h *SequenceHandler) Progress(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, ok := sequenceID(c)
	if !ok {
		return
	}
	p, err := h.uc.Progress(c.Request.Context(), orgID, id)
	if err != nil {
		h.fail(c, err)
		return
	}
	if p == nil {
		abortErr(c, http.StatusNotFound, "enrollment not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": p})
}

func (h *SequenceHandler) Cancel(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, ok := sequenceID(c)
	if !ok {
		return
	}
	changed, err := h.uc.Cancel(c.Request.Context(), orgID, id)
	if err != nil {
		h.fail(c, err)
		return
	}
	if !changed {
		abortErr(c, http.StatusConflict, "enrollment not found or already terminal")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": SeqEnrollCanceled}})
}

func sequenceID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid enrollment id")
		return uuid.Nil, false
	}
	return id, true
}
