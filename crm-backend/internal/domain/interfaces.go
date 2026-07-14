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
	// Two-factor (U6.4). TwoFactorRequired marks a CHALLENGE, not a session: the
	// password was right but the second factor is outstanding, so AccessToken and
	// RefreshToken are EMPTY and ChallengeToken must be exchanged at
	// POST /auth/2fa/verify. The handler must not set auth cookies on such a
	// response. TwoFactorEnrollRequired rides on a REAL session whose workspace
	// requires 2FA the user hasn't set up — they are signed in but confined to
	// enrolling.
	TwoFactorRequired       bool   `json:"two_factor_required,omitempty"`
	TwoFactorEnrollRequired bool   `json:"two_factor_enroll_required,omitempty"`
	ChallengeToken          string `json:"challenge_token,omitempty"`
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
	// Members-table columns (U4): when they joined, whether they've confirmed
	// their email, and their most recent session activity (nil = never signed in
	// / no live session).
	JoinedAt      time.Time  `json:"joined_at"`
	EmailVerified bool       `json:"email_verified"`
	LastActiveAt  *time.Time `json:"last_active_at,omitempty"`
	// TwoFactorEnabled surfaces who has actually enrolled (U6.4) — the column an
	// admin needs before turning the workspace policy on.
	TwoFactorEnabled bool `json:"two_factor_enabled"`
}

// MemberGroup is one user-group a member belongs to, for the member detail
// drawer (U4).
type MemberGroup struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// MemberDetail is the member drawer's full payload (U4): identity + the groups
// they're in, the records they own (the offboarding preview), and their live
// sessions for admin force-sign-out.
type MemberDetail struct {
	Member        MemberInfo    `json:"member"`
	Groups        []MemberGroup `json:"groups"`
	OwnedContacts int64         `json:"owned_contacts"`
	OwnedDeals    int64         `json:"owned_deals"`
	// OwnedCustom counts the custom-object records the member owns (U6.3).
	OwnedCustom int64 `json:"owned_custom"`
	Sessions      []SessionInfo `json:"sessions"`
}

// WorkspaceDetail is the Workspace General page payload (U4): the org's identity
// + defaults, its member count, and whether the caller is the owner (gates the
// destructive actions client-side; the server re-checks).
type WorkspaceDetail struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Currency    string    `json:"currency"`
	Locale      string    `json:"locale"`
	Timezone    string    `json:"timezone"`
	MemberCount int64     `json:"member_count"`
	IsOwner     bool      `json:"is_owner"`
	// RequireTwoFactor is the workspace 2FA policy (U6.4).
	RequireTwoFactor bool      `json:"require_two_factor"`
	CreatedAt        time.Time `json:"created_at"`
}

// UpdateWorkspaceInput carries the editable Workspace General fields (U4). All
// optional (pointers): only the provided fields are written. A blank Name is
// rejected in the usecase.
type UpdateWorkspaceInput struct {
	Name     *string `json:"name"`
	Currency *string `json:"currency"`
	Locale   *string `json:"locale"`
	Timezone *string `json:"timezone"`
	// RequireTwoFactor toggles the workspace 2FA policy (U6.4). A pointer so an
	// absent field leaves it alone — and because GORM omits a struct field holding
	// the zero value, turning the policy back OFF must be written through a
	// column-scoped update, not a struct Save.
	RequireTwoFactor *bool `json:"require_two_factor"`
}

// CreateWorkspaceInput creates a NEW workspace for an already-signed-in user (U4)
// — the "create workspace" path for an existing account (chooser + zero-workspace
// dead-end), distinct from Register which also creates the account.
type CreateWorkspaceInput struct {
	Name string `json:"name" binding:"required"`
	Type string `json:"type"`
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

// AcceptInviteResult identifies who joined which org, so the handler can mint a
// session and log the invitee straight in after a successful accept (U4).
// AutoLogin is true ONLY for a brand-new account created during this accept —
// an EXISTING account is not auto-logged-in, so an intercepted invite link can't
// be turned into a silent session over that user's whole multi-workspace account
// (they add the workspace and then sign in normally).
type AcceptInviteResult struct {
	UserID    uuid.UUID
	OrgID     uuid.UUID
	AutoLogin bool
}

// InvitationPreview is the public metadata the accept page reads BEFORE the
// invitee commits (U4): who invited them to what, whether the link is still good,
// and whether the email already has an account (so the page can offer "set a
// password" vs "sign in to accept"). Served token-authenticated (the raw token is
// the credential); never leaks anything an invite-link holder shouldn't see.
type InvitationPreview struct {
	Email    string `json:"email"`
	OrgName  string `json:"org_name"`
	RoleName string `json:"role_name"`
	// Status is one of: valid | expired | revoked | accepted | invalid.
	Status string `json:"status"`
	// HasAccount is true when an account already exists for Email — the accept
	// page hides the password fields and prompts a sign-in instead.
	HasAccount bool `json:"has_account"`
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

// IncomingInvitation is a pending invitation addressed to the authenticated user
// (matched by their account email), for the post-OAuth / zero-workspace "you've
// been invited" consent surface (U4 item 6). Unlike InvitationInfo (an admin's view
// of a workspace's OUTGOING invites) it names the workspace + role the user would
// join if they accept.
type IncomingInvitation struct {
	ID        uuid.UUID `json:"id"`
	OrgID     uuid.UUID `json:"org_id"`
	OrgName   string    `json:"org_name"`
	RoleName  string    `json:"role_name"`
	ExpiresAt time.Time `json:"expires_at"`
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
	GetOrganizationByID(ctx context.Context, id uuid.UUID) (*Organization, error)
	// UpdateOrganization writes an org's editable fields (name + workspace
	// defaults) (U4).
	UpdateOrganization(ctx context.Context, org *Organization) error
	// SoftDeleteOrganization marks the whole workspace deleted (U4). Membership
	// resolution (ListOrgsByUserID) already excludes soft-deleted orgs, so the
	// workspace vanishes from every member's chooser on their next request.
	SoftDeleteOrganization(ctx context.Context, id uuid.UUID) error
	CreateUser(ctx context.Context, user *User) error
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (*User, error)
	GetUserByGoogleID(ctx context.Context, googleID string) (*User, error)
	UpdateUser(ctx context.Context, user *User) error
	// UpdateUserProfile writes only the self-serve profile columns (U2) — a
	// column-scoped UPDATE, so a profile save can't clobber a concurrent
	// security write (token_version bump / password reset) the way the full-row
	// UpdateUser would.
	UpdateUserProfile(ctx context.Context, user *User) error

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

	// Two-factor authentication (U6.4).
	SetTOTPSecret(ctx context.Context, userID uuid.UUID, encryptedSecret string) error
	EnableTOTP(ctx context.Context, userID uuid.UUID, codeHashes []string) error
	DisableTOTP(ctx context.Context, userID uuid.UUID) error
	ReplaceBackupCodes(ctx context.Context, userID uuid.UUID, codeHashes []string) error
	ListUnusedBackupCodes(ctx context.Context, userID uuid.UUID) ([]TwoFactorBackupCode, error)
	// ConsumeBackupCode burns one code; false means it was already spent (the guard
	// is in the WHERE clause, so concurrent requests can't both win).
	ConsumeBackupCode(ctx context.Context, id uuid.UUID) (bool, error)
	CountBackupCodesRemaining(ctx context.Context, userID uuid.UUID) (int, error)
	CreateTwoFactorChallenge(ctx context.Context, ch *TwoFactorChallenge) error
	GetTwoFactorChallengeByHash(ctx context.Context, tokenHash string) (*TwoFactorChallenge, error)
	IncrementChallengeAttempts(ctx context.Context, id uuid.UUID) error
	// ClaimChallengeAttempt atomically spends one attempt against a live challenge;
	// false means there was none left (used, expired, or the cap reached).
	ClaimChallengeAttempt(ctx context.Context, id uuid.UUID, maxAttempts int) (bool, error)
	// ConsumeTwoFactorChallenge burns the challenge; false means someone else already
	// burned it (two concurrent verifies must not both mint a session).
	ConsumeTwoFactorChallenge(ctx context.Context, id uuid.UUID) (bool, error)
	DeleteExpiredTwoFactorChallenges(ctx context.Context) (int64, error)

	// RevokeAllUserAPITokens kills every live personal access token the user holds
	// (U6.5), in every workspace — fired when the account's password changes.
	RevokeAllUserAPITokens(ctx context.Context, userID uuid.UUID) (int64, error)

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
	// LatestSessionActivityByUsers returns userID → most recent live-session
	// activity (max of last_used_at/created_at over non-revoked, unexpired refresh
	// tokens) for the given users, for the members-table "Last active" column (U4).
	// Users with no live session are simply absent from the map.
	LatestSessionActivityByUsers(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]time.Time, error)
	// ListPendingInvitations returns an org's still-actionable invitations
	// (pending, unexpired, not revoked), newest first, for the members panel (P2).
	ListPendingInvitations(ctx context.Context, orgID uuid.UUID) ([]OrgInvitation, error)
	// ListOpenInvitations returns an org's outstanding invitations INCLUDING
	// expired ones (status 'pending', not revoked/accepted, any expiry), newest
	// first — so the members panel can show an expired invite with a Resend badge
	// instead of letting it silently vanish (U4). Revoked/accepted invites stay out.
	ListOpenInvitations(ctx context.Context, orgID uuid.UUID) ([]OrgInvitation, error)
	// GetPendingInvitationByEmail returns the org's outstanding (status 'pending',
	// not revoked) invitation for an email, expired or not, or nil — the dedupe
	// lookup so a re-invite resends the existing row instead of stacking a second
	// one (U4). Email is matched case-insensitively.
	GetPendingInvitationByEmail(ctx context.Context, orgID uuid.UUID, email string) (*OrgInvitation, error)
	// ListValidInvitationsByEmail returns every currently-acceptable invitation
	// (status 'pending', not revoked, NOT expired, and to a workspace that still
	// exists) for an email across ALL orgs, newest first — the by-email lookup for
	// the Google-first / zero-workspace consent flow (U4 item 6). Excludes
	// soft-deleted workspaces so a user is never offered (or auto-routed to) a dead
	// one. Email is matched case-insensitively.
	ListValidInvitationsByEmail(ctx context.Context, email string) ([]OrgInvitation, error)
	// GetOrgInvitationByIDUnscoped returns an invitation by id alone (no org scope),
	// or nil — for accepting one's OWN invitation, where authorization is the email
	// match against the authenticated account, not org membership (U4 item 6).
	GetOrgInvitationByIDUnscoped(ctx context.Context, id uuid.UUID) (*OrgInvitation, error)
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
	// IssueSessionForUser mints a fresh session (access + refresh) for an already
	// identity-proven user into a specific org, WITHOUT a password check — the
	// invite-accept auto-login path (U4). The caller must have proven identity by
	// another means (here, control of the invite link); it verifies the user has
	// an ACTIVE membership in orgID before issuing, and 403s otherwise.
	IssueSessionForUser(ctx context.Context, userID, orgID uuid.UUID, meta RequestMeta) (*AuthResponse, error)
	// CreateWorkspace creates a NEW workspace owned by an already-signed-in user
	// and returns a session scoped to it (U4) — the "create workspace" path for an
	// existing account (chooser + zero-workspace dead-end). Reuses the org-owner
	// seeding of Register without touching the user's credentials.
	CreateWorkspace(ctx context.Context, userID uuid.UUID, in CreateWorkspaceInput, meta RequestMeta) (*AuthResponse, error)
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

	// My Account (U2). UpdateProfile edits the caller's own identity/preferences.
	// ChangePassword requires the CURRENT password, then signs out every other
	// device (re-minting this one, like SignOutEverywhere). SetPassword is the
	// OAuth-only variant: allowed only while no password exists, so a Google-only
	// account can survive losing Google access. UnlinkGoogle requires a password
	// to exist (never strand an account with zero sign-in methods).
	UpdateProfile(ctx context.Context, userID uuid.UUID, input UpdateProfileInput) (*User, error)
	ChangePassword(ctx context.Context, userID, orgID uuid.UUID, input ChangePasswordInput, meta RequestMeta) (*AuthResponse, error)
	SetPassword(ctx context.Context, userID, orgID uuid.UUID, input SetPasswordInput, meta RequestMeta) (*AuthResponse, error)
	UnlinkGoogle(ctx context.Context, userID uuid.UUID, meta RequestMeta) error

	// Two-factor authentication (U6.4). Setup stores an unconfirmed secret; Enable
	// proves the authenticator works and returns the one-time backup codes; Verify
	// exchanges a login challenge for a real session.
	StartTwoFactorSetup(ctx context.Context, userID uuid.UUID) (*TwoFactorSetup, error)
	EnableTwoFactor(ctx context.Context, userID, orgID uuid.UUID, code string, meta RequestMeta) (*BackupCodesResult, error)
	DisableTwoFactor(ctx context.Context, userID, orgID uuid.UUID, code string, meta RequestMeta) error
	RegenerateBackupCodes(ctx context.Context, userID, orgID uuid.UUID, code string, meta RequestMeta) (*BackupCodesResult, error)
	GetTwoFactorStatus(ctx context.Context, userID, orgID uuid.UUID) (*TwoFactorStatus, error)
	VerifyTwoFactor(ctx context.Context, challengeToken, code string, meta RequestMeta) (*AuthResponse, error)
	// ResetMemberTwoFactor is the admin break-glass for a member who lost both their
	// device and their backup codes (members.manage-gated, audited).
	ResetMemberTwoFactor(ctx context.Context, orgID, actorID, targetUserID uuid.UUID, meta RequestMeta) error
}

// UpdateProfileInput carries the self-serve profile fields (U2). Pointer
// fields: nil = unchanged. Email is deliberately absent — changing it needs a
// re-verification flow (planned; see user_system_improvement_plan.md).
type UpdateProfileInput struct {
	FirstName           *string `json:"first_name"`
	LastName            *string `json:"last_name"`
	AvatarURL           *string `json:"avatar_url"`
	Timezone            *string `json:"timezone"`
	Locale              *string `json:"locale"`
	OnboardingCompleted *bool   `json:"onboarding_completed"`
}

type ChangePasswordInput struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
}

type SetPasswordInput struct {
	NewPassword string `json:"new_password" binding:"required"`
}

type WorkspaceUseCase interface {
	ListMembers(ctx context.Context, orgID uuid.UUID) ([]MemberInfo, error)
	// ListTeammates narrows ListMembers to the people the caller shares a team with
	// — the assignee set a 'team'-scoped role may pick from (U6.1).
	ListTeammates(ctx context.Context, orgID, userID uuid.UUID) ([]MemberInfo, error)
	InviteMember(ctx context.Context, orgID uuid.UUID, input InviteMemberInput) (*MemberInfo, *string, error)
	// AcceptInvite joins the invitee to the org transactionally, optionally setting
	// the password/name they chose on the accept page (P2). Returns who joined
	// which org so the handler can auto-login the invitee (U4).
	AcceptInvite(ctx context.Context, input AcceptInviteInput) (*AcceptInviteResult, error)
	// GetInvitationPreview resolves an invite token to its public accept-page
	// metadata (org/role/email + validity + whether the account exists) without
	// consuming it (U4). Never errors on a bad token — returns Status "invalid".
	GetInvitationPreview(ctx context.Context, token string) (*InvitationPreview, error)
	// ListMyInvitations returns the currently-acceptable invitations addressed to
	// the authenticated user's own account email, across all workspaces (U4 item 6)
	// — the post-OAuth / zero-workspace "you've been invited to X" consent surface.
	// Soft-deleted workspaces and expired/revoked invites are excluded.
	ListMyInvitations(ctx context.Context, userID uuid.UUID) ([]IncomingInvitation, error)
	// AcceptMyInvitation accepts one of the caller's own pending invitations (by id,
	// authorized by matching the invite's email to the caller's account email) and
	// returns the joined org so the handler can mint a session (U4 item 6). Unlike
	// token-based AcceptInvite it never creates a user or sets a password — the
	// account already exists and is authenticated.
	AcceptMyInvitation(ctx context.Context, userID, invitationID uuid.UUID) (uuid.UUID, error)
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
	// GetMemberDetail returns one member's drawer payload (U4): identity + groups
	// + owned-record counts + live sessions. 404s a non-member.
	GetMemberDetail(ctx context.Context, orgID, targetUserID uuid.UUID) (*MemberDetail, error)
	// ForceSignOutMember revokes ALL of a member's sessions and bumps their token
	// version so their access tokens die immediately (U4) — the admin "sign this
	// person out everywhere" button. Refuses to target the caller (use your own
	// sessions UI) and the owner.
	ForceSignOutMember(ctx context.Context, orgID, callerUserID, targetUserID uuid.UUID) error
	// GetCurrentWorkspace returns the Workspace General page payload (U4).
	GetCurrentWorkspace(ctx context.Context, orgID, callerUserID uuid.UUID) (*WorkspaceDetail, error)
	// UpdateWorkspace writes the editable workspace fields (name + defaults) (U4).
	// org.settings-gated at the route; a blank name is rejected here.
	UpdateWorkspace(ctx context.Context, orgID uuid.UUID, in UpdateWorkspaceInput) error
	// LeaveWorkspace removes the caller's OWN membership (U4). Guarded: the sole
	// owner can't leave (they must transfer ownership or delete the workspace
	// first), so an org can never be orphaned ownerless.
	LeaveWorkspace(ctx context.Context, orgID, callerUserID uuid.UUID) error
	// DeleteWorkspace soft-deletes the whole workspace (U4). Owner-only, verified
	// inside (not just at the route).
	DeleteWorkspace(ctx context.Context, orgID, callerUserID uuid.UUID) error
}

type RemoveMemberInput struct {
	ReassignToUserID *uuid.UUID `json:"reassign_to_user_id,omitempty"`
	Strategy         string     `json:"strategy,omitempty"` // "transfer" or "unassign"
}

type Mailer interface {
	// SendInvite emails a workspace invitation. orgName is the workspace's real
	// display name (U0.7 — it used to render the org UUID); inviterName may be
	// empty when the inviter couldn't be resolved.
	SendInvite(ctx context.Context, to, inviteLink, orgName, inviterName string) error
	// SendPasswordReset / SendVerification email a one-time action link (P1).
	SendPasswordReset(ctx context.Context, to, resetLink string) error
	SendVerification(ctx context.Context, to, verifyLink string) error
	// SendSecurityAlert notifies a user of a sensitive account change (e.g. a
	// password reset) so an unexpected change is noticed quickly.
	SendSecurityAlert(ctx context.Context, to, subject, message string) error
	// SendNotification emails a single in-app notification to a member who has the
	// email channel enabled for its type with immediate delivery (U5). link is an
	// absolute URL (frontend origin + the notification's in-app path); may be empty.
	SendNotification(ctx context.Context, to, title, body, link string) error
	// SendNotificationDigest emails a member one daily digest summarizing their
	// recent unread notifications (U5). items is non-empty and pre-ordered.
	SendNotificationDigest(ctx context.Context, to string, items []NotificationDigestItem) error
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
	FirstName   *string    `json:"first_name"`
	LastName    *string    `json:"last_name"`
	Email       *string    `json:"email"`
	Phone       *string    `json:"phone"`
	CompanyID   *uuid.UUID `json:"company_id"`
	OwnerUserID *uuid.UUID `json:"owner_user_id"`
	// ClearOwner unassigns the record (U6.3). A nil OwnerUserID means "not supplied",
	// so without this flag an owner could be set but never removed — the picker's
	// "Unassigned" option would silently do nothing.
	ClearOwner   bool         `json:"clear_owner"`
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
	// ClearOwner unassigns the deal (U6.3) — see UpdateContactInput.
	ClearOwner      bool    `json:"clear_owner"`
	ExpectedCloseAt *string `json:"expected_close_at"`
	CustomFields    *JSON   `json:"custom_fields"`
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
	// OwnerUserID assigns the record on create (U6.3). nil defaults to the creator,
	// so a row-scoped user's own record does not vanish from their view the moment
	// they make it.
	OwnerUserID *uuid.UUID `json:"owner_user_id"`
}

type UpdateRecordInput struct {
	Data        JSON    `json:"data"`
	DisplayName *string `json:"display_name"`
	// OwnerUserID reassigns the record. nil means "not supplied — leave as is",
	// which is why clearing an owner needs its own flag: with a pointer alone,
	// "unassign" and "don't touch" are the same value on the wire.
	OwnerUserID *uuid.UUID `json:"owner_user_id"`
	ClearOwner  bool       `json:"clear_owner"`
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

	// The record methods take the object SLUG as well as the def id because row
	// scope and record shares key off the slug (record_shares.record_type), and every
	// custom object multiplexes into the one custom_object_records table — the table
	// name cannot identify the object. Without the slug these queries cannot be
	// filtered, which is exactly why custom records were org-wide-visible before U6.3.
	ListRecords(ctx context.Context, orgID uuid.UUID, defID uuid.UUID, slug string, f RecordFilter) ([]CustomObjectRecord, int64, error)
	GetRecord(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) (*CustomObjectRecord, error)
	CreateRecord(ctx context.Context, r *CustomObjectRecord) error
	UpdateRecord(ctx context.Context, slug string, r *CustomObjectRecord) error
	SoftDeleteRecord(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) error
}

type CustomObjectUseCase interface {
	ListDefs(ctx context.Context, orgID uuid.UUID) ([]CustomObjectDef, error)
	GetDefBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*CustomObjectDef, error)
	CreateDef(ctx context.Context, orgID uuid.UUID, input CreateObjectDefInput) (*CustomObjectDef, error)
	UpdateDef(ctx context.Context, orgID uuid.UUID, slug string, input UpdateObjectDefInput) (*CustomObjectDef, error)
	DeleteDef(ctx context.Context, orgID uuid.UUID, slug string) error

	ListRecords(ctx context.Context, orgID uuid.UUID, slug string, f RecordFilter) ([]CustomObjectRecord, int64, error)
	GetRecord(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) (*CustomObjectRecord, error)
	CreateRecord(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, slug string, input CreateRecordInput) (*CustomObjectRecord, error)
	UpdateRecord(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID, input UpdateRecordInput) (*CustomObjectRecord, error)
	DeleteRecord(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) error
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

