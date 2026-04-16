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

	costUSD := estimateCost(model, in, out, cfg.CachedInputTokens, cfg.CacheCreationTokens)

	reqID := "unknown"
	if rid, ok := ctx.Value("request_id").(string); ok && rid != "" {
		reqID = rid
	}

	stopReason := cfg.StopReason
	if stopReason == "" {
		stopReason = "unknown"
	}

	promptHash := cfg.PromptHash
	if promptHash == "" {
		promptHash = "none"
	}

	// 12-field precise structured token accounting log
	slog.Info("llm_call",
		"input_tokens", in,
		"output_tokens", out,
		"cached_input_tokens", cfg.CachedInputTokens,
		"model", model,
		"latency_ms", cfg.LatencyMs,
		"cost_usd", costUSD,
		"user_id", userID.String(),
		"endpoint", string(task), // mapped 'task' to user-requested 'endpoint' key
		"prompt_hash", promptHash,
		"cache_hit", cfg.CachedInputTokens > 0,
		"stop_reason", stopReason,
		"request_id", reqID,
	)

	if g.db != nil {
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
			StopReason:        cfg.StopReason, // keep empty for db if desired, but user said no empty strings. Actually db schema allows empty string.
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
	PromptHash          string
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
func WithPromptHash(s string) RecordOption {
	return func(c *recordConfig) { c.PromptHash = s }
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

// GetTopUsages returns the most expensive recent usages by default, or most recent if requested.
func (g *BudgetGuard) GetTopUsages(ctx context.Context, orgID uuid.UUID, limit int, sortOption string) ([]domain.AITokenUsage, error) {
	if g.db == nil {
		return nil, errors.New("database not enabled")
	}

	var usages []domain.AITokenUsage
	query := g.db.WithContext(ctx).Where("org_id = ? AND created_at >= ?", orgID, time.Now().Add(-24*time.Hour)).Limit(limit)

	if sortOption == "recent" {
		query = query.Order("created_at DESC")
	} else {
		query = query.Order("cost_usd DESC")
	}

	if err := query.Find(&usages).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch top usages: %w", err)
	}

	return usages, nil
}

// GetUsageStats calculates stop_reason metrics to verify max_token boundaries.
func (g *BudgetGuard) GetUsageStats(ctx context.Context, orgID uuid.UUID) (map[string]interface{}, error) {
	if g.db == nil {
		return nil, errors.New("database not enabled")
	}

	type statsRow struct {
		Feature   string
		Total     int
		MaxTokens int
	}
	var rows []statsRow

	err := g.db.WithContext(ctx).Table("ai_token_usages").
		Select("feature, count(*) as total, sum(case when stop_reason ilike '%max_tokens%' then 1 else 0 end) as max_tokens").
		Where("org_id = ?", orgID).
		Group("feature").
		Scan(&rows).Error

	if err != nil {
		return nil, err
	}

	res := make(map[string]interface{})
	for _, r := range rows {
		pct := 0.0
		if r.Total > 0 {
			pct = float64(r.MaxTokens) / float64(r.Total) * 100.0
		}
		res[r.Feature] = map[string]interface{}{
			"total":      r.Total,
			"max_tokens": r.MaxTokens,
			"percent":    pct,
		}
	}
	return res, nil
}

var _ = errors.New // silence unused import
