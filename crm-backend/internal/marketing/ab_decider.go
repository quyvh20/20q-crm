package marketing

import (
	"context"
	"log/slog"
	"math"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

const (
	abDecideInterval = 2 * time.Minute
	// abMinCell: below this per-cell send count a two-proportion test can't give a
	// trustworthy verdict, so the decider falls back to the higher observed rate and
	// flags the result not-significant rather than overclaiming a winner.
	abMinCell = 30
	// abZ95 is the two-tailed z critical value for 95% confidence.
	abZ95 = 1.96
)

// decideABWinner picks the winning variant by unique open rate (subject A/B's metric).
// Returns the winner, whether the difference is statistically significant (95%
// two-proportion z-test), and a machine reason. When it can't be significant (a cell too
// small, or no variance) the winner defaults to the higher observed rate — A on a tie —
// so the held remainder always receives SOMETHING.
func decideABWinner(a, b ABVariantStat) (winner string, significant bool, reason string) {
	rateA, rateB := 0.0, 0.0
	if a.Sent > 0 {
		rateA = float64(a.Opened) / float64(a.Sent)
	}
	if b.Sent > 0 {
		rateB = float64(b.Opened) / float64(b.Sent)
	}
	winner = "A"
	if rateB > rateA {
		winner = "B"
	}
	if a.Sent < abMinCell || b.Sent < abMinCell {
		return winner, false, "cell_too_small"
	}
	pPool := float64(a.Opened+b.Opened) / float64(a.Sent+b.Sent)
	se := math.Sqrt(pPool * (1 - pPool) * (1.0/float64(a.Sent) + 1.0/float64(b.Sent)))
	if se == 0 {
		return winner, false, "no_variance"
	}
	z := math.Abs(rateA-rateB) / se
	if z >= abZ95 {
		return winner, true, "significant"
	}
	return winner, false, "not_significant"
}

// abDeciderStore is the persistence slice the decider needs.
type abDeciderStore interface {
	ClaimUndecidedABCampaigns(ctx context.Context) ([]domain.Campaign, error)
	ABVariantMetrics(ctx context.Context, campaignID uuid.UUID) (map[string]ABVariantStat, error)
	DecideABWinner(ctx context.Context, campaignID uuid.UUID, winner string) (marked bool, released int64, err error)
}

// ABDecider closes the A/B loop: once a campaign's test window has elapsed it computes the
// winner from the test cells' opens, records it (MarkABWinner is a single-winner race
// guard across replicas), and releases the held remainder to the winner — which re-enters
// the normal send lane and inherits every live M1–M4 gate.
type ABDecider struct {
	store  abDeciderStore
	logger *slog.Logger
}

// StartABDecider runs the decide loop until ctx is done.
func StartABDecider(ctx context.Context, store abDeciderStore, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	d := &ABDecider{store: store, logger: logger}
	ticker := time.NewTicker(abDecideInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.tick(ctx)
		}
	}
}

func (d *ABDecider) tick(ctx context.Context) {
	camps, err := d.store.ClaimUndecidedABCampaigns(ctx)
	if err != nil {
		d.logger.Error("marketing: ab decider claim failed", "error", err)
		return
	}
	for i := range camps {
		c := camps[i]
		metrics, err := d.store.ABVariantMetrics(ctx, c.ID)
		if err != nil {
			d.logger.Error("marketing: ab variant metrics failed", "campaign", c.ID.String(), "error", err)
			continue
		}
		winner, sig, reason := decideABWinner(metrics["A"], metrics["B"])
		// Atomic decide+release: guarded on ab_winner_variant='' (single-winner race) and
		// transactional (a failure rolls back → re-claimed next tick, no stranded remainder).
		marked, released, err := d.store.DecideABWinner(ctx, c.ID, winner)
		if err != nil {
			d.logger.Error("marketing: ab decide+release failed", "campaign", c.ID.String(), "error", err)
			continue
		}
		if !marked {
			continue
		}
		d.logger.Info("marketing: ab winner decided",
			"campaign", c.ID.String(), "winner", winner, "significant", sig, "reason", reason,
			"released", released,
			"a_sent", metrics["A"].Sent, "a_opened", metrics["A"].Opened,
			"b_sent", metrics["B"].Sent, "b_opened", metrics["B"].Opened,
		)
	}
}
