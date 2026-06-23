package http

import (
	"net/http"
	"strconv"
	"strings"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RecordHandler serves the uniform record API (P3): one set of CRUD endpoints
// over every object, system or custom, backed by RecordService. It is mounted
// under /api/registry/objects/:slug/records so it stays strictly additive to the
// legacy per-object routes (custom-object records at /api/objects/:slug/records,
// plus /api/contacts, /api/deals, /api/companies), which remain until P7.
type RecordHandler struct {
	svc domain.RecordService
}

func NewRecordHandler(svc domain.RecordService) *RecordHandler {
	return &RecordHandler{svc: svc}
}

// reservedListParams are query keys with dedicated meaning; everything else is
// treated as a relation/field filter (e.g. company, stage, contact, owner_user_id).
var reservedListParams = map[string]bool{
	"limit": true, "q": true, "cursor": true, "tag_ids": true, "semantic": true,
}

// List handles GET /api/registry/objects/:slug/records
func (h *RecordHandler) List(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "25"))

	// Any non-reserved single-value query param is a field filter, so the same
	// endpoint serves a contact's company filter, a deal's stage filter, and any
	// custom object's relation filters without per-object handler code.
	filters := map[string]string{}
	for key, vals := range c.Request.URL.Query() {
		if reservedListParams[key] || len(vals) == 0 || vals[0] == "" {
			continue
		}
		filters[key] = vals[0]
	}

	var tagIDs []uuid.UUID
	for _, raw := range c.QueryArray("tag_ids") {
		for _, part := range strings.Split(raw, ",") {
			if id, err := uuid.Parse(strings.TrimSpace(part)); err == nil {
				tagIDs = append(tagIDs, id)
			}
		}
	}

	page, err := h.svc.List(c.Request.Context(), orgID, slug, domain.RecordListInput{
		Limit:    limit,
		Q:        c.Query("q"),
		Cursor:   c.Query("cursor"),
		Filters:  filters,
		TagIDs:   tagIDs,
		Semantic: c.Query("semantic") == "true",
	})
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": page, "error": nil})
}

// Get handles GET /api/registry/objects/:slug/records/:id
func (h *RecordHandler) Get(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	rec, err := h.svc.Get(c.Request.Context(), orgID, slug, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rec, "error": nil})
}

// Create handles POST /api/registry/objects/:slug/records
func (h *RecordHandler) Create(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	userID := c.MustGet("user_id").(uuid.UUID)
	slug := c.Param("slug")

	var input domain.RecordWriteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	rec, err := h.svc.Create(c.Request.Context(), orgID, userID, slug, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": rec, "error": nil})
}

// Update handles PATCH /api/registry/objects/:slug/records/:id
func (h *RecordHandler) Update(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	var input domain.RecordWriteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	rec, err := h.svc.Update(c.Request.Context(), orgID, slug, id, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rec, "error": nil})
}

// Delete handles DELETE /api/registry/objects/:slug/records/:id
func (h *RecordHandler) Delete(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	if err := h.svc.Delete(c.Request.Context(), orgID, slug, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "deleted", "error": nil})
}

// ============================================================
// Universal relationships + tags (P4)
// ============================================================

// ListLinks handles GET /api/registry/objects/:slug/records/:id/links
func (h *RecordHandler) ListLinks(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	links, err := h.svc.ListLinks(c.Request.Context(), orgID, slug, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": links, "error": nil})
}

// AddLink handles POST /api/registry/objects/:slug/records/:id/links
func (h *RecordHandler) AddLink(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	userID := c.MustGet("user_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	var input domain.LinkInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	link, err := h.svc.AddLink(c.Request.Context(), orgID, userID, slug, id, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": link, "error": nil})
}

// RemoveLink handles DELETE /api/registry/links/:id
func (h *RecordHandler) RemoveLink(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	linkID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid link id"})
		return
	}
	if err := h.svc.RemoveLink(c.Request.Context(), orgID, linkID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "unlinked", "error": nil})
}

// ListTags handles GET /api/registry/objects/:slug/records/:id/tags
func (h *RecordHandler) ListTags(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	tags, err := h.svc.ListTags(c.Request.Context(), orgID, slug, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": tags, "error": nil})
}

// addTagBody is the AddTag request payload.
type addTagBody struct {
	TagID uuid.UUID `json:"tag_id" binding:"required"`
}

// AddTag handles POST /api/registry/objects/:slug/records/:id/tags
func (h *RecordHandler) AddTag(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	userID := c.MustGet("user_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	var body addTagBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	if err := h.svc.AddTag(c.Request.Context(), orgID, userID, slug, id, body.TagID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "tagged", "error": nil})
}

// RemoveTag handles DELETE /api/registry/objects/:slug/records/:id/tags/:tagId
func (h *RecordHandler) RemoveTag(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	tagID, err := uuid.Parse(c.Param("tagId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid tag id"})
		return
	}
	if err := h.svc.RemoveTag(c.Request.Context(), orgID, slug, id, tagID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "untagged", "error": nil})
}
