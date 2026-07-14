package repository

import (
	"context"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// recordShareRepository persists record_shares rows (U6.2). It mirrors
// reportShareRepository: an upserting Create keyed by the table's unique index, a
// batch name resolver so role/group targets render, and a shared-with-me lister.
type recordShareRepository struct {
	db *gorm.DB
}

func NewRecordShareRepository(db *gorm.DB) domain.RecordShareRepository {
	return &recordShareRepository{db: db}
}

// Upsert creates the grant or re-levels an existing one. The unique index on
// (record_type, record_id, target_type, target_id) makes this idempotent without a
// check-then-insert race — and, unlike the pre-U6 code, re-sharing at a different
// level actually changes the level instead of silently keeping the old one.
func (r *recordShareRepository) Upsert(ctx context.Context, s *domain.RecordShare) error {
	// RETURNING, so the caller gets the row's REAL id and timestamp back. Without it
	// the API echoed a share with a zero uuid, and the UI had nothing to revoke the
	// grant it had just created with.
	var out struct {
		ID        uuid.UUID
		CreatedAt time.Time
	}
	if err := r.db.WithContext(ctx).Raw(`
		INSERT INTO record_shares (org_id, record_type, record_id, target_type, target_id, grantee_user_id, permission_level, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (record_type, record_id, target_type, target_id)
		DO UPDATE SET permission_level = EXCLUDED.permission_level
		RETURNING id, created_at`,
		s.OrgID, s.RecordType, s.RecordID, s.TargetType, s.TargetID, s.GranteeUserID, s.PermissionLevel, s.CreatedBy).
		Scan(&out).Error; err != nil {
		return err
	}
	s.ID = out.ID
	s.CreatedAt = out.CreatedAt
	return nil
}

func (r *recordShareRepository) DeleteByID(ctx context.Context, orgID, id uuid.UUID, recordType string, recordID uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ? AND record_type = ? AND record_id = ?", orgID, id, recordType, recordID).
		Delete(&domain.RecordShare{})
	return res.RowsAffected, res.Error
}

func (r *recordShareRepository) DeleteByTarget(ctx context.Context, orgID uuid.UUID, targetType string, targetID uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("org_id = ? AND target_type = ? AND target_id = ?", orgID, targetType, targetID).
		Delete(&domain.RecordShare{})
	return res.RowsAffected, res.Error
}

// ListByRecord resolves each share's target display name (user / role / group) for
// the share dialog. A single LEFT JOIN on users — the pre-U6 shape — cannot render
// a role or group target at all.
func (r *recordShareRepository) ListByRecord(ctx context.Context, orgID uuid.UUID, recordType string, recordID uuid.UUID) ([]domain.RecordShareView, error) {
	var raw []domain.RecordShare
	if err := r.db.WithContext(ctx).
		Where("org_id = ? AND record_type = ? AND record_id = ?", orgID, recordType, recordID).
		Order("created_at ASC").
		Find(&raw).Error; err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return []domain.RecordShareView{}, nil
	}

	var userIDs, roleIDs, groupIDs []uuid.UUID
	for _, s := range raw {
		switch s.TargetType {
		case domain.ShareTargetUser:
			userIDs = append(userIDs, s.TargetID)
		case domain.ShareTargetRole:
			roleIDs = append(roleIDs, s.TargetID)
		case domain.ShareTargetGroup:
			groupIDs = append(groupIDs, s.TargetID)
		}
	}
	names, err := r.resolveTargetNames(ctx, userIDs, roleIDs, groupIDs)
	if err != nil {
		return nil, err
	}

	out := make([]domain.RecordShareView, 0, len(raw))
	for _, s := range raw {
		name := names[s.TargetID]
		if name == "" {
			name = "(removed)"
		}
		out = append(out, domain.RecordShareView{
			ID: s.ID, TargetType: s.TargetType, TargetID: s.TargetID,
			TargetName: name, Level: s.PermissionLevel, CreatedAt: s.CreatedAt,
		})
	}
	return out, nil
}

func (r *recordShareRepository) resolveTargetNames(ctx context.Context, userIDs, roleIDs, groupIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	names := map[uuid.UUID]string{}
	load := func(sql string, ids []uuid.UUID) error {
		if len(ids) == 0 {
			return nil
		}
		type row struct {
			ID   uuid.UUID
			Name string
		}
		var rows []row
		if err := r.db.WithContext(ctx).Raw(sql, ids).Scan(&rows).Error; err != nil {
			return err
		}
		for _, rw := range rows {
			names[rw.ID] = rw.Name
		}
		return nil
	}
	if err := load(`SELECT id, COALESCE(NULLIF(full_name,''), NULLIF(TRIM(first_name||' '||last_name),''), email) AS name FROM users WHERE id IN ?`, userIDs); err != nil {
		return nil, err
	}
	if err := load(`SELECT id, name FROM roles WHERE id IN ?`, roleIDs); err != nil {
		return nil, err
	}
	if err := load(`SELECT id, name FROM user_groups WHERE id IN ? AND deleted_at IS NULL`, groupIDs); err != nil {
		return nil, err
	}
	return names, nil
}

// BestLevelFor returns the highest level any share row grants the identity on the
// record — the record twin of the report level resolver. 'none' when nothing
// matches.
func (r *recordShareRepository) BestLevelFor(ctx context.Context, orgID uuid.UUID, recordType string, recordID uuid.UUID, ident domain.ShareIdentity) (string, error) {
	var rows []domain.RecordShare
	if err := r.db.WithContext(ctx).
		Where("org_id = ? AND record_type = ? AND record_id = ?", orgID, recordType, recordID).
		Find(&rows).Error; err != nil {
		return domain.ShareLevelNone, err
	}
	groups := make(map[uuid.UUID]bool, len(ident.GroupIDs))
	for _, g := range ident.GroupIDs {
		groups[g] = true
	}
	best := domain.ShareLevelNone
	for _, s := range rows {
		match := false
		switch s.TargetType {
		case domain.ShareTargetUser:
			match = s.TargetID == ident.UserID
		case domain.ShareTargetRole:
			// A Nil role id must never match a role-targeted row.
			match = ident.RoleID != uuid.Nil && s.TargetID == ident.RoleID
		case domain.ShareTargetGroup:
			match = groups[s.TargetID]
		}
		if match && domain.ShareLevelRank(s.PermissionLevel) > domain.ShareLevelRank(best) {
			best = s.PermissionLevel
		}
	}
	return best, nil
}

// sharedWithMeSQL is one UNION arm per shareable object. Semantics: "records shared
// TO me that I do NOT own". Not "records I can see thanks to a share" — for an
// 'all'-scoped role the row predicate short-circuits and shares are irrelevant, so
// that reading would render this page empty for every admin and manager.
const sharedWithMeSQL = `
SELECT * FROM (
	SELECT 'contact' AS object_slug,
	       'Contacts' AS object_label,
	       c.id AS record_id,
	       COALESCE(NULLIF(TRIM(c.first_name || ' ' || c.last_name), ''), c.email, 'Contact') AS display,
	       rs.permission_level AS level,
	       COALESCE(NULLIF(u.full_name, ''), u.email, '') AS owner_name,
	       c.updated_at AS updated_at
	FROM record_shares rs
	JOIN contacts c ON c.id = rs.record_id AND c.deleted_at IS NULL
	LEFT JOIN users u ON u.id = c.owner_user_id
	WHERE rs.org_id = ? AND rs.record_type = 'contact'
	  AND (c.owner_user_id IS NULL OR c.owner_user_id <> ?)
	  AND {{MATCH}}

	UNION ALL

	SELECT 'deal', 'Deals', d.id,
	       COALESCE(NULLIF(d.title, ''), 'Deal'),
	       rs.permission_level,
	       COALESCE(NULLIF(u.full_name, ''), u.email, ''),
	       d.updated_at
	FROM record_shares rs
	JOIN deals d ON d.id = rs.record_id AND d.deleted_at IS NULL
	LEFT JOIN users u ON u.id = d.owner_user_id
	WHERE rs.org_id = ? AND rs.record_type = 'deal'
	  AND (d.owner_user_id IS NULL OR d.owner_user_id <> ?)
	  AND {{MATCH}}

	UNION ALL

	SELECT od.slug, od.label_plural, cor.id,
	       COALESCE(NULLIF(cor.display_name, ''), 'Record'),
	       rs.permission_level,
	       COALESCE(NULLIF(u.full_name, ''), u.email, ''),
	       cor.updated_at
	FROM record_shares rs
	JOIN custom_object_records cor ON cor.id = rs.record_id AND cor.deleted_at IS NULL
	JOIN object_defs od ON od.id = cor.object_def_id AND od.slug = rs.record_type AND od.deleted_at IS NULL
	LEFT JOIN users u ON u.id = cor.owner_user_id
	WHERE rs.org_id = ? AND cor.org_id = ?
	  AND (cor.owner_user_id IS NULL OR cor.owner_user_id <> ?)
	  AND {{MATCH}}
) shared`

// shareMatchSQL matches a share row against the caller's handles (user / role /
// group). Group ids are expanded inline via a subquery rather than an IN list so
// the same SQL works no matter how many groups the caller belongs to.
const shareMatchSQL = `(
	(rs.target_type = 'user'  AND rs.target_id = ?)
 OR (rs.target_type = 'role'  AND rs.target_id = ?)
 OR (rs.target_type = 'group' AND rs.target_id IN (
		SELECT ugm.group_id FROM user_group_members ugm
		JOIN user_groups ug ON ug.id = ugm.group_id AND ug.deleted_at IS NULL
		WHERE ugm.user_id = ? AND ugm.org_id = ?))
)`

// SharedRecordTypes lists the object slugs the caller holds any share on — the
// input to the usecase's OLS whitelist, so the object filter can be pushed into
// the page query instead of applied to its result.
func (r *recordShareRepository) SharedRecordTypes(ctx context.Context, orgID uuid.UUID, ident domain.ShareIdentity) ([]string, error) {
	var out []string
	err := r.db.WithContext(ctx).Raw(
		`SELECT DISTINCT rs.record_type FROM record_shares rs WHERE rs.org_id = ? AND `+shareMatchSQL,
		orgID, ident.UserID, ident.RoleID, ident.UserID, orgID).Scan(&out).Error
	return out, err
}

// ListSharedWithMe returns the records shared to the caller that they do not own,
// restricted to allowedSlugs (the objects their role may read).
func (r *recordShareRepository) ListSharedWithMe(ctx context.Context, orgID uuid.UUID, ident domain.ShareIdentity, allowedSlugs []string, limit, offset int) ([]domain.SharedRecordView, int64, error) {
	if len(allowedSlugs) == 0 {
		return []domain.SharedRecordView{}, 0, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	base := strings.ReplaceAll(sharedWithMeSQL, "{{MATCH}}", shareMatchSQL)

	// Args in SQL order. Each arm: its org/owner args, then the four match args
	// (me, my role, me, org). The custom arm carries org_id twice (share + record).
	matchArgs := []any{ident.UserID, ident.RoleID, ident.UserID, orgID}
	args := []any{orgID, ident.UserID}
	args = append(args, matchArgs...)
	args = append(args, orgID, ident.UserID)
	args = append(args, matchArgs...)
	args = append(args, orgID, orgID, ident.UserID)
	args = append(args, matchArgs...)

	// The OLS whitelist filters the UNION's output, so the count and the page agree.
	where := " WHERE object_slug IN ?"
	args = append(args, allowedSlugs)

	var total int64
	if err := r.db.WithContext(ctx).
		Raw("SELECT COUNT(*) FROM ("+base+where+") counted", args...).
		Scan(&total).Error; err != nil {
		return nil, 0, err
	}

	pageArgs := append(append([]any{}, args...), limit, offset)
	var out []domain.SharedRecordView
	if err := r.db.WithContext(ctx).
		Raw(base+where+" ORDER BY updated_at DESC LIMIT ? OFFSET ?", pageArgs...).
		Scan(&out).Error; err != nil {
		return nil, 0, err
	}
	if out == nil {
		out = []domain.SharedRecordView{}
	}
	return out, total, nil
}
