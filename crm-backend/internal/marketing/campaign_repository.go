package marketing

import (
	"context"
	"errors"
	"time"

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

// StartCampaign flips a draft/scheduled campaign to 'sending' and stamps started_at.
// Only these two states may launch (an already-sending/sent/canceled campaign is a
// no-op → false), so a double-launch cannot restart a finished send.
func (r *Repository) StartCampaign(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).Exec(`
		UPDATE marketing_campaigns SET status = 'sending', started_at = NOW(), updated_at = NOW()
		WHERE org_id = ? AND id = ? AND deleted_at IS NULL AND status IN ('draft','scheduled')`,
		orgID, id)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ClaimSendableRecipients atomically claims up to `limit` pending, due recipients
// belonging to a 'sending', non-paused campaign/org (FOR UPDATE SKIP LOCKED), flips
// them to 'processing', stamps locked_at, and increments attempts. Global across all
// sending campaigns (mirrors the Resend webhook processor). A paused campaign or org,
// or a canceled campaign, drops out of the claim automatically — that IS the pause.
func (r *Repository) ClaimSendableRecipients(ctx context.Context, limit int) ([]domain.CampaignRecipient, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	var rows []domain.CampaignRecipient
	err := r.db.WithContext(ctx).Raw(`
		UPDATE marketing_campaign_recipients r
		SET status = 'processing', locked_at = NOW(), attempts = attempts + 1
		WHERE r.id IN (
			SELECT r2.id FROM marketing_campaign_recipients r2
			JOIN marketing_campaigns c ON c.id = r2.campaign_id AND c.deleted_at IS NULL AND c.status = 'sending'
			JOIN org_marketing_profile p ON p.org_id = r2.org_id
			WHERE r2.status = 'pending'
			  AND (r2.next_attempt_at IS NULL OR r2.next_attempt_at <= NOW())
			  AND (r2.scheduled_for IS NULL OR r2.scheduled_for <= NOW())
			  AND p.marketing_paused = false
			ORDER BY r2.created_at
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		)
		RETURNING r.*`, limit).Scan(&rows).Error
	return rows, err
}

// MarkRecipientSent is the terminal success write (idempotency key + this row are the
// durable no-double-send authority).
func (r *Repository) MarkRecipientSent(ctx context.Context, id uuid.UUID, providerMessageID string) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE marketing_campaign_recipients
		SET status = 'sent', processed_at = NOW(), provider_message_id = ?, error = NULL
		WHERE id = ?`, nullIfEmpty(providerMessageID), id).Error
}

// MarkRecipientFailed is the terminal permanent-failure write (4xx / attempts exhausted).
func (r *Repository) MarkRecipientFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE marketing_campaign_recipients
		SET status = 'failed', processed_at = NOW(), error = ?
		WHERE id = ?`, errMsg, id).Error
}

// MarkRecipientSuppressed is the terminal drop when the live M1 check says this
// address is not mailable (unsubscribed / no lawful basis) at claim time.
func (r *Repository) MarkRecipientSuppressed(ctx context.Context, id uuid.UUID, reason string) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE marketing_campaign_recipients
		SET status = 'suppressed', processed_at = NOW(), error = ?
		WHERE id = ?`, reason, id).Error
}

// RependRecipient returns a transiently-failed row to 'pending' with a backoff. attempts
// was already incremented at claim, so the reaper/attempt cap converges.
func (r *Repository) RependRecipient(ctx context.Context, id uuid.UUID, backoff time.Duration, errMsg string) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE marketing_campaign_recipients
		SET status = 'pending', locked_at = NULL, next_attempt_at = NOW() + make_interval(secs => ?), error = ?
		WHERE id = ?`, backoff.Seconds(), errMsg, id).Error
}

// ReapStrandedRecipients recovers rows stuck in 'processing' past the lease (a crashed
// worker): re-pends them if attempts remain, else fails them. On the single-send lane
// this is safe — the deterministic idempotency key makes a redelivery a Resend-cached
// no-op within 24h, and the row status is the authority beyond that.
func (r *Repository) ReapStrandedRecipients(ctx context.Context, grace time.Duration, maxAttempts int) (int64, error) {
	res := r.db.WithContext(ctx).Exec(`
		UPDATE marketing_campaign_recipients
		SET status = CASE WHEN attempts >= ? THEN 'failed' ELSE 'pending' END,
		    locked_at = NULL,
		    next_attempt_at = NOW(),
		    error = CASE WHEN attempts >= ? THEN 'stranded: max attempts' ELSE error END,
		    processed_at = CASE WHEN attempts >= ? THEN NOW() ELSE processed_at END
		WHERE status = 'processing' AND locked_at < NOW() - make_interval(secs => ?)`,
		maxAttempts, maxAttempts, maxAttempts, grace.Seconds())
	return res.RowsAffected, res.Error
}

// MarkDrainedCampaignsSent flips any 'sending' campaign with no remaining
// pending/processing recipients to 'sent' (finished_at). Called after each drain.
func (r *Repository) MarkDrainedCampaignsSent(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Exec(`
		UPDATE marketing_campaigns c
		SET status = 'sent', finished_at = NOW(), updated_at = NOW()
		WHERE c.status = 'sending'
		  AND NOT EXISTS (
			SELECT 1 FROM marketing_campaign_recipients r
			WHERE r.campaign_id = c.id AND r.status IN ('pending','processing'))`)
	return res.RowsAffected, res.Error
}

// GetOrgName reads the org's display name for the footer + merge context. Empty on
// a missing org (best-effort — the footer then omits the name).
func (r *Repository) GetOrgName(ctx context.Context, orgID uuid.UUID) (string, error) {
	var name string
	err := r.db.WithContext(ctx).Raw(`SELECT name FROM organizations WHERE id = ?`, orgID).Scan(&name).Error
	return name, err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
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
