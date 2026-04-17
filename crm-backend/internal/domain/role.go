package domain

import (
	"time"

	"github.com/google/uuid"
)

type Role struct {
	ID        uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID     *uuid.UUID `gorm:"type:uuid" json:"org_id,omitempty"`
	Name      string     `gorm:"size:255;not null" json:"name"`
	IsSystem  bool       `gorm:"not null;default:false" json:"is_system"`
	CreatedAt time.Time  `gorm:"not null;default:now()" json:"created_at"`

	Permissions []RolePermission `gorm:"foreignKey:RoleID" json:"permissions,omitempty"`
}

type RolePermission struct {
	RoleID         uuid.UUID `gorm:"type:uuid;primaryKey" json:"role_id"`
	PermissionCode string    `gorm:"size:255;primaryKey" json:"permission_code"`
}

type RecordShare struct {
	ID             uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	RecordType     string     `gorm:"size:50;not null" json:"record_type"`
	RecordID       uuid.UUID  `gorm:"type:uuid;not null" json:"record_id"`
	GranteeUserID  uuid.UUID  `gorm:"type:uuid;not null" json:"grantee_user_id"`
	PermissionLevel string    `gorm:"size:50;not null;default:'read'" json:"permission_level"`
	CreatedBy      *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt      time.Time  `gorm:"not null;default:now()" json:"created_at"`
}

type OrgInvitation struct {
	ID        uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	Email     string    `gorm:"size:255;not null" json:"email"`
	OrgID     uuid.UUID `gorm:"type:uuid;not null" json:"org_id"`
	RoleID    uuid.UUID `gorm:"type:uuid;not null" json:"role_id"`
	TokenHash string    `gorm:"size:255;not null" json:"-"`
	ExpiresAt time.Time `gorm:"not null" json:"expires_at"`
	Status    string    `gorm:"size:50;not null;default:'pending'" json:"status"`
	CreatedAt time.Time `gorm:"not null;default:now()" json:"created_at"`
}

// System Role Names
const (
	RoleOwner   = "owner"
	RoleAdmin   = "admin"
	RoleManager = "manager"
	RoleSales   = "sales_rep"
	RoleViewer  = "viewer"
)

// Membership Status
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
	StatusInvited   = "invited"
	StatusDeleted   = "deleted"
)
