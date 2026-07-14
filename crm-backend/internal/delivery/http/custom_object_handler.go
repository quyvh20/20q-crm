package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CustomObjectEventEmitter fires automation triggers for custom object events.
type CustomObjectEventEmitter func(ctx context.Context, orgID uuid.UUID, eventType string, payload map[string]any)

type CustomObjectHandler struct {
	uc               domain.CustomObjectUseCase
	invalidateSchema SchemaInvalidator
	emitEvent        CustomObjectEventEmitter
}

func NewCustomObjectHandler(uc domain.CustomObjectUseCase) *CustomObjectHandler {
	return &CustomObjectHandler{uc: uc}
}

// SetEventEmitter wires the automation trigger callback for custom object events.
func (h *CustomObjectHandler) SetEventEmitter(fn CustomObjectEventEmitter) {
	h.emitEvent = fn
}

// SetSchemaInvalidator sets the callback to invalidate the workflow schema cache.
func (h *CustomObjectHandler) SetSchemaInvalidator(fn SchemaInvalidator) {
	h.invalidateSchema = fn
}

func (h *CustomObjectHandler) invalidateSchemaIfSet(orgID uuid.UUID) {
	if h.invalidateSchema != nil {
		h.invalidateSchema(orgID)
	}
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
	h.invalidateSchemaIfSet(orgID)
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
	h.invalidateSchemaIfSet(orgID)
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
	h.invalidateSchemaIfSet(orgID)
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
	rec, err := h.uc.GetRecord(c.Request.Context(), orgID, c.Param("slug"), id)
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

	// Fire automation trigger asynchronously
	if h.emitEvent != nil {
		eventType := slug + "_created"
		payload := h.buildRecordPayload(rec, slug, eventType)
		go h.emitEvent(context.Background(), orgID, eventType, payload)
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

	// Fire automation trigger asynchronously
	if h.emitEvent != nil {
		eventType := slug + "_updated"
		payload := h.buildRecordPayload(rec, slug, eventType)
		go h.emitEvent(context.Background(), orgID, eventType, payload)
	}

	c.JSON(http.StatusOK, gin.H{"data": rec, "error": nil})
}

// buildRecordPayload constructs the trigger payload for custom object events.
// The record's data fields are flattened under the slug key for condition resolution.
func (h *CustomObjectHandler) buildRecordPayload(rec *domain.CustomObjectRecord, slug, eventType string) map[string]any {
	recordData := map[string]any{
		"id":           rec.ID.String(),
		"display_name": rec.DisplayName,
	}

	// Flatten Data JSONB fields into the record map
	if rec.Data != nil {
		var dataMap map[string]any
		if err := json.Unmarshal(rec.Data, &dataMap); err == nil {
			for k, v := range dataMap {
				recordData[k] = v
			}
		}
	}

	return map[string]any{
		"entity_id": rec.ID.String(),
		slug:        recordData,
		"trigger": map[string]any{
			"type":   eventType,
			"source": "crm_ui",
		},
	}
}

// DeleteRecord handles DELETE /api/objects/:slug/records/:id
func (h *CustomObjectHandler) DeleteRecord(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	if err := h.uc.DeleteRecord(c.Request.Context(), orgID, c.Param("slug"), id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "deleted", "error": nil})
}
