package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ChatSession represents one conversational thread (a "New Chat").
type ChatSession struct {
	ID        uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID     uuid.UUID  `gorm:"type:uuid;not null;index" json:"org_id"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null;index" json:"user_id"`
	Title     string     `gorm:"type:text;not null;default:''" json:"title"` // first 80 chars of first user msg
	Role      string     `gorm:"size:50;not null;default:''" json:"role"`    // user's role at session start
	CreatedAt time.Time  `gorm:"not null;default:now()" json:"created_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`

	// Joined relations (for admin list)
	User *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// ChatMessage is a single turn inside a ChatSession.
type ChatMessage struct {
	ID        uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	SessionID uuid.UUID `gorm:"type:uuid;not null;index" json:"session_id"`
	Role      string    `gorm:"size:20;not null" json:"role"` // "user" | "assistant"
	Content   string    `gorm:"type:text;not null" json:"content"`
	CreatedAt time.Time `gorm:"not null;default:now()" json:"created_at"`
}

// ChatSessionFilter is used for admin list queries.
type ChatSessionFilter struct {
	UserID *uuid.UUID
	Limit  int
	Offset int
}

// ChatSessionRepository is the persistence interface for chat history.
type ChatSessionRepository interface {
	CreateSession(ctx context.Context, s *ChatSession) error
	EndSession(ctx context.Context, sessionID uuid.UUID) error
	AppendMessage(ctx context.Context, m *ChatMessage) error
	ListSessions(ctx context.Context, orgID uuid.UUID, f ChatSessionFilter) ([]ChatSession, int64, error)
	ListMessages(ctx context.Context, sessionID uuid.UUID) ([]ChatMessage, error)
	DeleteSession(ctx context.Context, orgID, sessionID uuid.UUID) error
}
