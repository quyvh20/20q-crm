package repository

import (
	"context"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// OffboardRepository executes the data side of removing a member (U0.2): count,
// transfer, or release the records they own, and revoke their org-scoped grants
// so a later re-invite starts clean instead of silently restoring old access.
// This replaced the RemoveMember mock that validated the reassignment input and
// then ignored it.
//
// Ownership now spans contacts, deals AND custom records (U6.3 gave custom objects
// an owner). All three are counted and reassigned here — leaving custom records out
// would strand them on a departed member and, worse, under-report the impact in the
// confirmation dialog, which is where the admin actually makes the call.
type OffboardRepository struct {
	db *gorm.DB
}

func NewOffboardRepository(db *gorm.DB) *OffboardRepository {
	return &OffboardRepository{db: db}
}

// CountOwnedRecords returns how many live contacts, deals and custom records the
// user owns in the org — the numbers the RemoveMember 409 surfaces so the admin
// decides with real counts in front of them. Soft-deleted rows are excluded (GORM
// adds the deleted_at IS NULL guard), matching what the reassignment below touches.
func (r *OffboardRepository) CountOwnedRecords(ctx context.Context, orgID, userID uuid.UUID) (contacts, deals, custom int64, err error) {
	if err = r.db.WithContext(ctx).Model(&domain.Contact{}).
		Where("org_id = ? AND owner_user_id = ?", orgID, userID).
		Count(&contacts).Error; err != nil {
		return 0, 0, 0, err
	}
	if err = r.db.WithContext(ctx).Model(&domain.Deal{}).
		Where("org_id = ? AND owner_user_id = ?", orgID, userID).
		Count(&deals).Error; err != nil {
		return 0, 0, 0, err
	}
	if err = r.db.WithContext(ctx).Model(&domain.CustomObjectRecord{}).
		Where("org_id = ? AND owner_user_id = ?", orgID, userID).
		Count(&custom).Error; err != nil {
		return 0, 0, 0, err
	}
	return contacts, deals, custom, nil
}

// ReassignOwnedRecords transfers every live record the departing member owns to
// the new owner, atomically across all three tables.
func (r *OffboardRepository) ReassignOwnedRecords(ctx context.Context, orgID, fromUserID, toUserID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.Contact{}).
			Where("org_id = ? AND owner_user_id = ?", orgID, fromUserID).
			Update("owner_user_id", toUserID).Error; err != nil {
			return err
		}
		if err := tx.Model(&domain.Deal{}).
			Where("org_id = ? AND owner_user_id = ?", orgID, fromUserID).
			Update("owner_user_id", toUserID).Error; err != nil {
			return err
		}
		return tx.Model(&domain.CustomObjectRecord{}).
			Where("org_id = ? AND owner_user_id = ?", orgID, fromUserID).
			Update("owner_user_id", toUserID).Error
	})
}

// UnassignOwnedRecords releases the departing member's records to no owner (the
// "unassign" strategy) so they surface as unowned rather than staying pinned to a
// ghost member.
func (r *OffboardRepository) UnassignOwnedRecords(ctx context.Context, orgID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.Contact{}).
			Where("org_id = ? AND owner_user_id = ?", orgID, userID).
			Update("owner_user_id", nil).Error; err != nil {
			return err
		}
		if err := tx.Model(&domain.Deal{}).
			Where("org_id = ? AND owner_user_id = ?", orgID, userID).
			Update("owner_user_id", nil).Error; err != nil {
			return err
		}
		return tx.Model(&domain.CustomObjectRecord{}).
			Where("org_id = ? AND owner_user_id = ?", orgID, userID).
			Update("owner_user_id", nil).Error
	})
}

// RevokeUserGrants deletes the member's org-scoped access grants: record shares
// granted TO them, report shares targeting them as a user, and their group
// memberships. Without this, removing then re-inviting a member silently restored
// their old shared access.
//
// Record shares are now org-scoped and target-typed (U6.2), so this is a direct
// delete rather than the old per-record-type subquery over three tables. Group
// membership removal matters more than it used to: a group can now hold record
// shares AND define a team, so leaving the row behind would keep granting both.
func (r *OffboardRepository) RevokeUserGrants(ctx context.Context, orgID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("org_id = ? AND target_type = ? AND target_id = ?", orgID, domain.ShareTargetUser, userID).
			Delete(&domain.RecordShare{}).Error; err != nil {
			return err
		}
		if err := tx.Where("org_id = ? AND target_type = 'user' AND target_id = ?", orgID, userID).
			Delete(&domain.ReportShare{}).Error; err != nil {
			return err
		}
		// A personal access token authenticates as its owner, so a removed member's
		// tokens are a live key to a workspace they no longer belong to. The PAT
		// middleware also re-checks the membership on every request (belt and braces),
		// but a revoked token is the honest state — it should not still appear live.
		if err := tx.Model(&domain.APIToken{}).
			Where("org_id = ? AND user_id = ? AND revoked_at IS NULL", orgID, userID).
			Update("revoked_at", time.Now()).Error; err != nil {
			return err
		}
		return tx.Where("org_id = ? AND user_id = ?", orgID, userID).
			Delete(&domain.UserGroupMember{}).Error
	})
}
