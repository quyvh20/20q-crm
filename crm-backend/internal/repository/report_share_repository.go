package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// reportShareRepository persists report_shares rows and resolves the caller's
// share identity (role + groups) for level resolution.
type reportShareRepository struct {
	db *gorm.DB
}

func NewReportShareRepository(db *gorm.DB) domain.ReportShareRepository {
	return &reportShareRepository{db: db}
}

// Create upserts a share (re-sharing the same target updates the level).
func (r *reportShareRepository) Create(ctx context.Context, s *domain.ReportShare) error {
	return r.db.WithContext(ctx).Exec(`
		INSERT INTO report_shares (org_id, report_id, target_type, target_id, level, created_by)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (report_id, target_type, target_id)
		DO UPDATE SET level = EXCLUDED.level`,
		s.OrgID, s.ReportID, s.TargetType, s.TargetID, s.Level, s.CreatedBy).Error
}

func (r *reportShareRepository) ListRawByReport(ctx context.Context, orgID, reportID uuid.UUID) ([]domain.ReportShare, error) {
	var out []domain.ReportShare
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND report_id = ?", orgID, reportID).
		Order("created_at ASC").
		Find(&out).Error
	return out, err
}

// ListByReport resolves each share's target display name (user full name / role
// name / group name) for the share dialog.
func (r *reportShareRepository) ListByReport(ctx context.Context, orgID, reportID uuid.UUID) ([]domain.ReportShareView, error) {
	raw, err := r.ListRawByReport(ctx, orgID, reportID)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return []domain.ReportShareView{}, nil
	}

	// Batch-resolve names per target type.
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
	if err := load(`SELECT id, name FROM user_groups WHERE id IN ?`, groupIDs); err != nil {
		return nil, err
	}

	out := make([]domain.ReportShareView, 0, len(raw))
	for _, s := range raw {
		name := names[s.TargetID]
		if name == "" {
			name = "(removed)"
		}
		out = append(out, domain.ReportShareView{
			ID: s.ID, TargetType: s.TargetType, TargetID: s.TargetID,
			TargetName: name, Level: s.Level, CreatedAt: s.CreatedAt,
		})
	}
	return out, nil
}

func (r *reportShareRepository) Delete(ctx context.Context, orgID, reportID, shareID uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("org_id = ? AND report_id = ? AND id = ?", orgID, reportID, shareID).
		Delete(&domain.ReportShare{})
	return res.RowsAffected, res.Error
}

// GetShareIdentity resolves the caller's role id (org_users) and group ids
// (user_group_members) — the handles matched against report_shares. Record shares
// match against the same handles, so the implementation is shared
// (share_identity_repository.go).
func (r *reportShareRepository) GetShareIdentity(ctx context.Context, orgID, userID uuid.UUID) (domain.ShareIdentity, error) {
	return getShareIdentity(ctx, r.db, orgID, userID)
}
