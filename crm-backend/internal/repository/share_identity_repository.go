package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// getShareIdentity resolves the caller's share handles — the things a share row
// can name to reach them: their user id, their role (org_users), and every group
// they belong to. Both share systems (records and reports) match against exactly
// this, so there is one implementation.
//
// GORM note: the uuid columns are scanned through a struct FIELD, not into a bare
// []uuid.UUID. A bare uuid.UUID destination is treated as [16]byte and fails on
// the driver's string value.
func getShareIdentity(ctx context.Context, db *gorm.DB, orgID, userID uuid.UUID) (domain.ShareIdentity, error) {
	ident := domain.ShareIdentity{UserID: userID}

	var roleRow struct{ RoleID uuid.UUID }
	if err := db.WithContext(ctx).Raw(
		`SELECT role_id FROM org_users WHERE org_id = ? AND user_id = ? AND deleted_at IS NULL LIMIT 1`,
		orgID, userID).Scan(&roleRow).Error; err != nil {
		return ident, err
	}
	ident.RoleID = roleRow.RoleID

	var groupRows []struct{ GroupID uuid.UUID }
	if err := db.WithContext(ctx).Raw(
		`SELECT m.group_id FROM user_group_members m
		 JOIN user_groups g ON g.id = m.group_id AND g.deleted_at IS NULL
		 WHERE m.org_id = ? AND m.user_id = ?`, orgID, userID).Scan(&groupRows).Error; err != nil {
		return ident, err
	}
	for _, g := range groupRows {
		ident.GroupIDs = append(ident.GroupIDs, g.GroupID)
	}
	return ident, nil
}

// shareIdentityRepository exposes getShareIdentity as its own port, so a usecase
// that needs to know "who is the caller, for share matching" does not have to
// depend on the report-share repository to ask.
type shareIdentityRepository struct {
	db *gorm.DB
}

func NewShareIdentityRepository(db *gorm.DB) domain.ShareIdentityRepository {
	return &shareIdentityRepository{db: db}
}

func (r *shareIdentityRepository) GetShareIdentity(ctx context.Context, orgID, userID uuid.UUID) (domain.ShareIdentity, error) {
	return getShareIdentity(ctx, r.db, orgID, userID)
}
