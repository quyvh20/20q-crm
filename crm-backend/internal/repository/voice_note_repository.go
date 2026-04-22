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
	role, userID, ok := extractDataScope(ctx)
	if !ok || role != domain.RoleSales {
		// admin / manager / owner — full org access
		return db.Where("voice_notes.org_id = ?", orgID)
	}

	// sales: accessible if note's contact OR deal is owned/shared, OR note was created by them
	return db.Where(`voice_notes.org_id = ? AND (
		voice_notes.user_id = ?
		OR (voice_notes.contact_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM contacts c
			WHERE c.id = voice_notes.contact_id
			  AND c.deleted_at IS NULL
			  AND (c.owner_user_id = ? OR EXISTS (
				SELECT 1 FROM record_shares rs
				WHERE rs.record_id = c.id AND rs.record_type = 'contact' AND rs.grantee_user_id = ?
			))
		))
		OR (voice_notes.deal_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM deals d
			WHERE d.id = voice_notes.deal_id
			  AND d.deleted_at IS NULL
			  AND (d.owner_user_id = ? OR EXISTS (
				SELECT 1 FROM record_shares rs
				WHERE rs.record_id = d.id AND rs.record_type = 'deal' AND rs.grantee_user_id = ?
			))
		))
	)`, orgID, userID, userID, userID, userID, userID)
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
