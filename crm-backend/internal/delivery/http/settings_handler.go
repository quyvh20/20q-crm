package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type SettingsHandler struct {
	settingsUC       domain.OrgSettingsUseCase
	invalidateSchema SchemaInvalidator
}

func NewSettingsHandler(uc domain.OrgSettingsUseCase) *SettingsHandler {
	return &SettingsHandler{settingsUC: uc}
}

// SetSchemaInvalidator sets the callback to invalidate the workflow schema cache.
func (h *SettingsHandler) SetSchemaInvalidator(fn SchemaInvalidator) {
	h.invalidateSchema = fn
}

func (h *SettingsHandler) invalidateSchemaIfSet(orgID uuid.UUID) {
	if h.invalidateSchema != nil {
		h.invalidateSchema(orgID)
	}
}

// ListFieldDefs returns custom field definitions, optionally filtered by entity_type.
// GET /api/settings/fields?entity_type=contact
func (h *SettingsHandler) ListFieldDefs(c *gin.Context) {
	orgID, _ := c.Get("org_id")
	entityType := c.Query("entity_type")

	defs, err := h.settingsUC.GetFieldDefs(c.Request.Context(), orgID.(uuid.UUID), entityType)
	if err != nil {
		handleAppError(c, err)
		return
	}
	if defs == nil {
		defs = []domain.CustomFieldDef{}
	}

	c.JSON(http.StatusOK, gin.H{"data": defs})
}

// CreateFieldDef creates a new custom field definition.
// POST /api/settings/fields
func (h *SettingsHandler) CreateFieldDef(c *gin.Context) {
	orgID, _ := c.Get("org_id")

	var input domain.CreateFieldDefInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	def, err := h.settingsUC.CreateFieldDef(c.Request.Context(), orgID.(uuid.UUID), input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	h.invalidateSchemaIfSet(orgID.(uuid.UUID))
	c.JSON(http.StatusCreated, gin.H{"data": def})
}

// UpdateFieldDef updates an existing custom field definition by key.
// PUT /api/settings/fields/:key
func (h *SettingsHandler) UpdateFieldDef(c *gin.Context) {
	orgID, _ := c.Get("org_id")
	key := c.Param("key")

	var input domain.UpdateFieldDefInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	def, err := h.settingsUC.UpdateFieldDef(c.Request.Context(), orgID.(uuid.UUID), key, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	h.invalidateSchemaIfSet(orgID.(uuid.UUID))
	c.JSON(http.StatusOK, gin.H{"data": def})
}

// DeleteFieldDef removes a custom field definition by key.
// DELETE /api/settings/fields/:key
func (h *SettingsHandler) DeleteFieldDef(c *gin.Context) {
	orgID, _ := c.Get("org_id")
	key := c.Param("key")

	err := h.settingsUC.DeleteFieldDef(c.Request.Context(), orgID.(uuid.UUID), key)
	if err != nil {
		handleAppError(c, err)
		return
	}
	h.invalidateSchemaIfSet(orgID.(uuid.UUID))
	c.JSON(http.StatusOK, gin.H{"message": "field definition deleted"})
}



