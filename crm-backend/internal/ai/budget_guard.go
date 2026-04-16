package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
func (g *BudgetGuard) Record(ctx context.Context, orgID, userID uuid.UUID, task AITask, model, prov string, in, out int, opts ...RecordOption) {
	cfg := recordConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	total := in + out

	if g.redis != nil {
		key := redisKey(orgID)
		pipe := g.redis.Pipeline()
		pipe.IncrBy(ctx, key, int64(total))
		pipe.ExpireAt(ctx, key, firstDayNextMonth())
		pipe.Exec(ctx) //nolint:errcheck
	}

	// Structured token accounting log (item 2)
	slog.Info("ai_usage_record",
		"org_id", orgID.String(),
		"user_id", userID.String(),
		"task", string(task),
		"model", model,
		"provider", prov,
		"input_tokens", in,
		"output_tokens", out,
		"cached_input_tokens", cfg.CachedInputTokens,
		"latency_ms", cfg.LatencyMs,
		"stop_reason", cfg.StopReason,
		"cache_hit", cfg.CachedInputTokens > 0,
	)

	if g.db != nil {
		costUSD := estimateCost(model, in, out, cfg.CachedInputTokens, cfg.CacheCreationTokens)
		g.db.WithContext(ctx).Create(&domain.AITokenUsage{
			OrgID:             orgID,
			UserID:            userID,
			Model:             model,
			Provider:          prov,
			Feature:           string(task),
			InputTokens:       in,
			OutputTokens:      out,
			CachedInputTokens: cfg.CachedInputTokens,
			LatencyMs:         cfg.LatencyMs,
			StopReason:        cfg.StopReason,
			CacheHit:          cfg.CachedInputTokens > 0,
			CostUSD:           costUSD,
		})
	}
}

// RecordOption configures optional fields for Record.
type RecordOption func(*recordConfig)
type recordConfig struct {
	CachedInputTokens   int
	CacheCreationTokens int
	LatencyMs           int64
	StopReason          string
}

func WithCache(cached, created int) RecordOption {
	return func(c *recordConfig) { c.CachedInputTokens = cached; c.CacheCreationTokens = created }
}
func WithLatency(ms int64) RecordOption {
	return func(c *recordConfig) { c.LatencyMs = ms }
}
func WithStopReason(s string) RecordOption {
	return func(c *recordConfig) { c.StopReason = s }
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
// Haiku 4.5: $1/M input, $5/M output, $0.10/M cached-read, $1.25/M cache-creation
// Claude 3.5 Haiku: $0.80/$4.00 per 1M tokens in/out
func estimateCost(model string, in, out, cachedRead, cacheCreation int) float64 {
	switch {
	case strings.Contains(model, "haiku-4.5") || strings.Contains(model, "claude-haiku-4"):
		// Haiku 4.5 pricing
		inputCost := float64(in) * 1.00 / 1_000_000
		outputCost := float64(out) * 5.00 / 1_000_000
		cachedCost := float64(cachedRead) * 0.10 / 1_000_000
		creationCost := float64(cacheCreation) * 1.25 / 1_000_000
		return inputCost + outputCost + cachedCost + creationCost
	case strings.Contains(model, "claude"):
		// Claude 3.5 Haiku fallback pricing
		return float64(in)*0.80/1_000_000 + float64(out)*4.00/1_000_000
	default:
		return 0
	}
}

// GetTopUsages retrieves the most expensive queries for the org within the last 24h
func (g *BudgetGuard) GetTopUsages(ctx context.Context, orgID uuid.UUID, limit int) ([]domain.AITokenUsage, error) {
	var usages []domain.AITokenUsage
	if g.db == nil {
		return usages, nil
	}
	
	err := g.db.WithContext(ctx).
		Where("org_id = ? AND created_at > ?", orgID, time.Now().Add(-24*time.Hour)).
		Order("cost_usd DESC").
		Limit(limit).
		Find(&usages).Error
		
	return usages, err
}

var _ = errors.New // silence unused import
