package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SegmentHandler serves the M5 audiences API (dynamic segments + static lists).
// Route-gating is marketing.manage (applied in RegisterRoutes); segment DATA is
// re-authorized per viewer inside the usecase (OLS → FLS → row scope), like reports.
type SegmentHandler struct {
	uc domain.SegmentUseCase
}

// NewSegmentHandler builds the handler.
func NewSegmentHandler(uc domain.SegmentUseCase) *SegmentHandler { return &SegmentHandler{uc: uc} }

// RegisterRoutes mounts the segment routes under /api/marketing/segments, gated by
// the full protected stack + marketing.manage (mirrors the marketing handlers). The
// field catalog lives at /api/marketing/segment-fields (a distinct static path, so
// it never conflicts with the /segments/:id param route).
func (h *SegmentHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	capMW := requireCap(domain.CapMarketingManage)

	g := router.Group("/api/marketing/segments")
	g.Use(protected...)
	g.Use(capMW)
	{
		g.GET("", h.List)
		g.POST("", h.Create)
		g.GET("/:id", h.Get)
		g.PUT("/:id", h.Update)
		g.DELETE("/:id", h.Remove)
		g.GET("/:id/preview", h.Preview)
		g.GET("/:id/count", h.Count)
		g.POST("/:id/members", h.AddMembers)
		g.DELETE("/:id/members/:contactId", h.RemoveMember)
	}

	gf := router.Group("/api/marketing/segment-fields")
	gf.Use(protected...)
	gf.Use(capMW)
	gf.GET("", h.ListFields)

	// Dry-run preview of an UNSAVED definition (the builder's live count). A
	// distinct top path — never under /segments — so a static segment never
	// collides with the /segments/:id param route (the segment-fields precedent).
	gp := router.Group("/api/marketing/segment-preview")
	gp.Use(protected...)
	gp.Use(capMW)
	gp.POST("", h.PreviewDefinition)
}

func (h *SegmentHandler) callerIDs(c *gin.Context) (orgID, userID uuid.UUID, ok bool) {
	orgID, okOrg := GetOrgID(c)
	userID, okUser := GetUserID(c)
	if !okOrg || !okUser {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, true
}

func segmentIDParam(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid segment id"))
		return uuid.Nil, false
	}
	return id, true
}

func (h *SegmentHandler) List(c *gin.Context) {
	orgID, _, ok := h.callerIDs(c)
	if !ok {
		return
	}
	segs, err := h.uc.List(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	if segs == nil {
		segs = []domain.Segment{}
	}
	c.JSON(http.StatusOK, gin.H{"data": segs, "error": nil})
}

func (h *SegmentHandler) Get(c *gin.Context) {
	orgID, _, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, ok := segmentIDParam(c)
	if !ok {
		return
	}
	seg, err := h.uc.Get(c.Request.Context(), orgID, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": seg, "error": nil})
}

func (h *SegmentHandler) Create(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	var in domain.SegmentInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request body"))
		return
	}
	seg, err := h.uc.Create(c.Request.Context(), orgID, userID, in)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": seg, "error": nil})
}

func (h *SegmentHandler) Update(c *gin.Context) {
	orgID, _, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, ok := segmentIDParam(c)
	if !ok {
		return
	}
	var in domain.SegmentInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request body"))
		return
	}
	seg, err := h.uc.Update(c.Request.Context(), orgID, id, in)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": seg, "error": nil})
}

func (h *SegmentHandler) Remove(c *gin.Context) {
	orgID, _, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, ok := segmentIDParam(c)
	if !ok {
		return
	}
	if err := h.uc.Delete(c.Request.Context(), orgID, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"removed": true}, "error": nil})
}

func (h *SegmentHandler) Preview(c *gin.Context) {
	orgID, _, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, ok := segmentIDParam(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 {
		limit = 50
	}
	res, err := h.uc.Preview(c.Request.Context(), orgID, id, limit)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res, "error": nil})
}

func (h *SegmentHandler) Count(c *gin.Context) {
	orgID, _, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, ok := segmentIDParam(c)
	if !ok {
		return
	}
	n, err := h.uc.Count(c.Request.Context(), orgID, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"count": n}, "error": nil})
}

type previewDefinitionRequest struct {
	Definition json.RawMessage `json:"definition"`
	Limit      int             `json:"limit"`
}

func (h *SegmentHandler) PreviewDefinition(c *gin.Context) {
	orgID, _, ok := h.callerIDs(c)
	if !ok {
		return
	}
	var req previewDefinitionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request body"))
		return
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	res, err := h.uc.PreviewDefinition(c.Request.Context(), orgID, req.Definition, limit)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res, "error": nil})
}

func (h *SegmentHandler) ListFields(c *gin.Context) {
	orgID, _, ok := h.callerIDs(c)
	if !ok {
		return
	}
	fields, err := h.uc.ListFields(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	if fields == nil {
		fields = []domain.ReportFieldDescriptor{}
	}
	c.JSON(http.StatusOK, gin.H{"data": fields, "error": nil})
}

type addMembersRequest struct {
	ContactIDs []uuid.UUID `json:"contact_ids"`
}

func (h *SegmentHandler) AddMembers(c *gin.Context) {
	orgID, _, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, ok := segmentIDParam(c)
	if !ok {
		return
	}
	var req addMembersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request body"))
		return
	}
	added, err := h.uc.AddStaticMembers(c.Request.Context(), orgID, id, req.ContactIDs)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"added": added}, "error": nil})
}

func (h *SegmentHandler) RemoveMember(c *gin.Context) {
	orgID, _, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, ok := segmentIDParam(c)
	if !ok {
		return
	}
	contactID, err := uuid.Parse(c.Param("contactId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid contact id"))
		return
	}
	removed, err := h.uc.RemoveStaticMember(c.Request.Context(), orgID, id, contactID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	if !removed {
		c.JSON(http.StatusNotFound, domain.Err("member not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"removed": true}, "error": nil})
}
