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
	// IsOwner marks the god-mode role. Authorization must read THIS flag (via
	// IsOwnerRole), never compare the name string: names are tenant-editable
	// vocabulary, and the DB backs the flag with a one-owner-per-org unique index
	// plus a shadow CHECK so a tenant can never mint a second owner (P10 P0).
	IsOwner bool `gorm:"not null;default:false" json:"is_owner"`
	// TemplateKey records which built-in template a role descends from (system
	// roles: their own name; a custom role: the system template its lineage
	// resolves to). The roles_owner_lineage CHECK requires is_owner rows to carry
	// 'owner', so a materialized/custom row can never claim god-mode without owner
	// lineage. New-object OLS seeding (EnsureDefaults) reads it to give a custom
	// role its template's default access on objects added after the role (P6).
	TemplateKey *string `gorm:"size:40" json:"template_key,omitempty"`
	// Description is the admin-authored blurb shown in the roles UI / pickers (P6).
	Description string `gorm:"type:text;not null;default:''" json:"description"`
	// SeededFromRoleID records the concrete role this one was cloned/seeded from
	// (the new-role wizard's "start from a template or duplicate" — P6). Kept for
	// lineage display and new-object seeding; the FK is ON DELETE SET NULL so
	// deleting the source doesn't cascade.
	SeededFromRoleID *uuid.UUID `gorm:"type:uuid" json:"seeded_from_role_id,omitempty"`
	// DataScope is the row visibility of the role: 'own' (owned + shared records)
	// or 'all' (the whole org). Generalizes the hardcoded sales_rep check (P3, D6).
	DataScope string    `gorm:"size:10;not null;default:'all'" json:"data_scope"`
	CreatedAt time.Time `gorm:"not null;default:now()" json:"created_at"`

	Permissions []RolePermission `gorm:"foreignKey:RoleID" json:"permissions,omitempty"`
}

// IsOwnerRole reports whether r is the god-mode owner role. The is_owner column
// is authoritative; the (is_system, name) pair is honored as a fallback for rows
// created before the column's boot-guard backfill ran (e.g. an old dev DB).
func IsOwnerRole(r *Role) bool {
	return r != nil && (r.IsOwner || (r.IsSystem && r.Name == RoleOwner))
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

// Data scopes (roles.data_scope) — a role's row visibility, narrowest first.
//
//	own  — records the user owns (plus anything shared to them)
//	team — the above, plus records owned by anyone who shares a group with them
//	all  — every record in the workspace
//
// 'team' (U6.1) is the missing middle: before it, a manager who should see their
// reports' pipeline had to be given the whole workspace.
const (
	DataScopeOwn  = "own"
	DataScopeTeam = "team"
	DataScopeAll  = "all"
)

// IsValidDataScope reports whether s is a known scope.
func IsValidDataScope(s string) bool {
	return s == DataScopeOwn || s == DataScopeTeam || s == DataScopeAll
}

// NormalizeDataScope maps a stored/cached scope value onto the known vocabulary,
// defaulting an unknown value to the NARROWEST scope.
//
// This exists because the pre-U6 code wrote the coercion the other way round —
// `if scope != "own" { scope = "all" }` — in the middleware, the token minter and
// the session-cache parser. That shape silently promotes any value it does not
// recognize to full workspace access, so the day a third scope shipped, every
// team-scoped user would have been handed the entire org on the first cache hit.
// Widen the vocabulary here and the unknown case still fails closed.
func NormalizeDataScope(s string) string {
	if IsValidDataScope(s) {
		return s
	}
	return DataScopeOwn
}

// Record shares (the RecordShare model, its views, and the share ports) moved to
// record_share.go in U6.2 when they grew user/role/group targets — see share.go
// for the vocabulary both record and report sharing now speak.

type OrgInvitation struct {
	ID        uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	Email     string    `gorm:"size:255;not null" json:"email"`
	OrgID     uuid.UUID `gorm:"type:uuid;not null" json:"org_id"`
	RoleID    uuid.UUID `gorm:"type:uuid;not null" json:"role_id"`
	TokenHash string    `gorm:"size:255;not null" json:"-"`
	ExpiresAt time.Time `gorm:"not null" json:"expires_at"`
	Status    string    `gorm:"size:50;not null;default:'pending'" json:"status"`
	// ResentAt / RevokedAt stamp the invite-lifecycle actions added in P2: a
	// resend re-mints the token and bumps ResentAt; a revoke sets RevokedAt and
	// flips Status to 'revoked' so the pending token can no longer be accepted.
	ResentAt  *time.Time `json:"resent_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `gorm:"not null;default:now()" json:"created_at"`
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
	// CapAnalyticsView gates forecast/analytics — the AI forecast tool and the
	// pipeline forecast surface (P7). Seeded to admin+manager; an 'own'-scoped role
	// (sales_rep) is intentionally without it.
	CapAnalyticsView = "analytics.view"
	// CapOrgSettings will gate the workspace-general settings surface (rename,
	// branding, org defaults) when it ships (plan U4). billing.manage was DELETED
	// from the vocabulary (U0.3): it gated zero routes, so the roles grid showed
	// admins a sensitive toggle that did nothing. Re-add it together with a real
	// billing surface, never before.
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
	// CapIntegrationsManage gates lead-source configuration: minting the capture
	// keys third parties authenticate with, and choosing where their leads land.
	//
	// It is deliberately NOT a write power. Captured leads are written by a trusted
	// callerless actor, so object-level security never runs at ingest time — which
	// would make this capability an org-wide write primitive if nothing else
	// checked. The integrations handler therefore re-authorizes the CONFIGURING
	// admin's own create+edit permission on a source's target object, with their
	// real caller, whenever the target is set or changed.
	CapIntegrationsManage = "integrations.manage"
)

// AllCapabilities is the canonical list, used for validation and the roles UI.
var AllCapabilities = []string{
	CapMembersInvite, CapMembersManage, CapRolesManage, CapObjectsManage,
	CapWorkflowsManage, CapWorkflowsRunAny, CapAuditView, CapAnalyticsView,
	CapOrgSettings, CapDataExport, CapPipelineManage, CapKnowledgeManage, CapRecordsWrite,
	CapReportsManage, CapGroupsManage, CapIntegrationsManage,
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

// CapabilityInfo is the display metadata for one capability (P6): the plain-
// language label + description a non-technical admin sees, the group it renders
// under, and whether it warrants a ⚠ "sensitive" chip. The vocabulary
// (AllCapabilities) is the compile-time enforcement contract; this is the human
// layer over it, served by GET /api/roles/catalog.
type CapabilityInfo struct {
	Code        string `json:"code"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Group       string `json:"group"`
	Sensitive   bool   `json:"sensitive"`
}

// Capability groups, in display order (plan §3.2).
const (
	CapGroupPeople     = "People"
	CapGroupSetup      = "Permissions & setup"
	CapGroupRecords    = "Working with records"
	CapGroupAutomation = "Automation & AI"
	CapGroupOversight  = "Oversight"
)

// CapabilityGroups is the display order of the groups the roles UI renders.
var CapabilityGroups = []string{
	CapGroupPeople, CapGroupSetup, CapGroupRecords, CapGroupAutomation, CapGroupOversight,
}

// CapabilityCatalog is the human-facing description of every capability in
// AllCapabilities (P6). Sensitive ⚠ chips flag the powers that are effectively
// admin-equivalent or high-blast-radius: role/member management, workflow
// authoring/execution (org-wide write + email + outbound HTTP until the P8 actor
// model lands), org settings, bulk export, and billing. A test asserts this list
// stays 1:1 with AllCapabilities so a new capability can't ship without copy.
var CapabilityCatalog = []CapabilityInfo{
	{CapMembersInvite, "Invite members", "Send workspace invitations to new members.", CapGroupPeople, false},
	{CapMembersManage, "Manage members", "Change member roles, suspend or remove members, and transfer ownership.", CapGroupPeople, true},
	{CapGroupsManage, "Manage user groups", "Create user groups and edit their membership.", CapGroupPeople, false},
	{CapRolesManage, "Manage roles & permissions", "Create custom roles and edit the object/field permission grids. Effectively admin-equivalent.", CapGroupSetup, true},
	{CapObjectsManage, "Manage objects & fields", "Create and edit objects, fields, and detail layouts.", CapGroupSetup, false},
	{CapPipelineManage, "Manage pipeline stages", "Create and edit the deal pipeline stages.", CapGroupSetup, false},
	{CapOrgSettings, "Manage workspace settings", "Rename the workspace, set its defaults (currency, locale, timezone), and delete it.", CapGroupSetup, true},
	{CapRecordsWrite, "Edit collaboration records", "Create and edit tasks, activities, voice notes, tags, and record links.", CapGroupRecords, false},
	{CapWorkflowsManage, "Manage workflows", "Create and edit automation workflows (org-wide write + email + outbound HTTP).", CapGroupAutomation, true},
	{CapIntegrationsManage, "Manage integrations", "Connect lead sources and mint the capture keys third parties use to send leads in.", CapGroupAutomation, true},
	{CapWorkflowsRunAny, "Run any workflow", "Manually run any workflow, not just the ones you created.", CapGroupAutomation, true},
	{CapKnowledgeManage, "Manage knowledge base", "Edit the knowledge base that powers AI answers.", CapGroupAutomation, false},
	{CapAuditView, "View audit log", "See the who-did-what admin and security audit trail.", CapGroupOversight, false},
	{CapAnalyticsView, "View analytics & forecasts", "See pipeline forecasts and analytics, including from the AI assistant.", CapGroupOversight, false},
	{CapReportsManage, "Manage all reports", "Edit and delete reports created by other people.", CapGroupOversight, false},
	{CapDataExport, "Export report results", "Download report results as CSV files.", CapGroupOversight, true},
}

// CapabilityLabel returns the human-facing label for a capability code (from
// CapabilityCatalog), falling back to the raw code for anything unknown — so
// user-facing permission errors can name the capability the way the roles UI
// does ("Manage roles & permissions"), never the internal code (U3.5).
func CapabilityLabel(code string) string {
	for _, ci := range CapabilityCatalog {
		if ci.Code == code {
			return ci.Label
		}
	}
	return code
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
		CapWorkflowsManage, CapWorkflowsRunAny, CapAuditView, CapAnalyticsView, CapOrgSettings, CapDataExport,
		CapPipelineManage, CapKnowledgeManage, CapRecordsWrite, CapReportsManage, CapGroupsManage,
	},
	RoleManager: {
		CapMembersInvite, CapWorkflowsManage, CapWorkflowsRunAny, CapAuditView, CapAnalyticsView, CapDataExport,
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
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	IsSystem     bool       `json:"is_system"`
	IsOwner      bool       `json:"is_owner"`
	DataScope    string     `json:"data_scope"`
	TemplateKey  *string    `json:"template_key,omitempty"`
	SeededFrom   *uuid.UUID `json:"seeded_from_role_id,omitempty"`
	Capabilities []string   `json:"capabilities"`
	// MemberCount is how many active members hold this role (drives "in use").
	MemberCount int64 `json:"member_count"`
}

// RoleOption is the minimal role identity every member may read to populate role
// pickers (P6): the Share dialog, member/invite dropdowns. It deliberately omits
// the capability set (that stays behind roles.manage on the full List payload) —
// a picker needs only the name/description and the flags that gate selection
// (is_owner ⇒ rendered disabled, "transfer ownership instead").
type RoleOption struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	IsSystem    bool      `json:"is_system"`
	IsOwner     bool      `json:"is_owner"`
	DataScope   string    `json:"data_scope"`
}

// CreateRoleInput creates a custom role, optionally cloning another role's OLS,
// FLS, capabilities, and data_scope as the starting point (plan §3.3). CloneFromID
// is the wizard's "start from a template or duplicate an existing role"; the
// source is recorded on the new role's SeededFromRoleID/TemplateKey lineage so
// objects added later inherit the template's access (P6).
type CreateRoleInput struct {
	Name         string     `json:"name" binding:"required,min=2,max=60"`
	Description  string     `json:"description"`
	CloneFromID  *uuid.UUID `json:"clone_from_id"`
	DataScope    string     `json:"data_scope"`
	Capabilities []string   `json:"capabilities"`
}

// UpdateRoleInput edits a custom role's name, description, and/or row scope.
// Pointers so an omitted field is left unchanged.
type UpdateRoleInput struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	DataScope   *string `json:"data_scope"`
}

// DuplicateRoleInput clones a role (system or custom) into a new org-scoped custom
// role the admin can then tune — the in-place-edit substitute for the immutable
// system templates (plan §3.8, §5 decision #1). ReassignMembers moves every
// active member of the source onto the copy in the same operation.
type DuplicateRoleInput struct {
	Name            string `json:"name" binding:"required,min=2,max=60"`
	ReassignMembers bool   `json:"reassign_members"`
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
	// ListOptions returns the org's roles as minimal RoleOptions (no capabilities)
	// for the any-member role pickers (P6).
	ListOptions(ctx context.Context, orgID uuid.UUID) ([]RoleOption, error)
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
	// ListMemberIDs returns the user ids of the org members holding the role
	// (any status), so a role edit can evict their cached sessions.
	ListMemberIDs(ctx context.Context, orgID, roleID uuid.UUID) ([]uuid.UUID, error)
	// ReassignMembers moves every org_users row from fromRoleID to toRoleID within
	// the org in one atomic statement and returns the user ids actually moved — the
	// core of delete-with-reassign and duplicate-with-reassign (P6). Returning the
	// moved set (rather than a snapshot taken before the move) is what lets the
	// caller evict exactly those members' sessions without a capture-then-move race.
	ReassignMembers(ctx context.Context, orgID, fromRoleID, toRoleID uuid.UUID) ([]uuid.UUID, error)
}

// RoleUseCase is the admin-facing custom-role surface. Most methods are
// roles.manage-gated; Options is any-member (feeds the pickers, P6).
type RoleUseCase interface {
	List(ctx context.Context, orgID uuid.UUID) ([]RoleDetail, error)
	// GetDetail returns one role's identity + capabilities + active member count
	// for the role detail page (U3). The owner role synthesizes AllCapabilities
	// (it bypasses capability checks, so its table rows — none — aren't the truth).
	// 404 AppError when the role isn't visible to the org.
	GetDetail(ctx context.Context, orgID, id uuid.UUID) (*RoleDetail, error)
	// Options returns the minimal role list any member may read for pickers (P6).
	Options(ctx context.Context, orgID uuid.UUID) ([]RoleOption, error)
	Create(ctx context.Context, orgID uuid.UUID, in CreateRoleInput) (*Role, error)
	// Duplicate clones a role into a new custom role, optionally reassigning the
	// source's members onto the copy (P6).
	Duplicate(ctx context.Context, orgID, id uuid.UUID, in DuplicateRoleInput) (*Role, error)
	Update(ctx context.Context, orgID, id uuid.UUID, in UpdateRoleInput) error
	// Delete removes a custom role. When members still hold it, reassignTo must name
	// the role to move them onto (transactional); a nil reassignTo with members
	// present is a 409 (P6 delete-with-reassign).
	Delete(ctx context.Context, orgID, id uuid.UUID, reassignTo *uuid.UUID) error
	GetCapabilities(ctx context.Context, orgID, id uuid.UUID) ([]string, error)
	SetCapabilities(ctx context.Context, orgID, id uuid.UUID, in SetCapabilitiesInput) error
}
