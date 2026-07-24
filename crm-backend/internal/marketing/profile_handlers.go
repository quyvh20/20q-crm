package marketing

import (
	"log/slog"
	"net/http"
	"strings"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ProfileHandler serves the marketing sender-profile + topics management API (M3),
// mounted under /api/marketing and gated by marketing.manage. Like the other
// marketing handlers it writes only org-scoped marketing tables, so it takes no
// RecordAuthorizer.
type ProfileHandler struct {
	repo   *Repository
	logger *slog.Logger
}

// NewProfileHandler builds the handler.
func NewProfileHandler(repo *Repository, logger *slog.Logger) *ProfileHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ProfileHandler{repo: repo, logger: logger}
}

// RegisterRoutes mounts the profile + topics routes under the protected +
// marketing.manage group. These paths are all static children of /api/marketing, so
// they coexist with the M1/M2/M6 routes and the public /api/marketing/u/:token
// route without a router conflict.
func (h *ProfileHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	g := router.Group("/api/marketing")
	g.Use(protected...)
	g.Use(requireCap(domain.CapMarketingManage))
	{
		g.GET("/sender-profile", h.GetProfile)
		g.PUT("/sender-profile", h.UpsertProfile)
		g.POST("/sender-profile/resume", h.ResumeMarketing)
		g.GET("/topics", h.ListTopics)
		g.POST("/topics", h.CreateTopic)
		g.PUT("/topics/:id", h.UpdateTopic)
		g.DELETE("/topics/:id", h.DeleteTopic)
	}
}

// ── sender profile ───────────────────────────────────────────────────────────

type profileRequest struct {
	FromName              string `json:"from_name"`
	ReplyTo               string `json:"reply_to"`
	PhysicalPostalAddress string `json:"physical_postal_address"`
}

// profileResponse adds a derived sendability verdict so the UI can show the
// "complete this before sending" banner without reimplementing the gate.
func profileResponse(p *OrgMarketingProfile) gin.H {
	sendable, reason := SenderProfileSendable(p)
	out := gin.H{
		"from_name":               "",
		"reply_to":                "",
		"physical_postal_address": "",
		"marketing_paused":        false,
		"sendable":                sendable,
		"reason":                  reason,
	}
	if p != nil {
		out["from_name"] = p.FromName
		out["reply_to"] = p.ReplyTo
		out["physical_postal_address"] = p.PhysicalPostalAddress
		out["marketing_paused"] = p.MarketingPaused
	}
	return out
}

// GetProfile returns the org's sender profile (a zeroed, not-sendable shape when
// none has been saved).
func (h *ProfileHandler) GetProfile(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	p, err := h.repo.GetProfile(c.Request.Context(), orgID)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not load the sender profile")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": profileResponse(p)})
}

// UpsertProfile saves the org's sender identity + postal address. It never changes
// marketing_paused (that is the M4 breaker's target — see ResumeMarketing).
func (h *ProfileHandler) UpsertProfile(c *gin.Context) {
	orgID, userID, ok := actorFromCtx(c)
	if !ok {
		return
	}
	var req profileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	p := &OrgMarketingProfile{
		OrgID:                 orgID,
		FromName:              strings.TrimSpace(req.FromName),
		ReplyTo:               strings.TrimSpace(req.ReplyTo),
		PhysicalPostalAddress: strings.TrimSpace(req.PhysicalPostalAddress),
	}
	if err := h.repo.UpsertProfile(c.Request.Context(), p); err != nil {
		h.logger.Error("marketing: upsert sender profile failed", "error", err, "org_id", orgID.String())
		abortErr(c, http.StatusInternalServerError, "could not save the sender profile")
		return
	}
	// Re-read so the response carries marketing_paused + the derived verdict.
	saved, err := h.repo.GetProfile(c.Request.Context(), orgID)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not reload the sender profile")
		return
	}
	h.logger.Info("marketing: sender profile saved", "org_id", orgID.String(), "actor", userID.String())
	c.JSON(http.StatusOK, gin.H{"data": profileResponse(saved)})
}

// ResumeMarketing clears the marketing_paused flag (an admin resuming after an M4
// auto-pause). It is a deliberate, logged action, separate from saving the profile.
func (h *ProfileHandler) ResumeMarketing(c *gin.Context) {
	orgID, userID, ok := actorFromCtx(c)
	if !ok {
		return
	}
	if err := h.repo.SetMarketingPaused(c.Request.Context(), orgID, false); err != nil {
		abortErr(c, http.StatusInternalServerError, "could not resume marketing")
		return
	}
	h.logger.Info("marketing: marketing lane resumed", "org_id", orgID.String(), "actor", userID.String())
	saved, _ := h.repo.GetProfile(c.Request.Context(), orgID)
	c.JSON(http.StatusOK, gin.H{"data": profileResponse(saved)})
}

// ── topics ───────────────────────────────────────────────────────────────────

type topicRequest struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	OptInDefault bool   `json:"opt_in_default"`
}

// ListTopics returns the org's topics.
func (h *ProfileHandler) ListTopics(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	rows, err := h.repo.ListTopicsByOrg(c.Request.Context(), orgID)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not list topics")
		return
	}
	if rows == nil {
		rows = []MarketingTopic{}
	}
	c.JSON(http.StatusOK, gin.H{"data": rows})
}

// CreateTopic adds a topic. opt_in_default is set once here and is immutable after.
func (h *ProfileHandler) CreateTopic(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	var req topicRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		abortErr(c, http.StatusBadRequest, "topic name is required")
		return
	}
	t := &MarketingTopic{
		OrgID:        orgID,
		Name:         name,
		Description:  strings.TrimSpace(req.Description),
		OptInDefault: req.OptInDefault,
	}
	if err := h.repo.CreateTopic(c.Request.Context(), t); err != nil {
		if isUniqueViolation(err) {
			abortErr(c, http.StatusConflict, "a topic with that name already exists")
			return
		}
		h.logger.Error("marketing: create topic failed", "error", err, "org_id", orgID.String())
		abortErr(c, http.StatusInternalServerError, "could not create the topic")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": t})
}

// UpdateTopic edits a topic's name/description. opt_in_default is IMMUTABLE and is
// never changed here (the request field, if sent, is ignored).
func (h *ProfileHandler) UpdateTopic(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid topic id")
		return
	}
	var req topicRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		abortErr(c, http.StatusBadRequest, "topic name is required")
		return
	}
	updated, err := h.repo.UpdateTopicMeta(c.Request.Context(), orgID, id, name, strings.TrimSpace(req.Description))
	if err != nil {
		if isUniqueViolation(err) {
			abortErr(c, http.StatusConflict, "a topic with that name already exists")
			return
		}
		abortErr(c, http.StatusInternalServerError, "could not update the topic")
		return
	}
	if !updated {
		abortErr(c, http.StatusNotFound, "topic not found")
		return
	}
	t, _ := h.repo.GetTopicByID(c.Request.Context(), orgID, id)
	c.JSON(http.StatusOK, gin.H{"data": t})
}

// DeleteTopic removes a topic (hard delete — topics are not soft-deletable).
func (h *ProfileHandler) DeleteTopic(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid topic id")
		return
	}
	removed, err := h.repo.DeleteTopic(c.Request.Context(), orgID, id)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not delete the topic")
		return
	}
	if !removed {
		abortErr(c, http.StatusNotFound, "topic not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"removed": true}})
}
