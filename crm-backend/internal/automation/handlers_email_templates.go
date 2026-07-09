package automation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// handlers_email_templates.go implements the A5 email-templates library CRUD +
// test-send, all under /api/workflows/email-templates and gated by the
// workflows.manage capability (see RegisterRoutes). Reads/writes go through
// EmailTemplateRepository over the shared *gorm.DB.

// emailTemplates returns a repository over the handler's shared DB.
func (h *Handler) emailTemplates() *EmailTemplateRepository {
	return NewEmailTemplateRepository(h.db)
}

// ListEmailTemplates handles GET /api/workflows/email-templates.
func (h *Handler) ListEmailTemplates(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}
	rows, err := h.emailTemplates().List(c.Request.Context(), orgID)
	if err != nil {
		h.logger.Error("list email templates failed", "error", err)
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list email templates", nil)
		return
	}
	resp := EmailTemplateListResponse{Templates: make([]EmailTemplateResponse, 0, len(rows)), Total: len(rows)}
	for i := range rows {
		resp.Templates = append(resp.Templates, ToEmailTemplateResponse(&rows[i]))
	}
	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// GetEmailTemplate handles GET /api/workflows/email-templates/:id.
func (h *Handler) GetEmailTemplate(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}
	id, ok := h.parseTemplateID(c)
	if !ok {
		return
	}
	t, err := h.emailTemplates().Get(c.Request.Context(), orgID, id)
	if err != nil {
		h.logger.Error("get email template failed", "error", err)
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load email template", nil)
		return
	}
	if t == nil {
		h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", "email template not found", nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ToEmailTemplateResponse(t)})
}

// CreateEmailTemplate handles POST /api/workflows/email-templates.
func (h *Handler) CreateEmailTemplate(c *gin.Context) {
	orgID, userID := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}
	var req CreateEmailTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.errorResponse(c, http.StatusBadRequest, "VALIDATION_FAILED", err.Error(), nil)
		return
	}
	t := &EmailTemplate{
		OrgID:      orgID,
		Name:       strings.TrimSpace(req.Name),
		Subject:    req.Subject,
		BodyHTML:   req.BodyHTML,
		BodyJSON:   req.BodyJSON,
		ObjectSlug: req.ObjectSlug,
		CreatedBy:  userID,
		UpdatedBy:  userID,
	}
	if err := h.emailTemplates().Create(c.Request.Context(), t); err != nil {
		if errors.Is(err, ErrDuplicateTemplateName) {
			h.errorResponse(c, http.StatusConflict, "DUPLICATE_NAME", err.Error(), nil)
			return
		}
		h.logger.Error("create email template failed", "error", err)
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create email template", nil)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": ToEmailTemplateResponse(t)})
}

// UpdateEmailTemplate handles PUT /api/workflows/email-templates/:id.
func (h *Handler) UpdateEmailTemplate(c *gin.Context) {
	orgID, userID := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}
	id, ok := h.parseTemplateID(c)
	if !ok {
		return
	}
	var req UpdateEmailTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.errorResponse(c, http.StatusBadRequest, "VALIDATION_FAILED", err.Error(), nil)
		return
	}
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		req.Name = &trimmed
	}
	t, err := h.emailTemplates().Update(c.Request.Context(), orgID, id, userID, req.Name, req.Subject, req.BodyHTML, req.ObjectSlug, req.BodyJSON)
	if err != nil {
		if errors.Is(err, ErrDuplicateTemplateName) {
			h.errorResponse(c, http.StatusConflict, "DUPLICATE_NAME", err.Error(), nil)
			return
		}
		h.logger.Error("update email template failed", "error", err)
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update email template", nil)
		return
	}
	if t == nil {
		h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", "email template not found", nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ToEmailTemplateResponse(t)})
}

// DeleteEmailTemplate handles DELETE /api/workflows/email-templates/:id (soft delete).
func (h *Handler) DeleteEmailTemplate(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}
	id, ok := h.parseTemplateID(c)
	if !ok {
		return
	}
	deleted, err := h.emailTemplates().Delete(c.Request.Context(), orgID, id)
	if err != nil {
		h.logger.Error("delete email template failed", "error", err)
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete email template", nil)
		return
	}
	if !deleted {
		h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", "email template not found", nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": "deleted"}})
}

// TestSendEmailTemplate handles POST /api/workflows/email-templates/:id/test-send.
// It renders the template against a sample record (the org's most recent record of
// the template's object_slug; contact by default) and emails it to the caller.
func (h *Handler) TestSendEmailTemplate(c *gin.Context) {
	orgID, userID := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}
	id, ok := h.parseTemplateID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	t, err := h.emailTemplates().Get(ctx, orgID, id)
	if err != nil {
		h.logger.Error("test-send: load template failed", "error", err)
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load email template", nil)
		return
	}
	if t == nil {
		h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", "email template not found", nil)
		return
	}

	toEmail, err := h.loadUserEmail(ctx, orgID, userID)
	if err != nil {
		h.logger.Error("test-send: resolve caller email failed", "error", err)
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to resolve your email address", nil)
		return
	}
	if toEmail == "" {
		h.errorResponse(c, http.StatusBadRequest, "NO_RECIPIENT", "your account has no email address to send the test to", nil)
		return
	}

	evalCtx := h.buildTestSendContext(ctx, orgID, t.ObjectSlug)
	subject := InterpolateTemplate(t.Subject, evalCtx)
	body := InterpolateTemplate(t.BodyHTML, evalCtx)

	if err := h.engine.SendTestEmail(ctx, toEmail, subject, body); err != nil {
		h.logger.Error("test-send: send failed", "error", err, "template_id", id.String())
		h.errorResponse(c, http.StatusBadGateway, "SEND_FAILED", "failed to send test email: "+err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": TestSendEmailTemplateResponse{Status: "sent", To: toEmail}})
}

// --- Test-send helpers ---

// parseTemplateID parses and validates the :id route param, writing a 400 and
// returning ok=false on a malformed uuid.
func (h *Handler) parseTemplateID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid template id", nil)
		return uuid.Nil, false
	}
	return id, true
}

// loadUserEmail resolves a user's email within the org (mirrors the users query in
// GetWorkflowSchema). Empty string when the user has no active membership/email.
func (h *Handler) loadUserEmail(ctx context.Context, orgID, userID uuid.UUID) (string, error) {
	if userID == uuid.Nil {
		return "", nil
	}
	var email string
	err := h.db.WithContext(ctx).
		Table("users").
		Joins("JOIN org_users ON org_users.user_id = users.id").
		Where("users.id = ? AND org_users.org_id = ? AND org_users.status = 'active' AND org_users.deleted_at IS NULL", userID, orgID).
		Limit(1).
		Pluck("users.email", &email).Error
	if err != nil {
		return "", err
	}
	return email, nil
}

// buildTestSendContext synthesizes an EvalContext from a sample record of the
// template's object_slug (the org's most recent contact/deal). Returns an empty
// context (merge tags render blank) when no sample record exists or the slug isn't
// a system entity — a test-send is still useful to preview the shell/copy.
func (h *Handler) buildTestSendContext(ctx context.Context, orgID uuid.UUID, objectSlug string) EvalContext {
	// Only the system contact/deal objects have sample loaders. An unscoped template
	// defaults to a contact sample; a custom-object scope renders against an empty
	// context (its {{slug.*}} tags resolve blank — custom sample loading is a follow-up).
	kind := "contact"
	triggerType := TriggerContactCreated
	table := "contacts"
	switch objectSlug {
	case "", "contact":
		// contact defaults above
	case "deal":
		kind = "deal"
		triggerType = TriggerDealStageChanged
		table = "deals"
	default:
		return EvalContext{}
	}

	sampleID, ok := h.firstSampleEntityID(ctx, orgID, table)
	if !ok {
		return EvalContext{}
	}

	var entity map[string]any
	if kind == "deal" {
		entity, _ = h.loadDealForRun(ctx, orgID, sampleID)
	} else {
		entity, _ = h.loadContactForRun(ctx, orgID, sampleID)
	}
	if entity == nil {
		return EvalContext{}
	}

	trigCtx := buildRunNowTriggerContext(kind, triggerType, entity)
	raw, err := json.Marshal(trigCtx)
	if err != nil {
		return EvalContext{}
	}
	return h.engine.buildEvalContext(&WorkflowRun{OrgID: orgID, TriggerContext: datatypes.JSON(raw)})
}

// firstSampleEntityID returns the org's most recently created record id from the
// given table (contacts/deals), or ok=false when the org has none.
func (h *Handler) firstSampleEntityID(ctx context.Context, orgID uuid.UUID, table string) (uuid.UUID, bool) {
	var ids []uuid.UUID
	err := h.db.WithContext(ctx).
		Table(table).
		Where("org_id = ? AND deleted_at IS NULL", orgID).
		Order("created_at DESC").
		Limit(1).
		Pluck("id", &ids).Error
	if err != nil || len(ids) == 0 || ids[0] == uuid.Nil {
		return uuid.Nil, false
	}
	return ids[0], true
}
