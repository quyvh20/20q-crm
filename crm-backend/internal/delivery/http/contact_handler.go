package http

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ContactEventEmitter is called after contact create/update to fire automation triggers.
// The callback is injected from main.go to avoid a direct dependency on the automation package.
type ContactEventEmitter func(ctx context.Context, orgID uuid.UUID, eventType string, payload map[string]any)

type ContactHandler struct {
	contactUC    domain.ContactUseCase
	emitEvent    ContactEventEmitter
}

func NewContactHandler(contactUC domain.ContactUseCase) *ContactHandler {
	return &ContactHandler{contactUC: contactUC}
}

// SetEventEmitter wires the automation trigger callback (called from main.go after engine init).
func (h *ContactHandler) SetEventEmitter(fn ContactEventEmitter) {
	h.emitEvent = fn
}

// countWords returns the number of whitespace-separated tokens.
func countWords(s string) int {
	return len(strings.Fields(s))
}

// GET /api/contacts
func (h *ContactHandler) List(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	q := strings.TrimSpace(c.Query("q"))

	// ── Determine search mode ──────────────────────────────────────────────
	//   explicit: semantic=true  → always semantic
	//   auto:     3+ word query  → semantic
	//             1-2 word query → full-text
	//   no query                 → full list
	isExplicitSemantic := c.Query("semantic") == "true"
	isAutoSemantic := q != "" && countWords(q) >= 3

	if (isExplicitSemantic || isAutoSemantic) && q != "" {
		mode := "auto(3+ words)"
		if isExplicitSemantic {
			mode = "explicit(semantic=true)"
		}
		slog.Info("hybrid_search",
			"mode", "semantic",
			"trigger", mode,
			"query", q,
			"word_count", countWords(q),
			"org_id", orgID.String(),
		)

		limit := 20
		if limitStr := c.Query("limit"); limitStr != "" {
			var l int
			if _, err := parseIntParam(limitStr, &l); err == nil && l > 0 {
				limit = l
			}
		}
		contacts, err := h.contactUC.SemanticSearch(c.Request.Context(), orgID, q, limit)
		if err != nil {
			handleAppError(c, err)
			return
		}
		c.JSON(http.StatusOK, domain.SuccessWithMeta(contacts, domain.CursorMeta{
			Total: int64(len(contacts)),
		}))
		return
	}

	// ── Full-text / standard cursor-paginated list ─────────────────────────
	if q != "" {
		slog.Info("hybrid_search",
			"mode", "fulltext",
			"trigger", "auto(1-2 words)",
			"query", q,
			"word_count", countWords(q),
			"org_id", orgID.String(),
		)
	}

	var filter domain.ContactFilter
	filter.Q = q
	filter.Cursor = c.Query("cursor")

	if limitStr := c.Query("limit"); limitStr != "" {
		var limit int
		if _, err := parseIntParam(limitStr, &limit); err == nil {
			filter.Limit = limit
		}
	}

	if companyIDStr := c.Query("company_id"); companyIDStr != "" {
		if id, err := uuid.Parse(companyIDStr); err == nil {
			filter.CompanyID = &id
		}
	}

	if ownerIDStr := c.Query("owner_user_id"); ownerIDStr != "" {
		if id, err := uuid.Parse(ownerIDStr); err == nil {
			filter.OwnerUserID = &id
		}
	}

	if tagIDsStr := c.Query("tag_ids"); tagIDsStr != "" {
		for _, idStr := range strings.Split(tagIDsStr, ",") {
			if id, err := uuid.Parse(strings.TrimSpace(idStr)); err == nil {
				filter.TagIDs = append(filter.TagIDs, id)
			}
		}
	}

	contacts, nextCursor, err := h.contactUC.List(c.Request.Context(), orgID, filter)
	if err != nil {
		handleAppError(c, err)
		return
	}

	total, _ := h.contactUC.Count(c.Request.Context(), orgID)

	meta := domain.CursorMeta{
		NextCursor: nextCursor,
		HasMore:    nextCursor != "",
		Total:      total,
	}

	c.JSON(http.StatusOK, domain.SuccessWithMeta(contacts, meta))
}

// GET /api/contacts/:id
func (h *ContactHandler) GetByID(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid contact id"))
		return
	}

	contact, err := h.contactUC.GetByID(c.Request.Context(), orgID, id)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(contact))
}

// POST /api/contacts
func (h *ContactHandler) Create(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	var input domain.CreateContactInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	contact, err := h.contactUC.Create(c.Request.Context(), orgID, input)
	if err != nil {
		handleAppError(c, err)
		return
	}

	// Fire automation trigger asynchronously
	if h.emitEvent != nil {
		contactMap := map[string]any{
			"id":         contact.ID.String(),
			"first_name": contact.FirstName,
			"last_name":  contact.LastName,
		}
		if contact.Email != nil {
			contactMap["email"] = *contact.Email
		}
		if contact.Phone != nil {
			contactMap["phone"] = *contact.Phone
		}
		payload := map[string]any{
			"entity_id": contact.ID.String(),
			"contact":   contactMap,
			"trigger": map[string]any{
				"type":   "contact_created",
				"source": "crm_ui",
			},
		}
		go h.emitEvent(context.Background(), orgID, "contact_created", payload)
	}

	c.JSON(http.StatusCreated, domain.Success(contact))
}

// PUT /api/contacts/:id
func (h *ContactHandler) Update(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid contact id"))
		return
	}

	var input domain.UpdateContactInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	contact, err := h.contactUC.Update(c.Request.Context(), orgID, id, input)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(contact))
}

// DELETE /api/contacts/:id
func (h *ContactHandler) Delete(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid contact id"))
		return
	}

	if err := h.contactUC.Delete(c.Request.Context(), orgID, id); err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "contact deleted"}))
}

// POST /api/contacts/import
func (h *ContactHandler) Import(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("missing file field"))
		return
	}
	defer file.Close()

	// conflict_mode: "skip" (default) | "overwrite"
	conflictMode := c.DefaultQuery("conflict_mode", "skip")
	if conflictMode != "overwrite" {
		conflictMode = "skip"
	}

	result, err := h.contactUC.BulkImport(c.Request.Context(), orgID, file, header.Filename, conflictMode)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(result))
}

// ============================================================
// Helper
// ============================================================

func parseIntParam(s string, out *int) (bool, error) {
	var v int
	for _, c := range s {
		if c < '0' || c > '9' {
			return false, nil
		}
		v = v*10 + int(c-'0')
	}
	*out = v
	return true, nil
}

// POST /api/contacts/bulk-action
func (h *ContactHandler) BulkAction(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	var input domain.BulkActionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	result, err := h.contactUC.BulkAction(c.Request.Context(), orgID, input)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(result))
}
