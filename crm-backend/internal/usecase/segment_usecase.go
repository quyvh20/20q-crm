package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/datatypes"
)

// segmentUseCase is the M5 audiences surface. It mirrors reportUseCase's execution
// order — OLS (may I read contacts?) → catalog → FLS (does the AST touch a hidden
// field?) → run under the caller's row scope — reusing reportCatalogForDef for the
// contact field catalog (which resolves the crm_-prefixed attribution keys per org)
// and the shared SegmentStore compiler for injection-safe SQL.
type segmentUseCase struct {
	store    domain.SegmentStore
	registry domain.ObjectRegistryRepository
	authz    domain.RecordAuthorizer
	redis    *redis.Client
}

// NewSegmentUseCase builds the usecase. redisClient may be nil (dev / no Redis) — the
// count cache degrades to compute-live.
func NewSegmentUseCase(store domain.SegmentStore, registry domain.ObjectRegistryRepository, authz domain.RecordAuthorizer, redisClient *redis.Client) domain.SegmentUseCase {
	return &segmentUseCase{store: store, registry: registry, authz: authz, redis: redisClient}
}

const segmentCountCacheTTL = 60 * time.Second

const segmentContactSlug = "contact"

// maxSegmentNameLen matches the marketing_segments.name VARCHAR(160) column, counted
// in runes so a multi-byte name is bounded the same way Postgres bounds it (avoiding
// a 22001 → HTTP 500 on save).
const maxSegmentNameLen = 160

func validateSegmentName(name string) error {
	if name == "" {
		return domain.NewAppError(http.StatusBadRequest, "name is required")
	}
	if utf8.RuneCountInString(name) > maxSegmentNameLen {
		return domain.NewAppError(http.StatusBadRequest, "name must be 160 characters or fewer")
	}
	return nil
}

var errSegmentFieldHidden = domain.NewAppError(http.StatusForbidden, "segment references a field you cannot access")

// contactCatalog builds the whitelisted contact field catalog (object_fields +
// virtual columns), reusing the report catalog builder so attribution keys and the
// crm_-prefix collision resolve per-org automatically.
func (uc *segmentUseCase) contactCatalog(ctx context.Context, orgID uuid.UUID) ([]domain.ReportField, error) {
	if err := uc.registry.EnsureSystemObjects(ctx, orgID); err != nil {
		return nil, err
	}
	def, err := uc.registry.GetDefBySlug(ctx, orgID, segmentContactSlug)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, domain.NewAppError(http.StatusNotFound, "contact object not found")
	}
	fields, err := uc.registry.ListFields(ctx, def.ID)
	if err != nil {
		return nil, err
	}
	return reportCatalogForDef(def, fields), nil
}

func (uc *segmentUseCase) List(ctx context.Context, orgID uuid.UUID) ([]domain.Segment, error) {
	if err := uc.authz.Authorize(ctx, orgID, segmentContactSlug, domain.ActionRead); err != nil {
		return nil, err
	}
	segs, err := uc.store.ListSegmentsByOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for i := range segs {
		uc.applyReadRedactions(ctx, orgID, &segs[i])
	}
	return segs, nil
}

func (uc *segmentUseCase) Get(ctx context.Context, orgID, id uuid.UUID) (*domain.Segment, error) {
	if err := uc.authz.Authorize(ctx, orgID, segmentContactSlug, domain.ActionRead); err != nil {
		return nil, err
	}
	seg, err := uc.store.GetSegmentByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if seg == nil {
		return nil, domain.NewAppError(http.StatusNotFound, "segment not found")
	}
	uc.applyReadRedactions(ctx, orgID, seg)
	return seg, nil
}

// applyReadRedactions strips two caller-specific leaks from a segment returned by a
// read path: (1) count_cached is the ORG-WIDE total, shareable only with an org-wide
// caller (matching Count's own anti-leak rule) — a row-scoped caller sees 0; (2) if
// the caller's FLS mask hides a field the dynamic definition filters on, the whole
// definition is blanked (the hidden field key + threshold must not leak) and the
// segment is flagged restricted. The stored row is never mutated (this edits a copy).
func (uc *segmentUseCase) applyReadRedactions(ctx context.Context, orgID uuid.UUID, seg *domain.Segment) {
	if seg == nil {
		return
	}
	if !uc.orgWideCaller(ctx) {
		seg.CountCached = 0
		seg.CountCachedAt = nil
	}
	if seg.Type == domain.SegmentTypeDynamic && uc.definitionTouchesHidden(ctx, orgID, seg) {
		seg.Definition = datatypes.JSON("{}")
		seg.DefinitionRestricted = true
	}
}

// definitionTouchesHidden reports whether the caller's FLS mask hides any field the
// dynamic definition filters on. Used to restrict a read copy and to reject an edit
// by a caller who cannot see every field the segment already references.
func (uc *segmentUseCase) definitionTouchesHidden(ctx context.Context, orgID uuid.UUID, seg *domain.Segment) bool {
	if seg == nil || seg.Type != domain.SegmentTypeDynamic || len(seg.Definition) == 0 {
		return false
	}
	mask := uc.authz.FieldMask(ctx, orgID, segmentContactSlug)
	if mask.Empty() {
		return false
	}
	var ast domain.SegmentFilter
	if err := json.Unmarshal(seg.Definition, &ast); err != nil {
		return false
	}
	var fields, tags []string
	ast.Touched(&fields, &tags)
	for _, k := range fields {
		if mask.IsHidden(k) {
			return true
		}
	}
	return false
}

func (uc *segmentUseCase) Create(ctx context.Context, orgID, userID uuid.UUID, in domain.SegmentInput) (*domain.Segment, error) {
	if err := uc.authz.Authorize(ctx, orgID, segmentContactSlug, domain.ActionRead); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if err := validateSegmentName(name); err != nil {
		return nil, err
	}
	typ := strings.TrimSpace(in.Type)
	if typ == "" {
		typ = domain.SegmentTypeDynamic
	}
	if typ != domain.SegmentTypeDynamic && typ != domain.SegmentTypeStatic {
		return nil, domain.NewAppError(http.StatusBadRequest, "invalid segment type")
	}
	def := []byte(in.Definition)
	if len(def) == 0 {
		def = []byte("{}")
	}
	if typ == domain.SegmentTypeDynamic {
		if err := uc.validateDefinition(ctx, orgID, def); err != nil {
			return nil, err
		}
	}
	seg := &domain.Segment{OrgID: orgID, Name: name, Type: typ, Definition: datatypes.JSON(def)}
	if userID != uuid.Nil {
		seg.CreatedBy = &userID
	}
	if err := uc.store.CreateSegment(ctx, seg); err != nil {
		return nil, err
	}
	return seg, nil
}

func (uc *segmentUseCase) Update(ctx context.Context, orgID, id uuid.UUID, in domain.SegmentInput) (*domain.Segment, error) {
	if err := uc.authz.Authorize(ctx, orgID, segmentContactSlug, domain.ActionRead); err != nil {
		return nil, err
	}
	seg, err := uc.store.GetSegmentByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if seg == nil {
		return nil, domain.NewAppError(http.StatusNotFound, "segment not found")
	}
	// A caller whose FLS mask hides a field this segment ALREADY filters on cannot
	// edit it — otherwise a save would silently blank the hidden condition they were
	// never allowed to see (the read path returns it restricted).
	if uc.definitionTouchesHidden(ctx, orgID, seg) {
		return nil, errSegmentFieldHidden
	}
	name := strings.TrimSpace(in.Name)
	if err := validateSegmentName(name); err != nil {
		return nil, err
	}
	def := []byte(in.Definition)
	if len(def) == 0 {
		def = []byte("{}")
	}
	// Type is immutable after creation; validate against the existing type.
	if seg.Type == domain.SegmentTypeDynamic {
		if err := uc.validateDefinition(ctx, orgID, def); err != nil {
			return nil, err
		}
	}
	seg.Name = name
	seg.Definition = datatypes.JSON(def)
	seg.CountCached = 0
	seg.CountCachedAt = nil
	if err := uc.store.UpdateSegment(ctx, seg); err != nil {
		return nil, err
	}
	uc.invalidateCount(ctx, orgID, id)
	return seg, nil
}

func (uc *segmentUseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	if err := uc.authz.Authorize(ctx, orgID, segmentContactSlug, domain.ActionRead); err != nil {
		return err
	}
	removed, err := uc.store.SoftDeleteSegment(ctx, orgID, id)
	if err != nil {
		return err
	}
	if !removed {
		return domain.NewAppError(http.StatusNotFound, "segment not found")
	}
	uc.invalidateCount(ctx, orgID, id)
	return nil
}

func (uc *segmentUseCase) Preview(ctx context.Context, orgID, id uuid.UUID, limit int) (*domain.SegmentPreview, error) {
	if err := uc.authz.Authorize(ctx, orgID, segmentContactSlug, domain.ActionRead); err != nil {
		return nil, err
	}
	seg, err := uc.store.GetSegmentByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if seg == nil {
		return nil, domain.NewAppError(http.StatusNotFound, "segment not found")
	}
	if seg.Type == domain.SegmentTypeStatic {
		rows, err := uc.store.PreviewStatic(ctx, orgID, id, limit)
		if err != nil {
			return nil, err
		}
		cnt, err := uc.store.CountStatic(ctx, orgID, id)
		if err != nil {
			return nil, err
		}
		return &domain.SegmentPreview{Contacts: rows, Count: cnt}, nil
	}
	catalog, ast, err := uc.compileInputs(ctx, orgID, seg)
	if err != nil {
		return nil, err
	}
	rows, err := uc.store.PreviewDynamic(ctx, orgID, catalog, ast, limit)
	if err != nil {
		return nil, mapSegErr(err)
	}
	cnt, err := uc.store.CountDynamic(ctx, orgID, catalog, ast)
	if err != nil {
		return nil, mapSegErr(err)
	}
	return &domain.SegmentPreview{Contacts: rows, Count: cnt}, nil
}

// PreviewDefinition dry-runs an unsaved dynamic definition. It runs the full
// save-time validation (OLS → parse/compile-check → FLS → tag ownership) before
// touching the DB, so the builder's live count can never become a probe for a
// hidden field or a cross-org tag. Nothing is persisted and nothing is cached.
func (uc *segmentUseCase) PreviewDefinition(ctx context.Context, orgID uuid.UUID, definition json.RawMessage, limit int) (*domain.SegmentPreview, error) {
	if err := uc.authz.Authorize(ctx, orgID, segmentContactSlug, domain.ActionRead); err != nil {
		return nil, err
	}
	def := []byte(definition)
	if len(def) == 0 {
		def = []byte("{}")
	}
	if err := uc.validateDefinition(ctx, orgID, def); err != nil {
		return nil, err
	}
	catalog, ast, err := uc.compileInputs(ctx, orgID, &domain.Segment{Definition: datatypes.JSON(def)})
	if err != nil {
		return nil, err
	}
	rows, err := uc.store.PreviewDynamic(ctx, orgID, catalog, ast, limit)
	if err != nil {
		return nil, mapSegErr(err)
	}
	cnt, err := uc.store.CountDynamic(ctx, orgID, catalog, ast)
	if err != nil {
		return nil, mapSegErr(err)
	}
	return &domain.SegmentPreview{Contacts: rows, Count: cnt}, nil
}

func (uc *segmentUseCase) Count(ctx context.Context, orgID, id uuid.UUID) (int, error) {
	if err := uc.authz.Authorize(ctx, orgID, segmentContactSlug, domain.ActionRead); err != nil {
		return 0, err
	}
	seg, err := uc.store.GetSegmentByID(ctx, orgID, id)
	if err != nil {
		return 0, err
	}
	if seg == nil {
		return 0, domain.NewAppError(http.StatusNotFound, "segment not found")
	}
	// FLS is re-checked BEFORE the cache lookup: the cached VALUE is org-wide-shareable,
	// but the AUTHORIZATION to receive it is caller-specific — a field hidden after the
	// segment was saved must 403 even on a cache hit. compileInputs performs that gate
	// and yields the catalog/ast reused on a miss.
	var catalog []domain.ReportField
	var ast domain.SegmentFilter
	if seg.Type == domain.SegmentTypeDynamic {
		catalog, ast, err = uc.compileInputs(ctx, orgID, seg)
		if err != nil {
			return 0, err
		}
	}
	// Only an org-wide caller's count is shareable — a row-scoped caller's count is
	// caller-specific, so it is computed live and never cached (avoids a cross-user
	// count leak). The org-wide value is also what M7's callerless resolve will see.
	cacheable := uc.orgWideCaller(ctx)
	if cacheable {
		if n, ok := uc.cacheGet(ctx, orgID, id); ok {
			return n, nil
		}
	}
	var n int
	if seg.Type == domain.SegmentTypeStatic {
		n, err = uc.store.CountStatic(ctx, orgID, id)
	} else {
		n, err = uc.store.CountDynamic(ctx, orgID, catalog, ast)
	}
	if err != nil {
		return 0, mapSegErr(err)
	}
	if cacheable {
		uc.cacheSet(ctx, orgID, id, n)
		_ = uc.store.SetSegmentCount(ctx, orgID, id, n)
	}
	return n, nil
}

// AudienceQueryForSegments compiles the union(includes) minus union(excludes) of the
// given segments into a parameterized audience SELECT (M7). Callerless / org-wide: a
// bulk send has no acting user, and each referenced segment already passed FLS at its
// own save, so we trust the stored AST and apply no row scope (DataScopeAll). A
// missing (deleted / cross-org) segment simply contributes nothing.
func (uc *segmentUseCase) AudienceQueryForSegments(ctx context.Context, orgID uuid.UUID, includeIDs, excludeIDs []uuid.UUID) (*domain.AudienceQuery, error) {
	catalog, err := uc.contactCatalog(ctx, orgID)
	if err != nil {
		return nil, err
	}
	includes, err := uc.resolveSegments(ctx, orgID, includeIDs)
	if err != nil {
		return nil, err
	}
	excludes, err := uc.resolveSegments(ctx, orgID, excludeIDs)
	if err != nil {
		return nil, err
	}
	sql, args, err := uc.store.CompileAudienceQuery(orgID, catalog, includes, excludes)
	if err != nil {
		return nil, mapSegErr(err)
	}
	return &domain.AudienceQuery{SelectSQL: sql, Args: args}, nil
}

func (uc *segmentUseCase) resolveSegments(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]domain.ResolvedSegment, error) {
	out := make([]domain.ResolvedSegment, 0, len(ids))
	for _, id := range ids {
		seg, err := uc.store.GetSegmentByID(ctx, orgID, id)
		if err != nil {
			return nil, err
		}
		if seg == nil {
			continue
		}
		rs := domain.ResolvedSegment{ID: seg.ID, Type: seg.Type}
		if seg.Type == domain.SegmentTypeDynamic && len(seg.Definition) > 0 {
			if err := json.Unmarshal(seg.Definition, &rs.Filter); err != nil {
				return nil, domain.NewAppError(http.StatusBadRequest, "invalid segment definition")
			}
		}
		out = append(out, rs)
	}
	return out, nil
}

func (uc *segmentUseCase) ListFields(ctx context.Context, orgID uuid.UUID) ([]domain.ReportFieldDescriptor, error) {
	if err := uc.authz.Authorize(ctx, orgID, segmentContactSlug, domain.ActionRead); err != nil {
		return nil, err
	}
	catalog, err := uc.contactCatalog(ctx, orgID)
	if err != nil {
		return nil, err
	}
	mask := uc.authz.FieldMask(ctx, orgID, segmentContactSlug)
	out := make([]domain.ReportFieldDescriptor, 0, len(catalog))
	for _, f := range catalog {
		if mask.IsHidden(f.Key) {
			continue
		}
		out = append(out, domain.ReportFieldDescriptor{Key: f.Key, Label: f.Label, Type: f.Type, Options: f.Options})
	}
	return out, nil
}

func (uc *segmentUseCase) AddStaticMembers(ctx context.Context, orgID, id uuid.UUID, contactIDs []uuid.UUID) (int, error) {
	if err := uc.authz.Authorize(ctx, orgID, segmentContactSlug, domain.ActionRead); err != nil {
		return 0, err
	}
	seg, err := uc.store.GetSegmentByID(ctx, orgID, id)
	if err != nil {
		return 0, err
	}
	if seg == nil {
		return 0, domain.NewAppError(http.StatusNotFound, "segment not found")
	}
	if seg.Type != domain.SegmentTypeStatic {
		return 0, domain.NewAppError(http.StatusBadRequest, "not a static list")
	}
	n, err := uc.store.AddStaticMembers(ctx, orgID, id, contactIDs, "manual")
	if err != nil {
		return 0, err
	}
	uc.invalidateCount(ctx, orgID, id)
	return n, nil
}

func (uc *segmentUseCase) RemoveStaticMember(ctx context.Context, orgID, id, contactID uuid.UUID) (bool, error) {
	if err := uc.authz.Authorize(ctx, orgID, segmentContactSlug, domain.ActionRead); err != nil {
		return false, err
	}
	seg, err := uc.store.GetSegmentByID(ctx, orgID, id)
	if err != nil {
		return false, err
	}
	if seg == nil {
		return false, domain.NewAppError(http.StatusNotFound, "segment not found")
	}
	removed, err := uc.store.RemoveStaticMember(ctx, orgID, id, contactID)
	if err != nil {
		return false, err
	}
	uc.invalidateCount(ctx, orgID, id)
	return removed, nil
}

// ── internals ─────────────────────────────────────────────────────────────────

// validateDefinition parses + compile-checks the AST (catalog/operator/tag/structure)
// and enforces FLS (hidden field → 403) + tag ownership at save time.
func (uc *segmentUseCase) validateDefinition(ctx context.Context, orgID uuid.UUID, raw []byte) error {
	var ast domain.SegmentFilter
	if err := json.Unmarshal(raw, &ast); err != nil {
		return domain.NewAppError(http.StatusBadRequest, "invalid segment definition")
	}
	catalog, err := uc.contactCatalog(ctx, orgID)
	if err != nil {
		return err
	}
	if err := uc.store.ValidateDefinition(catalog, ast); err != nil {
		return domain.NewAppError(http.StatusBadRequest, err.Error())
	}
	var fields, tags []string
	ast.Touched(&fields, &tags)
	mask := uc.authz.FieldMask(ctx, orgID, segmentContactSlug)
	if !mask.Empty() {
		for _, k := range fields {
			if mask.IsHidden(k) {
				return errSegmentFieldHidden
			}
		}
	}
	for _, t := range tags {
		tid, err := uuid.Parse(t)
		if err != nil {
			return domain.NewAppError(http.StatusBadRequest, "invalid tag id")
		}
		ok, err := uc.store.TagBelongsToOrg(ctx, orgID, tid)
		if err != nil {
			return err
		}
		if !ok {
			return domain.NewAppError(http.StatusBadRequest, "unknown tag")
		}
	}
	return nil
}

// compileInputs builds the catalog + parses the AST and re-checks FLS at run time (a
// field hidden AFTER the segment was saved must not leak via preview/count).
func (uc *segmentUseCase) compileInputs(ctx context.Context, orgID uuid.UUID, seg *domain.Segment) ([]domain.ReportField, domain.SegmentFilter, error) {
	catalog, err := uc.contactCatalog(ctx, orgID)
	if err != nil {
		return nil, domain.SegmentFilter{}, err
	}
	var ast domain.SegmentFilter
	if len(seg.Definition) > 0 {
		if err := json.Unmarshal(seg.Definition, &ast); err != nil {
			return nil, domain.SegmentFilter{}, domain.NewAppError(http.StatusBadRequest, "invalid segment definition")
		}
	}
	mask := uc.authz.FieldMask(ctx, orgID, segmentContactSlug)
	if !mask.Empty() {
		var fields, tags []string
		ast.Touched(&fields, &tags)
		for _, k := range fields {
			if mask.IsHidden(k) {
				return nil, domain.SegmentFilter{}, errSegmentFieldHidden
			}
		}
	}
	return catalog, ast, nil
}

// orgWideCaller reports whether the caller sees every org row (owner or all-scope) —
// the only case where a segment count is shareable across users. Callerless
// (trusted in-process) counts as org-wide.
func (uc *segmentUseCase) orgWideCaller(ctx context.Context) bool {
	caller, ok := domain.CallerFromContext(ctx)
	if !ok {
		return true
	}
	return caller.IsOwner || caller.DataScope == domain.DataScopeAll
}

func (uc *segmentUseCase) cacheKey(orgID, id uuid.UUID) string {
	return fmt.Sprintf("marketing:seg:count:%s:%s", orgID, id)
}

func (uc *segmentUseCase) cacheGet(ctx context.Context, orgID, id uuid.UUID) (int, bool) {
	if uc.redis == nil {
		return 0, false
	}
	n, err := uc.redis.Get(ctx, uc.cacheKey(orgID, id)).Int()
	if err != nil {
		return 0, false
	}
	return n, true
}

func (uc *segmentUseCase) cacheSet(ctx context.Context, orgID, id uuid.UUID, n int) {
	if uc.redis == nil {
		return
	}
	_ = uc.redis.Set(ctx, uc.cacheKey(orgID, id), n, segmentCountCacheTTL).Err()
}

func (uc *segmentUseCase) invalidateCount(ctx context.Context, orgID, id uuid.UUID) {
	if uc.redis != nil {
		uc.redis.Del(ctx, uc.cacheKey(orgID, id))
	}
}

// mapSegErr turns the compiler's (defense-in-depth) "segment:"/"report:"-prefixed
// validation failures into 400s; anything else is a real DB error.
func mapSegErr(err error) error {
	if err == nil {
		return nil
	}
	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		return err
	}
	if strings.HasPrefix(err.Error(), "segment:") || strings.HasPrefix(err.Error(), "report:") {
		return domain.NewAppError(http.StatusBadRequest, err.Error())
	}
	return err
}
