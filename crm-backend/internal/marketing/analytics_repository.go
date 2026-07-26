package marketing

import (
	"context"

	"github.com/google/uuid"
)

// mppOpenWindowSeconds: an open firing within this window of the recipient's delivery is
// almost certainly an automated prefetch (Apple Mail Privacy Protection preloads the
// tracking pixel on delivery, GMail's proxy behaves similarly). Opens later than it are
// counted as "engaged". This is a heuristic — clicks stay the HEADLINE metric; opens are
// directional (M9). A recipient with no comparable delivery time counts as engaged
// (conservative — never over-exclude a real open).
const mppOpenWindowSeconds = 10

// CampaignAnalytics is the engagement rollup for one campaign, derived at query time from
// the svix-deduped event ledger — rates are never persisted so they stay recomputable and
// idempotent (a Resend redelivery inserts nothing; DISTINCT collapses repeat opens). All
// counts are UNIQUE recipients unless the field name says Total. OpenedEngaged excludes
// likely-automated (MPP) opens.
type CampaignAnalytics struct {
	Sent          int `json:"sent"`
	Delivered     int `json:"delivered"`
	Opened        int `json:"opened"`         // unique recipients who opened
	OpenedTotal   int `json:"opened_total"`   // total opens (incl. repeats + machine)
	OpenedEngaged int `json:"opened_engaged"` // unique recipients, MPP-window opens excluded
	Clicked       int `json:"clicked"`        // unique recipients who clicked (headline)
	ClickedTotal  int `json:"clicked_total"`
	Bounced       int `json:"bounced"`    // unique recipients with any bounce
	Complained    int `json:"complained"` // unique recipients who complained
}

// CampaignAnalytics computes the per-campaign engagement rollup. Org-scoped (owner-less
// table — org_id bind is the entire access story, no per-viewer predicate) and idempotent.
func (r *Repository) CampaignAnalytics(ctx context.Context, orgID, campaignID uuid.UUID) (CampaignAnalytics, error) {
	type grp struct {
		EventType string
		Total     int64
		Uniq      int64
	}
	var rows []grp
	if err := r.db.WithContext(ctx).Raw(`
		SELECT event_type,
		       COUNT(*) AS total,
		       COUNT(DISTINCT email_normalized) AS uniq
		FROM marketing_email_events
		WHERE org_id = ? AND campaign_id = ?
		GROUP BY event_type`, orgID, campaignID).Scan(&rows).Error; err != nil {
		return CampaignAnalytics{}, err
	}
	var a CampaignAnalytics
	for _, x := range rows {
		switch x.EventType {
		case ResendTypeSent:
			a.Sent = int(x.Uniq)
		case ResendTypeDelivered:
			a.Delivered = int(x.Uniq)
		case ResendTypeOpened:
			a.Opened = int(x.Uniq)
			a.OpenedTotal = int(x.Total)
		case ResendTypeClicked:
			a.Clicked = int(x.Uniq)
			a.ClickedTotal = int(x.Total)
		case ResendTypeBounced:
			a.Bounced = int(x.Uniq)
		case ResendTypeComplained:
			a.Complained = int(x.Uniq)
		}
	}

	// Engaged (MPP-excluded) unique opens: recipients with at least one open that is NOT
	// within mppOpenWindow of their own delivery. Only run when there are opens at all.
	if a.Opened > 0 {
		var engaged int64
		if err := r.db.WithContext(ctx).Raw(`
			SELECT COUNT(DISTINCT o.email_normalized)
			FROM marketing_email_events o
			LEFT JOIN LATERAL (
				SELECT MIN(d.occurred_at) AS delivered_at
				FROM marketing_email_events d
				WHERE d.org_id = o.org_id AND d.campaign_id = o.campaign_id
				  AND d.email_normalized = o.email_normalized
				  AND d.event_type = ?
			) d ON TRUE
			WHERE o.org_id = ? AND o.campaign_id = ? AND o.event_type = ?
			  AND (d.delivered_at IS NULL OR o.occurred_at IS NULL
			       OR o.occurred_at > d.delivered_at + make_interval(secs => ?))`,
			ResendTypeDelivered, orgID, campaignID, ResendTypeOpened, mppOpenWindowSeconds,
		).Scan(&engaged).Error; err != nil {
			return CampaignAnalytics{}, err
		}
		a.OpenedEngaged = int(engaged)
	}
	return a, nil
}
