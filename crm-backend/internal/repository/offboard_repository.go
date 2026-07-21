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

// RoutingSourceNames names the lead sources that would send NEW leads to this
// member — as the single owner, or as part of a rotation.
//
// Offboarding otherwise only reasons about records the member ALREADY owns, which
// says nothing about the ones still arriving. An admin who reassigns a departing
// rep's contacts and hears nothing about their three live rotations discovers the
// gap when a customer asks why nobody called back.
func (r *OffboardRepository) RoutingSourceNames(ctx context.Context, orgID, userID uuid.UUID) ([]string, error) {
	var names []string
	err := r.db.WithContext(ctx).Raw(`
		SELECT name FROM lead_sources
		 WHERE org_id = ? AND deleted_at IS NULL
		   AND (default_owner_id = ? OR owner_pool @> ?::jsonb)
		 ORDER BY name`, orgID, userID, `"`+userID.String()+`"`).Scan(&names).Error
	return names, err
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
		if err := tx.Where("org_id = ? AND user_id = ?", orgID, userID).
			Delete(&domain.UserGroupMember{}).Error; err != nil {
			return err
		}
		// Lead-source rotations, for exactly the reason above: a departed member left
		// in a pool silently re-arms if they are ever re-invited, because org_users'
		// primary key is (user_id, org_id) and a re-invite flips that same row back to
		// active. Removal prunes; SUSPENSION deliberately does not, because a
		// suspension is reversible and routing already skips a suspended member.
		//
		// Raw SQL rather than the integrations repository: usecase and repository must
		// not depend on that package, and `owner_pool - ?` is jsonb array element
		// removal, which leaves an admin's ordering otherwise intact.
		if err := tx.Exec(`
			UPDATE lead_sources
			   SET owner_pool = owner_pool - ?, updated_at = NOW()
			 WHERE org_id = ? AND deleted_at IS NULL AND owner_pool @> ?::jsonb`,
			userID.String(), orgID, `"`+userID.String()+`"`).Error; err != nil {
			return err
		}
		// The single-owner half of the same binding, and it was the live bug: pruning
		// only the POOL left `default_owner_id` pointing at a person with no org_users
		// row at all, and the non-pooled branch of resolveOwner stamps that column
		// UNCHECKED. So every source they were the default owner of went on assigning
		// new contacts to a departed member — invisible to own-scoped reps, triaged by
		// nobody, which is verbatim the failure this whole platform exists to fix. The
		// 409 copy even promised otherwise ("they will also be removed from the lead
		// rotation on: …"), because RoutingSourceNames matches BOTH bindings while only
		// one of them was ever repaired.
		//
		// Cleared to NULL rather than reassigned to the removal's transfer target: that
		// target answers "who inherits the records they already own", which is a
		// different question from "who should get the next lead this form receives",
		// and quietly reusing one answer for the other is how a rep discovers they own
		// a channel nobody told them about. Unowned is also the deliberately louder
		// state — L2.5 settled that stamping an inactive member is WORSE than leaving a
		// lead unassigned, because it looks handled — and the unowned path already
		// shouts in four places (ledger note, slog, a capture-response warning, and a
		// badge). The admin is told which sources this hit, and picks a new owner.
		//
		// Same asymmetry as above: suspension does not clear this, because it is
		// reversible and the column is how the rep gets their pipe back on reinstate.
		return tx.Exec(`
			UPDATE lead_sources
			   SET default_owner_id = NULL, updated_at = NOW()
			 WHERE org_id = ? AND deleted_at IS NULL AND default_owner_id = ?`,
			orgID, userID).Error
	})
}
