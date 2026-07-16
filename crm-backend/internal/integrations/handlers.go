package integrations

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// captureBodyLimit caps an inbound lead body. A lead is a handful of form fields;
// anything larger is a mistake or an attack.
const captureBodyLimit = 256 * 1024

// Handler serves the integrations module's own routes — both the capability-gated
// management API and the public capture endpoint.
type Handler struct {
	repo    *Repository
	ingest  *LeadIngestService
	authz   domain.RecordAuthorizer
	limiter *RateLimiter
	logger  *slog.Logger
}

// NewHandler builds the handler. A nil authorizer panics rather than degrading:
// the source-save OLS re-check is the only thing stopping integrations.manage from
// becoming an org-wide write primitive, so it must not be optional.
func NewHandler(repo *Repository, ingest *LeadIngestService, authz domain.RecordAuthorizer, limiter *RateLimiter, logger *slog.Logger) *Handler {
	if authz == nil {
		panic("integrations: authorizer is required — the source-save OLS check is a security control")
	}
	return &Handler{repo: repo, ingest: ingest, authz: authz, limiter: limiter, logger: logger}
}

// RegisterRoutes mounts the module's routes.
//
// `protected` is the FULL protected-group stack, taken as a slice rather than a
// single middleware on purpose. The automation precedent takes one gin.HandlerFunc
// and main.go hands it the plain session middleware — so automation's management
// routes reject PATs and skip the workspace 2FA policy. That signature makes the
// bug unfixable without changing it; a slice makes the correct stack one value
// main.go builds once and hands over whole.
func (h *Handler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	// Public: no auth middleware. Authenticates itself with the source credential.
	router.POST("/api/capture/leads", h.CaptureLead)

	g := router.Group("/api/integrations")
	g.Use(protected...)
	g.Use(requireCap(domain.CapIntegrationsManage))
	{
		g.GET("/sources", h.ListSources)
		g.POST("/sources", h.CreateSource)
		g.GET("/sources/:id", h.GetSource)
		g.PATCH("/sources/:id", h.UpdateSource)
		g.DELETE("/sources/:id", h.DeleteSource)
		g.POST("/sources/:id/rotate-key", h.RotateKey)
		g.GET("/sources/:id/events", h.ListEvents)
	}
}

// ── Public capture ───────────────────────────────────────────────────────────

type captureRequest struct {
	Fields  map[string]any `json:"fields"`
	Context map[string]any `json:"context"`
	Consent map[string]any `json:"consent"`
}

type captureResponse struct {
	RecordID string `json:"record_id"`
	Outcome  string `json:"outcome"`
	EventID  string `json:"event_id"`
	// Quarantined names keys the payload sent that were not written. Returned so an
	// integrator finds out at integration time, not from missing data weeks later.
	Quarantined []string `json:"quarantined,omitempty"`
}

// CaptureLead is the public port: POST /api/capture/leads, Bearer crm_lead_…
func (h *Handler) CaptureLead(c *gin.Context) {
	key := bearerToken(c)
	if !IsLeadKey(key) {
		h.captureError(c, http.StatusUnauthorized, "invalid capture key")
		return
	}
	hash := HashLeadKey(key)

	// Throttle BEFORE the DB probe. WebhookInbound looks up its token first, which
	// makes every unauthenticated request a free query — an invalid-key flood is
	// then a DB amplifier. The key hash is derivable without touching the DB, so
	// the limit can precede the lookup; the client IP is limited too, or an
	// attacker rotating random keys gets a fresh bucket per request.
	if !h.allow(c, "k:"+hash) || !h.allow(c, "ip:"+c.ClientIP()) {
		return // allow() has already written 429 + Retry-After
	}

	source, err := h.repo.FindSourceByTokenHash(c.Request.Context(), hash)
	if err != nil {
		h.logger.Error("integrations: source lookup failed", "error", err)
		h.captureError(c, http.StatusInternalServerError, "lookup failed")
		return
	}
	// One message for unknown/revoked/disabled alike: which of the three it is, is
	// not the caller's business.
	if source == nil || !source.IsLive() {
		h.captureError(c, http.StatusUnauthorized, "invalid capture key")
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, captureBodyLimit))
	if err != nil {
		h.captureError(c, http.StatusBadRequest, "could not read body")
		return
	}
	var req captureRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.captureError(c, http.StatusBadRequest, "body must be a JSON object")
		return
	}
	if len(req.Fields) == 0 {
		h.captureError(c, http.StatusUnprocessableEntity, "fields is required")
		return
	}

	// The daily cap is the backstop that survives both limiters being wrong.
	if source.DailyCap > 0 {
		n, err := h.repo.CountCreatedToday(c.Request.Context(), source.ID, time.Now())
		if err == nil && n >= int64(source.DailyCap) {
			c.Header("Retry-After", "3600")
			h.captureError(c, http.StatusTooManyRequests, "daily capture limit reached for this source")
			return
		}
	}

	lead := RawLead{
		Fields:  req.Fields,
		Context: req.Context,
		Consent: req.Consent,
		// Namespaced per source by the dedupe index. IsTest is NOT read from the
		// payload: a caller who could set it would be able to file real-looking
		// leads that never page sales.
		ProviderEventID: strings.TrimSpace(c.GetHeader("Idempotency-Key")),
	}

	res, err := h.ingest.Ingest(c.Request.Context(), source, lead)
	if err != nil {
		if appErr, ok := err.(*domain.AppError); ok {
			h.captureError(c, appErr.Code, appErr.Message)
			return
		}
		h.logger.Error("integrations: ingest failed", "error", err, "source_id", source.ID.String())
		// 500 is safe to retry: the delivery is deduped by Idempotency-Key.
		h.captureError(c, http.StatusInternalServerError, "could not process lead")
		return
	}

	out := captureResponse{Outcome: res.Outcome, EventID: res.EventID.String()}
	if res.RecordID != uuid.Nil {
		out.RecordID = res.RecordID.String()
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// allow applies one rate-limit key, writing 429 + Retry-After when exceeded.
func (h *Handler) allow(c *gin.Context, key string) bool {
	ok, retry := h.limiter.Allow(c.Request.Context(), key)
	if ok {
		return true
	}
	secs := int(retry.Seconds())
	if secs < 1 {
		secs = 1
	}
	c.Header("Retry-After", strconv.Itoa(secs))
	h.captureError(c, http.StatusTooManyRequests, "rate limit exceeded")
	return false
}

// captureError writes the public endpoint's error envelope.
//
// A local shape rather than domain.Err: handleAppError is unexported in
// delivery/http and unreachable from here, and this endpoint's audience is curl,
// Make and Zapier — never the frontend's parseJsonSafe. It also must never share
// the package-level *AppError sentinels, since mutating one (e.g. RetryAfter)
// races across every request in the process.
func (h *Handler) captureError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

func bearerToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// ── Management ───────────────────────────────────────────────────────────────

type sourceRequest struct {
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	TargetSlug     string  `json:"target_slug"`
	UpdatePolicy   string  `json:"update_policy"`
	DefaultOwnerID *string `json:"default_owner_id"`
	DailyCap       int     `json:"daily_cap"`
	Status         *string `json:"status"`
}

type sourceWithKey struct {
	*LeadSource
	// PlaintextKey is populated exactly once, at creation or rotation.
	PlaintextKey string `json:"plaintext_key,omitempty"`
}

// CreateSource mints a source and returns its key once.
func (h *Handler) CreateSource(c *gin.Context) {
	orgID, userID, ok := h.actor(c)
	if !ok {
		return
	}
	var req sourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.mgmtError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		h.mgmtError(c, http.StatusBadRequest, "name is required")
		return
	}
	if req.Kind == "" {
		req.Kind = KindAPI
	}
	if !IsValidKind(req.Kind) {
		h.mgmtError(c, http.StatusBadRequest, "unknown source kind: "+req.Kind)
		return
	}
	if req.TargetSlug == "" {
		req.TargetSlug = "contact"
	}
	if req.UpdatePolicy == "" {
		req.UpdatePolicy = UpdatePolicyFillBlankOnly
	}
	if !IsValidUpdatePolicy(req.UpdatePolicy) {
		h.mgmtError(c, http.StatusBadRequest, "unknown update policy: "+req.UpdatePolicy)
		return
	}
	if !h.authorizeTarget(c, orgID, req.TargetSlug) {
		return
	}

	owner, ok := h.parseOwner(c, req.DefaultOwnerID)
	if !ok {
		return
	}

	plaintext, hash, prefix, err := GenerateLeadKey()
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not mint key")
		return
	}
	src := &LeadSource{
		OrgID:        orgID,
		Kind:         req.Kind,
		Name:         strings.TrimSpace(req.Name),
		TokenHash:    hash,
		TokenPrefix:  prefix,
		TargetSlug:   req.TargetSlug,
		UpdatePolicy: req.UpdatePolicy,
		// Set explicitly: GORM sends these columns on INSERT, so leaving them nil
		// persists NULL/[] and silently defeats the column DEFAULT.
		MatchFields:    datatypes.JSON(`["email"]`),
		FieldMap:       datatypes.JSON(`{}`),
		Config:         datatypes.JSON(`{}`),
		DefaultOwnerID: owner,
		DailyCap:       req.DailyCap,
		Status:         SourceStatusActive,
	}
	if userID != uuid.Nil {
		src.CreatedBy = &userID
	}
	if err := h.repo.CreateSource(c.Request.Context(), src); err != nil {
		h.logger.Error("integrations: create source failed", "error", err)
		h.mgmtError(c, http.StatusInternalServerError, "could not create source")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": sourceWithKey{LeadSource: src, PlaintextKey: plaintext}})
}

// ListSources returns the org's sources.
func (h *Handler) ListSources(c *gin.Context) {
	orgID, _, ok := h.actor(c)
	if !ok {
		return
	}
	out, err := h.repo.ListSources(c.Request.Context(), orgID)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not list sources")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// GetSource returns one source.
func (h *Handler) GetSource(c *gin.Context) {
	src, ok := h.loadSource(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": src})
}

// UpdateSource edits a source's mutable config.
func (h *Handler) UpdateSource(c *gin.Context) {
	src, ok := h.loadSource(c)
	if !ok {
		return
	}
	var req sourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.mgmtError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		src.Name = strings.TrimSpace(req.Name)
	}
	if req.UpdatePolicy != "" {
		if !IsValidUpdatePolicy(req.UpdatePolicy) {
			h.mgmtError(c, http.StatusBadRequest, "unknown update policy: "+req.UpdatePolicy)
			return
		}
		src.UpdatePolicy = req.UpdatePolicy
	}
	if req.TargetSlug != "" && req.TargetSlug != src.TargetSlug {
		// Re-authorize on every target change, not just at create: otherwise a source
		// created against a permitted object could be repointed at a forbidden one.
		if !h.authorizeTarget(c, src.OrgID, req.TargetSlug) {
			return
		}
		src.TargetSlug = req.TargetSlug
	}
	if req.DefaultOwnerID != nil {
		owner, ok := h.parseOwner(c, req.DefaultOwnerID)
		if !ok {
			return
		}
		src.DefaultOwnerID = owner
	}
	if req.DailyCap > 0 {
		src.DailyCap = req.DailyCap
	}
	if req.Status != nil {
		switch *req.Status {
		case SourceStatusActive:
			src.Status = SourceStatusActive
			src.DisabledAt = nil
			src.ConsecutiveFailures = 0
		case SourceStatusDisabled:
			now := time.Now()
			src.Status = SourceStatusDisabled
			src.DisabledAt = &now
		default:
			h.mgmtError(c, http.StatusBadRequest, "status must be active or disabled")
			return
		}
	}
	if err := h.repo.UpdateSource(c.Request.Context(), src); err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not update source")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": src})
}

// DeleteSource retires a source (soft — the ledger outlives it).
func (h *Handler) DeleteSource(c *gin.Context) {
	src, ok := h.loadSource(c)
	if !ok {
		return
	}
	if err := h.repo.SoftDeleteSource(c.Request.Context(), src.OrgID, src.ID); err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not delete source")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
}

// RotateKey mints a new credential, invalidating the old one immediately.
func (h *Handler) RotateKey(c *gin.Context) {
	src, ok := h.loadSource(c)
	if !ok {
		return
	}
	plaintext, hash, prefix, err := GenerateLeadKey()
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not mint key")
		return
	}
	src.TokenHash = hash
	src.TokenPrefix = prefix
	if err := h.repo.UpdateSource(c.Request.Context(), src); err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not rotate key")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": sourceWithKey{LeadSource: src, PlaintextKey: plaintext}})
}

// ListEvents returns a source's recent deliveries.
func (h *Handler) ListEvents(c *gin.Context) {
	src, ok := h.loadSource(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	out, err := h.repo.ListEvents(c.Request.Context(), src.OrgID, &src.ID, limit)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not list events")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// ── helpers ──────────────────────────────────────────────────────────────────

// actor reads the caller's org/user off the gin context. Copied rather than
// imported: internal/delivery/http is imported only by main, and importing it here
// would invert this package's dependency direction.
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

// loadSource resolves :id within the caller's org.
func (h *Handler) loadSource(c *gin.Context) (*LeadSource, bool) {
	orgID, _, ok := h.actor(c)
	if !ok {
		return nil, false
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.mgmtError(c, http.StatusBadRequest, "invalid source id")
		return nil, false
	}
	src, err := h.repo.GetSource(c.Request.Context(), orgID, id)
	if err != nil {
		h.mgmtError(c, http.StatusInternalServerError, "could not load source")
		return nil, false
	}
	if src == nil {
		h.mgmtError(c, http.StatusNotFound, "source not found")
		return nil, false
	}
	return src, true
}

// authorizeTarget re-checks the CONFIGURING ADMIN's own object permission on the
// target, using their real caller from the request context.
//
// This is a security control, not a formality. Ingest writes callerless, so OLS
// never runs at write time — meaning without this check `integrations.manage`
// alone would let an admin point a source at any object in the org (HR, finance)
// and write to it, regardless of their role. The capability says "may configure
// integrations"; it must not silently mean "may write anything".
func (h *Handler) authorizeTarget(c *gin.Context, orgID uuid.UUID, slug string) bool {
	ctx := c.Request.Context()
	if err := h.authz.Authorize(ctx, orgID, slug, domain.ActionCreate); err != nil {
		h.mgmtError(c, http.StatusForbidden, "you do not have permission to create "+slug+" records")
		return false
	}
	if err := h.authz.Authorize(ctx, orgID, slug, domain.ActionEdit); err != nil {
		h.mgmtError(c, http.StatusForbidden, "you do not have permission to edit "+slug+" records")
		return false
	}
	return true
}

func (h *Handler) parseOwner(c *gin.Context, raw *string) (*uuid.UUID, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, true
	}
	id, err := uuid.Parse(*raw)
	if err != nil {
		h.mgmtError(c, http.StatusBadRequest, "invalid default_owner_id")
		return nil, false
	}
	return &id, true
}

// mgmtError writes the management API's error envelope, matching what the
// frontend's apiError expects ({error: "..."}).
func (h *Handler) mgmtError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

// compile-time assertion that the ingest service satisfies what the handler needs.
var _ = context.Background
