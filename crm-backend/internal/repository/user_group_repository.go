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
		if members == nil {
			// A zero-member group misses the map and yields a nil slice, which
			// marshals as `"members": null` — the frontend expects an array.
			members = []domain.GroupMemberInfo{}
		}
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

// SoftDelete removes the group and, in the same transaction, every grant that
// names it. A group is now three things at once — a share target for reports, a
// share target for records (U6.2), and a team for row scope (U6.1) — so a
// half-deleted group is a live access path. The membership rows go too: the team
// predicate joins user_group_members, and a soft-deleted group's rows would
// otherwise keep two users "teammates" forever.
func (r *userGroupRepository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM report_shares WHERE org_id = ? AND target_type = 'group' AND target_id = ?`, orgID, id).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM record_shares WHERE org_id = ? AND target_type = 'group' AND target_id = ?`, orgID, id).Error; err != nil {
			return err
		}
		if err := tx.Where("org_id = ? AND group_id = ?", orgID, id).Delete(&domain.UserGroupMember{}).Error; err != nil {
			return err
		}
		return tx.Where("org_id = ? AND id = ?", orgID, id).Delete(&domain.UserGroup{}).Error
	})
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
	// Scan through a struct field so GORM uses uuid's Scanner (a bare
	// []uuid.UUID dest fails on the driver's string representation).
	var rows []struct{ GroupID uuid.UUID }
	if err := r.db.WithContext(ctx).Raw(`
		SELECT m.group_id FROM user_group_members m
		JOIN user_groups g ON g.id = m.group_id AND g.deleted_at IS NULL
		WHERE m.org_id = ? AND m.user_id = ?`, orgID, userID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.GroupID)
	}
	return ids, nil
}

func (r *userGroupRepository) ExistsInOrg(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&domain.UserGroup{}).
		Where("org_id = ? AND id = ?", orgID, id).Count(&n).Error
	return n > 0, err
}

// TeammateIDs returns the active org members who share at least one group with the
// user — the same relation the 'team' data scope filters on (U6.1), exposed so the
// UI can offer a team-scoped user the exact set of people they may assign to.
//
// It must agree with the TEAM_CLAUSE in access_predicate.go: same self-join over
// user_group_members, same exclusion of soft-deleted groups. The caller themselves
// is included — you are on your own team.
func (r *userGroupRepository) TeammateIDs(ctx context.Context, orgID, userID uuid.UUID) ([]uuid.UUID, error) {
	var rows []struct{ UserID uuid.UUID }
	if err := r.db.WithContext(ctx).Raw(`
		SELECT DISTINCT m2.user_id
		FROM user_group_members m1
		JOIN user_group_members m2 ON m2.group_id = m1.group_id
		JOIN user_groups g ON g.id = m1.group_id AND g.deleted_at IS NULL
		JOIN org_users ou ON ou.user_id = m2.user_id AND ou.org_id = ? AND ou.status = 'active' AND ou.deleted_at IS NULL
		WHERE m1.user_id = ? AND m1.org_id = ?`, orgID, userID, orgID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(rows)+1)
	seenSelf := false
	for _, row := range rows {
		if row.UserID == userID {
			seenSelf = true
		}
		ids = append(ids, row.UserID)
	}
	if !seenSelf {
		ids = append(ids, userID)
	}
	return ids, nil
}
