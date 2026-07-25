package marketing

import (
	"context"
	"errors"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Campaign persistence + roster fan-out (M7). These methods hang off the shared
// marketing Repository. The roster is the sole durable authority for send state;
// the fan-out wraps the M5 audience SELECT (union of segments minus exclusions,
// deduped, live contacts) into a set-based INSERT…SELECT — no 100-row cap, one
// statement regardless of audience size.

func (r *Repository) CreateCampaign(ctx context.Context, c *domain.Campaign) error {
	return r.db.WithContext(ctx).Create(c).Error
}

func (r *Repository) GetCampaignByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Campaign, error) {
	var c domain.Campaign
	err := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).First(&c).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (r *Repository) ListCampaignsByOrg(ctx context.Context, orgID uuid.UUID) ([]domain.Campaign, error) {
	var rows []domain.Campaign
	if err := r.db.WithContext(ctx).Where("org_id = ?", orgID).Order("updated_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *Repository) UpdateCampaign(ctx context.Context, c *domain.Campaign) error {
	return r.db.WithContext(ctx).Save(c).Error
}

func (r *Repository) SoftDeleteCampaign(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).Delete(&domain.Campaign{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// SetCampaignStatus flips the status (launch / pause / cancel / done). Org-scoped;
// returns whether a row changed.
func (r *Repository) SetCampaignStatus(ctx context.Context, orgID, id uuid.UUID, status string) (bool, error) {
	res := r.db.WithContext(ctx).Model(&domain.Campaign{}).
		Where("org_id = ? AND id = ?", orgID, id).
		Update("status", status)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ClearRoster removes a campaign's roster rows (a re-snapshot before launch). Never
// call this once sending has begun — the roster is the durable send-state authority.
func (r *Repository) ClearRoster(ctx context.Context, campaignID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("campaign_id = ?", campaignID).
		Delete(&domain.CampaignRecipient{}).Error
}

// SnapshotRoster materializes the audience into the roster via one set-based
// INSERT…SELECT, deduped by UNIQUE(campaign_id, email_normalized). Returns the roster
// size. id/status/attempts/created_at all take their DDL defaults.
func (r *Repository) SnapshotRoster(ctx context.Context, campaignID, orgID uuid.UUID, aud domain.AudienceQuery) (int, error) {
	insert := `INSERT INTO marketing_campaign_recipients (campaign_id, org_id, contact_id, email_normalized)
		SELECT ?, ?, aud.contact_id, aud.email_normalized FROM (` + aud.SelectSQL + `) aud
		ON CONFLICT (campaign_id, email_normalized) DO NOTHING`
	args := append([]any{campaignID, orgID}, aud.Args...)
	if err := r.db.WithContext(ctx).Exec(insert, args...).Error; err != nil {
		return 0, err
	}
	var n int64
	if err := r.db.WithContext(ctx).
		Raw(`SELECT count(*) FROM marketing_campaign_recipients WHERE campaign_id = ?`, campaignID).
		Scan(&n).Error; err != nil {
		return 0, err
	}
	return int(n), nil
}

// EstimateAudience counts the DISTINCT mailable-membership addresses without
// materializing anything (the composer's live count).
func (r *Repository) EstimateAudience(ctx context.Context, aud domain.AudienceQuery) (int, error) {
	var n int64
	q := `SELECT count(DISTINCT email_normalized) FROM (` + aud.SelectSQL + `) aud`
	if err := r.db.WithContext(ctx).Raw(q, aud.Args...).Scan(&n).Error; err != nil {
		return 0, err
	}
	return int(n), nil
}

// CountRosterByStatus powers progress (pending / sent / failed / suppressed / …).
func (r *Repository) CountRosterByStatus(ctx context.Context, campaignID uuid.UUID) (map[string]int, error) {
	type row struct {
		Status string
		Count  int
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Raw(`SELECT status, count(*) AS count FROM marketing_campaign_recipients WHERE campaign_id = ? GROUP BY status`, campaignID).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, x := range rows {
		out[x.Status] = x.Count
	}
	return out, nil
}
