package marketing

import (
	"context"
	"net/http"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// TestCampaign_ABConfigFlowsThrough is the regression for the review's HIGH finding: the
// A/B config must survive the request DTO → Create/Update → persisted campaign. (An
// earlier version dropped it because domain.CampaignInput had no A/B fields.)
func TestCampaign_ABConfigFlowsThrough(t *testing.T) {
	env := newCampEnv()
	ctx := context.Background()

	// pct>0 with no variant-B subject → 400.
	if code := campErrCode(t, mustErr(env.uc.Create(ctx, env.orgID, uuid.New(),
		domain.CampaignInput{Name: "C", ABTestPct: 20}))); code != http.StatusBadRequest {
		t.Fatalf("A/B without subject B code=%d want 400", code)
	}
	// pct out of range → 400.
	if code := campErrCode(t, mustErr(env.uc.Create(ctx, env.orgID, uuid.New(),
		domain.CampaignInput{Name: "C", ABTestPct: 150, ABSubjectB: "B"}))); code != http.StatusBadRequest {
		t.Fatalf("A/B pct 150 code=%d want 400", code)
	}

	// Valid A/B create persists the config.
	c, err := env.uc.Create(ctx, env.orgID, uuid.New(),
		domain.CampaignInput{Name: "AB", ABTestPct: 20, ABSubjectB: "Alt subject", ABTestWindowHours: 8})
	if err != nil {
		t.Fatalf("valid A/B create: %v", err)
	}
	if c.ABTestPct != 20 || c.ABSubjectB != "Alt subject" || c.ABTestWindowHours != 8 {
		t.Fatalf("A/B config not set on create: pct=%d subj=%q window=%d", c.ABTestPct, c.ABSubjectB, c.ABTestWindowHours)
	}

	// Update persists an edited A/B config.
	updated, err := env.uc.Update(ctx, env.orgID, c.ID,
		domain.CampaignInput{Name: "AB", ABTestPct: 30, ABSubjectB: "Newer subject", ABTestWindowHours: 24})
	if err != nil {
		t.Fatalf("A/B update: %v", err)
	}
	if updated.ABTestPct != 30 || updated.ABSubjectB != "Newer subject" || updated.ABTestWindowHours != 24 {
		t.Fatalf("A/B config not persisted on update: pct=%d subj=%q window=%d", updated.ABTestPct, updated.ABSubjectB, updated.ABTestWindowHours)
	}

	// Disabling (pct=0) clears the config.
	off, err := env.uc.Update(ctx, env.orgID, c.ID, domain.CampaignInput{Name: "AB", ABTestPct: 0, ABSubjectB: "leftover"})
	if err != nil {
		t.Fatalf("A/B disable: %v", err)
	}
	if off.ABTestPct != 0 || off.ABSubjectB != "" {
		t.Fatalf("disabling A/B should clear it: pct=%d subj=%q", off.ABTestPct, off.ABSubjectB)
	}
}
