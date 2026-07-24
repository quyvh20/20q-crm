package marketing

import (
	"context"
	"encoding/json"
	"time"
	"log/slog"
	"net/http"
	"strings"

	"crm-backend/internal/automation"
	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// TestEmailSender sends one already-resolved email (reuses automation.Engine —
// which sends with no idempotency key, so every test click delivers).
type TestEmailSender interface {
	SendTestEmail(ctx context.Context, to, subject, bodyHTML string) error
}

// CallerEmailResolver returns the acting user's own email — the test-send recipient
// (a test always goes to the caller, never an arbitrary address).
type CallerEmailResolver func(ctx context.Context, orgID, userID uuid.UUID) (string, error)

// ContentHandler serves the marketing campaign-content API (M6): CRUD, ad-hoc
// preview/compile, and a test-send. Compilation (block JSON → email-safe HTML) and
// merge-scope/fallback validation run at save; a save is BLOCKED if either fails —
// raw contenteditable HTML is never stored or sent.
type ContentHandler struct {
	repo         *Repository
	compiler     *Compiler
	sender       TestEmailSender     // nil-tolerant: test-send 503s if unset
	resolveEmail CallerEmailResolver // nil-tolerant
	logger       *slog.Logger
}

// NewContentHandler builds the handler.
func NewContentHandler(repo *Repository, compiler *Compiler, sender TestEmailSender, resolveEmail CallerEmailResolver, logger *slog.Logger) *ContentHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ContentHandler{repo: repo, compiler: compiler, sender: sender, resolveEmail: resolveEmail, logger: logger}
}

// RegisterRoutes mounts the content routes under /api/marketing/content.
func (h *ContentHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	g := router.Group("/api/marketing/content")
	g.Use(protected...)
	g.Use(requireCap(domain.CapMarketingManage))
	{
		g.GET("", h.List)
		g.POST("", h.Create)
		g.POST("/preview", h.Preview)
		g.GET("/:id", h.Get)
		g.PUT("/:id", h.Update)
		g.DELETE("/:id", h.Remove)
		g.POST("/:id/test-send", h.TestSend)
	}
}

type contentRequest struct {
	Name       string          `json:"name"`
	Subject    string          `json:"subject"`
	Preheader  string          `json:"preheader"`
	BodyJSON   json.RawMessage `json:"body_json"`
	MergeScope []string        `json:"merge_scope"`
}

type previewRequest struct {
	Subject    string          `json:"subject"`
	Preheader  string          `json:"preheader"`
	BodyJSON   json.RawMessage `json:"body_json"`
	MergeScope []string        `json:"merge_scope"`
}

// List returns the org's campaign content (metadata; body included).
func (h *ContentHandler) List(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	rows, err := h.repo.ListContentByOrg(c.Request.Context(), orgID)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not list content")
		return
	}
	if rows == nil {
		rows = []CampaignContent{}
	}
	c.JSON(http.StatusOK, gin.H{"data": rows})
}

// Get returns one content row.
func (h *ContentHandler) Get(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid content id")
		return
	}
	row, err := h.repo.GetContentByID(c.Request.Context(), orgID, id)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not load content")
		return
	}
	if row == nil {
		abortErr(c, http.StatusNotFound, "content not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": row})
}

// Create validates + compiles + stores a new campaign content.
func (h *ContentHandler) Create(c *gin.Context) {
	orgID, userID, ok := actorFromCtx(c)
	if !ok {
		return
	}
	var req contentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		abortErr(c, http.StatusBadRequest, "name is required")
		return
	}
	doc, res, scope, ok := h.build(c, req.Subject, req.Preheader, req.BodyJSON, req.MergeScope)
	if !ok {
		return
	}
	bodyJSON, _ := json.Marshal(doc)
	scopeJSON, _ := json.Marshal(scope)
	now := time.Now()
	row := &CampaignContent{
		OrgID:             orgID,
		Name:              strings.TrimSpace(req.Name),
		Subject:           req.Subject,
		Preheader:         req.Preheader,
		BodyJSON:          datatypes.JSON(bodyJSON),
		BodyHTMLCompiled:  res.HTML,
		PlainText:         res.PlainText,
		MergeScope:        datatypes.JSON(scopeJSON),
		CompiledSizeBytes: res.SizeBytes,
		CompiledAt:        &now,
	}
	if userID != uuid.Nil {
		row.CreatedBy = &userID
	}
	if err := h.repo.CreateContent(c.Request.Context(), row); err != nil {
		h.logger.Error("marketing: create content failed", "error", err, "org_id", orgID.String())
		abortErr(c, http.StatusInternalServerError, "could not save content")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": row})
}

// Update re-validates + re-compiles + persists.
func (h *ContentHandler) Update(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid content id")
		return
	}
	row, err := h.repo.GetContentByID(c.Request.Context(), orgID, id)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not load content")
		return
	}
	if row == nil {
		abortErr(c, http.StatusNotFound, "content not found")
		return
	}
	var req contentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	doc, res, scope, ok := h.build(c, req.Subject, req.Preheader, req.BodyJSON, req.MergeScope)
	if !ok {
		return
	}
	bodyJSON, _ := json.Marshal(doc)
	scopeJSON, _ := json.Marshal(scope)
	if strings.TrimSpace(req.Name) == "" {
		abortErr(c, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.BodyJSON) == 0 {
		// Full-replace PUT: refuse a partial body that would silently blank the
		// stored subject/body/scope of an existing campaign.
		abortErr(c, http.StatusBadRequest, "body_json is required — send the full content on update")
		return
	}
	now := time.Now()
	row.Name = strings.TrimSpace(req.Name)
	row.Subject = req.Subject
	row.Preheader = req.Preheader
	row.BodyJSON = datatypes.JSON(bodyJSON)
	row.BodyHTMLCompiled = res.HTML
	row.PlainText = res.PlainText
	row.MergeScope = datatypes.JSON(scopeJSON)
	row.CompiledSizeBytes = res.SizeBytes
	row.CompiledAt = &now
	if err := h.repo.UpdateContent(c.Request.Context(), row); err != nil {
		abortErr(c, http.StatusInternalServerError, "could not save content")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": row})
}

// Remove soft-deletes a content row.
func (h *ContentHandler) Remove(c *gin.Context) {
	orgID, _, ok := actorFromCtx(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid content id")
		return
	}
	removed, err := h.repo.SoftDeleteContent(c.Request.Context(), orgID, id)
	if err != nil {
		abortErr(c, http.StatusInternalServerError, "could not delete content")
		return
	}
	if !removed {
		abortErr(c, http.StatusNotFound, "content not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"removed": true}})
}

// Preview compiles ad-hoc body JSON (not persisted) for the live/dark-mode preview.
func (h *ContentHandler) Preview(c *gin.Context) {
	if _, _, ok := actorFromCtx(c); !ok {
		return
	}
	var req previewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortErr(c, http.StatusBadRequest, "invalid request body")
		return
	}
	doc, verrs, err := parseAndValidate(req.Subject, req.Preheader, req.BodyJSON, req.MergeScope)
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid body_json")
		return
	}
	res, cerr := h.compiler.Compile(c.Request.Context(), doc, req.Preheader)
	if cerr != nil {
		// Preview surfaces compile errors rather than blocking — the editor shows them.
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"compile_error": cerr.Error(), "validation_errors": verrs}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"html":              res.HTML,
		"plain_text":        res.PlainText,
		"size_bytes":        res.SizeBytes,
		"too_large":         res.TooLarge,
		"validation_errors": verrs,
		"warnings":          lintWarnings(doc, res),
	}})
}

// TestSend compiles the stored content, resolves merge tags (to their fallbacks —
// per-recipient hydration is M7), and sends it to the caller's own email.
func (h *ContentHandler) TestSend(c *gin.Context) {
	orgID, userID, ok := actorFromCtx(c)
	if !ok {
		return
	}
	if h.sender == nil || h.resolveEmail == nil {
		abortErr(c, http.StatusServiceUnavailable, "email sending isn't configured on this deployment")
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid content id")
		return
	}
	row, err := h.repo.GetContentByID(c.Request.Context(), orgID, id)
	if err != nil || row == nil {
		abortErr(c, http.StatusNotFound, "content not found")
		return
	}
	to, err := h.resolveEmail(c.Request.Context(), orgID, userID)
	if err != nil || to == "" {
		abortErr(c, http.StatusBadRequest, "could not resolve your email address for the test send")
		return
	}
	// Empty context → every merge tag renders its fallback (proves the compile +
	// fallback grammar end to end; real per-recipient hydration is M7).
	var empty automation.EvalContext
	subject := automation.InterpolateTemplate(row.Subject, empty)
	body := automation.InterpolateTemplateHTML(row.BodyHTMLCompiled, empty)
	if err := h.sender.SendTestEmail(c.Request.Context(), to, subject, body); err != nil {
		h.logger.Error("marketing: content test-send failed", "error", err, "org_id", orgID.String())
		abortErr(c, http.StatusBadGateway, "the email provider rejected the test send")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"sent_to": to}})
}

// build parses/validates/compiles for Create+Update. On a validation or compile
// failure it writes the HTTP error and returns ok=false. TooLarge blocks the save.
func (h *ContentHandler) build(c *gin.Context, subject, preheader string, rawBody json.RawMessage, scope []string) (BlockDocument, CompileResult, []string, bool) {
	doc, verrs, err := parseAndValidate(subject, preheader, rawBody, scope)
	if err != nil {
		abortErr(c, http.StatusBadRequest, "invalid body_json")
		return BlockDocument{}, CompileResult{}, nil, false
	}
	if len(verrs) > 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "content has invalid merge tags", "validation_errors": verrs})
		return BlockDocument{}, CompileResult{}, nil, false
	}
	res, cerr := h.compiler.Compile(c.Request.Context(), doc, preheader)
	if cerr != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "email content failed to compile", "detail": cerr.Error()})
		return BlockDocument{}, CompileResult{}, nil, false
	}
	if res.TooLarge {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "compiled email exceeds 100KB (Gmail clips larger messages) — trim images/content", "size_bytes": res.SizeBytes})
		return BlockDocument{}, CompileResult{}, nil, false
	}
	return doc, res, NormalizeMergeScope(scope), true
}

// parseAndValidate parses body_json into a BlockDocument and runs the scope/fallback
// validation, returning the doc + human-readable validation errors.
func parseAndValidate(subject, preheader string, rawBody json.RawMessage, scope []string) (BlockDocument, []string, error) {
	var doc BlockDocument
	if len(rawBody) > 0 {
		if err := json.Unmarshal(rawBody, &doc); err != nil {
			return BlockDocument{}, nil, err
		}
	}
	norm := NormalizeMergeScope(scope)
	cerrs := ValidateContent(subject, preheader, doc, norm)
	msgs := make([]string, 0, len(cerrs))
	for _, e := range cerrs {
		msgs = append(msgs, e.Field+": "+e.Tag+" — "+e.Reason)
	}
	return doc, msgs, nil
}

// lintWarnings are advisory (non-blocking) content checks for the pre-send checklist.
func lintWarnings(doc BlockDocument, res CompileResult) []string {
	var w []string
	hasImageNoAlt := false
	hasLink := false
	var walk func([]Block)
	walk = func(bs []Block) {
		for _, b := range bs {
			if b.Type == BlockImage && strings.TrimSpace(b.Alt) == "" {
				hasImageNoAlt = true
			}
			if b.Type == BlockButton && b.Href != "" {
				hasLink = true
			}
			if b.Type == BlockText && strings.Contains(b.Text, "href=") {
				hasLink = true
			}
			for _, col := range b.Columns {
				walk(col)
			}
		}
	}
	walk(doc.Blocks)
	if hasImageNoAlt {
		w = append(w, "one or more images have no alt text (hurts accessibility and spam score)")
	}
	if !hasLink {
		w = append(w, "no links found — most marketing emails need at least one call to action")
	}
	if res.SizeBytes > 80*1024 {
		w = append(w, "compiled email is over 80KB and approaching Gmail's 102KB clipping limit")
	}
	return w
}
