package ai

import (
	"context"
	"fmt"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// KnowledgeBuilder builds a company-aware system prompt from the knowledge base.
type KnowledgeBuilder struct {
	repo    domain.KnowledgeBaseRepository
	redis   *redis.Client
}

func NewKnowledgeBuilder(repo domain.KnowledgeBaseRepository, redisClient *redis.Client) *KnowledgeBuilder {
	return &KnowledgeBuilder{
		repo:  repo,
		redis: redisClient,
	}
}

func (b *KnowledgeBuilder) cacheKey(orgID uuid.UUID) string {
	return fmt.Sprintf("kb_prompt:%s", orgID)
}

// BustCache removes the cached prompt for an org (call on KB update).
func (b *KnowledgeBuilder) BustCache(ctx context.Context, orgID uuid.UUID) {
	if b.redis != nil {
		b.redis.Del(ctx, b.cacheKey(orgID))
	}
}

// BuildSystemPrompt compiles all active KB sections into a structured prompt.
func (b *KnowledgeBuilder) BuildSystemPrompt(ctx context.Context, orgID uuid.UUID) (string, error) {
	// Try cache first
	if b.redis != nil {
		if cached, err := b.redis.Get(ctx, b.cacheKey(orgID)).Result(); err == nil && cached != "" {
			return cached, nil
		}
	}

	entries, err := b.repo.GetAllActive(ctx, orgID)
	if err != nil {
		return "", fmt.Errorf("knowledge_builder: %w", err)
	}

	getSection := func(section string) string {
		for _, e := range entries {
			if e.Section == section && e.Content != "" {
				return e.Content
			}
		}
		return "(not configured yet)"
	}

	prompt := fmt.Sprintf(`You are an AI sales assistant.

COMPANY:
%s

PRODUCTS & SERVICES:
%s

SALES PLAYBOOK (tone, key messages, objection handling):
%s

OUR PROCESS:
%s

COMPETITIVE ADVANTAGES:
%s

CRITICAL INSTRUCTIONS:
- Always communicate in the tone defined in the Sales Playbook
- Reference specific products, prices, and USPs when composing emails or recommendations
- Use the objection handling scripts when customer concerns arise
- When drafting emails, include the company name and a relevant USP naturally
- Keep all responses concise and action-oriented`,
		getSection("company"),
		getSection("products"),
		getSection("playbook"),
		getSection("process"),
		getSection("competitors"),
	)

	// Cache for 30 minutes
	if b.redis != nil {
		b.redis.Set(ctx, b.cacheKey(orgID), prompt, 30*time.Minute)
	}

	return prompt, nil
}
