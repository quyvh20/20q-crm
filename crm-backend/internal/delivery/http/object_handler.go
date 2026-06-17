package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ObjectRegistryHandler serves the read-only Object Registry (P2): a uniform
// view over every object — system (contact/deal/company) and custom — assembled
// from the existing storage. It is mounted under /api/registry/objects so it is
// strictly additive to the live custom-object routes at /api/objects, which stay
// until P7 retires them.
type ObjectRegistryHandler struct {
	uc domain.ObjectRegistryUseCase
}

func NewObjectRegistryHandler(uc domain.ObjectRegistryUseCase) *ObjectRegistryHandler {
	return &ObjectRegistryHandler{uc: uc}
}

// ListObjects handles GET /api/registry/objects
func (h *ObjectRegistryHandler) ListObjects(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	objects, err := h.uc.ListObjects(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": objects, "error": nil})
}

// GetSchema handles GET /api/registry/objects/:slug/schema
func (h *ObjectRegistryHandler) GetSchema(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	descriptor, err := h.uc.GetSchema(c.Request.Context(), orgID, slug)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": descriptor, "error": nil})
}
