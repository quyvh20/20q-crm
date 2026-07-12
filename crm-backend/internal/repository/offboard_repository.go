package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// OffboardRepository executes the data side of removing a member (U0.2): count,
// transfer, or release the records they own, and revoke their org-scoped grants
// so a later re-invite starts clean instead of silently restoring old access.
// This replaced the RemoveMember mock that validated the reassignment input and
// then ignored it. Ownership exists only on contacts and deals today (companies
// and custom records have no owner column), so those are the two tables in
// scope; grant revocation additionally sweeps custom-object record shares.
type OffboardRepository struct {
	db *gorm.DB
}

func NewOffboardRepository(db *gorm.DB) *OffboardRepository {
	return &OffboardRepository{db: db}
}

// CountOwnedRecords returns how many live contacts and deals the user owns in
// the org — the numbers the RemoveMember 409 surfaces so the admin decides with
// real counts in front of them. Soft-deleted rows are excluded (GORM adds the
// deleted_at IS NULL guard), matching what the reassignment below will touch.
func (r *OffboardRepository) CountOwnedRecords(ctx context.Context, orgID, userID uuid.UUID) (contacts, deals int64, err error) {
	if err = r.db.WithContext(ctx).Model(&domain.Contact{}).
		Where("org_id = ? AND owner_user_id = ?", orgID, userID).
		Count(&contacts).Error; err != nil {
		return 0, 0, err
	}
	if err = r.db.WithContext(ctx).Model(&domain.Deal{}).
		Where("org_id = ? AND owner_user_id = ?", orgID, userID).
		Count(&deals).Error; err != nil {
		return 0, 0, err
	}
	return contacts, deals, nil
}

// ReassignOwnedRecords transfers every live contact and deal the departing
// member owns to the new owner, atomically across both tables.
func (r *OffboardRepository) ReassignOwnedRecords(ctx context.Context, orgID, fromUserID, toUserID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.Contact{}).
			Where("org_id = ? AND owner_user_id = ?", orgID, fromUserID).
			Update("owner_user_id", toUserID).Error; err != nil {
			return err
		}
		return tx.Model(&domain.Deal{}).
			Where("org_id = ? AND owner_user_id = ?", orgID, fromUserID).
			Update("owner_user_id", toUserID).Error
	})
}

// UnassignOwnedRecords releases the departing member's contacts and deals to
// no owner (the "unassign" strategy) so they surface as unowned rather than
// staying pinned to a ghost member.
func (r *OffboardRepository) UnassignOwnedRecords(ctx context.Context, orgID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.Contact{}).
			Where("org_id = ? AND owner_user_id = ?", orgID, userID).
			Update("owner_user_id", nil).Error; err != nil {
			return err
		}
		return tx.Model(&domain.Deal{}).
			Where("org_id = ? AND owner_user_id = ?", orgID, userID).
			Update("owner_user_id", nil).Error
	})
}

// RevokeUserGrants deletes the member's org-scoped access grants: record shares
// granted TO them on this org's records, report shares targeting them as a
// user, and their group memberships. Without this, removing then re-inviting a
// member silently restored their old shared access. record_shares has no org
// column, so shares are scoped through the org's record ids per record type.
func (r *OffboardRepository) RevokeUserGrants(ctx context.Context, orgID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			DELETE FROM record_shares rs
			WHERE rs.grantee_user_id = ?
			  AND (
			    (rs.record_type = 'contact' AND rs.record_id IN (SELECT id FROM contacts  WHERE org_id = ?)) OR
			    (rs.record_type = 'deal'    AND rs.record_id IN (SELECT id FROM deals     WHERE org_id = ?)) OR
			    rs.record_id IN (SELECT id FROM custom_object_records WHERE org_id = ?)
			  )`, userID, orgID, orgID, orgID).Error; err != nil {
			return err
		}
		if err := tx.Where("org_id = ? AND target_type = 'user' AND target_id = ?", orgID, userID).
			Delete(&domain.ReportShare{}).Error; err != nil {
			return err
		}
		return tx.Where("org_id = ? AND user_id = ?", orgID, userID).
			Delete(&domain.UserGroupMember{}).Error
	})
}
