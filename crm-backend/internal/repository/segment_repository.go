package repository

import (
	"context"
	"errors"
	"strconv"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SegmentRepository persists marketing segments + their membership and runs the
// compiled dynamic-segment queries. It implements domain.SegmentStore.
type SegmentRepository struct {
	db *gorm.DB
}

// NewSegmentRepository builds the repository.
func NewSegmentRepository(db *gorm.DB) *SegmentRepository { return &SegmentRepository{db: db} }

var _ domain.SegmentStore = (*SegmentRepository)(nil)

// scopeFromCtx reads the caller's row scope; a callerless (trusted in-process / M7)
// ctx resolves to DataScopeAll (org-only), matching the report runner.
func (r *SegmentRepository) scopeFromCtx(ctx context.Context) reportScope {
	scope, userID, roleID, ok := extractCallerScope(ctx)
	if !ok {
		scope = domain.DataScopeAll
	}
	return reportScope{Scope: scope, UserID: userID, RoleID: roleID}
}

// ── segment CRUD ──────────────────────────────────────────────────────────────

func (r *SegmentRepository) CreateSegment(ctx context.Context, s *domain.Segment) error {
	if len(s.Definition) == 0 {
		s.Definition = []byte("{}")
	}
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *SegmentRepository) GetSegmentByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Segment, error) {
	var s domain.Segment
	err := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).First(&s).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *SegmentRepository) ListSegmentsByOrg(ctx context.Context, orgID uuid.UUID) ([]domain.Segment, error) {
	var rows []domain.Segment
	if err := r.db.WithContext(ctx).Where("org_id = ?", orgID).Order("updated_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *SegmentRepository) UpdateSegment(ctx context.Context, s *domain.Segment) error {
	return r.db.WithContext(ctx).Save(s).Error
}

func (r *SegmentRepository) SoftDeleteSegment(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).Delete(&domain.Segment{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// SetSegmentCount persists the durable cached count (the coarse fallback below the
// Redis hot layer).
func (r *SegmentRepository) SetSegmentCount(ctx context.Context, orgID, id uuid.UUID, count int) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE marketing_segments SET count_cached = ?, count_cached_at = NOW()
		WHERE org_id = ? AND id = ?`, count, orgID, id).Error
}

// ValidateDefinition delegates to the pure compile-check.
func (r *SegmentRepository) ValidateDefinition(catalog []domain.ReportField, ast domain.SegmentFilter) error {
	return ValidateSegmentFilter(catalog, ast)
}

// ── dynamic query ─────────────────────────────────────────────────────────────

func (r *SegmentRepository) fieldMap(catalog []domain.ReportField) map[string]domain.ReportField {
	m := make(map[string]domain.ReportField, len(catalog))
	for _, f := range catalog {
		m[f.Key] = f
	}
	return m
}

// CountDynamic runs SELECT COUNT(*) over the compiled predicate under the caller's
// scope (org + row-access + AST).
func (r *SegmentRepository) CountDynamic(ctx context.Context, orgID uuid.UUID, catalog []domain.ReportField, ast domain.SegmentFilter) (int, error) {
	where, args, err := buildSegmentWhere(r.fieldMap(catalog), ast, orgID, r.scopeFromCtx(ctx))
	if err != nil {
		return 0, err
	}
	var n int64
	if err := r.db.WithContext(ctx).Raw("SELECT COUNT(*) FROM contacts WHERE "+where, args...).Scan(&n).Error; err != nil {
		return 0, err
	}
	return int(n), nil
}

// PreviewDynamic returns a sample of matching contacts (id + display name + email
// only) newest-first.
func (r *SegmentRepository) PreviewDynamic(ctx context.Context, orgID uuid.UUID, catalog []domain.ReportField, ast domain.SegmentFilter, limit int) ([]domain.SegmentContactRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where, args, err := buildSegmentWhere(r.fieldMap(catalog), ast, orgID, r.scopeFromCtx(ctx))
	if err != nil {
		return nil, err
	}
	q := `SELECT contacts.id AS id,
	             TRIM(COALESCE(contacts.first_name,'') || ' ' || COALESCE(contacts.last_name,'')) AS name,
	             COALESCE(contacts.email,'') AS email
	      FROM contacts WHERE ` + where + ` ORDER BY contacts.created_at DESC LIMIT ` + strconv.Itoa(limit)
	var rows []domain.SegmentContactRow
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ── static list membership ──────────────────────────────────────────────────

// staticScoped returns a *gorm.DB over marketing_segment_static_members joined to
// contacts and restricted to the caller's org + row scope + live contacts.
func (r *SegmentRepository) staticScoped(ctx context.Context, orgID, segmentID uuid.UUID) *gorm.DB {
	db := r.db.WithContext(ctx).
		Table("marketing_segment_static_members sm").
		Joins("JOIN contacts ON contacts.id = sm.contact_id").
		Where("sm.segment_id = ?", segmentID).
		Where("contacts.deleted_at IS NULL")
	return applyScopeFromCtx(db, ctx, orgID, "contacts", "contact")
}

func (r *SegmentRepository) CountStatic(ctx context.Context, orgID, segmentID uuid.UUID) (int, error) {
	var n int64
	if err := r.staticScoped(ctx, orgID, segmentID).Count(&n).Error; err != nil {
		return 0, err
	}
	return int(n), nil
}

func (r *SegmentRepository) PreviewStatic(ctx context.Context, orgID, segmentID uuid.UUID, limit int) ([]domain.SegmentContactRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []domain.SegmentContactRow
	err := r.staticScoped(ctx, orgID, segmentID).
		Select("contacts.id AS id, TRIM(COALESCE(contacts.first_name,'') || ' ' || COALESCE(contacts.last_name,'')) AS name, COALESCE(contacts.email,'') AS email").
		Order("contacts.created_at DESC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// AddStaticMembers inserts membership rows for the given contacts, but ONLY those
// that belong to the org (a cross-org id is silently ignored, never added), deduped
// on the composite PK.
func (r *SegmentRepository) AddStaticMembers(ctx context.Context, orgID, segmentID uuid.UUID, contactIDs []uuid.UUID, source string) (int, error) {
	if len(contactIDs) == 0 {
		return 0, nil
	}
	// The write path re-derives the caller's row scope exactly like the read path
	// (staticScoped): a row-scoped caller may only add contacts they can SEE, so an
	// 'own'-scoped caller can't inject a colleague's contact into a list an org-wide
	// resolve (or the M7 callerless send) will act on. A callerless / DataScopeAll ctx
	// yields no predicate → org-only, matching the report runner and Ingest.
	where := "c.id IN ? AND c.org_id = ? AND c.deleted_at IS NULL"
	args := []any{segmentID, source, contactIDs, orgID}
	if aArgs, ok := accessArgsFromCtx(ctx, orgID, "c", "contact", false); ok {
		if sql, sqlArgs := RecordAccessPredicate(aArgs); sql != "" {
			where += " AND " + sql
			args = append(args, sqlArgs...)
		}
	}
	res := r.db.WithContext(ctx).Exec(`
		INSERT INTO marketing_segment_static_members (segment_id, contact_id, source)
		SELECT ?, c.id, ? FROM contacts c
		WHERE `+where+`
		ON CONFLICT (segment_id, contact_id) DO NOTHING`,
		args...)
	return int(res.RowsAffected), res.Error
}

func (r *SegmentRepository) RemoveStaticMember(ctx context.Context, orgID, segmentID, contactID uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).
		Where("segment_id = ? AND contact_id = ?", segmentID, contactID).
		Delete(&domain.SegmentStaticMember{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// TagBelongsToOrg validates a tag-leaf id at save time so a caller cannot probe
// cross-org tag membership by id.
func (r *SegmentRepository) TagBelongsToOrg(ctx context.Context, orgID, tagID uuid.UUID) (bool, error) {
	var ok bool
	err := r.db.WithContext(ctx).Raw(
		"SELECT EXISTS(SELECT 1 FROM tags WHERE id = ? AND org_id = ?)", tagID, orgID).Scan(&ok).Error
	return ok, err
}
