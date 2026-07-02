package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ============================================================
// Object-Level Security + audit (plan §7, P5a)
// ============================================================

// RecordAction is one of the four CRUD verbs OLS gates per object.
type RecordAction string

const (
	ActionRead   RecordAction = "read"
	ActionCreate RecordAction = "create"
	ActionEdit   RecordAction = "edit"
	ActionDelete RecordAction = "delete"
)

// ObjectPermission is one (role × object) access row. It is keyed by object_slug
// rather than an object_defs FK so it covers custom objects too (which aren't in
// object_defs until P7) — see migration 000017 for the full rationale. Absence of
// a row means no access (default-deny); a row with all-false bits is an explicit
// lock-down that survives the idempotent default seed.
type ObjectPermission struct {
	OrgID      uuid.UUID `gorm:"type:uuid;primaryKey" json:"org_id"`
	RoleID     uuid.UUID `gorm:"type:uuid;primaryKey" json:"role_id"`
	ObjectSlug string    `gorm:"size:100;primaryKey" json:"object_slug"`
	CanRead    bool      `gorm:"not null;default:false" json:"can_read"`
	CanCreate  bool      `gorm:"not null;default:false" json:"can_create"`
	CanEdit    bool      `gorm:"not null;default:false" json:"can_edit"`
	CanDelete  bool      `gorm:"not null;default:false" json:"can_delete"`
	CreatedAt  time.Time `gorm:"not null;default:now()" json:"created_at"`
	UpdatedAt  time.Time `gorm:"not null;default:now()" json:"updated_at"`
}

func (ObjectPermission) TableName() string { return "object_permissions" }

// ObjectAccess is the per-(role, object) access bits, decoupled from the storage
// row so the OLS cache and the grid DTO can share one shape.
type ObjectAccess struct {
	Read   bool `json:"read"`
	Create bool `json:"create"`
	Edit   bool `json:"edit"`
	Delete bool `json:"delete"`
}

// Allows reports whether this access grants the given action.
func (a ObjectAccess) Allows(action RecordAction) bool {
	switch action {
	case ActionRead:
		return a.Read
	case ActionCreate:
		return a.Create
	case ActionEdit:
		return a.Edit
	case ActionDelete:
		return a.Delete
	}
	return false
}

// ObjectAudit is one append-only record of a write routed through RecordService.
type ObjectAudit struct {
	ID         uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID      uuid.UUID  `gorm:"type:uuid;not null" json:"org_id"`
	ObjectSlug string     `gorm:"size:100;not null" json:"object_slug"`
	RecordID   uuid.UUID  `gorm:"type:uuid;not null" json:"record_id"`
	ActorID    *uuid.UUID `gorm:"type:uuid" json:"actor_id,omitempty"`
	Action     string     `gorm:"size:20;not null" json:"action"`
	Changes    JSON       `gorm:"type:jsonb;default:'{}'" json:"changes"`
	CreatedAt  time.Time  `gorm:"not null;default:now()" json:"created_at"`
}

func (ObjectAudit) TableName() string { return "object_audit" }

// AuditEntry is the input RecordService hands to the authorizer to record one
// write. Changes is a field-level diff: { key: {"old": …, "new": …} }.
type AuditEntry struct {
	OrgID      uuid.UUID
	ActorID    uuid.UUID // uuid.Nil when unknown (trusted/internal call)
	ObjectSlug string
	RecordID   uuid.UUID
	Action     RecordAction
	Changes    map[string]interface{}
}

// RecordAuthorizer is the narrow security port RecordService depends on. Keeping
// it small (rather than depending on the whole PermissionUseCase) means the
// record-service unit tests can wire a tiny fake, and OLS/audit stay the only two
// concerns RecordService knows about.
type RecordAuthorizer interface {
	// Authorize returns nil when the context's caller may perform action on slug,
	// or a 403 AppError otherwise. A context with no caller is a trusted
	// in-process call and is always allowed; the "owner" role bypasses OLS.
	Authorize(ctx context.Context, orgID uuid.UUID, slug string, action RecordAction) error
	// Audit records one write. Best-effort: a failure is logged, never surfaced
	// to the caller, so an audit hiccup can't roll back a successful write.
	Audit(ctx context.Context, e AuditEntry)
	// FieldMask returns the context caller's Field-Level Security restrictions for
	// an object (P5b). The empty mask means "no restriction" — returned for a
	// trusted in-process call, the owner role, or any object/role with no
	// field_permissions rows — so FLS stays free until a field is restricted.
	FieldMask(ctx context.Context, orgID uuid.UUID, slug string) FieldMask
}

// ============================================================
// Grid DTOs (the admin role × object matrix)
// ============================================================

// PermissionGrid is everything the admin grid needs in one payload: the objects
// (rows), the roles (columns), and the current access matrix. The frontend joins
// them by (role_id, object_slug).
type PermissionGrid struct {
	Objects []PermObjectInfo       `json:"objects"`
	Roles   []PermRoleInfo         `json:"roles"`
	Matrix  []PermissionMatrixCell `json:"matrix"`
}

type PermObjectInfo struct {
	Slug     string `json:"slug"`
	Label    string `json:"label"`
	Icon     string `json:"icon"`
	IsSystem bool   `json:"is_system"`
}

type PermRoleInfo struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	IsSystem bool      `json:"is_system"`
	// IsOwner roles bypass OLS entirely; the grid shows their row as locked-on.
	IsOwner bool `json:"is_owner"`
}

type PermissionMatrixCell struct {
	RoleID     uuid.UUID `json:"role_id"`
	ObjectSlug string    `json:"object_slug"`
	ObjectAccess
}

// SetPermissionInput upserts one (role, object) cell.
type SetPermissionInput struct {
	RoleID     uuid.UUID `json:"role_id" binding:"required"`
	ObjectSlug string    `json:"object_slug" binding:"required"`
	CanRead    bool      `json:"can_read"`
	CanCreate  bool      `json:"can_create"`
	CanEdit    bool      `json:"can_edit"`
	CanDelete  bool      `json:"can_delete"`
}

// AuditView is one audit row rendered for the per-record history endpoint, with
// the actor's display name resolved.
type AuditView struct {
	ID         uuid.UUID              `json:"id"`
	Action     string                 `json:"action"`
	ActorID    *uuid.UUID             `json:"actor_id,omitempty"`
	ActorName  string                 `json:"actor_name"`
	Changes    map[string]interface{} `json:"changes"`
	CreatedAt  time.Time              `json:"created_at"`
	ObjectSlug string                 `json:"object_slug"`
	RecordID   uuid.UUID              `json:"record_id"`
}

// ============================================================
// Ports
// ============================================================

// PermissionRepository persists OLS rows and the audit trail, and seeds the
// non-breaking defaults. It "knows" which objects exist (system constants +
// custom_object_defs) so the seed can cover every current object without the hot
// OLS path calling back into the registry usecase.
type PermissionRepository interface {
	// EnsureDefaults idempotently seeds the default access matrix (mirroring the
	// legacy RequireRole gates) for the system roles, for every current object
	// that has ZERO permission rows. Advisory-locked per org; safe to call on the
	// cache-miss load path.
	EnsureDefaults(ctx context.Context, orgID uuid.UUID) error
	// LoadOrgAccess returns roleName → objectSlug → access for an org, joining
	// object_permissions to roles. Populates the OLS cache in one query.
	LoadOrgAccess(ctx context.Context, orgID uuid.UUID) (map[string]map[string]ObjectAccess, error)
	// ListRoles returns the org's roles (system + org-scoped custom).
	ListRoles(ctx context.Context, orgID uuid.UUID) ([]Role, error)
	// LoadOrgCapabilities returns roleName → capabilityCode → true for an org,
	// joining role_permissions to roles (system roles + this org's custom roles).
	// Populates the capability half of the cache in one query (P3, D5).
	LoadOrgCapabilities(ctx context.Context, orgID uuid.UUID) (map[string]map[string]bool, error)
	// ListPermissions returns the raw rows for the grid.
	ListPermissions(ctx context.Context, orgID uuid.UUID) ([]ObjectPermission, error)
	// UpsertPermission writes one (role, object) cell (insert or update). Rows are
	// never deleted, so an all-false cell is a durable explicit denial.
	UpsertPermission(ctx context.Context, p ObjectPermission) error
	// WriteAudit appends one audit row.
	WriteAudit(ctx context.Context, a *ObjectAudit) error
	// ListAudit returns a record's audit rows newest-first, with actor names.
	ListAudit(ctx context.Context, orgID uuid.UUID, slug string, recordID uuid.UUID, limit int) ([]AuditView, error)

	// --- Field-Level Security (P5b) ---

	// LoadOrgFieldAccess returns roleName → objectSlug → fieldKey → level for an
	// org, joining field_permissions to roles. Populates the FLS half of the cache
	// in one query; an org with no restrictions returns an empty map (zero overhead).
	LoadOrgFieldAccess(ctx context.Context, orgID uuid.UUID) (map[string]map[string]map[string]string, error)
	// ListFieldPermissions returns the raw restriction rows for one object (the
	// admin field-security grid). Only genuine restrictions exist as rows.
	ListFieldPermissions(ctx context.Context, orgID uuid.UUID, slug string) ([]FieldPermission, error)
	// UpsertFieldPermission writes one (role, field) restriction (insert or update).
	UpsertFieldPermission(ctx context.Context, p FieldPermission) error
	// DeleteFieldPermission removes one (role, field) restriction, returning the
	// field to its default (fully accessible) — used when a level is set to 'edit'.
	DeleteFieldPermission(ctx context.Context, orgID, roleID uuid.UUID, slug, fieldKey string) error
}

// PermissionUseCase is the admin-facing surface for the role × object grid and
// the per-record audit view. It embeds RecordAuthorizer (the narrower port
// RecordService enforces through) because one concrete type implements both — so
// the constructor can return this single interface and the same value can be
// handed to RecordService as a RecordAuthorizer.
// CapabilityChecker answers "may this caller perform this admin capability?" It
// is the capability counterpart to RecordAuthorizer.Authorize (which gates object
// CRUD). Kept as its own port so RequireCapability middleware can depend on the
// narrow surface. PermissionUseCase implements it.
type CapabilityChecker interface {
	// HasCapability returns nil when the context caller holds capability, or a 403
	// AppError otherwise. A context with no caller is a trusted in-process call and
	// is allowed; the owner role bypasses all capability checks (god-mode).
	HasCapability(ctx context.Context, orgID uuid.UUID, capability string) error
	// CallerCapabilities returns the context caller's effective capability codes for
	// the org (all of them for owner). Drives permission-aware UI. Empty when there
	// is no caller.
	CallerCapabilities(ctx context.Context, orgID uuid.UUID) []string
}

type PermissionUseCase interface {
	RecordAuthorizer
	CapabilityChecker
	GetGrid(ctx context.Context, orgID uuid.UUID) (*PermissionGrid, error)
	SetPermission(ctx context.Context, orgID uuid.UUID, in SetPermissionInput) error
	ListRecordAudit(ctx context.Context, orgID uuid.UUID, slug string, recordID uuid.UUID) ([]AuditView, error)
	// GetFieldGrid returns the field × role level matrix for one object — the admin
	// Field-Level Security UI (P5b).
	GetFieldGrid(ctx context.Context, orgID uuid.UUID, slug string) (*FieldPermissionGrid, error)
	// SetFieldPermission sets one (role, field) level; level 'edit' clears the
	// restriction. Busts the cache so the change applies on the next request.
	SetFieldPermission(ctx context.Context, orgID uuid.UUID, in SetFieldPermissionInput) error
	// Invalidate drops the cached OLS + FLS maps for an org (called when permissions
	// or the object set change, so live edits apply without a restart).
	Invalidate(orgID uuid.UUID)
	// EnsureSeeded idempotently seeds the org's default OLS grid for any object
	// that has no rows yet. Called before cloning a role, so the clone source has
	// its default grid materialized to copy from.
	EnsureSeeded(ctx context.Context, orgID uuid.UUID) error
}
