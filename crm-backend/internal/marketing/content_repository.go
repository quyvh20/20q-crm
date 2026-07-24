package marketing

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CreateContent inserts a campaign-content row.
func (r *Repository) CreateContent(ctx context.Context, c *CampaignContent) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// GetContentByID returns one content row within an org, or (nil, nil) when absent.
func (r *Repository) GetContentByID(ctx context.Context, orgID, id uuid.UUID) (*CampaignContent, error) {
	var c CampaignContent
	err := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).First(&c).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// ListContentByOrg returns an org's campaign content, newest first.
func (r *Repository) ListContentByOrg(ctx context.Context, orgID uuid.UUID) ([]CampaignContent, error) {
	var rows []CampaignContent
	if err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("updated_at DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// UpdateContent persists a mutated content row. Save writes all columns; content is
// only ever written by the authoring handlers (no concurrent machine writes).
func (r *Repository) UpdateContent(ctx context.Context, c *CampaignContent) error {
	return r.db.WithContext(ctx).Save(c).Error
}

// SoftDeleteContent soft-deletes one content row within an org.
func (r *Repository) SoftDeleteContent(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).Delete(&CampaignContent{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
