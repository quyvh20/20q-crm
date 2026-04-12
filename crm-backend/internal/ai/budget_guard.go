package ai

import (
	"context"
	"errors"
	"fmt"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// ============================================================
// Plan limits
// ============================================================

type planLimits struct {
	MonthlyAITokens    int
	AIFeaturesAdvanced bool
}

var tierLimits = map[string]planLimits{
	"free":     {MonthlyAITokens: 10_000, AIFeaturesAdvanced: false},
	"starter":  {MonthlyAITokens: 50_000, AIFeaturesAdvanced: false},
	"pro":      {MonthlyAITokens: 500_000, AIFeaturesAdvanced: true},
	"business": {MonthlyAITokens: 2_000_000, AIFeaturesAdvanced: true},
}

// ============================================================
// Custom errors
// ============================================================

type ErrBudgetExceeded struct {
	Used    int
	Limit   int
	ResetAt time.Time
}

func (e ErrBudgetExceeded) Error() string {
	return fmt.Sprintf("monthly AI token budget exceeded (%d/%d). Resets %s",
		e.Used, e.Limit, e.ResetAt.Format("Jan 2"))
}

type ErrFeatureNotInPlan struct {
	Feature      string
	RequiresPlan string
}

func (e ErrFeatureNotInPlan) Error() string {
	return fmt.Sprintf("feature '%s' requires %s plan or higher", e.Feature, e.RequiresPlan)
}

// ErrAITimeout is returned when the upstream AI provider does not respond
// within the configured deadline. Callers should respond with HTTP 503 and
// a Retry-After header so clients can back off gracefully.
type ErrAITimeout struct {
	Provider string
	After    int // suggested retry delay in seconds
}

func (e ErrAITimeout) Error() string {
	return fmt.Sprintf("AI provider %q timed out; retry after %ds", e.Provider, e.After)
}

// ============================================================
// BudgetGuard
// ============================================================

type BudgetGuard struct {
	db    *gorm.DB
	redis *redis.Client
}

func NewBudgetGuard(db *gorm.DB, redis *redis.Client) *BudgetGuard {
	return &BudgetGuard{db: db, redis: redis}
}

// Check verifies the org has budget remaining and the feature is in their plan.
func (g *BudgetGuard) Check(ctx context.Context, orgID uuid.UUID, task AITask, estimated int) error {
	limits := g.getPlanLimits(ctx, orgID)

	if IsAdvancedTask(task) && !limits.AIFeaturesAdvanced {
		return ErrFeatureNotInPlan{Feature: string(task), RequiresPlan: "pro"}
	}

	if g.redis == nil {
		return nil // no Redis — skip budget enforcement in dev
	}

	key := redisKey(orgID)
	used, _ := g.redis.Get(ctx, key).Int()
	if used+estimated > limits.MonthlyAITokens {
		return ErrBudgetExceeded{
			Used:    used,
			Limit:   limits.MonthlyAITokens,
			ResetAt: firstDayNextMonth(),
		}
	}
	return nil
}

// Record increments Redis counter and asynchronously writes an audit row.
func (g *BudgetGuard) Record(ctx context.Context, orgID, userID uuid.UUID, task AITask, model, prov string, in, out int) {
	total := in + out

	if g.redis != nil {
		key := redisKey(orgID)
		pipe := g.redis.Pipeline()
		pipe.IncrBy(ctx, key, int64(total))
		pipe.ExpireAt(ctx, key, firstDayNextMonth())
		pipe.Exec(ctx) //nolint:errcheck
	}

	if g.db != nil {
		costUSD := estimateCost(model, in, out)
		g.db.WithContext(ctx).Create(&domain.AITokenUsage{
			OrgID:        orgID,
			UserID:       userID,
			Model:        model,
			Provider:     prov,
			Feature:      string(task),
			InputTokens:  in,
			OutputTokens: out,
			CostUSD:      costUSD,
		})
	}
}

// GetUsage returns current month's usage for an org.
func (g *BudgetGuard) GetUsage(ctx context.Context, orgID uuid.UUID) (used int, limit int, resetAt time.Time) {
	limits := g.getPlanLimits(ctx, orgID)
	limit = limits.MonthlyAITokens
	resetAt = firstDayNextMonth()

	if g.redis != nil {
		used, _ = g.redis.Get(ctx, redisKey(orgID)).Int()
	}
	return
}

// ============================================================
// Internal helpers
// ============================================================

func (g *BudgetGuard) getPlanLimits(ctx context.Context, orgID uuid.UUID) planLimits {
	if g.db == nil {
		return tierLimits["pro"] // dev mode
	}
	var org struct{ PlanTier string }
	g.db.WithContext(ctx).Model(&domain.Organization{}).
		Select("plan_tier").
		Where("id = ?", orgID).
		First(&org)

	if l, ok := tierLimits[org.PlanTier]; ok {
		return l
	}
	return tierLimits["free"]
}

func redisKey(orgID uuid.UUID) string {
	return fmt.Sprintf("ai_used:%s:%s", orgID, time.Now().Format("2006-01"))
}

func firstDayNextMonth() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
}

// estimateCost returns approximate USD cost based on known pricing.
func estimateCost(model string, in, out int) float64 {
	// Anthropic claude-3-5-haiku: $0.80/$4.00 per 1M tokens in/out
	// CF Workers AI llama: effectively free up to included quota
	switch {
	case len(model) > 0 && model[:6] == "claude":
		return float64(in)*0.80/1_000_000 + float64(out)*4.00/1_000_000
	default:
		return 0
	}
}

var _ = errors.New // silence unused import
