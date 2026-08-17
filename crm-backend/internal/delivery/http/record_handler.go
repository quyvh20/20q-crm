package http

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
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
//
// registry/layoutUC/authz/tags exist for the composite record-page endpoint
// (GetPage in record_page_handler.go), which folds schema, record, related
// lists, and tags into one response; the plain CRUD endpoints don't use them.
type RecordHandler struct {
	svc      domain.RecordService
	related  domain.RelatedListsUseCase
	registry domain.ObjectRegistryUseCase
	layoutUC domain.ObjectLayoutUseCase
	authz    domain.RecordAuthorizer
	tags     domain.TagUseCase
	shares   domain.ShareUseCase
}

func NewRecordHandler(
	svc domain.RecordService,
	related domain.RelatedListsUseCase,
	registry domain.ObjectRegistryUseCase,
	layoutUC domain.ObjectLayoutUseCase,
	authz domain.RecordAuthorizer,
	tags domain.TagUseCase,
	shares domain.ShareUseCase,
) *RecordHandler {
	return &RecordHandler{svc: svc, related: related, registry: registry, layoutUC: layoutUC, authz: authz, tags: tags, shares: shares}
}

// Share handles POST /api/registry/objects/:slug/records/:id/share — grant a
// record to a member (the escape hatch for 'own'-scoped roles, I2).
func (h *RecordHandler) Share(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	actorID, _ := GetUserID(c)
	slug := c.Param("slug")
	recordID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	var input domain.ShareRecordInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	share, err := h.shares.Share(c.Request.Context(), orgID, actorID, slug, recordID, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": share, "error": nil})
}

// Unshare handles DELETE /api/registry/objects/:slug/records/:id/share/:shareId.
func (h *RecordHandler) Unshare(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	actorID, _ := GetUserID(c)
	slug := c.Param("slug")
	recordID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	shareID, err := uuid.Parse(c.Param("shareId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid share id"})
		return
	}
	if err := h.shares.Unshare(c.Request.Context(), orgID, actorID, slug, recordID, shareID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "revoked", "error": nil})
}

// ListShares handles GET /api/registry/objects/:slug/records/:id/shares.
func (h *RecordHandler) ListShares(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	recordID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	shares, err := h.shares.List(c.Request.Context(), orgID, slug, recordID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": shares, "error": nil})
}

// SharedWithMe handles GET /api/registry/shared-with-me?slug=&limit=&offset= —
// records someone else owns that are shared to the caller as a user, via their
// role, or via a group.
func (h *RecordHandler) SharedWithMe(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	userID, _ := GetUserID(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	rows, total, err := h.shares.ListSharedWithMe(c.Request.Context(), orgID, userID, c.Query("slug"), limit, offset)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "error": nil})
}

// reservedListParams are query keys with dedicated meaning; everything else is
// treated as a relation/field filter (e.g. company, stage, contact, owner_user_id).
//
// Anything NOT listed here becomes a field filter, so forgetting to reserve a
// param does not produce an error — it produces a jsonb equality test against a
// field that does not exist, i.e. an empty list. That is how sort_by would have
// behaved before R7.3: `?sort_by=value` silently matched nothing instead of
// sorting. The cost of reserving a key is that an admin-defined field with that
// exact key stops being filterable, which is why the set stays this small.
var reservedListParams = map[string]bool{
	"limit": true, "q": true, "cursor": true, "tag_ids": true, "semantic": true,
	"sort_by": true, "sort_order": true, "filter": true,
}

// parseRecordListQuery builds the uniform list input from the request's query
// string — shared by List and ExportCSV so the CSV always exports exactly what
// the list shows. The reserved `filter` param carries a JSON filter AST (the
// same {op, rules:[{field,operator,value}…]} grammar reports and automation
// conditions use); a malformed one is a 400 here, and an AST naming unknown
// fields/operators is a 400 in RecordService — never a silently empty page.
func parseRecordListQuery(c *gin.Context) (domain.RecordListInput, error) {
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

	var filterAST *domain.ReportFilterGroup
	if raw := strings.TrimSpace(c.Query("filter")); raw != "" {
		var g domain.ReportFilterGroup
		if err := json.Unmarshal([]byte(raw), &g); err != nil {
			return domain.RecordListInput{}, domain.NewAppError(http.StatusBadRequest, "filter is not valid JSON")
		}
		filterAST = &g
	}

	return domain.RecordListInput{
		Limit:    limit,
		Q:        c.Query("q"),
		Cursor:   c.Query("cursor"),
		Filters:  filters,
		Filter:   filterAST,
		TagIDs:   tagIDs,
		Semantic: c.Query("semantic") == "true",
		// Passed through raw. RecordService normalises against the object's
		// declared sortable columns and echoes what it settled on in
		// RecordList.Sort, so the handler holds no opinion about either.
		SortBy:    c.Query("sort_by"),
		SortOrder: c.Query("sort_order"),
	}, nil
}

// List handles GET /api/registry/objects/:slug/records
func (h *RecordHandler) List(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")

	in, err := parseRecordListQuery(c)
	if err != nil {
		handleAppError(c, err)
		return
	}

	page, err := h.svc.List(c.Request.Context(), orgID, slug, in)
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

// RelatedLists handles GET /api/registry/objects/:slug/records/:id/related-lists.
// It returns the record's reverse related lists — every child object's records
// that point back at this record through a typed relation field (e.g. a contact's
// deals). Read-level: the underlying per-child List enforces OLS, so a caller only
// sees children they can read.
func (h *RecordHandler) RelatedLists(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	lists, err := h.related.ListRelatedLists(c.Request.Context(), orgID, slug, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": lists, "error": nil})
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
// Import / export (R8.2)
// ============================================================

// BulkImport handles POST /api/registry/objects/:slug/import — the generic
// column-mapped importer for companies/deals/custom objects (contacts keep
// the older contact-specific /api/contacts/import, which does dedup/company
// resolution this generic path does not attempt). Accepts a CSV file plus a
// `mapping` form field: JSON `{csv_header: field_key}`, built client-side
// against the object's schema. Writes through RecordService.BulkCreate, so
// FLS/OLS/validation and automation-suppression all come from there.
func (h *RecordHandler) BulkImport(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	userID := c.MustGet("user_id").(uuid.UUID)
	slug := c.Param("slug")

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "file is required"})
		return
	}
	var mapping map[string]string
	if err := json.Unmarshal([]byte(c.PostForm("mapping")), &mapping); err != nil || len(mapping) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "mapping is required: JSON object of {csv_header: field_key}"})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "failed to read file"})
		return
	}
	defer file.Close()

	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "failed to parse CSV: " + err.Error()})
		return
	}
	if len(rows) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "file must contain a header row and at least one data row"})
		return
	}

	colIdx := map[string]int{}
	for i, col := range rows[0] {
		colIdx[col] = i
	}

	writes := make([]domain.RecordWriteInput, 0, len(rows)-1)
	for _, row := range rows[1:] {
		fields := map[string]interface{}{}
		for csvHeader, fieldKey := range mapping {
			idx, ok := colIdx[csvHeader]
			if !ok || idx >= len(row) {
				continue
			}
			val := strings.TrimSpace(row[idx])
			if val != "" {
				fields[fieldKey] = val
			}
		}
		if len(fields) > 0 {
			writes = append(writes, domain.RecordWriteInput{Fields: fields})
		}
	}

	result, err := h.svc.BulkCreate(c.Request.Context(), orgID, userID, slug, writes)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result, "error": nil})
}

// exportPageLimit caps how many records ExportCSV pulls per RecordService.List
// call — the CapDataExport cap is on WHO can export, not how much; this is the
// no-silent-caps guard on volume (see the truncation notice below).
const exportPageLimit = 200

// exportRowCap is the hard ceiling on one export, mirroring bulkCreateRowCap's
// reasoning: a truncated export must say so, not read as complete.
const exportRowCap = 20000

// ExportCSV handles GET /api/registry/objects/:slug/records/export.csv. It
// streams through RecordService.List exactly like the list view (same filters,
// same cursor), so OLS/FLS apply per row identically — no separate security
// path to keep in sync.
func (h *RecordHandler) ExportCSV(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")

	// Shared with List, so the CSV exports exactly what the list shows — filter
	// AST included. Parsed before any header is written so a malformed filter is
	// still a proper JSON 400.
	in, err := parseRecordListQuery(c)
	if err != nil {
		handleAppError(c, err)
		return
	}
	in.Limit = exportPageLimit

	schema, err := h.registry.GetSchema(c.Request.Context(), orgID, slug)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+csvSafe(slug)+`-export.csv"`)
	w := csv.NewWriter(c.Writer)

	header := []string{"id", "number", "display"}
	for _, f := range schema.Fields {
		header = append(header, f.Key)
	}
	_ = w.Write(header)

	written := 0
	for {
		page, err := h.svc.List(c.Request.Context(), orgID, slug, in)
		if err != nil {
			// Headers are already flushed to the client at this point, so a mid-stream
			// error can't become a JSON error response — best-effort log-and-stop.
			break
		}
		for _, rec := range page.Records {
			if written >= exportRowCap {
				break
			}
			row := []string{rec.ID.String(), csvSafe(rec.Number), csvSafe(rec.Display)}
			for _, f := range schema.Fields {
				cell := ""
				if val, ok := rec.Fields[f.Key]; ok && val != nil {
					cell = fmt.Sprint(val)
				}
				row = append(row, csvSafe(cell))
			}
			_ = w.Write(row)
			written++
		}
		if page.NextCursor == "" || written >= exportRowCap {
			break
		}
		in.Cursor = page.NextCursor
	}
	w.Flush()
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
