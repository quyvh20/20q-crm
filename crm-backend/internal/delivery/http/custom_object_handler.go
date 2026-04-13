package http

import (
	"net/http"
	"strconv"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type CustomObjectHandler struct {
	uc domain.CustomObjectUseCase
}

func NewCustomObjectHandler(uc domain.CustomObjectUseCase) *CustomObjectHandler {
	return &CustomObjectHandler{uc: uc}
}

// ============================================================
// Definitions
// ============================================================

// ListDefs handles GET /api/objects
func (h *CustomObjectHandler) ListDefs(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	defs, err := h.uc.ListDefs(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": defs, "error": nil})
}

// GetDef handles GET /api/objects/:slug
func (h *CustomObjectHandler) GetDef(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	def, err := h.uc.GetDefBySlug(c.Request.Context(), orgID, slug)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": def, "error": nil})
}

// CreateDef handles POST /api/objects
func (h *CustomObjectHandler) CreateDef(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	var input domain.CreateObjectDefInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	def, err := h.uc.CreateDef(c.Request.Context(), orgID, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": def, "error": nil})
}

// UpdateDef handles PUT /api/objects/:slug
func (h *CustomObjectHandler) UpdateDef(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	var input domain.UpdateObjectDefInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	def, err := h.uc.UpdateDef(c.Request.Context(), orgID, slug, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": def, "error": nil})
}

// DeleteDef handles DELETE /api/objects/:slug
func (h *CustomObjectHandler) DeleteDef(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	if err := h.uc.DeleteDef(c.Request.Context(), orgID, slug); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "deleted", "error": nil})
}

// ============================================================
// Records
// ============================================================

// ListRecords handles GET /api/objects/:slug/records
func (h *CustomObjectHandler) ListRecords(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "25"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	q := c.Query("q")

	records, total, err := h.uc.ListRecords(c.Request.Context(), orgID, slug, domain.RecordFilter{
		Limit:  limit,
		Offset: offset,
		Q:      q,
	})
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":  records,
		"total": total,
		"error": nil,
	})
}

// GetRecord handles GET /api/objects/:slug/records/:id
func (h *CustomObjectHandler) GetRecord(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	rec, err := h.uc.GetRecord(c.Request.Context(), orgID, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rec, "error": nil})
}

// CreateRecord handles POST /api/objects/:slug/records
func (h *CustomObjectHandler) CreateRecord(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	userID := c.MustGet("user_id").(uuid.UUID)
	slug := c.Param("slug")

	var input domain.CreateRecordInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}

	rec, err := h.uc.CreateRecord(c.Request.Context(), orgID, userID, slug, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": rec, "error": nil})
}

// UpdateRecord handles PUT /api/objects/:slug/records/:id
func (h *CustomObjectHandler) UpdateRecord(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}

	var input domain.UpdateRecordInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}

	rec, err := h.uc.UpdateRecord(c.Request.Context(), orgID, slug, id, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rec, "error": nil})
}

// DeleteRecord handles DELETE /api/objects/:slug/records/:id
func (h *CustomObjectHandler) DeleteRecord(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	if err := h.uc.DeleteRecord(c.Request.Context(), orgID, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "deleted", "error": nil})
}
