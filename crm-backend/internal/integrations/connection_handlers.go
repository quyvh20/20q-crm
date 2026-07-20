package integrations

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// ConnectionHandler serves the provider-connector routes (L5.1). It is a
// separate handler from the capture/source Handler so the connection flow can be
// wired without changing the source handler's constructor — but it registers
// into the SAME protected group, so the PAT + 2FA + capability stack applies
// identically.
//
// The management routes live under distinct STATIC prefixes (`providers`,
// `connections`, `pending`) rather than a `:provider` wildcard directly under
// /api/integrations, so they never collide with the source routes' `sources`
// segment in gin's route tree.
type ConnectionHandler struct {
	repo *Repository
	svc  *ConnectionService
	// authz + schema are needed by the enable-form path, which creates a
	// facebook_form lead source: it re-checks the admin's own OLS on `contact` (the
	// same control CreateSource applies — integrations.manage alone must not become
	// an OLS bypass) and validates the seeded field map against the target schema.
	authz  domain.RecordAuthorizer
	schema SchemaProvider
	logger *slog.Logger
}

// NewConnectionHandler builds the handler. authz is required for the same reason
// the source handler's is: the form-enable path writes to `contact`, and the OLS
// re-check is a security control, not a formality.
func NewConnectionHandler(repo *Repository, svc *ConnectionService, authz domain.RecordAuthorizer, schema SchemaProvider, logger *slog.Logger) *ConnectionHandler {
	if authz == nil {
		panic("integrations: connection handler needs an authorizer — the form-enable OLS check is a security control")
	}
	return &ConnectionHandler{repo: repo, svc: svc, authz: authz, schema: schema, logger: logger}
}

// RegisterRoutes mounts the connection routes: a PUBLIC provider callback (a
// browser navigation with no session — authenticated by the state row, not by
// the protected stack) plus the capability-gated management routes.
func (h *ConnectionHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	// Public: the provider redirects the user's BROWSER here after consent. It
	// carries no bearer/PAT and cannot join the protected group; the single-use
	// `state` param is its whole authentication, resolved server-side.
	router.GET("/api/integrations/providers/:provider/callback", h.Callback)

	g := router.Group("/api/integrations")
	g.Use(protected...)
	g.Use(requireCap(domain.CapIntegrationsManage))
	{
		// The registered providers the FE may offer a connect button for. A static
		// leaf under providers/ — no conflict with the providers/:provider/* routes.
		g.GET("/providers", h.Providers)
		// Initiate is a POST (not a browser GET) on purpose: it must carry the
		// caller's bearer, which a full-page navigation would not send. It returns
		// the provider auth URL for the frontend to redirect to.
		g.POST("/providers/:provider/connect", h.Connect)
		g.GET("/connections", h.List)
		g.DELETE("/connections/:id", h.Disconnect)
		// Per-form config (L5.4): discover a connection's provider forms, and enable
		// one (create its facebook_form source).
		g.GET("/connections/:id/forms", h.ListForms)
		g.POST("/connections/:id/forms", h.EnableForm)
		// The account picker: read candidates (peek), then commit one (select).
		g.GET("/pending/:token", h.Candidates)
		g.POST("/pending/:token/select", h.Select)
	}
}

// Providers lists the registered providers a connect button may be shown for.
func (h *ConnectionHandler) Providers(c *gin.Context) {
	if _, _, ok := h.actor(c); !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": h.svc.Providers()})
}

// Connect begins a provider connect and returns the consent URL.
func (h *ConnectionHandler) Connect(c *gin.Context) {
	orgID, userID, ok := h.actor(c)
	if !ok {
		return
	}
	provider := strings.TrimSpace(c.Param("provider"))
	// Body is optional — a return_to lets the UI send the admin back to where they
	// started. Ignored if it is not a safe same-site path (see safeReturnTo).
	var req struct {
		ReturnTo string `json:"return_to"`
	}
	_ = c.ShouldBindJSON(&req)

	authURL, err := h.svc.StartConnect(c.Request.Context(), orgID, userID, provider, req.ReturnTo)
	if err != nil {
		h.writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"auth_url": authURL}})
}

// Callback is the provider's redirect target. It ALWAYS redirects the browser
// back to the frontend — never returns a JSON error — because a provider sent a
// human here, not an API client. Failures land on the integrations page with a
// short machine reason, never a raw error string (which could carry provider
// detail or token fragments).
func (h *ConnectionHandler) Callback(c *gin.Context) {
	provider := strings.TrimSpace(c.Param("provider"))
	if reason := c.Query("error"); reason != "" {
		// The user declined consent, or the provider refused. "denied" is deliberately
		// generic: the provider's own error text is not something to reflect into our
		// UI unfiltered.
		c.Redirect(http.StatusFound, h.svc.ErrorRedirect("denied"))
		return
	}
	token, err := h.svc.HandleCallback(c.Request.Context(), provider, c.Query("code"), c.Query("state"))
	if err != nil {
		if h.logger != nil {
			h.logger.Warn("integrations: connect callback failed", "provider", provider, "error", err)
		}
		c.Redirect(http.StatusFound, h.svc.ErrorRedirect("connect_failed"))
		return
	}
	c.Redirect(http.StatusFound, h.svc.PickerRedirect(provider, token))
}

// Candidates returns the token-free account choices for a pending selection.
func (h *ConnectionHandler) Candidates(c *gin.Context) {
	orgID, userID, ok := h.actor(c)
	if !ok {
		return
	}
	provider, choices, err := h.svc.Candidates(c.Request.Context(), orgID, userID, strings.TrimSpace(c.Param("token")))
	if err != nil {
		h.writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"provider": provider, "accounts": choices}})
}

// Select promotes one candidate account to a stored connection.
func (h *ConnectionHandler) Select(c *gin.Context) {
	orgID, userID, ok := h.actor(c)
	if !ok {
		return
	}
	var req struct {
		AccountID string `json:"account_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.AccountID) == "" {
		h.err(c, http.StatusBadRequest, "account_id is required")
		return
	}
	conn, err := h.svc.SelectAccount(c.Request.Context(), orgID, userID, strings.TrimSpace(c.Param("token")), strings.TrimSpace(req.AccountID))
	if err != nil {
		if errors.Is(err, ErrAccountClaimedElsewhere) {
			// A distinct 409 with the friendly message: the account is fine, it just
			// belongs to another workspace right now. Never names which one.
			h.err(c, http.StatusConflict, "This account is already connected to another workspace. Disconnect it there first, then try again.")
			return
		}
		h.writeErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": ViewOfConnection(conn)})
}

// List returns the org's provider connections (token-free views).
func (h *ConnectionHandler) List(c *gin.Context) {
	orgID, _, ok := h.actor(c)
	if !ok {
		return
	}
	conns, err := h.repo.ListConnections(c.Request.Context(), orgID)
	if err != nil {
		h.logger.Error("integrations: list connections failed", "error", err)
		h.err(c, http.StatusInternalServerError, "could not load connections")
		return
	}
	views := make([]ConnectionView, 0, len(conns))
	for i := range conns {
		views = append(views, ViewOfConnection(&conns[i]))
	}
	c.JSON(http.StatusOK, gin.H{"data": views})
}

// Disconnect removes a connection (best-effort provider teardown + soft delete).
func (h *ConnectionHandler) Disconnect(c *gin.Context) {
	orgID, _, ok := h.actor(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		h.err(c, http.StatusBadRequest, "invalid connection id")
		return
	}
	if err := h.svc.Disconnect(c.Request.Context(), orgID, id); err != nil {
		h.writeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// loadConnection resolves :id within the caller's org.
func (h *ConnectionHandler) loadConnection(c *gin.Context, orgID uuid.UUID) (*IntegrationConnection, bool) {
	id, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		h.err(c, http.StatusBadRequest, "invalid connection id")
		return nil, false
	}
	conn, err := h.repo.GetConnection(c.Request.Context(), orgID, id)
	if err != nil {
		h.err(c, http.StatusInternalServerError, "could not load the connection")
		return nil, false
	}
	if conn == nil {
		h.err(c, http.StatusNotFound, "connection not found")
		return nil, false
	}
	return conn, true
}

// formView is one provider form in the picker.
type formView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status,omitempty"`
	Enabled  bool   `json:"enabled"`
	SourceID string `json:"source_id,omitempty"`
}

// ListForms discovers a connection's provider lead forms and marks which are
// already enabled (have a facebook_form source).
func (h *ConnectionHandler) ListForms(c *gin.Context) {
	orgID, _, ok := h.actor(c)
	if !ok {
		return
	}
	conn, ok := h.loadConnection(c, orgID)
	if !ok {
		return
	}
	prov, ok := h.svc.registry.Get(conn.Provider)
	if !ok {
		h.err(c, http.StatusBadRequest, "this provider is not available")
		return
	}
	creds, err := h.svc.openCredentials(conn)
	if err != nil {
		h.err(c, http.StatusBadGateway, "could not read the connection's credentials — reconnect the account")
		return
	}
	forms, err := prov.ListForms(c.Request.Context(), conn, creds)
	if err != nil {
		h.writeErr(c, err)
		return
	}
	enabled, err := h.repo.EnabledFormIDs(c.Request.Context(), orgID, conn.ID)
	if err != nil {
		h.err(c, http.StatusInternalServerError, "could not load enabled forms")
		return
	}
	out := make([]formView, 0, len(forms))
	for _, f := range forms {
		v := formView{ID: f.ID, Name: f.Name, Status: f.Status}
		if sid, on := enabled[f.ID]; on {
			v.Enabled = true
			v.SourceID = sid.String()
		}
		out = append(out, v)
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// EnableForm creates the facebook_form source for a provider form (idempotent: a
// form already enabled returns its existing source).
func (h *ConnectionHandler) EnableForm(c *gin.Context) {
	orgID, userID, ok := h.actor(c)
	if !ok {
		return
	}
	conn, ok := h.loadConnection(c, orgID)
	if !ok {
		return
	}
	var req struct {
		FormID   string `json:"form_id"`
		FormName string `json:"form_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.FormID) == "" {
		h.err(c, http.StatusBadRequest, "form_id is required")
		return
	}
	formID := strings.TrimSpace(req.FormID)

	// Idempotent: re-enabling a form returns the existing source, never a duplicate.
	enabled, err := h.repo.EnabledFormIDs(c.Request.Context(), orgID, conn.ID)
	if err != nil {
		h.err(c, http.StatusInternalServerError, "could not check enabled forms")
		return
	}
	if sid, on := enabled[formID]; on {
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"source_id": sid.String(), "form_id": formID, "already_enabled": true}})
		return
	}

	// OLS re-check: the enabling admin must be able to create AND edit contacts,
	// with their own caller — ingest writes callerless, so this is the only place
	// the admin's own permission on the target is enforced.
	ctx := c.Request.Context()
	if err := h.authz.Authorize(ctx, orgID, "contact", domain.ActionCreate); err != nil {
		h.err(c, http.StatusForbidden, "you do not have permission to create contact records")
		return
	}
	if err := h.authz.Authorize(ctx, orgID, "contact", domain.ActionEdit); err != nil {
		h.err(c, http.StatusForbidden, "you do not have permission to edit contact records")
		return
	}

	// Validate the seed map against the contact schema — a broken seed is our bug,
	// worth failing loudly on rather than quarantining every future lead.
	seed := facebookSeedFieldMap()
	if h.schema != nil {
		allow, aerr := BuildAllowlist(ctx, h.schema, orgID, "contact")
		if aerr != nil {
			h.err(c, http.StatusInternalServerError, "could not load the contact fields")
			return
		}
		if problems := ValidateFieldMap(seed, allow); len(problems) > 0 {
			h.logger.Error("integrations: facebook seed field map invalid", "problems", problems)
			h.err(c, http.StatusInternalServerError, "could not seed the field mapping")
			return
		}
	}

	fmapJSON, _ := json.Marshal(seed)
	cfgJSON, _ := json.Marshal(map[string]any{"facebook": map[string]any{"form_id": formID}})
	name := strings.TrimSpace(req.FormName)
	if name == "" {
		name = "Facebook form " + formID
	}
	src := &LeadSource{
		OrgID:        orgID,
		Kind:         KindFacebookForm,
		Name:         name,
		TargetSlug:   "contact",
		UpdatePolicy: UpdatePolicyFillBlankOnly,
		// Facebook forms are phone-heavy; match on email then phone so a phone-only
		// lead still dedupes (the L2/L3 rationale).
		MatchFields: datatypes.JSON(`["email","phone"]`),
		FieldMap:    datatypes.JSON(fmapJSON),
		Config:      datatypes.JSON(cfgJSON),
		DailyCap:    defaultDailyCap,
		Status:      SourceStatusActive,
	}
	if userID != uuid.Nil {
		src.CreatedBy = &userID
	}
	if err := h.repo.CreateConnectionSource(ctx, src); err != nil {
		// Lost the idempotency race (a concurrent enable of the same form won). The
		// unique index caught it; resolve to the existing source rather than error.
		if IsFormSourceConflict(err) {
			if again, e := h.repo.EnabledFormIDs(ctx, orgID, conn.ID); e == nil {
				if sid, on := again[formID]; on {
					c.JSON(http.StatusOK, gin.H{"data": gin.H{"source_id": sid.String(), "form_id": formID, "already_enabled": true}})
					return
				}
			}
		}
		h.logger.Error("integrations: could not create facebook_form source", "error", err)
		h.err(c, http.StatusInternalServerError, "could not enable the form")
		return
	}
	if err := h.repo.SetSourceConnection(ctx, orgID, src.ID, conn.ID); err != nil {
		// The source exists but is not bound to its connection — the webhook processor
		// could never resolve it. Roll it back rather than leave a dead form.
		_ = h.repo.SoftDeleteSource(ctx, orgID, src.ID)
		h.logger.Error("integrations: could not bind facebook_form source to its connection", "error", err)
		h.err(c, http.StatusInternalServerError, "could not enable the form")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": gin.H{"source_id": src.ID.String(), "form_id": formID, "name": name}})
}

// actor pulls the authenticated org/user from the request context, matching the
// source Handler's actor. Duplicated rather than shared so the connection handler
// stays independent of the source handler's struct.
func (h *ConnectionHandler) actor(c *gin.Context) (orgID, userID uuid.UUID, ok bool) {
	o, exists := c.Get("org_id")
	if !exists {
		h.err(c, http.StatusUnauthorized, "unauthorized")
		return uuid.Nil, uuid.Nil, false
	}
	u, _ := c.Get("user_id")
	orgID, _ = o.(uuid.UUID)
	userID, _ = u.(uuid.UUID)
	if orgID == uuid.Nil {
		h.err(c, http.StatusUnauthorized, "unauthorized")
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, true
}

// writeErr maps a service error to the management API envelope.
func (h *ConnectionHandler) writeErr(c *gin.Context, err error) {
	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		h.err(c, appErr.Code, appErr.Message)
		return
	}
	if h.logger != nil {
		h.logger.Error("integrations: connection request failed", "error", err)
	}
	h.err(c, http.StatusInternalServerError, "something went wrong")
}

func (h *ConnectionHandler) err(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}
