package marketing

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ABVariantStat is one variant's unique engagement, used to pick the winner.
type ABVariantStat struct {
	Variant string `json:"variant"`
	Sent    int    `json:"sent"`
	Opened  int    `json:"opened"`
	Clicked int    `json:"clicked"`
}

// AssignABTestCells splits a random testPct% of the campaign's still-unassigned pending
// roster into alternating A/B test cells (which send immediately) and HOLDS the rest
// (scheduled_for far in the future, so the send worker's `scheduled_for <= NOW()` claim
// skips them) until a winner is released. Returns the number of test-cell rows assigned.
// Called once inside the exclusive snapshotting launch window, so no concurrency guard is
// needed beyond only touching variant-less pending rows.
func (r *Repository) AssignABTestCells(ctx context.Context, campaignID uuid.UUID, testPct int) (int, error) {
	if testPct <= 0 {
		testPct = 20
	}
	if testPct > 100 {
		testPct = 100
	}
	res := r.db.WithContext(ctx).Exec(`
		WITH total AS (
			SELECT count(*) AS n FROM marketing_campaign_recipients
			WHERE campaign_id = ? AND status = 'pending' AND variant IS NULL
		),
		picked AS (
			SELECT id, row_number() OVER (ORDER BY random()) AS rn
			FROM marketing_campaign_recipients
			WHERE campaign_id = ? AND status = 'pending' AND variant IS NULL
		),
		test AS (
			SELECT p.id, p.rn FROM picked p, total t
			WHERE p.rn <= GREATEST(1, CEIL(t.n * ? / 100.0))
		)
		UPDATE marketing_campaign_recipients r
		SET variant = CASE WHEN t.rn % 2 = 0 THEN 'A' ELSE 'B' END
		FROM test t WHERE r.id = t.id`,
		campaignID, campaignID, testPct)
	if res.Error != nil {
		return 0, res.Error
	}
	assigned := res.RowsAffected

	// Hold the remainder far in the future so the claim query never picks it up until
	// the winner is released.
	if err := r.db.WithContext(ctx).Exec(`
		UPDATE marketing_campaign_recipients
		SET scheduled_for = NOW() + INTERVAL '100 years'
		WHERE campaign_id = ? AND status = 'pending' AND variant IS NULL`,
		campaignID).Error; err != nil {
		return 0, err
	}
	return int(assigned), nil
}

// ABVariantMetrics rolls up unique per-variant engagement for the test cells, joining the
// deduped event ledger to the roster on (campaign_id, email_normalized) → variant. Unique
// via COUNT(DISTINCT email_normalized); the double LEFT JOIN fans out per recipient but
// DISTINCT collapses it, so opened/clicked are distinct recipients, not raw events.
func (r *Repository) ABVariantMetrics(ctx context.Context, campaignID uuid.UUID) (map[string]ABVariantStat, error) {
	type row struct {
		Variant string
		Sent    int
		Opened  int
		Clicked int
	}
	var rows []row
	if err := r.db.WithContext(ctx).Raw(`
		SELECT r.variant AS variant,
		       COUNT(DISTINCT r.email_normalized) FILTER (WHERE r.status = 'sent') AS sent,
		       COUNT(DISTINCT eo.email_normalized) AS opened,
		       COUNT(DISTINCT ec.email_normalized) AS clicked
		FROM marketing_campaign_recipients r
		LEFT JOIN marketing_email_events eo
		  ON eo.org_id = r.org_id AND eo.campaign_id = r.campaign_id
		 AND eo.email_normalized = r.email_normalized AND eo.event_type = ?
		LEFT JOIN marketing_email_events ec
		  ON ec.org_id = r.org_id AND ec.campaign_id = r.campaign_id
		 AND ec.email_normalized = r.email_normalized AND ec.event_type = ?
		WHERE r.campaign_id = ? AND r.variant IN ('A','B')
		GROUP BY r.variant`,
		ResendTypeOpened, ResendTypeClicked, campaignID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]ABVariantStat, len(rows))
	for _, x := range rows {
		out[x.Variant] = ABVariantStat{Variant: x.Variant, Sent: x.Sent, Opened: x.Opened, Clicked: x.Clicked}
	}
	return out, nil
}

// DecideABWinner atomically records the winner AND releases the held remainder to it in a
// SINGLE transaction — so the two can never diverge. A crash or error rolls the whole
// thing back, leaving ab_winner_variant='' so the decider re-claims and retries next tick
// (no stranded remainder). The mark is guarded on ab_winner_variant='' so only ONE
// replica wins the race; the loser marks nothing, releases nothing, and returns
// marked=false. Release stamps ONLY the held (variant IS NULL, pending) rows — never the
// already-sent test cells — so no roster row is ever sent under two variants. The
// released rows re-enter the normal claim/send lane and inherit every live M1–M4 gate.
func (r *Repository) DecideABWinner(ctx context.Context, campaignID uuid.UUID, winner string) (marked bool, released int64, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`
			UPDATE marketing_campaigns
			SET ab_winner_variant = ?, ab_decided_at = NOW(), updated_at = NOW()
			WHERE id = ? AND ab_winner_variant = '' AND status = 'sending'`,
			winner, campaignID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil // another replica already decided — do not release
		}
		marked = true
		rel := tx.Exec(`
			UPDATE marketing_campaign_recipients
			SET variant = ?, scheduled_for = NOW()
			WHERE campaign_id = ? AND status = 'pending' AND variant IS NULL`,
			winner, campaignID)
		if rel.Error != nil {
			return rel.Error
		}
		released = rel.RowsAffected
		return nil
	})
	if err != nil {
		return false, 0, err
	}
	return marked, released, nil
}

// ClaimUndecidedABCampaigns returns A/B campaigns whose test window has elapsed and whose
// winner is not yet decided — the decider's work list. No binds (only column refs), so no
// gorm ?-placeholder trap.
func (r *Repository) ClaimUndecidedABCampaigns(ctx context.Context) ([]domain.Campaign, error) {
	var rows []domain.Campaign
	err := r.db.WithContext(ctx).Raw(`
		SELECT * FROM marketing_campaigns
		WHERE deleted_at IS NULL
		  AND status = 'sending'
		  AND ab_test_pct > 0
		  AND ab_winner_variant = ''
		  AND started_at IS NOT NULL
		  AND started_at + make_interval(hours => ab_test_window_hours) <= NOW()`).Scan(&rows).Error
	return rows, err
}
