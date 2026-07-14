package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type voiceNoteRepository struct {
	db *gorm.DB
}

func NewVoiceNoteRepository(db *gorm.DB) domain.VoiceNoteRepository {
	return &voiceNoteRepository{db: db}
}

func (r *voiceNoteRepository) Create(ctx context.Context, v *domain.VoiceNote) error {
	return r.db.WithContext(ctx).Create(v).Error
}

// voiceNoteScope restricts voice_notes to those whose linked contact or deal
// is accessible by the requesting user. For sales users it applies the same
// ownership / record_shares logic used by contact_repository and deal_repository.
//
// A voice note is accessible if ANY of the following is true:
//   - The user is admin/manager/owner (full org scope)
//   - The note's contact is owned by the user OR shared with them
//   - The note's deal   is owned by the user OR shared with them
//   - The note has no linked contact/deal (user created it themselves — checked via user_id)
func voiceNoteScope(db *gorm.DB, ctx context.Context, orgID uuid.UUID) *gorm.DB {
	scope, userID, roleID, ok := extractCallerScope(ctx)
	if !ok || scope == domain.DataScopeAll {
		// 'all'-scope roles (admin / manager / owner / any all-scoped custom role) —
		// full org access. !ok is a trusted in-process call.
		//
		// The guard tests for 'all', not "not own": the pre-U6 shape (!= own ⇒ whole
		// org) would have handed a team-scoped caller every voice note in the
		// workspace the moment the new scope value shipped.
		return db.Where("voice_notes.org_id = ?", orgID)
	}

	// Row-scoped: a note is reachable if its linked contact OR deal is reachable
	// (same predicate as the contact/deal list pages — access_predicate.go), or the
	// caller recorded it themselves.
	cSQL, cArgs := RecordAccessPredicate(RecordAccessArgs{
		Table: "c", RecordType: "contact", OrgID: orgID, Scope: scope, UserID: userID, RoleID: roleID,
	})
	dSQL, dArgs := RecordAccessPredicate(RecordAccessArgs{
		Table: "d", RecordType: "deal", OrgID: orgID, Scope: scope, UserID: userID, RoleID: roleID,
	})

	args := []any{orgID, userID}
	args = append(args, cArgs...)
	args = append(args, dArgs...)

	return db.Where(`voice_notes.org_id = ? AND (
		voice_notes.user_id = ?
		OR (voice_notes.contact_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM contacts c
			WHERE c.id = voice_notes.contact_id
			  AND c.deleted_at IS NULL
			  AND `+cSQL+`
		))
		OR (voice_notes.deal_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM deals d
			WHERE d.id = voice_notes.deal_id
			  AND d.deleted_at IS NULL
			  AND `+dSQL+`
		))
	)`, args...)
}

func (r *voiceNoteRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.VoiceNote, error) {
	var v domain.VoiceNote
	err := voiceNoteScope(r.db.WithContext(ctx), ctx, orgID).
		Preload("Contact").
		Preload("Deal").
		Where("voice_notes.id = ?", id).
		First(&v).Error
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *voiceNoteRepository) List(ctx context.Context, orgID uuid.UUID, f domain.VoiceNoteFilter) ([]domain.VoiceNote, error) {
	var notes []domain.VoiceNote
	q := voiceNoteScope(r.db.WithContext(ctx), ctx, orgID).
		Preload("Contact").
		Preload("Deal")

	if f.ContactID != nil {
		q = q.Where("voice_notes.contact_id = ?", *f.ContactID)
	}
	if f.DealID != nil {
		q = q.Where("voice_notes.deal_id = ?", *f.DealID)
	}

	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	q = q.Order("voice_notes.created_at DESC").Limit(limit)

	if err := q.Find(&notes).Error; err != nil {
		return nil, err
	}
	return notes, nil
}

func (r *voiceNoteRepository) Update(ctx context.Context, v *domain.VoiceNote) error {
	return r.db.WithContext(ctx).Omit("Contact", "Deal").Save(v).Error
}

func (r *voiceNoteRepository) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	// Verify access first via scoped GetByID before deleting
	if _, err := r.GetByID(ctx, orgID, id); err != nil {
		return gorm.ErrRecordNotFound
	}
	result := r.db.WithContext(ctx).
		Where("id = ? AND org_id = ?", id, orgID).
		Delete(&domain.VoiceNote{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
