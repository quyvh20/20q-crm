package http

import (
	"net/http"
	"strconv"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SearchHandler serves global, cross-object search (P6): one endpoint that spans
// every searchable object and returns OLS/FLS-safe results grouped by object,
// backed by SearchUseCase. Mounted under /api/registry/search so it stays additive
// alongside the legacy per-object search (e.g. /api/contacts?semantic=true);
// promoted to /api/search at the P7 cutover.
type SearchHandler struct {
	uc domain.SearchUseCase
}

func NewSearchHandler(uc domain.SearchUseCase) *SearchHandler {
	return &SearchHandler{uc: uc}
}

// Search handles GET /api/registry/search?q=...&limit=...
// No coarse role gate: results are filtered per object by OLS inside SearchUseCase,
// so each caller only ever sees what they may read.
func (h *SearchHandler) Search(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	res, err := h.uc.Search(c.Request.Context(), orgID, c.Query("q"), limit)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res, "error": nil})
}
