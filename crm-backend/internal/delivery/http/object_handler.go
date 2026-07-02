package http

import (
	"context"
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

	foldLayout(c.Request.Context(), h.layoutUC, h.authz, orgID, slug, descriptor)

	c.JSON(http.StatusOK, gin.H{"data": descriptor, "error": nil})
}

// foldLayout resolves the caller's effective detail layout and folds it into the
// descriptor (P8) — shared by the schema endpoint and the composite record-page
// endpoint so both serve the identical schema shape. Best-effort: a nil layout
// usecase, a caller-less context, or a resolve error all simply leave the layout
// absent, and the frontend falls back to flat field order.
func foldLayout(ctx context.Context, layoutUC domain.ObjectLayoutUseCase, authz domain.RecordAuthorizer, orgID uuid.UUID, slug string, descriptor *domain.ObjectDescriptor) {
	if layoutUC == nil || descriptor == nil {
		return
	}
	caller, ok := domain.CallerFromContext(ctx)
	if !ok {
		return
	}
	var hiddenKeys map[string]bool
	if authz != nil {
		hiddenKeys = authz.FieldMask(ctx, orgID, slug).Hidden
	}
	if sections, err := layoutUC.ResolveLayout(ctx, orgID, slug, caller.Role, hiddenKeys); err == nil {
		descriptor.Layout = sections
	}
}

// setNumberPrefixBody is the SetNumberPrefix request payload.
type setNumberPrefixBody struct {
	NumberPrefix string `json:"number_prefix"`
}

// SetNumberPrefix handles PUT /api/registry/objects/:slug/number-prefix (admin).
// It updates the object's record-number label prefix (e.g. "INV" → INV-0001); an
// empty prefix resets to the slug default.
func (h *ObjectRegistryHandler) SetNumberPrefix(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	var body setNumberPrefixBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	if err := h.uc.SetNumberPrefix(c.Request.Context(), orgID, slug, body.NumberPrefix); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "updated", "error": nil})
}
