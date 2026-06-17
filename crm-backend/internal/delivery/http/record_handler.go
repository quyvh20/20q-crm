package http

import (
	"net/http"
	"strconv"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RecordHandler serves the uniform record API (P3): one set of CRUD endpoints
// over every object, system or custom, backed by RecordService. It is mounted
// under /api/registry/objects/:slug/records so it stays strictly additive to the
// legacy per-object routes (custom-object records at /api/objects/:slug/records,
// plus /api/contacts, /api/deals, /api/companies), which remain until P7.
type RecordHandler struct {
	svc domain.RecordService
}

func NewRecordHandler(svc domain.RecordService) *RecordHandler {
	return &RecordHandler{svc: svc}
}

// List handles GET /api/registry/objects/:slug/records
func (h *RecordHandler) List(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "25"))
	page, err := h.svc.List(c.Request.Context(), orgID, slug, domain.RecordListInput{
		Limit:  limit,
		Q:      c.Query("q"),
		Cursor: c.Query("cursor"),
	})
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": page, "error": nil})
}

// Get handles GET /api/registry/objects/:slug/records/:id
func (h *RecordHandler) Get(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	rec, err := h.svc.Get(c.Request.Context(), orgID, slug, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rec, "error": nil})
}

// Create handles POST /api/registry/objects/:slug/records
func (h *RecordHandler) Create(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	userID := c.MustGet("user_id").(uuid.UUID)
	slug := c.Param("slug")

	var input domain.RecordWriteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	rec, err := h.svc.Create(c.Request.Context(), orgID, userID, slug, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": rec, "error": nil})
}

// Update handles PATCH /api/registry/objects/:slug/records/:id
func (h *RecordHandler) Update(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	var input domain.RecordWriteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	rec, err := h.svc.Update(c.Request.Context(), orgID, slug, id, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rec, "error": nil})
}

// Delete handles DELETE /api/registry/objects/:slug/records/:id
func (h *RecordHandler) Delete(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	if err := h.svc.Delete(c.Request.Context(), orgID, slug, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "deleted", "error": nil})
}
