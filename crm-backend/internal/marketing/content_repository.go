package marketing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
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

// ContentRef is the slim projection the synced-block scans need: matching an
// instance only reads body_json, so pulling body_html_compiled and plain_text
// for every row in the org (they are the two largest columns, tens of KB each)
// is pure waste on a request that runs whenever the inspector shows a count.
type ContentRef struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	BodyJSON []byte    `json:"body_json"`
}

// ListContentRefsByOrg returns id/name/body_json for the org's content.
func (r *Repository) ListContentRefsByOrg(ctx context.Context, orgID uuid.UUID) ([]ContentRef, error) {
	var rows []ContentRef
	err := r.db.WithContext(ctx).Model(&CampaignContent{}).
		Select("id", "name", "body_json").
		Where("org_id = ?", orgID).
		Find(&rows).Error
	return rows, err
}

// UpdateContentBody rewrites a content row's document AND its compiled output in
// one column-scoped write. Used by synced-block propagation: body_html_compiled
// is the SEND source, so rewriting body_json without it would show the new block
// in the builder while recipients kept receiving the old one.
func (r *Repository) UpdateContentBody(ctx context.Context, orgID, id uuid.UUID, bodyJSON []byte, res CompileResult, at time.Time) error {
	return r.db.WithContext(ctx).Model(&CampaignContent{}).
		Where("org_id = ? AND id = ?", orgID, id).
		Updates(map[string]any{
			"body_json":           datatypes.JSON(bodyJSON),
			"body_html_compiled":  res.HTML,
			"plain_text":          res.PlainText,
			"compiled_size_bytes": res.SizeBytes,
			"compiled_at":         at,
			"updated_at":          at,
		}).Error
}

// UpdateContentFolder moves a template between folders (name-only column write —
// deliberately NOT Save(), which would rewrite every column).
func (r *Repository) UpdateContentFolder(ctx context.Context, orgID, id uuid.UUID, folder string) (bool, error) {
	res := r.db.WithContext(ctx).Model(&CampaignContent{}).
		Where("org_id = ? AND id = ?", orgID, id).
		Update("folder", folder)
	return res.RowsAffected > 0, res.Error
}

// SoftDeleteContent soft-deletes one content row within an org.
func (r *Repository) SoftDeleteContent(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).Delete(&CampaignContent{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
