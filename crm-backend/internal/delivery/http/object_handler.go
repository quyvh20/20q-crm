package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ObjectRegistryHandler serves the read-only Object Registry (P2): a uniform
// view over every object — system (contact/deal/company) and custom — assembled
// from object_defs/object_fields (now the sole store after the P7 convergence).
//
// As of P8, GetSchema also folds the caller's effective detail layout into the
// response, so the frontend needs no separate fetch to render a sectioned page.
type ObjectRegistryHandler struct {
	uc       domain.ObjectRegistryUseCase
	layoutUC domain.ObjectLayoutUseCase // nil when layouts not yet configured
	authz    domain.RecordAuthorizer    // nil when running without OLS/FLS
}

func NewObjectRegistryHandler(
	uc domain.ObjectRegistryUseCase,
	layoutUC domain.ObjectLayoutUseCase,
	authz domain.RecordAuthorizer,
) *ObjectRegistryHandler {
	return &ObjectRegistryHandler{uc: uc, layoutUC: layoutUC, authz: authz}
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

// GetSchema handles GET /api/registry/objects/:slug/schema.
//
// P8 addition: after assembling the descriptor it resolves the caller's effective
// detail layout (3-tier: role-assigned → default → nil) and folds it into the
// response as descriptor.layout. Hidden fields (FLS) are stripped from the layout
// sections using the same mask RecordService applies. An empty/absent layout field
// means "no layout configured; use flat field order" — backward-compatible.
func (h *ObjectRegistryHandler) GetSchema(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")

	descriptor, err := h.uc.GetSchema(c.Request.Context(), orgID, slug)
	if err != nil {
		handleAppError(c, err)
		return
	}

	// P8 layout fold — only when layouts are wired.
	if h.layoutUC != nil {
		if caller, ok := domain.CallerFromContext(c.Request.Context()); ok {
			var hiddenKeys map[string]bool
			if h.authz != nil {
				mask := h.authz.FieldMask(c.Request.Context(), orgID, slug)
				hiddenKeys = mask.Hidden
			}
			// Best-effort: a cache miss or DB error is non-fatal — the frontend
			// falls back to field order, the same as if no layout existed.
			if sections, resolveErr := h.layoutUC.ResolveLayout(
				c.Request.Context(), orgID, slug, caller.Role, hiddenKeys,
			); resolveErr == nil {
				descriptor.Layout = sections
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": descriptor, "error": nil})
}
