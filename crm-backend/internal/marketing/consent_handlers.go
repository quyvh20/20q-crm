package marketing

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ConsentHandler exposes the admin lawful-basis declaration.
type ConsentHandler struct {
	uc     *ConsentUseCase
	logger *slog.Logger
}

func NewConsentHandler(uc *ConsentUseCase, logger *slog.Logger) *ConsentHandler {
	return &ConsentHandler{uc: uc, logger: logger}
}

// RegisterRoutes mounts under /api/marketing/consent. A STATIC child of
// /api/marketing is required: every child at that level is static today, and gin
// panics at boot if a param child is added alongside them — the same reason
// campaign-estimate and segment-preview exist as separate top-level paths.
func (h *ConsentHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	g := router.Group("/api/marketing/consent")
	g.Use(protected...)
	g.Use(requireCap(domain.CapMarketingManage))
	{
		g.GET("/bases", h.Bases)
		g.POST("/preview", h.Preview)
		g.POST("/grant", h.Grant)
	}
}

func (h *ConsentHandler) fail(c *gin.Context, err error) {
	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		abortErr(c, appErr.Code, appErr.Message)
		return
	}
	if h.logger != nil {
		h.logger.Error("marketing: consent request failed", "error", err)
	}
	abortErr(c, http.StatusInternalServerError, "internal error")
}

type consentGrantRequest struct {
	Basis             string     `json:"basis"`
	Source            string     `json:"source"`
	SegmentIDs        []string   `json:"segment_ids"`
	ExcludeSegmentIDs []string   `json:"exclude_segment_ids"`
	ContactIDs        []string   `json:"contact_ids"`
	CASLExpiresAt     *time.Time `json:"casl_expires_at"`
}

func parseUUIDs(in []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(in))
	for _, s := range in {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, badRequest("invalid id: " + s)
		}
		out = append(out, id)
	}
	return out, nil
}

func (h *ConsentHandler) bind(c *gin.Context) (ConsentGrantInput, bool) {
	var req consentGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return ConsentGrantInput{}, false
	}
	segIDs, err := parseUUIDs(req.SegmentIDs)
	if err != nil {
		h.fail(c, err)
		return ConsentGrantInput{}, false
	}
	exIDs, err := parseUUIDs(req.ExcludeSegmentIDs)
	if err != nil {
		h.fail(c, err)
		return ConsentGrantInput{}, false
	}
	contactIDs, err := parseUUIDs(req.ContactIDs)
	if err != nil {
		h.fail(c, err)
		return ConsentGrantInput{}, false
	}
	return ConsentGrantInput{
		Basis:             req.Basis,
		Source:            req.Source,
		SegmentIDs:        segIDs,
		ExcludeSegmentIDs: exIDs,
		ContactIDs:        contactIDs,
		CASLExpiresAt:     req.CASLExpiresAt,
	}, true
}

// Bases lists the bases an administrator may declare, so the UI picker cannot
// offer one the usecase will reject.
func (h *ConsentHandler) Bases(c *gin.Context) {
	type basis struct {
		Value        string `json:"value"`
		RequiresCASL bool   `json:"requires_casl_expiry"`
	}
	out := make([]basis, 0, 4)
	for _, b := range GrantableConsentBases() {
		out = append(out, basis{Value: b, RequiresCASL: CASLImpliedBasis(b)})
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// Preview reports what a grant would touch, writing nothing.
func (h *ConsentHandler) Preview(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	in, ok := h.bind(c)
	if !ok {
		return
	}
	counts, err := h.uc.Preview(c.Request.Context(), orgID, in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": counts})
}

// Grant records the declaration.
func (h *ConsentHandler) Grant(c *gin.Context) {
	orgID, userID, ok := actorFromCtx(c)
	if !ok {
		return
	}
	in, ok := h.bind(c)
	if !ok {
		return
	}
	counts, err := h.uc.Grant(c.Request.Context(), orgID, userID, in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": counts})
}
