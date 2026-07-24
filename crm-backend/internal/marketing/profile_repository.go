package marketing

import (
	"context"
	"errors"
	"strings"

	"crm-backend/internal/emailutil"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// GetProfile returns the org's marketing sender profile, or (nil, nil) when none
// has been saved yet (which SenderProfileSendable treats as not-sendable).
func (r *Repository) GetProfile(ctx context.Context, orgID uuid.UUID) (*OrgMarketingProfile, error) {
	var p OrgMarketingProfile
	err := r.db.WithContext(ctx).Where("org_id = ?", orgID).First(&p).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// UpsertProfile inserts or updates the org's sender profile. It writes ONLY the
// editable identity/compliance fields — marketing_paused is deliberately excluded
// from the update set so saving the address can never un-pause an M4 breaker
// (that is what SetMarketingPaused is for). Raw SQL rather than gorm.Save because
// GORM omits zero-value fields on a struct update, which would drop an empty
// reply_to and mishandle the boolean.
func (r *Repository) UpsertProfile(ctx context.Context, p *OrgMarketingProfile) error {
	if p.OrgID == uuid.Nil {
		return errors.New("marketing: profile needs an org id")
	}
	return r.db.WithContext(ctx).Exec(`
		INSERT INTO org_marketing_profile (org_id, from_name, reply_to, physical_postal_address, marketing_paused, created_at, updated_at)
		VALUES (?, ?, ?, ?, false, NOW(), NOW())
		ON CONFLICT (org_id) DO UPDATE SET
			from_name = EXCLUDED.from_name,
			reply_to = EXCLUDED.reply_to,
			physical_postal_address = EXCLUDED.physical_postal_address,
			updated_at = NOW()`,
		p.OrgID, strings.TrimSpace(p.FromName), strings.TrimSpace(p.ReplyTo), strings.TrimSpace(p.PhysicalPostalAddress),
	).Error
}

// SetMarketingPaused flips the org-level marketing pause flag. M4's complaint
// breaker sets it true; an admin resume sets it false. Upserts so a pause can land
// even before the org has saved a full profile (the row then exists with empty
// identity, which SenderProfileSendable still blocks on).
func (r *Repository) SetMarketingPaused(ctx context.Context, orgID uuid.UUID, paused bool) error {
	if orgID == uuid.Nil {
		return errors.New("marketing: org id required")
	}
	return r.db.WithContext(ctx).Exec(`
		INSERT INTO org_marketing_profile (org_id, marketing_paused, created_at, updated_at)
		VALUES (?, ?, NOW(), NOW())
		ON CONFLICT (org_id) DO UPDATE SET marketing_paused = EXCLUDED.marketing_paused, updated_at = NOW()`,
		orgID, paused,
	).Error
}

// SetMarketingStatus upserts the lifecycle status on contact_marketing_state,
// keyed on the normalized email. Used by the one-click unsubscribe to flip a global
// opt-out to 'unsubscribed' so the contact badge and HasLawfulBasis reflect it. It
// is a consistency write, NOT the enforcement point: the suppression row alone
// already blocks IsSendable, so a failure here is best-effort (the caller logs it).
func (r *Repository) SetMarketingStatus(ctx context.Context, orgID uuid.UUID, emailNorm, status string) error {
	emailNorm = emailutil.Normalize(emailNorm)
	if orgID == uuid.Nil || emailNorm == "" {
		return errors.New("marketing: org id and email required")
	}
	if !IsValidStatus(status) {
		return errors.New("marketing: invalid marketing status")
	}
	return r.db.WithContext(ctx).Exec(`
		INSERT INTO contact_marketing_state (org_id, email_normalized, marketing_status, created_at, updated_at)
		VALUES (?, ?, ?, NOW(), NOW())
		ON CONFLICT (org_id, email_normalized) DO UPDATE SET marketing_status = EXCLUDED.marketing_status, updated_at = NOW()`,
		orgID, emailNorm, status,
	).Error
}

// ── marketing topics ─────────────────────────────────────────────────────────

// CreateTopic inserts a new topic.
func (r *Repository) CreateTopic(ctx context.Context, t *MarketingTopic) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// GetTopicByID returns one topic within an org, or (nil, nil) when absent.
func (r *Repository) GetTopicByID(ctx context.Context, orgID, id uuid.UUID) (*MarketingTopic, error) {
	var t MarketingTopic
	err := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// ListTopicsByOrg returns an org's topics, oldest first (stable display order).
func (r *Repository) ListTopicsByOrg(ctx context.Context, orgID uuid.UUID) ([]MarketingTopic, error) {
	var rows []MarketingTopic
	if err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// UpdateTopicMeta updates ONLY a topic's name + description. opt_in_default is
// immutable after creation, so it is never written here.
func (r *Repository) UpdateTopicMeta(ctx context.Context, orgID, id uuid.UUID, name, description string) (bool, error) {
	res := r.db.WithContext(ctx).Model(&MarketingTopic{}).
		Where("org_id = ? AND id = ?", orgID, id).
		Updates(map[string]any{"name": name, "description": description, "updated_at": gorm.Expr("NOW()")})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// DeleteTopic hard-deletes a topic within an org (topics are not soft-deletable).
// Suppressions that referenced the topic_id simply stop matching any future send —
// there is no FK, so nothing dangles.
func (r *Repository) DeleteTopic(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).Delete(&MarketingTopic{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
