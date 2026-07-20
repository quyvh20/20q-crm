package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// defaultPersona is the assistant's identity when an org has not set one. It is
// the exact string this prompt opened with before personas existed, so an org
// without an override gets a byte-identical prompt.
const defaultPersona = "You are an AI sales assistant."

// KnowledgeBuilder builds a company-aware system prompt from the knowledge base.
type KnowledgeBuilder struct {
	repo        domain.KnowledgeBaseRepository
	settingsUC  domain.OrgSettingsUseCase
	customObjUC domain.CustomObjectUseCase
	// orgSettingsRepo reads the org_settings ROW, which is where the per-org AI
	// persona lives. Deliberately NOT settingsUC: that interface exposes field-def
	// methods only and can never reach AIContextOverride. Optional — nil yields the
	// default persona — and set via SetOrgSettingsRepo so the constructor signature
	// (and its two existing call sites) stay unchanged.
	orgSettingsRepo domain.OrgSettingsRepository
	redis           *redis.Client
}

func NewKnowledgeBuilder(repo domain.KnowledgeBaseRepository, settingsUC domain.OrgSettingsUseCase, customObjUC domain.CustomObjectUseCase, redisClient *redis.Client) *KnowledgeBuilder {
	return &KnowledgeBuilder{
		repo:        repo,
		settingsUC:  settingsUC,
		customObjUC: customObjUC,
		redis:       redisClient,
	}
}

func (b *KnowledgeBuilder) cacheKey(orgID uuid.UUID) string {
	return fmt.Sprintf("kb_prompt:%s", orgID)
}

// SetCustomObjectUC sets the custom object use case (used to break circular init).
func (b *KnowledgeBuilder) SetCustomObjectUC(uc domain.CustomObjectUseCase) {
	b.customObjUC = uc
}

// SetOrgSettingsRepo supplies the repository the per-org AI persona is read from.
// Optional, mirroring SetCustomObjectUC: unset, the assistant keeps its default
// identity.
func (b *KnowledgeBuilder) SetOrgSettingsRepo(repo domain.OrgSettingsRepository) {
	b.orgSettingsRepo = repo
}

// persona resolves the org's assistant identity, falling back to the default.
//
// A lookup failure degrades to the default rather than erroring the prompt: a
// blip reading one optional row must not take the whole AI assistant down.
func (b *KnowledgeBuilder) persona(ctx context.Context, orgID uuid.UUID) string {
	if b.orgSettingsRepo == nil {
		return defaultPersona
	}
	settings, err := b.orgSettingsRepo.GetByOrgID(ctx, orgID)
	if err != nil || settings == nil || settings.AIContextOverride == nil {
		return defaultPersona
	}
	if p := strings.TrimSpace(*settings.AIContextOverride); p != "" {
		return p
	}
	return defaultPersona
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

	// ── Build Schema Section (base objects + custom objects) ──
	var schemaBuilder string

	// Base objects: contact, deal — with their custom fields
	for _, entityType := range []string{"contact", "deal"} {
		schemaBuilder += fmt.Sprintf("\n## %s (base object)\n", entityType)
		schemaBuilder += fmt.Sprintf("Base fields: standard CRM %s fields (name, email, phone for contacts; title, value, stage for deals)\n", entityType)
		if fields, err := b.settingsUC.GetFieldDefs(ctx, orgID, entityType); err == nil && len(fields) > 0 {
			schemaBuilder += "Custom fields:\n"
			for _, f := range fields {
				req := ""
				if f.Required {
					req = " (REQUIRED)"
				}
				opts := ""
				if f.Type == "select" && len(f.Options) > 0 {
					optsJSON, _ := json.Marshal(f.Options)
					opts = fmt.Sprintf(" options=%s", string(optsJSON))
				}
				schemaBuilder += fmt.Sprintf("- %s [%s]%s%s: %s\n", f.Key, f.Type, req, opts, f.Label)
			}
		} else {
			schemaBuilder += "Custom fields: (none)\n"
		}
	}

	// Custom objects — dynamic, org-specific
	if b.customObjUC != nil {
		if defs, err := b.customObjUC.ListDefs(ctx, orgID); err == nil && len(defs) > 0 {
			for _, def := range defs {
				schemaBuilder += fmt.Sprintf("\n## %s (custom object, slug: \"%s\", icon: %s)\n", def.Label, def.Slug, def.Icon)
				schemaBuilder += fmt.Sprintf("Use search_objects with object_slug=\"%s\" to query. Use create_object_record with object_slug=\"%s\" to create.\n", def.Slug, def.Slug)
				if def.Searchable {
					schemaBuilder += "Semantic + fulltext search enabled (P6): these records are indexed for global search across the org.\n"
				}

				// Parse fields from JSONB
				var fields []struct {
					Key      string   `json:"key"`
					Label    string   `json:"label"`
					Type     string   `json:"type"`
					Required bool     `json:"required"`
					Options  []string `json:"options,omitempty"`
				}
				if err := json.Unmarshal(def.Fields, &fields); err == nil && len(fields) > 0 {
					schemaBuilder += "Fields:\n"
					for _, f := range fields {
						req := ""
						if f.Required {
							req = " (REQUIRED)"
						}
						opts := ""
						if f.Type == "select" && len(f.Options) > 0 {
							optsJSON, _ := json.Marshal(f.Options)
							opts = fmt.Sprintf(" options=%s", string(optsJSON))
						}
						schemaBuilder += fmt.Sprintf("- %s [%s]%s%s: %s\n", f.Key, f.Type, req, opts, f.Label)
					}
				} else {
					schemaBuilder += "Fields: (none defined)\n"
				}
			}
		}
	}

	if schemaBuilder == "" {
		schemaBuilder = "(no custom schema defined)"
	}

	// The persona REPLACES the opening identity line rather than being appended to
	// it. An industry template's persona is itself a role statement ("You are a CRM
	// assistant for a real estate agency…"), so appending would hand the model two
	// competing identities; role framing has to be singular and first.
	prompt := fmt.Sprintf(`%s

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

CRM SCHEMA (All Objects & Fields — use this to understand what data exists in the org):
%s

CRITICAL INSTRUCTIONS:
- Always communicate in the tone defined in the Sales Playbook
- Reference specific products, prices, and USPs when composing emails or recommendations
- Use the objection handling scripts when customer concerns arise
- When drafting emails, include the company name and a relevant USP naturally
- When calling form tools (e.g. create_contact, create_deal), you MUST extract relevant values from the user's message and put them in the 'custom_fields' property mapping to the keys defined in the CRM SCHEMA above.
- For custom objects, use search_objects and create_object_record tools with the correct object_slug from the schema.
- Keep all responses concise and action-oriented`,
		b.persona(ctx, orgID),
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
