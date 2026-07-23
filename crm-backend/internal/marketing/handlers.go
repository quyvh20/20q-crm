package marketing

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"crm-backend/internal/domain"
	"crm-backend/internal/emailutil"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler serves the marketing management API. M1 exposes only the suppression &
// consent surface: list/add/remove suppressions and a per-email status lookup.
//
// Unlike integrations.NewHandler, this constructor takes NO RecordAuthorizer and
// does not panic on nil: M1 handlers write only to the marketing tables
// (org-scoped, capability-gated), never to CRM records through the callerless
// path — so there is no object-level-security re-check to protect. When a later
// phase adds a record-writing handler, it must take an authorizer then.
type Handler struct {
	repo   *Repository
	guard  *SuppressionGuard
	logger *slog.Logger
}

// NewHandler builds the marketing handler.
func NewHandler(repo *Repository, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{repo: repo, guard: NewSuppressionGuard(repo), logger: logger}
}

// RegisterRoutes mounts the marketing management API under /api/marketing, gated
// by marketing.manage. `protected` is the FULL protected-group stack (mirror the
// integrations shape, NOT automation's single-middleware signature) so these
// routes inherit personal-access-token auth + the workspace 2FA policy.
func (h *Handler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	g := router.Group("/api/marketing")
	g.Use(protected...)
	g.Use(requireCap(domain.CapMarketingManage))
	{
		g.GET("/suppressions", h.ListSuppressions)
		g.POST("/suppressions", h.AddSuppression)
		g.DELETE("/suppressions/:id", h.RemoveSuppression)
		// Per-email marketing status for the contact-detail badge. POST (not GET with
		// an ?email= param) so the address never lands in a URL/query string or an
		// access log.
		g.POST("/status", h.MarketingStatus)
	}
}

// ── requests / responses ─────────────────────────────────────────────────────

type addSuppressionRequest struct {
	Email   string  `json:"email"`
	Reason  string  `json:"reason"`  // defaults to "manual"
	Scope   string  `json:"scope"`   // defaults from reason
	Source  string  `json:"source"`  // free-text provenance
	TopicID *string `json:"topic_id"`
}

type statusRequest struct {
	Email string `json:"email"`
}

// ── handlers ─────────────────────────────────────────────────────────────────

// ListSuppressions returns the org's suppression ledger, filterable by email
// substring and reason.
func (h *Handler) ListSuppressions(c *gin.Context) {
	orgID, _, ok := h.actor(c)
	if !ok {
		return
	}
	q := c.Query("q")
	reason := c.Query("reason")
	if reason != "" && !IsValidReason(reason) {
		h.mgmtError(c, http.StatusBadRequest, "unknown reason filter: "+reason)
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))

	rows, total, err := h.repo.ListSuppressions(c.Request.Context(), orgID, q, reason, limit, offset)
	if err != nil {
		h.logger.Error("marketing: list suppressions failed", "error", err, "org_id", orgID.String())
		h.mgmtError(c, http.StatusInternalServerError, "could not list suppressions")
		return
	}
	if rows == nil {
		rows = []Suppression{} // never serialize null for an array (FE ?? [] guard)
	}
	c.JSON(http.StatusOK, gin.H{"data": rows, "meta": gin.H{"total": total}})
}

// AddSuppression manually adds an address to the ledger (immediate). Reason
// defaults to "manual"; an admin may also pick "unsubscribe"/"complaint"/etc.
func (h *Handler) AddSuppression(c *gin.Context) {
	orgID, userID, ok := h.actor(c)
	if !ok {
		return
	}
	var req addSuppressionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.mgmtError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	email := emailutil.Normalize(req.Email)
	if email == "" {
		h.mgmtError(c, http.StatusBadRequest, "email is required")
		return
	}
	reason := req.Reason
	if reason == "" {
		reason = ReasonManual
	}
	if !IsValidReason(reason) {
		h.mgmtError(c, http.StatusBadRequest, "unknown reason: "+reason)
		return
	}
	scope := req.Scope
	if scope != "" && !IsValidScope(scope) {
		h.mgmtError(c, http.StatusBadRequest, "unknown scope: "+scope)
		return
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "manual_admin"
	}

	row := &Suppression{
		OrgID:           orgID,
		EmailNormalized: email,
		Reason:          reason,
		Scope:           scope, // repo fills the default from reason when empty
		Source:          source,
	}
	if req.TopicID != nil && strings.TrimSpace(*req.TopicID) != "" {
		tid, err := uuid.Parse(strings.TrimSpace(*req.TopicID))
		if err != nil {
			h.mgmtError(c, http.StatusBadRequest, "invalid topic_id")
			return
		}
		row.TopicID = &tid
	}

	inserted, err := h.repo.AddSuppression(c.Request.Context(), row)
	if err != nil {
		h.logger.Error("marketing: add suppression failed", "error", err, "org_id", orgID.String())
		h.mgmtError(c, http.StatusInternalServerError, "could not add suppression")
		return
	}
	if !inserted {
		// ON CONFLICT DO NOTHING inserts nothing, so `row` never received its
		// DB-generated id/created_at — returning it as-is would serialize a nil id and
		// epoch timestamp. Re-read the existing suppression that matched the dedupe key
		// so the idempotent response carries the real row.
		if existing, e := h.repo.SuppressionsForEmail(c.Request.Context(), orgID, email); e == nil {
			for i := range existing {
				if existing[i].Reason == reason && sameTopic(existing[i].TopicID, row.TopicID) {
					row = &existing[i]
					break
				}
			}
		}
	}
	// Audit trail: a manual suppression change is a deliberate admin action.
	h.logger.Info("marketing: suppression added",
		"org_id", orgID.String(), "actor", userID.String(),
		"email", email, "reason", reason, "scope", row.Scope, "inserted", inserted)

	status := http.StatusCreated
	if !inserted {
		status = http.StatusOK // already suppressed — idempotent no-op
	}
	c.JSON(status, gin.H{"data": row, "already_suppressed": !inserted})
}

// RemoveSuppression deletes one suppression (a deliberate, logged admin action —
// distinct from contact deletion, which never touches this table).
func (h *Handler) RemoveSuppression(c *gin.Context) {
	orgID, userID, ok := h.actor(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.mgmtError(c, http.StatusBadRequest, "invalid suppression id")
		return
	}
	removed, err := h.repo.RemoveSuppression(c.Request.Context(), orgID, id)
	if err != nil {
		h.logger.Error("marketing: remove suppression failed", "error", err, "org_id", orgID.String())
		h.mgmtError(c, http.StatusInternalServerError, "could not remove suppression")
		return
	}
	if !removed {
		h.mgmtError(c, http.StatusNotFound, "suppression not found")
		return
	}
	h.logger.Info("marketing: suppression removed",
		"org_id", orgID.String(), "actor", userID.String(), "suppression_id", id.String())
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"removed": true}})
}

// MarketingStatus reports an email's current marketing standing for the contact
// badge: its lifecycle status, consent basis, whether it is suppressed (with the
// reasons), and whether a marketing send would currently be permitted.
func (h *Handler) MarketingStatus(c *gin.Context) {
	orgID, _, ok := h.actor(c)
	if !ok {
		return
	}
	var req statusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.mgmtError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	email := emailutil.Normalize(req.Email)
	if email == "" {
		h.mgmtError(c, http.StatusBadRequest, "email is required")
		return
	}

	sups, err := h.repo.SuppressionsForEmail(c.Request.Context(), orgID, email)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not read suppressions")
		return
	}
	state, err := h.repo.MarketingStateForEmail(c.Request.Context(), orgID, email)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not read marketing state")
		return
	}

	reasons := make([]string, 0, len(sups))
	suppressed := false
	for _, s := range sups {
		if s.Suppresses(ChannelMarketing, nil) {
			suppressed = true
			reasons = append(reasons, s.Reason)
		}
	}
	verdict := h.guard.IsSendable(c.Request.Context(), orgID, email, ChannelMarketing, nil)

	resp := gin.H{
		"email":               email,
		"suppressed":          suppressed,
		"suppression_reasons": reasons,
		"sendable_marketing":  verdict.Sendable,
		"not_sendable_reason": verdict.Reason,
		"marketing_status":    "",
		"consent_basis":       "",
	}
	if state != nil {
		resp["marketing_status"] = state.MarketingStatus
		if state.ConsentBasis != nil {
			resp["consent_basis"] = *state.ConsentBasis
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// ── helpers (mirror integrations' actor/mgmtError) ───────────────────────────

func (h *Handler) actor(c *gin.Context) (orgID, userID uuid.UUID, ok bool) {
	o, exists := c.Get("org_id")
	if !exists {
		h.mgmtError(c, http.StatusUnauthorized, "unauthorized")
		return uuid.Nil, uuid.Nil, false
	}
	u, _ := c.Get("user_id")
	orgID, _ = o.(uuid.UUID)
	userID, _ = u.(uuid.UUID)
	if orgID == uuid.Nil {
		h.mgmtError(c, http.StatusUnauthorized, "unauthorized")
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, true
}

func (h *Handler) mgmtError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

// sameTopic reports whether two optional topic ids refer to the same topic
// (both nil = the same global scope).
func sameTopic(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
