package repository

import (
	"context"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type chatSessionRepository struct {
	db *gorm.DB
}

func NewChatSessionRepository(db *gorm.DB) domain.ChatSessionRepository {
	return &chatSessionRepository{db: db}
}

func (r *chatSessionRepository) CreateSession(ctx context.Context, s *domain.ChatSession) error {
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *chatSessionRepository) EndSession(ctx context.Context, sessionID uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&domain.ChatSession{}).
		Where("id = ?", sessionID).
		Update("ended_at", now).Error
}

func (r *chatSessionRepository) AppendMessage(ctx context.Context, m *domain.ChatMessage) error {
	return r.db.WithContext(ctx).Create(m).Error
}

func (r *chatSessionRepository) ListSessions(ctx context.Context, orgID uuid.UUID, f domain.ChatSessionFilter) ([]domain.ChatSession, int64, error) {
	q := r.db.WithContext(ctx).
		Model(&domain.ChatSession{}).
		Where("org_id = ?", orgID)

	if f.UserID != nil {
		q = q.Where("user_id = ?", f.UserID)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}

	var sessions []domain.ChatSession
	err := q.
		Preload("User").
		Order("created_at DESC").
		Limit(limit).
		Offset(f.Offset).
		Find(&sessions).Error

	return sessions, total, err
}

func (r *chatSessionRepository) ListMessages(ctx context.Context, sessionID uuid.UUID) ([]domain.ChatMessage, error) {
	var messages []domain.ChatMessage
	err := r.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("created_at ASC").
		Find(&messages).Error
	return messages, err
}

func (r *chatSessionRepository) DeleteSession(ctx context.Context, orgID, sessionID uuid.UUID) error {
	// Delete messages first
	if err := r.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Delete(&domain.ChatMessage{}).Error; err != nil {
		return err
	}
	// Then delete session (with org scoping for safety)
	return r.db.WithContext(ctx).
		Where("id = ? AND org_id = ?", sessionID, orgID).
		Delete(&domain.ChatSession{}).Error
}
