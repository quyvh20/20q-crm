package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type activityRepository struct {
	db *gorm.DB
}

func NewActivityRepository(db *gorm.DB) domain.ActivityRepository {
	return &activityRepository{db: db}
}

// activityScope restricts activities to those a row-scoped caller may see
// (U0.1-ext). Activity titles/bodies carry call notes and emails; before this,
// GET /api/activities returned any activity in the org given only a record UUID,
// leaking the notes of a contact/deal the caller can't otherwise open.
//
// An activity is reachable if ANY of the following holds:
//   - the caller is 'all'-scoped (admin/manager/owner/any all-scoped role) — full org
//   - the caller logged it themselves (activities.user_id — self-logged, e.g. an
//     unlinked note)
//   - its linked contact OR deal is reachable by the caller (same predicate the
//     contact/deal list pages use — access_predicate.go)
//
// Mirrors voiceNoteScope; !ok is a trusted in-process call and is unrestricted.
// The guard tests for 'all', not "not own", so a new scope value can't silently
// widen to the whole org.
func activityScope(db *gorm.DB, ctx context.Context, orgID uuid.UUID) *gorm.DB {
	scope, userID, roleID, ok := extractCallerScope(ctx)
	if !ok || scope == domain.DataScopeAll {
		return db.Where("activities.org_id = ?", orgID)
	}

	cSQL, cArgs := RecordAccessPredicate(RecordAccessArgs{
		Table: "c", RecordType: "contact", OrgID: orgID, Scope: scope, UserID: userID, RoleID: roleID,
	})
	dSQL, dArgs := RecordAccessPredicate(RecordAccessArgs{
		Table: "d", RecordType: "deal", OrgID: orgID, Scope: scope, UserID: userID, RoleID: roleID,
	})

	args := []any{orgID, userID}
	args = append(args, cArgs...)
	args = append(args, dArgs...)

	return db.Where(`activities.org_id = ? AND (
		activities.user_id = ?
		OR (activities.contact_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM contacts c
			WHERE c.id = activities.contact_id
			  AND c.deleted_at IS NULL
			  AND `+cSQL+`
		))
		OR (activities.deal_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM deals d
			WHERE d.id = activities.deal_id
			  AND d.deleted_at IS NULL
			  AND `+dSQL+`
		))
	)`, args...)
}

func (r *activityRepository) List(ctx context.Context, orgID uuid.UUID, f domain.ActivityFilter) ([]domain.Activity, error) {
	query := activityScope(r.db.WithContext(ctx), ctx, orgID)

	if f.DealID != nil {
		query = query.Where("activities.deal_id = ?", *f.DealID)
	}
	if f.ContactID != nil {
		query = query.Where("activities.contact_id = ?", *f.ContactID)
	}

	var activities []domain.Activity
	err := query.
		Order("activities.occurred_at DESC").
		Limit(100).
		Find(&activities).Error
	return activities, err
}

func (r *activityRepository) Create(ctx context.Context, a *domain.Activity) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Create(a).Error
}
