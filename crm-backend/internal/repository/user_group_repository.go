package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// userGroupRepository persists user groups and their membership. Every query is
// org-scoped.
type userGroupRepository struct {
	db *gorm.DB
}

func NewUserGroupRepository(db *gorm.DB) domain.UserGroupRepository {
	return &userGroupRepository{db: db}
}

func (r *userGroupRepository) Create(ctx context.Context, g *domain.UserGroup) error {
	return r.db.WithContext(ctx).Create(g).Error
}

func (r *userGroupRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.UserGroup, error) {
	var g domain.UserGroup
	err := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).First(&g).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// List returns the org's groups newest-first with member counts and the
// resolved member roster (one extra query, joined to users).
func (r *userGroupRepository) List(ctx context.Context, orgID uuid.UUID) ([]domain.UserGroupView, error) {
	var groups []domain.UserGroup
	if err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("created_at DESC").
		Find(&groups).Error; err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return []domain.UserGroupView{}, nil
	}

	ids := make([]uuid.UUID, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.ID)
	}

	type memberRow struct {
		GroupID uuid.UUID
		UserID  uuid.UUID
		Name    string
		Email   string
	}
	var rows []memberRow
	if err := r.db.WithContext(ctx).Raw(`
		SELECT m.group_id, m.user_id,
		       COALESCE(NULLIF(u.full_name, ''), NULLIF(TRIM(u.first_name || ' ' || u.last_name), ''), u.email) AS name,
		       u.email
		FROM user_group_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.org_id = ? AND m.group_id IN ?
		ORDER BY name`, orgID, ids).Scan(&rows).Error; err != nil {
		return nil, err
	}

	byGroup := make(map[uuid.UUID][]domain.GroupMemberInfo, len(groups))
	for _, m := range rows {
		byGroup[m.GroupID] = append(byGroup[m.GroupID], domain.GroupMemberInfo{UserID: m.UserID, Name: m.Name, Email: m.Email})
	}

	out := make([]domain.UserGroupView, 0, len(groups))
	for _, g := range groups {
		members := byGroup[g.ID]
		out = append(out, domain.UserGroupView{
			ID: g.ID, Name: g.Name, Description: g.Description,
			MemberCount: len(members), Members: members, CreatedAt: g.CreatedAt,
		})
	}
	return out, nil
}

func (r *userGroupRepository) Update(ctx context.Context, g *domain.UserGroup) error {
	return r.db.WithContext(ctx).Save(g).Error
}

func (r *userGroupRepository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		Delete(&domain.UserGroup{}).Error
}

// AddMember is idempotent (ON CONFLICT DO NOTHING on the composite PK).
func (r *userGroupRepository) AddMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Exec(`
		INSERT INTO user_group_members (group_id, user_id, org_id)
		VALUES (?, ?, ?) ON CONFLICT (group_id, user_id) DO NOTHING`,
		groupID, userID, orgID).Error
}

func (r *userGroupRepository) RemoveMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Exec(`
		DELETE FROM user_group_members WHERE org_id = ? AND group_id = ? AND user_id = ?`,
		orgID, groupID, userID).Error
}

func (r *userGroupRepository) GroupIDsForUser(ctx context.Context, orgID, userID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := r.db.WithContext(ctx).Raw(`
		SELECT m.group_id FROM user_group_members m
		JOIN user_groups g ON g.id = m.group_id AND g.deleted_at IS NULL
		WHERE m.org_id = ? AND m.user_id = ?`, orgID, userID).Scan(&ids).Error
	return ids, err
}

func (r *userGroupRepository) ExistsInOrg(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&domain.UserGroup{}).
		Where("org_id = ? AND id = ?", orgID, id).Count(&n).Error
	return n > 0, err
}
