package http

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The composite record-page endpoint. The record detail page needs five reads
// (schema, record, related lists, record tags, tag palette) plus one read per
// relation/mirror field for its display label. Served individually, a remote
// client pays a network round trip for each; this endpoint folds all of them
// into ONE response, with the sub-reads running concurrently server-side where
// the DB is ~1ms away. The individual endpoints remain — this is additive, and
// the frontend falls back to them if this one is unavailable (deploy skew).

// recordPage is the one-shot payload for a record detail page.
type recordPage struct {
	Schema       *domain.ObjectDescriptor `json:"schema"`
	Record       *domain.UniformRecord    `json:"record"`
	RelatedLists []domain.RelatedList     `json:"related_lists"`
	Tags         []domain.Tag             `json:"tags"`
	AllTags      []domain.Tag             `json:"all_tags"`
	// RelationLabels maps a relation field's key to its target record's display
	// title (e.g. company → "Stark Industries"), so the browser doesn't have to
	// fetch each target record just to render its name.
	RelationLabels map[string]string `json:"relation_labels"`
	// MirrorValues maps a mirror field's key to the linked record's source-field
	// value, pre-resolved for the same reason.
	MirrorValues map[string]string `json:"mirror_values"`
}

// GetPage handles GET /api/registry/objects/:slug/records/:id/page.
//
// Schema and record errors are fatal (they carry the 403/404 the caller needs);
// the auxiliary panels are best-effort and degrade to empty, matching how the
// frontend treated their individual endpoints (`.catch(() => [])`).
func (h *RecordHandler) GetPage(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	ctx := c.Request.Context()

	page := &recordPage{
		RelatedLists:   []domain.RelatedList{},
		Tags:           []domain.Tag{},
		AllTags:        []domain.Tag{},
		RelationLabels: map[string]string{},
		MirrorValues:   map[string]string{},
	}

	var schemaErr, recordErr error
	var wg sync.WaitGroup
	wg.Add(5)
	go func() {
		defer wg.Done()
		s, err := h.registry.GetSchema(ctx, orgID, slug)
		if err != nil {
			schemaErr = err
			return
		}
		foldLayout(ctx, h.layoutUC, h.authz, orgID, slug, s)
		page.Schema = s
	}()
	go func() {
		defer wg.Done()
		r, err := h.svc.Get(ctx, orgID, slug, id)
		if err != nil {
			recordErr = err
			return
		}
		page.Record = r
	}()
	go func() {
		defer wg.Done()
		if lists, err := h.related.ListRelatedLists(ctx, orgID, slug, id); err == nil {
			page.RelatedLists = lists
		}
	}()
	go func() {
		defer wg.Done()
		if tags, err := h.svc.ListTags(ctx, orgID, slug, id); err == nil {
			page.Tags = tags
		}
	}()
	go func() {
		defer wg.Done()
		if h.tags == nil {
			return
		}
		if all, err := h.tags.List(ctx, orgID); err == nil {
			page.AllTags = all
		}
	}()
	wg.Wait()

	if schemaErr != nil {
		handleAppError(c, schemaErr)
		return
	}
	if recordErr != nil {
		handleAppError(c, recordErr)
		return
	}

	resolveRecordLabels(ctx, h.svc, orgID, page)

	c.JSON(http.StatusOK, gin.H{"data": page, "error": nil})
}

// resolveRecordLabels resolves, server-side, the display strings the detail page
// used to fetch from the browser one target record at a time: each relation
// field's target title and each mirror field's source value. Best-effort per
// field — an unreadable (OLS) or dangling target simply contributes no label —
// and deduplicated so several fields pointing at the same record cost one read.
// Reads go through RecordService.Get, so OLS/FLS apply exactly as they did to
// the browser's own fetches. The stage pseudo-relation (empty target slug) is
// left to the client, which resolves it against the pipeline-stage list.
func resolveRecordLabels(ctx context.Context, svc domain.RecordService, orgID uuid.UUID, page *recordPage) {
	if page.Schema == nil || page.Record == nil {
		return
	}
	fieldByKey := map[string]domain.FieldDescriptor{}
	for _, f := range page.Schema.Fields {
		fieldByKey[f.Key] = f
	}

	type ref struct {
		slug string
		id   uuid.UUID
	}
	relTargets := map[string]ref{} // relation field key → its target record
	mirTargets := map[string]ref{} // mirror field key → its via-relation's target
	for _, f := range page.Schema.Fields {
		switch {
		case f.Type == "relation" && f.TargetSlug != "":
			if id, ok := parseRecordRef(page.Record.Fields[f.Key]); ok {
				relTargets[f.Key] = ref{f.TargetSlug, id}
			}
		case f.Type == "mirror" && f.ViaField != "" && f.SourceField != "":
			via, ok := fieldByKey[f.ViaField]
			if !ok || via.TargetSlug == "" {
				continue
			}
			if id, ok := parseRecordRef(page.Record.Fields[f.ViaField]); ok {
				mirTargets[f.Key] = ref{via.TargetSlug, id}
			}
		}
	}

	need := map[ref]bool{}
	for _, r := range relTargets {
		need[r] = true
	}
	for _, r := range mirTargets {
		need[r] = true
	}
	if len(need) == 0 {
		return
	}

	var mu sync.Mutex
	fetched := map[ref]*domain.UniformRecord{}
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for r := range need {
		wg.Add(1)
		go func(r ref) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if target, err := svc.Get(ctx, orgID, r.slug, r.id); err == nil {
				mu.Lock()
				fetched[r] = target
				mu.Unlock()
			}
		}(r)
	}
	wg.Wait()

	for key, r := range relTargets {
		if t := fetched[r]; t != nil && t.Display != "" {
			page.RelationLabels[key] = t.Display
		}
	}
	for key, r := range mirTargets {
		t := fetched[r]
		if t == nil {
			continue
		}
		if v := displayValue(t.Fields[fieldByKey[key].SourceField]); v != "" {
			page.MirrorValues[key] = v
		}
	}
}

// parseRecordRef reads a relation field value as a record id. Relation values
// are UUID strings in the uniform shape; anything else is treated as "no link".
func parseRecordRef(v interface{}) (uuid.UUID, bool) {
	s, ok := v.(string)
	if !ok || s == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// displayValue renders a mirrored field value the way the browser's String()
// would — notably plain decimal for numbers, since JSON numbers arrive as
// float64 and fmt's %v would print large ones in scientific notation.
func displayValue(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}
