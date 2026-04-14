package repository

import (
	"context"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type knowledgeBaseRepository struct {
	db *gorm.DB
}

func NewKnowledgeBaseRepository(db *gorm.DB) domain.KnowledgeBaseRepository {
	return &knowledgeBaseRepository{db: db}
}

func (r *knowledgeBaseRepository) GetAllActive(ctx context.Context, orgID uuid.UUID) ([]domain.KnowledgeBaseEntry, error) {
	var entries []domain.KnowledgeBaseEntry
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND is_active = true", orgID).
		Order("section ASC").
		Find(&entries).Error
	return entries, err
}

func (r *knowledgeBaseRepository) GetBySection(ctx context.Context, orgID uuid.UUID, section string) (*domain.KnowledgeBaseEntry, error) {
	var entry domain.KnowledgeBaseEntry
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND section = ? AND is_active = true", orgID, section).
		First(&entry).Error
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func (r *knowledgeBaseRepository) Upsert(ctx context.Context, entry *domain.KnowledgeBaseEntry) error {
	entry.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing domain.KnowledgeBaseEntry
		err := tx.Where("org_id = ? AND section = ? AND is_active = true", entry.OrgID, entry.Section).
			First(&existing).Error
		if err == nil {
			// Update existing
			existing.Title = entry.Title
			existing.Content = entry.Content
			existing.UpdatedAt = time.Now()
			existing.CreatedBy = entry.CreatedBy
			if err := tx.Save(&existing).Error; err != nil {
				return err
			}
			*entry = existing
			return nil
		}
		// Create new
		return tx.Create(entry).Error
	})
}
