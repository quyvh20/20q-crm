package domain

import (
	"context"
	"mime/multipart"
	"time"

	"github.com/google/uuid"
)

type RegisterInput struct {
	OrgName   string `json:"org_name" binding:"required,min=2"`
	OrgType   string `json:"org_type"`
	Email     string `json:"email" binding:"required,email"`
	Password  string `json:"password" binding:"required,min=8"`
	FirstName string `json:"first_name" binding:"required,min=1"`
	LastName  string `json:"last_name"`
}

type LoginInput struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
	// OrgID lets a programmatic client bind the session to a specific workspace at
	// login (R2, P3). Validated as an ACTIVE membership; a mismatch is a 403, never
	// a silent fallback. Browser logins omit it and let the selection chain decide.
	OrgID *uuid.UUID `json:"org_id"`
}

type RefreshInput struct {
	RefreshToken string     `json:"refresh_token" binding:"required"`
	OrgID        *uuid.UUID `json:"org_id"` // optional: refresh into a specific workspace
}

type SwitchWorkspaceInput struct {
	OrgID uuid.UUID `json:"org_id" binding:"required"`
	// SetDefault persists OrgID as the user's default workspace (R2, P3) so the
	// chooser isn't shown again — the "Make this my default" checkbox / switcher star.
	SetDefault bool `json:"set_default"`
}

type WorkspaceInfo struct {
	OrgID   uuid.UUID `json:"org_id"`
	OrgName string    `json:"org_name"`
	OrgType string    `json:"org_type"`
	Role    string    `json:"role"`
	Status  string    `json:"status"`
	// MemberCount is the org's active-member count, shown on the chooser cards (P3).
	MemberCount int `json:"member_count"`
}

type AuthResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	User         User            `json:"user"`
	Workspaces   []WorkspaceInfo `json:"workspaces"`
	// R2 org-selection contract (P3). ActiveOrgID is the org the access token is
	// bound to (uuid.Nil ⇒ the user has no active workspace: the zero-membership
	// dead-end). DefaultOrgID echoes the user's saved default (drives the switcher
	// star). NeedsChooser is true when there are multiple active workspaces and no
	// valid default resolved one — the SPA shows /choose-workspace.
	ActiveOrgID  uuid.UUID  `json:"active_org_id"`
	DefaultOrgID *uuid.UUID `json:"default_org_id,omitempty"`
	NeedsChooser bool       `json:"needs_chooser"`
}

// InviteMemberInput / UpdateMemberRoleInput are keyed by role_id, not the role
// NAME (P6): names are tenant-editable vocabulary, so a rename/duplicate must
// never re-point an assignment. The server resolves the id to a role usable by
// the org and enforces the owner + escalation guards.
type InviteMemberInput struct {
	Email  string    `json:"email" binding:"required,email"`
	RoleID uuid.UUID `json:"role_id" binding:"required"`
}

type UpdateMemberRoleInput struct {
	RoleID uuid.UUID `json:"role_id" binding:"required"`
}

type MemberInfo struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	FullName  string    `json:"full_name"`
	AvatarURL *string   `json:"avatar_url,omitempty"`
	// RoleID is the authoritative role identity; Role is its name for display (P6).
	RoleID uuid.UUID `json:"role_id"`
	Role   string    `json:"role"`
	Status string    `json:"status"`
}

// AcceptInviteInput carries the invite token and, for a brand-new non-OAuth
// invitee, the password + name they set on the accept page (P2). Password is
// optional: an existing account (or one that will use "Continue with Google")
// accepts without it. When present it is policy-checked before being stored.
type AcceptInviteInput struct {
	Token     string `json:"token" binding:"required"`
	Password  string `json:"password"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// InvitationInfo is one pending invitation rendered for the members panel (P2):
// enough to show who was invited, as what, and whether it can still be accepted.
type InvitationInfo struct {
	ID        uuid.UUID  `json:"id"`
	Email     string     `json:"email"`
	RoleID    uuid.UUID  `json:"role_id"`
	Role      string     `json:"role"`
	Status    string     `json:"status"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
	ResentAt  *time.Time `json:"resent_at,omitempty"`
}

type GoogleUserInfo struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"verified_email"`
	Name          string `json:"name"`
	GivenName     string `json:"given_name"`
	FamilyName    string `json:"family_name"`
	Picture       string `json:"picture"`
}

type AuthRepository interface {
	CreateOrganization(ctx context.Context, org *Organization) error
	CreateUser(ctx context.Context, user *User) error
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (*User, error)
	GetUserByGoogleID(ctx context.Context, googleID string) (*User, error)
	UpdateUser(ctx context.Context, user *User) error

	CreateRefreshToken(ctx context.Context, token *RefreshToken) error
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	// GetRefreshTokenByHashAny returns the row for a hash regardless of its
	// revoked/expiry state, so refresh can distinguish "never existed" from
	// "already rotated/revoked" (the reuse-detection signal). P2.
	GetRefreshTokenByHashAny(ctx context.Context, tokenHash string) (*RefreshToken, error)
	// RefreshTokenHasSuccessor reports whether any token was rotated FROM this one
	// (rotated_from = id). It separates genuine reuse/theft (a rotated token — one
	// with a successor — replayed) from a deliberately-ended session (logout,
	// revoke-device, sign-out-everywhere: revoked with no successor), so the latter
	// fails closed without nuking the user's other sessions or crying theft. P4.
	RefreshTokenHasSuccessor(ctx context.Context, tokenID uuid.UUID) (bool, error)
	RevokeRefreshToken(ctx context.Context, tokenID uuid.UUID) error
	RevokeAllUserRefreshTokens(ctx context.Context, userID uuid.UUID) error

	// IncrementUserTokenVersion bumps users.token_version, invalidating every
	// outstanding access token for the user. GetUserTokenVersion reads it for the
	// middleware's per-request check (P2).
	IncrementUserTokenVersion(ctx context.Context, userID uuid.UUID) error
	GetUserTokenVersion(ctx context.Context, userID uuid.UUID) (int, error)

	CreateOrgUser(ctx context.Context, ou *OrgUser) error
	GetOrgUser(ctx context.Context, userID, orgID uuid.UUID) (*OrgUser, error)
	ListOrgsByUserID(ctx context.Context, userID uuid.UUID) ([]OrgUser, error)
	// ListAllOrgMembershipsByUserID returns memberships in ALL statuses for DISPLAY
	// only (the chooser's suspended cards). Never use it for org selection — that
	// path stays active-only via ListOrgsByUserID.
	ListAllOrgMembershipsByUserID(ctx context.Context, userID uuid.UUID) ([]OrgUser, error)
	// SetUserDefaultOrg sets (or, with a nil orgID, clears) users.default_org_id —
	// the durable home-workspace memory the R2 selection chain honors (P3).
	SetUserDefaultOrg(ctx context.Context, userID uuid.UUID, orgID *uuid.UUID) error
	// CountActiveMembersByOrgs returns active-member counts for the given orgs in
	// ONE aggregate query (no N+1), for the chooser cards' member count (P3).
	CountActiveMembersByOrgs(ctx context.Context, orgIDs []uuid.UUID) (map[uuid.UUID]int, error)
	ListMembersByOrgID(ctx context.Context, orgID uuid.UUID) ([]OrgUser, error)
	UpdateOrgUserRole(ctx context.Context, userID, orgID, roleID uuid.UUID) error
	UpdateOrgUserStatus(ctx context.Context, userID, orgID uuid.UUID, status string) error
	DeleteOrgUser(ctx context.Context, userID, orgID uuid.UUID) error
	GetOrgUserByEmail(ctx context.Context, email string, orgID uuid.UUID) (*OrgUser, error)
	CountOrgUsersByRole(ctx context.Context, orgID, roleID uuid.UUID, status string) (int64, error)

	GetRoleByName(ctx context.Context, name string, orgID *uuid.UUID) (*Role, error)
	GetRoleByID(ctx context.Context, id uuid.UUID) (*Role, error)

	// TransferOrgOwnership atomically demotes the current owner to demoteRoleID
	// and promotes the target to ownerRoleID in one transaction, so the org can
	// never be observed with zero or two owners.
	TransferOrgOwnership(ctx context.Context, orgID, fromUserID, toUserID, ownerRoleID, demoteRoleID uuid.UUID) error

	CreateOrgInvitation(ctx context.Context, inv *OrgInvitation) error
	GetOrgInvitationByTokenHash(ctx context.Context, tokenHash string) (*OrgInvitation, error)
	GetOrgInvitationByID(ctx context.Context, id, orgID uuid.UUID) (*OrgInvitation, error)
	UpdateOrgInvitation(ctx context.Context, inv *OrgInvitation) error
	// ListPendingInvitations returns an org's still-actionable invitations
	// (pending, unexpired, not revoked), newest first, for the members panel (P2).
	ListPendingInvitations(ctx context.Context, orgID uuid.UUID) ([]OrgInvitation, error)
	// AcceptInvitation runs the whole accept in ONE transaction (P2): create the
	// invitee (createUser) or set a password on the existing account
	// (newPasswordHash), UPSERT the org_users membership to active (reinstating a
	// previously-removed row), and mark the invitation accepted — so a partial
	// accept can never strand a half-joined member. user must carry the final id.
	AcceptInvitation(ctx context.Context, inv *OrgInvitation, user *User, createUser bool, newPasswordHash *string) error

	// Account recovery (P1) — hashed, single-use, short-TTL tokens.
	CreatePasswordResetToken(ctx context.Context, t *PasswordResetToken) error
	GetPasswordResetTokenByHash(ctx context.Context, tokenHash string) (*PasswordResetToken, error)
	// GetLatestPasswordResetToken returns the user's most recently issued reset
	// token regardless of state (nil when none) — backs the per-email cooldown.
	GetLatestPasswordResetToken(ctx context.Context, userID uuid.UUID) (*PasswordResetToken, error)
	// VoidActivePasswordResetTokens marks every outstanding (unused) reset token
	// consumed so only the newest link works — issuing a fresh token must not
	// widen the interception window with older, still-live ones.
	VoidActivePasswordResetTokens(ctx context.Context, userID uuid.UUID) error
	// MarkPasswordResetTokenUsed atomically claims a token (UPDATE … WHERE id = ?
	// AND used_at IS NULL) and returns rows affected: 1 = this caller won the
	// single-use claim, 0 = already consumed (reject). This is the authoritative
	// single-use gate, so it is race-safe and its result must be checked.
	MarkPasswordResetTokenUsed(ctx context.Context, id uuid.UUID) (int64, error)
	// CountAdminResetTokensSince counts admin-initiated reset links (initiated_by
	// IS NOT NULL) minted for a user since `since` — the per-target daily cap that
	// stops any workspace admin from bombing a shared user with reset emails (P2).
	CountAdminResetTokensSince(ctx context.Context, userID uuid.UUID, since time.Time) (int64, error)

	CreateEmailVerificationToken(ctx context.Context, t *EmailVerificationToken) error
	GetEmailVerificationTokenByHash(ctx context.Context, tokenHash string) (*EmailVerificationToken, error)
	MarkEmailVerificationTokenUsed(ctx context.Context, id uuid.UUID) (int64, error)
	// GetLatestEmailVerificationToken returns a user's most recently issued
	// verification token (used for the resend cooldown). nil when none exists.
	GetLatestEmailVerificationToken(ctx context.Context, userID uuid.UUID) (*EmailVerificationToken, error)

	// WriteAuthEvent appends one auth/admin/security event (P0). Best-effort.
	WriteAuthEvent(ctx context.Context, e *AuthEvent) error

	// --- Admin audit query + session/device management (P4) ---

	// ListAuthEvents returns a page of the org's audit log (newest first) joined
	// with each actor's name/email, plus the total matching count for pagination.
	ListAuthEvents(ctx context.Context, orgID uuid.UUID, f AuthEventFilter) ([]AuthEventView, int64, error)
	// ListActiveRefreshTokens returns a user's live (non-revoked, unexpired)
	// refresh tokens — one per device/session — for the sessions UI.
	ListActiveRefreshTokens(ctx context.Context, userID uuid.UUID) ([]RefreshToken, error)
	// RevokeRefreshTokenForUser revokes one refresh token scoped to its owner and
	// returns rows affected (0 = not found / not owned / already revoked), so a
	// caller can only ever revoke their own sessions.
	RevokeRefreshTokenForUser(ctx context.Context, id, userID uuid.UUID) (int64, error)
}

// AuthEventWriter is the narrow append-only port the admin usecases
// (workspace/role/permission) depend on to record an auth_events row (P4),
// without pulling in the whole AuthRepository. AuthRepository satisfies it.
type AuthEventWriter interface {
	WriteAuthEvent(ctx context.Context, e *AuthEvent) error
}

// AuditUseCase reads the admin/auth audit log for the transparency UI (P4).
type AuditUseCase interface {
	ListEvents(ctx context.Context, orgID uuid.UUID, f AuthEventFilter) ([]AuthEventView, int64, error)
}

type AuthUseCase interface {
	Register(ctx context.Context, input RegisterInput) (*AuthResponse, error)
	Login(ctx context.Context, input LoginInput, meta RequestMeta) (*AuthResponse, error)
	RefreshToken(ctx context.Context, input RefreshInput, meta RequestMeta) (*AuthResponse, error)
	Logout(ctx context.Context, refreshToken string) error
	GetMe(ctx context.Context, userID uuid.UUID) (*User, error)
	GoogleLogin(ctx context.Context, code string) (*AuthResponse, error)
	GetGoogleAuthURL(state string) string
	// SwitchWorkspace re-mints the session for a validated ACTIVE membership. It
	// threads request meta into the new refresh token and revokes the presented
	// one (switch hygiene, P3) so a switch never orphans a live 7-day credential;
	// input.SetDefault persists the target as the user's default.
	SwitchWorkspace(ctx context.Context, userID uuid.UUID, input SwitchWorkspaceInput, meta RequestMeta, currentRefreshToken string) (*AuthResponse, error)
	ListWorkspaces(ctx context.Context, userID uuid.UUID) ([]WorkspaceInfo, error)

	// Account recovery + email verification (P1). ForgotPassword and
	// ResendVerification return a non-nil debug token only in non-production, to
	// ease local testing (mirrors InviteMember). ForgotPassword never reveals
	// whether the email exists.
	ForgotPassword(ctx context.Context, input ForgotPasswordInput, meta RequestMeta) (*string, error)
	ResetPassword(ctx context.Context, input ResetPasswordInput, meta RequestMeta) error
	VerifyEmail(ctx context.Context, input VerifyEmailInput) error
	ResendVerification(ctx context.Context, userID uuid.UUID, meta RequestMeta) (*string, error)

	// Session/device management (P4). ListSessions marks the caller's own session
	// (matched by the current refresh token) as Current. RevokeSession revokes one
	// of the caller's sessions. SignOutEverywhere revokes all sessions and bumps
	// token_version (killing every access token instantly), then mints a fresh
	// session for the current device so the caller stays signed in here.
	ListSessions(ctx context.Context, userID uuid.UUID, currentRefreshToken string) ([]SessionInfo, error)
	RevokeSession(ctx context.Context, userID, orgID, sessionID uuid.UUID) error
	SignOutEverywhere(ctx context.Context, userID, orgID uuid.UUID, currentRefreshToken string) (*AuthResponse, error)
}

type WorkspaceUseCase interface {
	ListMembers(ctx context.Context, orgID uuid.UUID) ([]MemberInfo, error)
	InviteMember(ctx context.Context, orgID uuid.UUID, input InviteMemberInput) (*MemberInfo, *string, error)
	// AcceptInvite joins the invitee to the org transactionally, optionally setting
	// the password/name they chose on the accept page (P2).
	AcceptInvite(ctx context.Context, input AcceptInviteInput) error
	// ListInvitations / ResendInvitation / RevokeInvitation drive the pending-
	// invitations panel (P2). Resend re-mints a fresh 256-bit token; revoke kills
	// the pending token so it can no longer be accepted.
	ListInvitations(ctx context.Context, orgID uuid.UUID) ([]InvitationInfo, error)
	ResendInvitation(ctx context.Context, orgID, invitationID uuid.UUID) (*string, error)
	RevokeInvitation(ctx context.Context, orgID, invitationID uuid.UUID) error
	UpdateMemberRole(ctx context.Context, orgID uuid.UUID, targetUserID uuid.UUID, input UpdateMemberRoleInput) error
	SuspendMember(ctx context.Context, orgID uuid.UUID, targetUserID uuid.UUID) error
	ReinstateMember(ctx context.Context, orgID uuid.UUID, targetUserID uuid.UUID) error
	// SendMemberResetLink emails a target member a password-reset link on an
	// admin's behalf (P2). The admin never sees or sets the password (accounts are
	// global); the token is never returned in any env. Shares the self-serve
	// per-email cooldown plus a per-target daily cap.
	SendMemberResetLink(ctx context.Context, orgID, callerUserID, targetUserID uuid.UUID, meta RequestMeta) error
	// TransferOwnership is the ONLY path that mints an owner. The caller must be
	// the current owner (verified inside, not just at the route), and the current
	// owner is demoted in the same transaction the target is promoted.
	TransferOwnership(ctx context.Context, orgID uuid.UUID, callerUserID, targetUserID uuid.UUID) error
	RemoveMember(ctx context.Context, orgID uuid.UUID, targetUserID uuid.UUID, input RemoveMemberInput) error
}

type RemoveMemberInput struct {
	ReassignToUserID *uuid.UUID `json:"reassign_to_user_id,omitempty"`
	Strategy         string     `json:"strategy,omitempty"` // "transfer" or "unassign"
}

type Mailer interface {
	SendInvite(ctx context.Context, to, inviteLink, orgName string) error
	// SendPasswordReset / SendVerification email a one-time action link (P1).
	SendPasswordReset(ctx context.Context, to, resetLink string) error
	SendVerification(ctx context.Context, to, verifyLink string) error
	// SendSecurityAlert notifies a user of a sensitive account change (e.g. a
	// password reset) so an unexpected change is noticed quickly.
	SendSecurityAlert(ctx context.Context, to, subject, message string) error
}

type ContactFilter struct {
	Q           string      `form:"q"`
	Semantic    bool        `form:"semantic"`
	CompanyID   *uuid.UUID  `form:"company_id"`
	TagIDs      []uuid.UUID `form:"tag_ids"`
	OwnerUserID *uuid.UUID  `form:"owner_user_id"`
	// CustomFilters matches custom (jsonb) fields exactly (custom_fields ->> key = value),
	// powering reverse related lists for custom lookups on contacts.
	CustomFilters map[string]string `form:"-"`
	Cursor      string      `form:"cursor"`
	Limit       int         `form:"limit"`
	SortBy      string      `form:"sort_by"`    // "name", "created_at" (default)
	SortOrder   string      `form:"sort_order"` // "asc" or "desc" (default)
}

type ImportResult struct {
	Created      int      `json:"created"`
	Skipped      int      `json:"skipped"`
	Errors       int      `json:"errors"`
	ErrorDetails []string `json:"error_details,omitempty"`
}

type CreateContactInput struct {
	FirstName    string      `json:"first_name" binding:"required,min=1"`
	LastName     string      `json:"last_name"`
	Email        *string     `json:"email"`
	Phone        *string     `json:"phone"`
	CompanyID    *uuid.UUID  `json:"company_id"`
	OwnerUserID  *uuid.UUID  `json:"owner_user_id"`
	CustomFields JSON        `json:"custom_fields"`
	TagIDs       []uuid.UUID `json:"tag_ids"`
}

type UpdateContactInput struct {
	FirstName    *string      `json:"first_name"`
	LastName     *string      `json:"last_name"`
	Email        *string      `json:"email"`
	Phone        *string      `json:"phone"`
	CompanyID    *uuid.UUID   `json:"company_id"`
	OwnerUserID  *uuid.UUID   `json:"owner_user_id"`
	CustomFields *JSON        `json:"custom_fields"`
	TagIDs       *[]uuid.UUID `json:"tag_ids"`
}

type ContactRepository interface {
	List(ctx context.Context, orgID uuid.UUID, f ContactFilter) ([]Contact, string, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Contact, error)
	Create(ctx context.Context, c *Contact) error
	Update(ctx context.Context, c *Contact) error
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) error
	BulkCreate(ctx context.Context, contacts []Contact) (int64, error)
	Count(ctx context.Context, orgID uuid.UUID) (int64, error)
	FindTagsByNames(ctx context.Context, orgID uuid.UUID, names []string) ([]Tag, error)
	CreateTags(ctx context.Context, tags []Tag) error
	FindCompanyByName(ctx context.Context, orgID uuid.UUID, name string) (*Company, error)
	CreateCompany(ctx context.Context, c *Company) error
	ReplaceContactTags(ctx context.Context, contactID uuid.UUID, tagIDs []uuid.UUID) error
	BulkDeleteByIDs(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) (int64, error)
	BulkAssignTag(ctx context.Context, orgID uuid.UUID, contactIDs []uuid.UUID, tagID uuid.UUID) (int64, error)
	SemanticSearch(ctx context.Context, orgID uuid.UUID, vec []float32, threshold float32, limit int) ([]Contact, error)
}

type BulkActionInput struct {
	Action     string      `json:"action" binding:"required"`
	ContactIDs []uuid.UUID `json:"contact_ids" binding:"required,min=1"`
	TagID      *uuid.UUID  `json:"tag_id"`
}

type BulkActionResult struct {
	Affected int    `json:"affected"`
	Message  string `json:"message"`
}

type ContactUseCase interface {
	List(ctx context.Context, orgID uuid.UUID, f ContactFilter) ([]Contact, string, error)
	SemanticSearch(ctx context.Context, orgID uuid.UUID, query string, limit int) ([]Contact, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Contact, error)
	Create(ctx context.Context, orgID uuid.UUID, input CreateContactInput) (*Contact, error)
	Update(ctx context.Context, orgID, id uuid.UUID, input UpdateContactInput) (*Contact, error)
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	BulkImport(ctx context.Context, orgID uuid.UUID, file multipart.File, filename string, conflictMode string) (*ImportResult, error)
	BulkAction(ctx context.Context, orgID uuid.UUID, input BulkActionInput) (*BulkActionResult, error)
	Count(ctx context.Context, orgID uuid.UUID) (int64, error)
}

type EmbeddingQueue interface {
	EnqueueContact(c *Contact)
}

type CompanyFilter struct {
	Q      string `form:"q"`
	Cursor string `form:"cursor"`
	Limit  int    `form:"limit"`
	// CustomFilters matches custom (jsonb) fields exactly (custom_fields ->> key = value),
	// powering reverse related lists for custom lookups on companies.
	CustomFilters map[string]string `form:"-"`
}

type CreateCompanyInput struct {
	Name         string  `json:"name" binding:"required,min=1"`
	Industry     *string `json:"industry"`
	Website      *string `json:"website"`
	CustomFields JSON    `json:"custom_fields"`
}

type UpdateCompanyInput struct {
	Name         *string `json:"name"`
	Industry     *string `json:"industry"`
	Website      *string `json:"website"`
	CustomFields *JSON   `json:"custom_fields"`
}

type CompanyRepository interface {
	List(ctx context.Context, orgID uuid.UUID, f CompanyFilter) ([]Company, string, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Company, error)
	Create(ctx context.Context, c *Company) error
	Update(ctx context.Context, c *Company) error
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) error
	Count(ctx context.Context, orgID uuid.UUID) (int64, error)
}

type CompanyUseCase interface {
	List(ctx context.Context, orgID uuid.UUID, f CompanyFilter) ([]Company, string, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Company, error)
	Create(ctx context.Context, orgID uuid.UUID, input CreateCompanyInput) (*Company, error)
	Update(ctx context.Context, orgID, id uuid.UUID, input UpdateCompanyInput) (*Company, error)
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	Count(ctx context.Context, orgID uuid.UUID) (int64, error)
}

type CreateTagInput struct {
	Name  string `json:"name" binding:"required,min=1"`
	Color string `json:"color"`
}

type UpdateTagInput struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

type TagRepository interface {
	List(ctx context.Context, orgID uuid.UUID) ([]Tag, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Tag, error)
	Create(ctx context.Context, t *Tag) error
	Update(ctx context.Context, t *Tag) error
	Delete(ctx context.Context, orgID, id uuid.UUID) error
}

type TagUseCase interface {
	List(ctx context.Context, orgID uuid.UUID) ([]Tag, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Tag, error)
	Create(ctx context.Context, orgID uuid.UUID, input CreateTagInput) (*Tag, error)
	Update(ctx context.Context, orgID, id uuid.UUID, input UpdateTagInput) (*Tag, error)
	Delete(ctx context.Context, orgID, id uuid.UUID) error
}

type DealFilter struct {
	Q           string     `form:"q"`
	StageID     *uuid.UUID `form:"stage_id"`
	OwnerUserID *uuid.UUID `form:"owner_user_id"`
	ContactID   *uuid.UUID `form:"contact_id"`
	CompanyID   *uuid.UUID `form:"company_id"`
	// CustomFilters matches custom (jsonb) fields exactly: custom_fields ->> key = value.
	// It lets a deal be listed by an admin-defined relation field, which powers
	// reverse related lists for custom lookups on deals.
	CustomFilters map[string]string `form:"-"`
	Cursor        string            `form:"cursor"`
	Limit         int               `form:"limit"`
	SortBy        string            `form:"sort_by"`    // "value", "probability", "created_at" (default)
	SortOrder     string            `form:"sort_order"` // "asc" or "desc" (default)
}

type CreateDealInput struct {
	Title           string     `json:"title" binding:"required,min=1"`
	ContactID       *uuid.UUID `json:"contact_id"`
	CompanyID       *uuid.UUID `json:"company_id"`
	StageID         *uuid.UUID `json:"stage_id"`
	Value           float64    `json:"value"`
	Probability     int        `json:"probability"`
	OwnerUserID     *uuid.UUID `json:"owner_user_id"`
	ExpectedCloseAt *string    `json:"expected_close_at"`
	CustomFields    JSON       `json:"custom_fields"`
}

type UpdateDealInput struct {
	Title           *string    `json:"title"`
	ContactID       *uuid.UUID `json:"contact_id"`
	CompanyID       *uuid.UUID `json:"company_id"`
	StageID         *uuid.UUID `json:"stage_id"`
	Value           *float64   `json:"value"`
	Probability     *int       `json:"probability"`
	OwnerUserID     *uuid.UUID `json:"owner_user_id"`
	ExpectedCloseAt *string    `json:"expected_close_at"`
	CustomFields    *JSON      `json:"custom_fields"`
}

type UpdateDealStageInput struct {
	StageID    uuid.UUID `json:"stage_id" binding:"required"`
	LostReason *string   `json:"lost_reason"`
}

type ForecastRow struct {
	Month           string  `json:"month"`
	ExpectedRevenue float64 `json:"expected_revenue"`
	DealsCount      int     `json:"deals_count"`
}

type DealRepository interface {
	List(ctx context.Context, orgID uuid.UUID, f DealFilter) ([]Deal, string, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Deal, error)
	Create(ctx context.Context, d *Deal) error
	Update(ctx context.Context, d *Deal) error
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) error
	Count(ctx context.Context, orgID uuid.UUID) (int64, error)
	Forecast(ctx context.Context, orgID uuid.UUID) ([]ForecastRow, error)
}

type DealUseCase interface {
	List(ctx context.Context, orgID uuid.UUID, f DealFilter) ([]Deal, string, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Deal, error)
	Create(ctx context.Context, orgID uuid.UUID, input CreateDealInput) (*Deal, error)
	Update(ctx context.Context, orgID, id uuid.UUID, input UpdateDealInput) (*Deal, error)
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	ChangeStage(ctx context.Context, orgID, dealID uuid.UUID, input UpdateDealStageInput) (*Deal, error)
	Forecast(ctx context.Context, orgID uuid.UUID) ([]ForecastRow, error)
	Count(ctx context.Context, orgID uuid.UUID) (int64, error)
}

type CreateStageInput struct {
	Name     string `json:"name" binding:"required,min=1"`
	Position int    `json:"position"`
	Color    string `json:"color"`
	IsWon    bool   `json:"is_won"`
	IsLost   bool   `json:"is_lost"`
}

type UpdateStageInput struct {
	Name     *string `json:"name"`
	Position *int    `json:"position"`
	Color    *string `json:"color"`
	IsWon    *bool   `json:"is_won"`
	IsLost   *bool   `json:"is_lost"`
}

type PipelineStageRepository interface {
	List(ctx context.Context, orgID uuid.UUID) ([]PipelineStage, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*PipelineStage, error)
	Create(ctx context.Context, s *PipelineStage) error
	Update(ctx context.Context, s *PipelineStage) error
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	CountByOrg(ctx context.Context, orgID uuid.UUID) (int64, error)
}

type PipelineStageUseCase interface {
	List(ctx context.Context, orgID uuid.UUID) ([]PipelineStage, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*PipelineStage, error)
	Create(ctx context.Context, orgID uuid.UUID, input CreateStageInput) (*PipelineStage, error)
	Update(ctx context.Context, orgID, id uuid.UUID, input UpdateStageInput) (*PipelineStage, error)
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	SeedDefaults(ctx context.Context, orgID uuid.UUID) ([]PipelineStage, error)
}

type ActivityFilter struct {
	DealID    *uuid.UUID `form:"deal_id"`
	ContactID *uuid.UUID `form:"contact_id"`
}

type CreateActivityInput struct {
	Type            string     `json:"type" binding:"required"`
	DealID          *uuid.UUID `json:"deal_id"`
	ContactID       *uuid.UUID `json:"contact_id"`
	Title           string     `json:"title"`
	Body            *string    `json:"body"`
	DurationMinutes *int       `json:"duration_minutes"`
	OccurredAt      *string    `json:"occurred_at"`
}

type ActivityRepository interface {
	List(ctx context.Context, orgID uuid.UUID, f ActivityFilter) ([]Activity, error)
	Create(ctx context.Context, a *Activity) error
}

type ActivityUseCase interface {
	List(ctx context.Context, orgID uuid.UUID, f ActivityFilter) ([]Activity, error)
	Create(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, input CreateActivityInput) (*Activity, error)
}

type TaskFilter struct {
	DealID     *uuid.UUID `form:"deal_id"`
	ContactID  *uuid.UUID `form:"contact_id"`
	AssignedTo *uuid.UUID `form:"assigned_to"`
	Completed  *bool      `form:"completed"`
}

type CreateTaskInput struct {
	Title      string     `json:"title" binding:"required,min=1"`
	DealID     *uuid.UUID `json:"deal_id"`
	ContactID  *uuid.UUID `json:"contact_id"`
	AssignedTo *uuid.UUID `json:"assigned_to"`
	DueAt      *string    `json:"due_at"`
	Priority   string     `json:"priority"`
}

type UpdateTaskInput struct {
	Title      *string    `json:"title"`
	AssignedTo *uuid.UUID `json:"assigned_to"`
	DueAt      *string    `json:"due_at"`
	Priority   *string    `json:"priority"`
	Completed  *bool      `json:"completed"`
}

type TaskRepository interface {
	List(ctx context.Context, orgID uuid.UUID, f TaskFilter) ([]Task, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Task, error)
	Create(ctx context.Context, t *Task) error
	Update(ctx context.Context, t *Task) error
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) error
}

type TaskUseCase interface {
	List(ctx context.Context, orgID uuid.UUID, f TaskFilter) ([]Task, error)
	Create(ctx context.Context, orgID uuid.UUID, input CreateTaskInput) (*Task, error)
	Update(ctx context.Context, orgID uuid.UUID, id uuid.UUID, input UpdateTaskInput) (*Task, error)
	Delete(ctx context.Context, orgID uuid.UUID, id uuid.UUID) error
}

type UserRepository interface {
	ListByOrgID(ctx context.Context, orgID uuid.UUID) ([]User, error)
}

type CreateFieldDefInput struct {
	Key        string   `json:"key" binding:"required,min=1"`
	Label      string   `json:"label" binding:"required,min=1"`
	Type       string   `json:"type" binding:"required"`
	EntityType string   `json:"entity_type" binding:"required"`
	Options    []string `json:"options"`
	// TargetSlug is required when Type == "relation": the object this lookup points at.
	TargetSlug string `json:"target_slug"`
	// ViaField/SourceField are required when Type == "mirror": follow ViaField (a
	// relation on this object) to the linked record and display its SourceField.
	ViaField    string `json:"via_field"`
	SourceField string `json:"source_field"`
	Required    bool   `json:"required"`
	Position    *int   `json:"position"`
}

type UpdateFieldDefInput struct {
	Label    *string  `json:"label"`
	Type     *string  `json:"type"`
	Options  []string `json:"options"`
	// TargetSlug repoints a relation field's lookup target. Ignored for non-relation types.
	TargetSlug *string `json:"target_slug"`
	// ViaField/SourceField repoint a mirror field. Ignored for non-mirror types.
	ViaField    *string `json:"via_field"`
	SourceField *string `json:"source_field"`
	Required    *bool   `json:"required"`
	Position    *int    `json:"position"`
}

type OrgSettingsRepository interface {
	GetByOrgID(ctx context.Context, orgID uuid.UUID) (*OrgSettings, error)
	Upsert(ctx context.Context, settings *OrgSettings) error
}

type OrgSettingsUseCase interface {
	GetFieldDefs(ctx context.Context, orgID uuid.UUID, entityType string) ([]CustomFieldDef, error)
	CreateFieldDef(ctx context.Context, orgID uuid.UUID, input CreateFieldDefInput) (*CustomFieldDef, error)
	UpdateFieldDef(ctx context.Context, orgID uuid.UUID, key string, input UpdateFieldDefInput) (*CustomFieldDef, error)
	DeleteFieldDef(ctx context.Context, orgID uuid.UUID, key string) error
	ValidateCustomFields(ctx context.Context, orgID uuid.UUID, entityType string, fields JSON) error
}

type CreateObjectDefInput struct {
	Slug        string `json:"slug" binding:"required,min=1"`
	Label       string `json:"label" binding:"required,min=1"`
	LabelPlural string `json:"label_plural" binding:"required,min=1"`
	Icon        string `json:"icon"`
	Fields      JSON   `json:"fields"`
	Searchable  bool   `json:"searchable"`
}

type UpdateObjectDefInput struct {
	Label       *string `json:"label"`
	LabelPlural *string `json:"label_plural"`
	Icon        *string `json:"icon"`
	Fields      JSON    `json:"fields"`
	// Searchable is a pointer so "not supplied" (nil) leaves the flag unchanged,
	// distinct from an explicit false that turns search off.
	Searchable *bool `json:"searchable"`
}

// CreateRecordInput / UpdateRecordInput no longer carry contact_id/deal_id: custom
// record relationships are managed through object_links (the …/links API) as of P7,
// not hardcoded FK columns.
type CreateRecordInput struct {
	Data JSON `json:"data" binding:"required"`
}

type UpdateRecordInput struct {
	Data        JSON    `json:"data"`
	DisplayName *string `json:"display_name"`
}

type RecordFilter struct {
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
	Q      string `json:"q"`
	// Filters maps a field key to an exact-match value, applied against the
	// record's JSONB data (data ->> key = value). It carries relation-field
	// filters so a custom object can be listed by "all records whose <relation>
	// is X" — the jsonb counterpart to the system adapters' typed filters, and
	// what powers reverse related lists for custom-object children.
	Filters map[string]string `json:"filters,omitempty"`
}

type CustomObjectRepository interface {
	ListDefs(ctx context.Context, orgID uuid.UUID) ([]CustomObjectDef, error)
	GetDefBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*CustomObjectDef, error)
	GetDefByID(ctx context.Context, orgID uuid.UUID, id uuid.UUID) (*CustomObjectDef, error)
	CreateDef(ctx context.Context, def *CustomObjectDef) error
	UpdateDef(ctx context.Context, def *CustomObjectDef) error
	SoftDeleteDef(ctx context.Context, orgID uuid.UUID, id uuid.UUID) error

	ListRecords(ctx context.Context, orgID uuid.UUID, defID uuid.UUID, f RecordFilter) ([]CustomObjectRecord, int64, error)
	GetRecord(ctx context.Context, orgID uuid.UUID, id uuid.UUID) (*CustomObjectRecord, error)
	CreateRecord(ctx context.Context, r *CustomObjectRecord) error
	UpdateRecord(ctx context.Context, r *CustomObjectRecord) error
	SoftDeleteRecord(ctx context.Context, orgID uuid.UUID, id uuid.UUID) error
}

type CustomObjectUseCase interface {
	ListDefs(ctx context.Context, orgID uuid.UUID) ([]CustomObjectDef, error)
	GetDefBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*CustomObjectDef, error)
	CreateDef(ctx context.Context, orgID uuid.UUID, input CreateObjectDefInput) (*CustomObjectDef, error)
	UpdateDef(ctx context.Context, orgID uuid.UUID, slug string, input UpdateObjectDefInput) (*CustomObjectDef, error)
	DeleteDef(ctx context.Context, orgID uuid.UUID, slug string) error

	ListRecords(ctx context.Context, orgID uuid.UUID, slug string, f RecordFilter) ([]CustomObjectRecord, int64, error)
	GetRecord(ctx context.Context, orgID uuid.UUID, id uuid.UUID) (*CustomObjectRecord, error)
	CreateRecord(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, slug string, input CreateRecordInput) (*CustomObjectRecord, error)
	UpdateRecord(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID, input UpdateRecordInput) (*CustomObjectRecord, error)
	DeleteRecord(ctx context.Context, orgID uuid.UUID, id uuid.UUID) error
}

type UpsertKBInput struct {
	Title   string `json:"title" binding:"required,min=1"`
	Content string `json:"content" binding:"required"`
}

type KnowledgeBaseRepository interface {
	GetAllActive(ctx context.Context, orgID uuid.UUID) ([]KnowledgeBaseEntry, error)
	GetBySection(ctx context.Context, orgID uuid.UUID, section string) (*KnowledgeBaseEntry, error)
	Upsert(ctx context.Context, entry *KnowledgeBaseEntry) error
}

// SchemaCacheBuster is implemented by the KnowledgeBuilder to allow
// non-AI packages to invalidate the AI schema cache.
type SchemaCacheBuster interface {
	BustCache(ctx context.Context, orgID uuid.UUID)
}

type KnowledgeBaseUseCase interface {
	ListSections(ctx context.Context, orgID uuid.UUID) ([]KnowledgeBaseEntry, error)
	GetSection(ctx context.Context, orgID uuid.UUID, section string) (*KnowledgeBaseEntry, error)
	UpsertSection(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, section string, input UpsertKBInput) (*KnowledgeBaseEntry, error)
	GetAIPrompt(ctx context.Context, orgID uuid.UUID) (string, error)
}

type VoiceNoteFilter struct {
	ContactID *uuid.UUID `form:"contact_id"`
	DealID    *uuid.UUID `form:"deal_id"`
	Limit     int        `form:"limit"`
}

type UploadVoiceNoteInput struct {
	AudioBytes      []byte
	OriginalName    string
	LanguageCode    string
	ContactID       *uuid.UUID
	DealID          *uuid.UUID
	DurationSeconds int
	AutoAnalyze     bool
}

type VoiceNoteRepository interface {
	Create(ctx context.Context, v *VoiceNote) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*VoiceNote, error)
	List(ctx context.Context, orgID uuid.UUID, f VoiceNoteFilter) ([]VoiceNote, error)
	Update(ctx context.Context, v *VoiceNote) error
	Delete(ctx context.Context, orgID, id uuid.UUID) error
}

type VoiceNoteUseCase interface {
	Upload(ctx context.Context, orgID, userID uuid.UUID, input UploadVoiceNoteInput) (*VoiceNote, string, error)
	Analyze(ctx context.Context, orgID, userID, noteID uuid.UUID) error
	List(ctx context.Context, orgID uuid.UUID, f VoiceNoteFilter) ([]VoiceNote, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*VoiceNote, error)
	ApplyContactUpdates(ctx context.Context, orgID, id uuid.UUID) error
	Delete(ctx context.Context, orgID, id uuid.UUID) error
}

