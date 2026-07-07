package domain

import (
	"time"

	"github.com/google/uuid"
)

// PasswordResetToken and EmailVerificationToken mirror the org_invitations
// pattern (plan §4.2): only a SHA-256 hash of the URL token is stored, and each
// token is single-use (UsedAt) and short-TTL (ExpiresAt). The raw token lives
// only in the emailed link.
type PasswordResetToken struct {
	ID        uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null" json:"user_id"`
	TokenHash string     `gorm:"size:255;not null" json:"-"`
	ExpiresAt time.Time  `gorm:"not null" json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	// InitiatedBy is the admin who sent an admin-triggered reset link (P2). NULL
	// for a self-serve forgot-password request. It both audits the sender and
	// keys the per-target daily cap (count of admin-initiated links), so one org's
	// admin can't harass a user who is a member of several workspaces.
	InitiatedBy *uuid.UUID `gorm:"type:uuid" json:"initiated_by,omitempty"`
	CreatedAt   time.Time  `gorm:"not null;default:now()" json:"created_at"`
}

func (PasswordResetToken) TableName() string { return "password_reset_tokens" }

type EmailVerificationToken struct {
	ID        uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null" json:"user_id"`
	TokenHash string     `gorm:"size:255;not null" json:"-"`
	ExpiresAt time.Time  `gorm:"not null" json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedAt time.Time  `gorm:"not null;default:now()" json:"created_at"`
}

func (EmailVerificationToken) TableName() string { return "email_verification_tokens" }

// AuthEvent is one append-only auth/admin/security event (plan §4.1). OrgID is
// nullable for pre-org events (e.g. a login before a workspace is resolved).
// Writes are best-effort — a failure is logged, never surfaced (mirrors
// object_audit).
type AuthEvent struct {
	ID        uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID     *uuid.UUID `gorm:"type:uuid" json:"org_id,omitempty"`
	ActorID   *uuid.UUID `gorm:"type:uuid" json:"actor_id,omitempty"`
	TargetID  *uuid.UUID `gorm:"type:uuid" json:"target_id,omitempty"`
	Category  string     `gorm:"size:20;not null" json:"category"`   // auth | admin | security
	EventType string     `gorm:"size:60;not null" json:"event_type"` // login.success, password.reset, …
	IP        *string    `gorm:"column:ip;type:inet" json:"ip,omitempty"`
	UserAgent *string    `gorm:"type:text" json:"user_agent,omitempty"`
	Metadata  JSON       `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt time.Time  `gorm:"not null;default:now()" json:"created_at"`
}

func (AuthEvent) TableName() string { return "auth_events" }

// AuthEventView is an AuthEvent joined with its actor's name/email for the admin
// audit UI (P4). ActorName/ActorEmail are empty when the actor is a deleted user
// or a system/pre-auth action (actor_id NULL).
type AuthEventView struct {
	AuthEvent
	ActorName  string `json:"actor_name" gorm:"column:actor_name"`
	ActorEmail string `json:"actor_email" gorm:"column:actor_email"`
}

// AuthEventFilter narrows the admin audit query (P4). Zero-value fields are
// ignored; Limit/Offset drive pagination (the usecase clamps Limit).
type AuthEventFilter struct {
	Category  string     // '', 'auth', 'admin', 'security'
	EventType string     // exact event_type match
	ActorID   *uuid.UUID // filter to one actor
	From      *time.Time // created_at >= From
	To        *time.Time // created_at <= To
	Limit     int
	Offset    int
}

// SessionInfo is one active device/session for the sessions UI (P4), projected
// from a live refresh_tokens row. Current marks the session making the request.
type SessionInfo struct {
	ID          uuid.UUID  `json:"id"`
	DeviceLabel string     `json:"device_label"`
	IP          string     `json:"ip"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	Current     bool       `json:"current"`
}

// RequestMeta carries the transport-level detail (IP, User-Agent) an auth event
// records. The handler fills it from the gin request so usecases never depend on
// gin.
type RequestMeta struct {
	IP        string
	UserAgent string
}

// --- Account-recovery input DTOs (P1) ---

type ForgotPasswordInput struct {
	Email string `json:"email" binding:"required,email"`
}

type ResetPasswordInput struct {
	Token    string `json:"token" binding:"required"`
	Password string `json:"password" binding:"required,min=8"`
}

type VerifyEmailInput struct {
	Token string `json:"token" binding:"required"`
}
