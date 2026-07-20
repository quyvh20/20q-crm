package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SystemTemplateHandler serves the industry starter-template catalog and the
// apply endpoint. Reads are open to any member — the picker has to render for
// whoever is setting the workspace up — while Apply is gated at the router by the
// org-settings capability, because it installs schema.
type SystemTemplateHandler struct {
	uc domain.SystemTemplateUseCase
	// schemaInvalidatorSetter is optional; see SetSchemaInvalidator.
	setInvalidator func(func(orgID uuid.UUID))
}

func NewSystemTemplateHandler(uc domain.SystemTemplateUseCase) *SystemTemplateHandler {
	h := &SystemTemplateHandler{uc: uc}
	if s, ok := uc.(interface{ SetSchemaInvalidator(func(uuid.UUID)) }); ok {
		h.setInvalidator = s.SetSchemaInvalidator
	}
	return h
}

// SetSchemaInvalidator forwards the workflow-builder cache buster to the usecase.
// Wired in main.go after the automation handler exists, mirroring how the custom
// object handler receives the same dependency.
func (h *SystemTemplateHandler) SetSchemaInvalidator(fn func(orgID uuid.UUID)) {
	if h.setInvalidator != nil {
		h.setInvalidator(fn)
	}
}

// List handles GET /api/templates
func (h *SystemTemplateHandler) List(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	out, err := h.uc.List(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": out, "error": nil})
}

// ListApplied handles GET /api/templates/applied.
//
// Registered alongside GET /:slug in the same group; gin resolves the static
// segment first, so "applied" can never be read as a slug.
func (h *SystemTemplateHandler) ListApplied(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	out, err := h.uc.ListApplied(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": out, "error": nil})
}

// Get handles GET /api/templates/:slug
func (h *SystemTemplateHandler) Get(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	out, err := h.uc.Get(c.Request.Context(), orgID, c.Param("slug"))
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": out, "error": nil})
}

// Apply handles POST /api/templates/:slug/apply
//
// Returns 200 even for a "partial" result: Phase A (the schema install) having
// succeeded means the customer has a working workspace, and the per-item report
// tells the UI exactly which optional pieces to offer a retry for. Only a Phase A
// rollback — where nothing was installed — surfaces as an error status.
func (h *SystemTemplateHandler) Apply(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	userID, ok := GetUserID(c)
	if !ok {
		// The applying user becomes the security principal of every workflow this
		// installs, so an anonymous apply is not a thing we can safely do.
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	force := c.Query("force") == "true"

	result, err := h.uc.Apply(c.Request.Context(), orgID, userID, c.Param("slug"), force)
	if err != nil {
		// A failed apply still carries its per-item report; send it rather than a
		// bare error so the UI can say WHAT failed.
		if result != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"data": result, "error": "template could not be applied"})
			return
		}
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result, "error": nil})
}
