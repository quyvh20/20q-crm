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
	repo       domain.KnowledgeBaseRepository
	settingsUC domain.OrgSettingsUseCase
	redis      *redis.Client
}

func NewKnowledgeBuilder(repo domain.KnowledgeBaseRepository, settingsUC domain.OrgSettingsUseCase, redisClient *redis.Client) *KnowledgeBuilder {
	return &KnowledgeBuilder{
		repo:       repo,
		settingsUC: settingsUC,
		redis:      redisClient,
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

	// ── Build Schema Section ──
	var schemaBuilder string
	for _, entityType := range []string{"contact", "deal"} {
		if fields, err := b.settingsUC.GetFieldDefs(ctx, orgID, entityType); err == nil && len(fields) > 0 {
			schemaBuilder += fmt.Sprintf("\n%s fields:\n", entityType)
			for _, f := range fields {
				req := ""
				if f.Required {
					req = " (REQUIRED)"
				}
				schemaBuilder += fmt.Sprintf("- %s [%s]%s: %s\n", f.Key, f.Type, req, f.Label)
			}
		}
	}
	if schemaBuilder == "" {
		schemaBuilder = "(no custom schema defined)"
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

CRM SCHEMA (Available Custom Fields):
%s

CRITICAL INSTRUCTIONS:
- Always communicate in the tone defined in the Sales Playbook
- Reference specific products, prices, and USPs when composing emails or recommendations
- Use the objection handling scripts when customer concerns arise
- When drafting emails, include the company name and a relevant USP naturally
- When calling form tools (e.g. create_contact, create_deal), you MUST extract relevant values from the user's message and put them in the 'custom_fields' property mapping to the keys defined in the CRM SCHEMA above.
- Keep all responses concise and action-oriented`,
		getSection("company"),
		getSection("products"),
		getSection("playbook"),
		getSection("process"),
		getSection("competitors"),
		schemaBuilder,
	)

	// Cache for 30 minutes
	if b.redis != nil {
		b.redis.Set(ctx, b.cacheKey(orgID), prompt, 30*time.Minute)
	}

	return prompt, nil
}
