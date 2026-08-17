package marketing

import (
	"context"
	"errors"
	"time"

	"crm-backend/internal/emailutil"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm/clause"
)

// InsertEvent inserts a webhook event, deduped on (org_id, svix_id) via ON CONFLICT
// DO NOTHING — a Resend redelivery of the same svix-id is a no-op (inserted=false).
// This idempotent ledger is what makes the rate breaker safe to recompute on every
// delivery (a COUNT over deduped rows can't double-count).
func (r *Repository) InsertEvent(ctx context.Context, e *MarketingEmailEvent) (inserted bool, err error) {
	if e.OrgID == uuid.Nil {
		return false, errors.New("marketing: event needs an org id")
	}
	if e.SvixID == "" {
		return false, errors.New("marketing: event needs a svix id")
	}
	if e.Status == "" {
		e.Status = EventStatusPending
	}
	if len(e.RawPayload) == 0 {
		e.RawPayload = datatypes.JSON("{}")
	}
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(e)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ClaimPendingEvents atomically claims up to `limit` pending events for processing
// (pending→processing, attempts++, claimed_at=NOW()) using FOR UPDATE SKIP LOCKED so
// multiple workers/replicas take disjoint sets. RETURNING avoids a second read.
func (r *Repository) ClaimPendingEvents(ctx context.Context, limit int) ([]MarketingEmailEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []MarketingEmailEvent
	err := r.db.WithContext(ctx).Raw(`
		UPDATE marketing_email_events
		SET status = ?, claimed_at = NOW(), attempts = attempts + 1
		WHERE id IN (
			SELECT id FROM marketing_email_events
			WHERE status = ?
			ORDER BY created_at
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		)
		RETURNING *`,
		EventStatusProcessing, EventStatusPending, limit).Scan(&rows).Error
	return rows, err
}

// RependEvent returns a claimed event to pending for a later poll (the transient-retry
// path). Org-scoped so a worker can only touch rows in the org it claimed.
func (r *Repository) RependEvent(ctx context.Context, orgID, eventID uuid.UUID, note string) error {
	return r.db.WithContext(ctx).
		Model(&MarketingEmailEvent{}).
		Where("id = ? AND org_id = ?", eventID, orgID).
		Updates(map[string]any{"status": EventStatusPending, "claimed_at": nil, "error": note}).Error
}

// FinishEvent marks an event terminal (done or failed), stamping processed_at.
func (r *Repository) FinishEvent(ctx context.Context, orgID, eventID uuid.UUID, status, errMsg string) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE marketing_email_events
		SET status = ?, processed_at = NOW(), error = ?
		WHERE id = ? AND org_id = ?`,
		status, errMsg, eventID, orgID).Error
}

// ReapStrandedEvents recovers events stuck in `processing` past the grace window (a
// worker crashed mid-claim): back to pending if there is retry budget left, else
// failed. Returns rows affected.
func (r *Repository) ReapStrandedEvents(ctx context.Context, grace time.Duration, maxAttempts int) (int64, error) {
	res := r.db.WithContext(ctx).Exec(`
		UPDATE marketing_email_events
		SET status = CASE WHEN attempts >= ? THEN ? ELSE ? END,
		    claimed_at = NULL,
		    processed_at = CASE WHEN attempts >= ? THEN NOW() ELSE processed_at END,
		    error = CASE WHEN attempts >= ? THEN 'reaped: max attempts exceeded' ELSE error END
		WHERE status = ? AND claimed_at < NOW() - make_interval(secs => ?)`,
		maxAttempts, EventStatusFailed, EventStatusPending,
		maxAttempts, maxAttempts,
		EventStatusProcessing, int(grace.Seconds()))
	return res.RowsAffected, res.Error
}

// RecordSoftBounce sets an email's soft_bounce suppression count to the number of
// DISTINCT soft-bounce events for that email in the rolling SoftBounceWindow, derived
// by COUNTing the deduped marketing_email_events ledger — NOT a blind `+1`. This is
// IDEMPOTENT: the consumer is at-least-once (a crash/FinishEvent-failure strands a row
// that the reaper repends), and a blind increment would double-count one physical
// bounce off that reprocess loop, prematurely tripping SoftBounceSuppressThreshold —
// and because the row is scope=all, over-suppressing transactional mail too. Deriving
// from the svix-deduped ledger (the same discipline as the breaker's DeliverabilityRates)
// makes re-application a no-op: the count is stable across repends and redeliveries.
//
// The current event is already in the ledger (inserted at enqueue), so it is included
// in the COUNT; GREATEST(...,1) guards the degenerate case. scope=all matches
// DefaultScopeForReason(soft_bounce); Suppresses() suppresses once the count reaches
// the threshold. Atomic via ON CONFLICT on the existing functional dedupe index.
func (r *Repository) RecordSoftBounce(ctx context.Context, orgID uuid.UUID, emailNorm, source string) (int, error) {
	emailNorm = emailutil.Normalize(emailNorm)
	if orgID == uuid.Nil || emailNorm == "" {
		return 0, errors.New("marketing: org id and email required")
	}
	if source == "" {
		source = "resend_webhook"
	}
	var count int
	err := r.db.WithContext(ctx).Raw(`
		INSERT INTO marketing_suppressions
			(org_id, email_normalized, reason, scope, source, soft_bounce_count, last_soft_bounce_at, created_at)
		VALUES (?, ?, 'soft_bounce', 'all', ?, GREATEST((
			SELECT COUNT(*) FROM marketing_email_events
			WHERE org_id = ? AND email_normalized = ? AND reason = 'soft_bounce'
			  AND created_at >= NOW() - make_interval(secs => ?)
		), 1), NOW(), NOW())
		ON CONFLICT (org_id, email_normalized, reason, COALESCE(topic_id, '00000000-0000-0000-0000-000000000000'::uuid))
		DO UPDATE SET
			soft_bounce_count = EXCLUDED.soft_bounce_count,
			last_soft_bounce_at = NOW()
		RETURNING soft_bounce_count`,
		orgID, emailNorm, source, orgID, emailNorm, int(SoftBounceWindow.Seconds())).Scan(&count).Error
	return count, err
}

// DeliverabilityRates is a per-org rolling-window rollup over the deduped event
// ledger. Rates are derived (never persisted) so they stay recomputable — and
// idempotent to redeliveries because the ledger dedupes on svix-id.
type DeliverabilityRates struct {
	Delivered     int     `json:"delivered"`
	Sent          int     `json:"sent"`
	Complaints    int     `json:"complaints"`
	HardBounces   int     `json:"hard_bounces"`
	ComplaintRate float64 `json:"complaint_rate"` // complaints / delivered
	BounceRate    float64 `json:"bounce_rate"`    // hard bounces / sent
}

// DeliverabilityRates computes the per-org complaint/bounce rates over `window`.
func (r *Repository) DeliverabilityRates(ctx context.Context, orgID uuid.UUID, window time.Duration) (DeliverabilityRates, error) {
	var raw struct {
		Delivered   int64
		Sent        int64
		Complaints  int64
		HardBounces int64
	}
	err := r.db.WithContext(ctx).Raw(`
		SELECT
			COUNT(*) FILTER (WHERE event_type = ?) AS delivered,
			COUNT(*) FILTER (WHERE event_type = ?) AS sent,
			COUNT(*) FILTER (WHERE event_type = ?) AS complaints,
			COUNT(*) FILTER (WHERE event_type = ? AND bounce_type = 'permanent') AS hard_bounces
		FROM marketing_email_events
		WHERE org_id = ? AND created_at >= NOW() - make_interval(secs => ?)`,
		ResendTypeDelivered, ResendTypeSent, ResendTypeComplained, ResendTypeBounced,
		orgID, int(window.Seconds())).Scan(&raw).Error
	if err != nil {
		return DeliverabilityRates{}, err
	}
	out := DeliverabilityRates{
		Delivered:   int(raw.Delivered),
		Sent:        int(raw.Sent),
		Complaints:  int(raw.Complaints),
		HardBounces: int(raw.HardBounces),
	}
	if out.Delivered > 0 {
		out.ComplaintRate = float64(out.Complaints) / float64(out.Delivered)
	}
	if out.Sent > 0 {
		out.BounceRate = float64(out.HardBounces) / float64(out.Sent)
	}
	return out, nil
}

// CampaignExists reports whether id is a LIVE campaign of the org — the
// email_opened trigger's v1 gate: M8 sequence sends echo a WORKFLOW uuid in
// the campaign_id tag and must not fire engagement triggers (loop protection).
// Soft-deleted campaigns don't count (every other reader of this table filters
// them; a deleted campaign ghost-firing workflows the UI can't show would be
// undiagnosable).
func (r *Repository) CampaignExists(ctx context.Context, orgID, campaignID uuid.UUID) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).
		Table("marketing_campaigns").
		Where("org_id = ? AND id = ? AND deleted_at IS NULL", orgID, campaignID).
		Count(&n).Error
	return n > 0, err
}

// HadDeliveredWithin reports whether the recipient's campaign email was
// delivered within `window` of `at` — on EITHER side: the analytics Apple-MPP
// heuristic near-mirrored for emit time. The forward half absorbs sub-second
// timestamp inversion between Resend's delivery and pixel subsystems (analytics
// classifies an open at-or-before its delivery as machine; a strictly backward
// window would call it human). Bounded on both sides so a much-later
// re-delivery can't retroactively mark a real open as machine. Served by the
// idx_marketing_email_events_recipient (org_id, email_normalized, event_type,
// occurred_at) index.
func (r *Repository) HadDeliveredWithin(ctx context.Context, orgID uuid.UUID, emailNorm string, campaignID uuid.UUID, at time.Time, window time.Duration) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).
		Table("marketing_email_events").
		Where("org_id = ? AND email_normalized = ? AND event_type = ? AND campaign_id = ? AND occurred_at > ? AND occurred_at <= ?",
			orgID, emailNorm, ResendTypeDelivered, campaignID, at.Add(-window), at.Add(window)).
		Count(&n).Error
	return n > 0, err
}
