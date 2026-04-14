package usecase

import (
	"context"
	"fmt"

	"crm-backend/internal/ai"
	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

type knowledgeBaseUseCase struct {
	repo    domain.KnowledgeBaseRepository
	builder *ai.KnowledgeBuilder
}

func NewKnowledgeBaseUseCase(repo domain.KnowledgeBaseRepository, builder *ai.KnowledgeBuilder) domain.KnowledgeBaseUseCase {
	return &knowledgeBaseUseCase{repo: repo, builder: builder}
}

func (uc *knowledgeBaseUseCase) ListSections(ctx context.Context, orgID uuid.UUID) ([]domain.KnowledgeBaseEntry, error) {
	return uc.repo.GetAllActive(ctx, orgID)
}

func (uc *knowledgeBaseUseCase) GetSection(ctx context.Context, orgID uuid.UUID, section string) (*domain.KnowledgeBaseEntry, error) {
	if _, ok := domain.ValidKBSections[section]; !ok {
		return nil, fmt.Errorf("invalid section: %s", section)
	}
	return uc.repo.GetBySection(ctx, orgID, section)
}

func (uc *knowledgeBaseUseCase) UpsertSection(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, section string, input domain.UpsertKBInput) (*domain.KnowledgeBaseEntry, error) {
	if _, ok := domain.ValidKBSections[section]; !ok {
		return nil, fmt.Errorf("invalid section: %s", section)
	}

	entry := &domain.KnowledgeBaseEntry{
		OrgID:     orgID,
		Section:   section,
		Title:     input.Title,
		Content:   input.Content,
		IsActive:  true,
		CreatedBy: &userID,
	}

	if err := uc.repo.Upsert(ctx, entry); err != nil {
		return nil, err
	}

	// Bust AI prompt cache
	if uc.builder != nil {
		uc.builder.BustCache(ctx, orgID)
	}

	return entry, nil
}

func (uc *knowledgeBaseUseCase) GetAIPrompt(ctx context.Context, orgID uuid.UUID) (string, error) {
	if uc.builder == nil {
		return "", fmt.Errorf("knowledge builder not configured")
	}
	return uc.builder.BuildSystemPrompt(ctx, orgID)
}
