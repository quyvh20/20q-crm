package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Role struct {
	ID        uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID     *uuid.UUID `gorm:"type:uuid" json:"org_id,omitempty"`
	Name      string     `gorm:"size:255;not null" json:"name"`
	IsSystem  bool       `gorm:"not null;default:false" json:"is_system"`
	// DataScope is the row visibility of the role: 'own' (owned + shared records)
	// or 'all' (the whole org). Generalizes the hardcoded sales_rep check (P3, D6).
	DataScope string    `gorm:"size:10;not null;default:'all'" json:"data_scope"`
	CreatedAt time.Time `gorm:"not null;default:now()" json:"created_at"`

	Permissions []RolePermission `gorm:"foreignKey:RoleID" json:"permissions,omitempty"`
}

// RolePermission is one (role, capability) grant — the system-capability store
// (plan D5). PermissionCode holds a capability code (see Cap* constants), not
// object CRUD. System-role rows have OrgID nil; custom-role rows are org-scoped
// so they cascade with the org.
type RolePermission struct {
	RoleID         uuid.UUID  `gorm:"type:uuid;primaryKey" json:"role_id"`
	PermissionCode string     `gorm:"size:255;primaryKey" json:"permission_code"`
	OrgID          *uuid.UUID `gorm:"type:uuid" json:"org_id,omitempty"`
}

func (RolePermission) TableName() string { return "role_permissions" }

// Data scopes (roles.data_scope).
const (
	DataScopeOwn = "own"
	DataScopeAll = "all"
)

type RecordShare struct {
	ID             uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	RecordType     string     `gorm:"size:50;not null" json:"record_type"`
	RecordID       uuid.UUID  `gorm:"type:uuid;not null" json:"record_id"`
	GranteeUserID  uuid.UUID  `gorm:"type:uuid;not null" json:"grantee_user_id"`
	PermissionLevel string    `gorm:"size:50;not null;default:'read'" json:"permission_level"`
	CreatedBy      *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt      time.Time  `gorm:"not null;default:now()" json:"created_at"`
}

func (RecordShare) TableName() string { return "record_shares" }

// ShareView is one record share rendered for the record-page share list, with the
// grantee's display name resolved.
type ShareView struct {
	ID              uuid.UUID `json:"id"`
	GranteeUserID   uuid.UUID `json:"grantee_user_id"`
	GranteeName     string    `json:"grantee_name"`
	PermissionLevel string    `json:"permission_level"`
	CreatedAt       time.Time `json:"created_at"`
}

// ShareRecordInput grants a record to a user (the escape hatch for 'own'-scoped
// roles, I2). PermissionLevel defaults to 'read'.
type ShareRecordInput struct {
	GranteeUserID   uuid.UUID `json:"grantee_user_id" binding:"required"`
	PermissionLevel string    `json:"permission_level"`
}

// RecordShareRepository persists per-record grants (record_shares).
type RecordShareRepository interface {
	Create(ctx context.Context, s *RecordShare) error
	// DeleteByID removes a share by id, scoped to (record_type, record_id) so a
	// caller can only revoke a share on the record they addressed. Returns rows
	// affected.
	DeleteByID(ctx context.Context, id uuid.UUID, recordType string, recordID uuid.UUID) (int64, error)
	// ListByRecord returns a record's shares with grantee display names resolved.
	ListByRecord(ctx context.Context, recordType string, recordID uuid.UUID) ([]ShareView, error)
	// ExistsForGrantee reports whether the record is already shared with the user
	// (to keep grants idempotent).
	ExistsForGrantee(ctx context.Context, recordType string, recordID, granteeUserID uuid.UUID) (bool, error)
}

// ShareUseCase creates/revokes/lists record shares (P3, I2). Visibility of the
// record under the caller's own data scope is the ownership gate: a caller can
// only share a record they can see, so an 'own'-scoped role shares only its own
// records while an 'all'-scoped role can share any.
type ShareUseCase interface {
	Share(ctx context.Context, orgID, actorID uuid.UUID, slug string, recordID uuid.UUID, in ShareRecordInput) (*RecordShare, error)
	Unshare(ctx context.Context, orgID, actorID uuid.UUID, slug string, recordID, shareID uuid.UUID) error
	List(ctx context.Context, orgID uuid.UUID, slug string, recordID uuid.UUID) ([]ShareView, error)
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

// System capabilities — the admin/workspace powers a role may hold (plan §3.4).
// Stored in role_permissions.permission_code and checked by RequireCapability.
// This is the fixed, documented vocabulary; a code not in this list is not a
// capability. Object CRUD is NOT here — that is Object-Level Security.
const (
	CapMembersInvite   = "members.invite"   // invite members
	CapMembersManage   = "members.manage"   // role change, suspend, remove, transfer
	CapRolesManage     = "roles.manage"     // custom roles + OLS/FLS grids
	CapObjectsManage   = "objects.manage"   // objects, fields, layouts
	CapWorkflowsManage = "workflows.manage" // create/edit workflows
	CapWorkflowsRunAny = "workflows.run_any"
	CapAuditView       = "audit.view" // view auth/admin + record audit
	CapBillingManage   = "billing.manage"
	CapOrgSettings     = "org.settings"     // org-level settings/templates
	CapDataExport      = "data.export"
	CapPipelineManage  = "pipeline.manage"  // create/edit pipeline stages
	CapKnowledgeManage = "knowledge.manage" // edit the knowledge base
	// CapRecordsWrite gates writes to the collaboration objects that have no OLS
	// grid of their own — tasks, activities, voice notes, tags, record links. It is
	// a capability (not OLS) only because those objects aren't in the registry; an
	// admin grants/revokes it per role like any other capability.
	CapRecordsWrite = "records.write"
	// CapReportsManage is an oversight power over OTHER people's reports (edit/
	// delete any org-shared report). Creating and running one's own reports needs
	// no capability — report DATA is gated per-viewer by OLS/FLS instead.
	CapReportsManage = "reports.manage"
	// CapGroupsManage gates creating/editing user groups and their membership.
	// Listing groups needs no capability (any member picks a group when sharing).
	CapGroupsManage = "groups.manage"
)

// AllCapabilities is the canonical list, used for validation and the roles UI.
var AllCapabilities = []string{
	CapMembersInvite, CapMembersManage, CapRolesManage, CapObjectsManage,
	CapWorkflowsManage, CapWorkflowsRunAny, CapAuditView, CapBillingManage,
	CapOrgSettings, CapDataExport, CapPipelineManage, CapKnowledgeManage, CapRecordsWrite,
	CapReportsManage, CapGroupsManage,
}

// IsCapability reports whether code is a recognized capability.
func IsCapability(code string) bool {
	for _, c := range AllCapabilities {
		if c == code {
			return true
		}
	}
	return false
}

// DefaultRoleCapabilities is the DEFAULT capability matrix seeded for the system
// roles. It is only a starting point — capabilities are data, and an admin can
// grant or revoke any of them per role (owner excepted). owner is intentionally
// absent: it bypasses capability checks entirely (god-mode), so an empty table
// can never lock the owner out.
//
// Defaults reproduce the pre-P3 route behavior: manager keeps pipeline + KB
// editing (pipeline.manage / knowledge.manage) and sales+ keep the collaboration
// writes (records.write); the admin-only powers stay admin-only.
var DefaultRoleCapabilities = map[string][]string{
	RoleAdmin: {
		CapMembersInvite, CapMembersManage, CapRolesManage, CapObjectsManage,
		CapWorkflowsManage, CapWorkflowsRunAny, CapAuditView, CapOrgSettings, CapDataExport,
		CapPipelineManage, CapKnowledgeManage, CapRecordsWrite, CapReportsManage, CapGroupsManage,
	},
	RoleManager: {
		CapMembersInvite, CapWorkflowsManage, CapWorkflowsRunAny, CapAuditView, CapDataExport,
		CapPipelineManage, CapKnowledgeManage, CapRecordsWrite, CapReportsManage, CapGroupsManage,
	},
	RoleSales:  {CapRecordsWrite},
	RoleViewer: {},
}

// DefaultRoleDataScope is the seeded row-scope for each system role (plan §3.4).
var DefaultRoleDataScope = map[string]string{
	RoleOwner:   DataScopeAll,
	RoleAdmin:   DataScopeAll,
	RoleManager: DataScopeAll,
	RoleSales:   DataScopeOwn,
	RoleViewer:  DataScopeAll,
}

// ============================================================
// Custom role management (P3)
// ============================================================

// RoleDetail is one role rendered for the admin Roles UI: identity plus its
// capabilities and row scope, so the manager can show/edit everything in one view.
type RoleDetail struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	IsSystem     bool      `json:"is_system"`
	IsOwner      bool      `json:"is_owner"`
	DataScope    string    `json:"data_scope"`
	Capabilities []string  `json:"capabilities"`
	// MemberCount is how many active members hold this role (drives "in use").
	MemberCount int64 `json:"member_count"`
}

// CreateRoleInput creates a custom role, optionally cloning another role's OLS,
// FLS, capabilities, and data_scope as the starting point (plan §3.3).
type CreateRoleInput struct {
	Name          string     `json:"name" binding:"required,min=2,max=60"`
	CloneFromID   *uuid.UUID `json:"clone_from_id"`
	DataScope     string     `json:"data_scope"`
	Capabilities  []string   `json:"capabilities"`
}

// UpdateRoleInput edits a custom role's name and/or row scope. Pointers so an
// omitted field is left unchanged.
type UpdateRoleInput struct {
	Name      *string `json:"name"`
	DataScope *string `json:"data_scope"`
}

// SetCapabilitiesInput replaces a role's full capability set (idempotent PUT).
type SetCapabilitiesInput struct {
	Capabilities []string `json:"capabilities"`
}

// RoleRepository persists roles and their capability rows, plus the clone copy of
// OLS/FLS grids. Custom roles are org-scoped; system roles are global (org NULL).
type RoleRepository interface {
	// ListDetailed returns the org's roles (system + custom) with capabilities,
	// data_scope, and active member counts, for the admin Roles UI.
	ListDetailed(ctx context.Context, orgID uuid.UUID) ([]RoleDetail, error)
	// GetInOrg returns a role usable by the org (its own custom role or a global
	// system role); nil if not found / not visible to the org.
	GetInOrg(ctx context.Context, orgID, id uuid.UUID) (*Role, error)
	// FindByNameInOrg returns a role with this name visible to the org (system or
	// custom), for uniqueness/shadowing checks. nil when none.
	FindByNameInOrg(ctx context.Context, orgID uuid.UUID, name string) (*Role, error)
	CreateRole(ctx context.Context, r *Role) error
	UpdateRole(ctx context.Context, r *Role) error
	// DeleteRole removes a custom role and its capability/OLS/FLS rows. Callers
	// must ensure it is unused first.
	DeleteRole(ctx context.Context, orgID, id uuid.UUID) error
	// GetCapabilities returns a role's capability codes.
	GetCapabilities(ctx context.Context, roleID uuid.UUID) ([]string, error)
	// SetCapabilities replaces a role's capability rows with the given set
	// (org-scoped for custom roles).
	SetCapabilities(ctx context.Context, orgID, roleID uuid.UUID, codes []string) error
	// ClonePermissions copies OLS (object_permissions) + FLS (field_permissions)
	// rows from srcRole to dstRole within the org, so a cloned role starts from the
	// source's data grids.
	ClonePermissions(ctx context.Context, orgID, srcRoleID, dstRoleID uuid.UUID) error
	// CountActiveMembers returns how many active org members hold the role.
	CountActiveMembers(ctx context.Context, orgID, roleID uuid.UUID) (int64, error)
}

// RoleUseCase is the admin-facing custom-role surface (roles.manage gated).
type RoleUseCase interface {
	List(ctx context.Context, orgID uuid.UUID) ([]RoleDetail, error)
	Create(ctx context.Context, orgID uuid.UUID, in CreateRoleInput) (*Role, error)
	Update(ctx context.Context, orgID, id uuid.UUID, in UpdateRoleInput) error
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	GetCapabilities(ctx context.Context, orgID, id uuid.UUID) ([]string, error)
	SetCapabilities(ctx context.Context, orgID, id uuid.UUID, in SetCapabilitiesInput) error
}
