package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ============================================================
// User Groups
// ============================================================
//
// A UserGroup is a named, org-scoped set of members. Built as a general entity
// (reusable for record sharing later) but first wired to granular report
// sharing: a report can be shared with a group, granting every current member
// the share's access level.

type UserGroup struct {
	ID          uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID       uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	Name        string         `gorm:"size:120;not null" json:"name"`
	Description string         `gorm:"not null;default:''" json:"description"`
	CreatedBy   *uuid.UUID     `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

func (UserGroup) TableName() string { return "user_groups" }

type UserGroupMember struct {
	GroupID   uuid.UUID `gorm:"type:uuid;primaryKey" json:"group_id"`
	UserID    uuid.UUID `gorm:"type:uuid;primaryKey" json:"user_id"`
	OrgID     uuid.UUID `gorm:"type:uuid;not null" json:"org_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (UserGroupMember) TableName() string { return "user_group_members" }

// UserGroupView is one group rendered for the admin UI with its members
// resolved (id + display name) and count.
type UserGroupView struct {
	ID          uuid.UUID         `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	MemberCount int               `json:"member_count"`
	Members     []GroupMemberInfo `json:"members"`
	CreatedAt   time.Time         `json:"created_at"`
}

type GroupMemberInfo struct {
	UserID uuid.UUID `json:"user_id"`
	Name   string    `json:"name"`
	Email  string    `json:"email"`
}

type UserGroupInput struct {
	Name        string `json:"name" binding:"required,min=1,max=120"`
	Description string `json:"description"`
}

// ============================================================
// Ports
// ============================================================

// UserGroupRepository persists groups and their membership.
type UserGroupRepository interface {
	Create(ctx context.Context, g *UserGroup) error
	// List returns the org's groups (with member counts + names) for the admin
	// grid and the share picker.
	List(ctx context.Context, orgID uuid.UUID) ([]UserGroupView, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*UserGroup, error)
	Update(ctx context.Context, g *UserGroup) error
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) error
	// AddMember/RemoveMember manage membership; AddMember is idempotent.
	AddMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error
	RemoveMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error
	// GroupIDsForUser returns the ids of every group a user belongs to — the
	// input to group-based share resolution.
	GroupIDsForUser(ctx context.Context, orgID, userID uuid.UUID) ([]uuid.UUID, error)
	// ExistsInOrg reports whether the group id belongs to the org (validation
	// before sharing a report or record to it).
	ExistsInOrg(ctx context.Context, orgID, id uuid.UUID) (bool, error)
	// TeammateIDs returns the active members who share at least one group with the
	// user — the relation the 'team' data scope filters on (U6.1). Includes the user.
	TeammateIDs(ctx context.Context, orgID, userID uuid.UUID) ([]uuid.UUID, error)
}

// UserGroupUseCase is the admin-facing surface (groups.manage gated for
// mutations; list readable by any member for the share picker).
type UserGroupUseCase interface {
	List(ctx context.Context, orgID uuid.UUID) ([]UserGroupView, error)
	Create(ctx context.Context, orgID, actorID uuid.UUID, in UserGroupInput) (*UserGroup, error)
	Update(ctx context.Context, orgID, id uuid.UUID, in UserGroupInput) (*UserGroup, error)
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	AddMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error
	RemoveMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error
}
