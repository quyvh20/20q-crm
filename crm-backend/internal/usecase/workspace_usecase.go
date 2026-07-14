package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	// inviteTokenDuration is how long a fresh (or resent) invite link is valid.
	inviteTokenDuration = 7 * 24 * time.Hour
	// adminResetLinkDailyCap bounds how many admin-sent reset links a single
	// target can receive per day, across ALL workspaces they belong to — the
	// cross-org anti-harassment guard (P2).
	adminResetLinkDailyCap = 3
)

// roleCapReader reports a role's capability codes — enough for the workspace
// usecase to enforce escalation guard #2 (P6) without depending on the whole
// RoleRepository. roleRepository satisfies it.
type roleCapReader interface {
	GetCapabilities(ctx context.Context, roleID uuid.UUID) ([]string, error)
}

// offboardStore executes the data side of removing a member (U0.2): count,
// transfer, or release the records they own, and revoke their org-scoped
// grants. repository.OffboardRepository satisfies it. Nil in unit tests →
// RemoveMember behaves as if the member owns nothing; main.go always wires it.
type offboardStore interface {
	CountOwnedRecords(ctx context.Context, orgID, userID uuid.UUID) (contacts, deals, custom int64, err error)
	ReassignOwnedRecords(ctx context.Context, orgID, fromUserID, toUserID uuid.UUID) error
	UnassignOwnedRecords(ctx context.Context, orgID, userID uuid.UUID) error
	RevokeUserGrants(ctx context.Context, orgID, userID uuid.UUID) error
}

// groupMembershipReader resolves which groups a member belongs to, for the
// member detail drawer (U4). userGroupRepository satisfies it. Nil in unit tests
// → GetMemberDetail returns no groups; main.go always wires it.
type groupMembershipReader interface {
	GroupIDsForUser(ctx context.Context, orgID, userID uuid.UUID) ([]uuid.UUID, error)
	List(ctx context.Context, orgID uuid.UUID) ([]domain.UserGroupView, error)
	// TeammateIDs backs ListTeammates — the assignee list a 'team'-scoped user may
	// pick from (U6.1).
	TeammateIDs(ctx context.Context, orgID, userID uuid.UUID) ([]uuid.UUID, error)
}

type workspaceUseCase struct {
	authRepo    domain.AuthRepository
	mailer      domain.Mailer
	appEnv      string
	frontendURL string
	// sessions evicts the middleware's per-(user, org) session cache so a
	// membership change (role/suspend/remove/transfer) takes effect on the
	// target's NEXT request, not after the 5-minute cache TTL (P10 P0). May be
	// nil in tests — evictSession no-ops.
	sessions SessionEvictor
	// caps + roleCaps back escalation guard #2 (P6): assigning/inviting into a role
	// that itself holds roles.manage or members.manage requires the CALLER to hold
	// roles.manage. caps checks the caller (owner bypasses); roleCaps reads the
	// TARGET role's capabilities. Both nil in unit tests → the guard no-ops; main.go
	// always wires them.
	caps     domain.CapabilityChecker
	roleCaps roleCapReader
	// offboard reassigns/releases a departing member's owned records and revokes
	// their grants (U0.2). Nil in unit tests; main.go always wires it.
	offboard offboardStore
	// groups resolves a member's group membership for the detail drawer (U4).
	// Nil in unit tests; main.go always wires it.
	groups groupMembershipReader
}

func NewWorkspaceUseCase(authRepo domain.AuthRepository, mailer domain.Mailer, appEnv string, frontendURL string, sessions SessionEvictor, caps domain.CapabilityChecker, roleCaps roleCapReader, offboard offboardStore, groups groupMembershipReader) domain.WorkspaceUseCase {
	return &workspaceUseCase{
		authRepo:    authRepo,
		mailer:      mailer,
		appEnv:      appEnv,
		frontendURL: frontendURL,
		sessions:    sessions,
		caps:        caps,
		roleCaps:    roleCaps,
		offboard:    offboard,
		groups:      groups,
	}
}

// resolveAssignableRole loads a role by id and confirms it is usable by the org —
// a global system role (org_id NULL) or one of the org's own custom roles, never
// another tenant's role (P6 role_id member APIs). A miss / cross-tenant id is a
// 400 "invalid role".
func (uc *workspaceUseCase) resolveAssignableRole(ctx context.Context, orgID, roleID uuid.UUID) (*domain.Role, error) {
	role, err := uc.authRepo.GetRoleByID(ctx, roleID)
	if err != nil || role == nil {
		return nil, domain.NewAppError(400, "invalid role")
	}
	if role.OrgID != nil && *role.OrgID != orgID {
		return nil, domain.NewAppError(400, "invalid role")
	}
	return role, nil
}

// guardRoleAssignment enforces escalation guard #2 (plan §3.2): a caller may only
// assign/invite into a role holding roles.manage or members.manage if the caller
// themselves holds roles.manage (the owner bypasses via HasCapability). No-op when
// the guard deps aren't wired (unit tests).
func (uc *workspaceUseCase) guardRoleAssignment(ctx context.Context, orgID uuid.UUID, role *domain.Role) error {
	if uc.caps == nil || uc.roleCaps == nil {
		return nil
	}
	codes, err := uc.roleCaps.GetCapabilities(ctx, role.ID)
	if err != nil {
		return domain.ErrInternal
	}
	privileged := false
	for _, c := range codes {
		if c == domain.CapRolesManage || c == domain.CapMembersManage {
			privileged = true
			break
		}
	}
	if !privileged {
		return nil
	}
	if err := uc.caps.HasCapability(ctx, orgID, domain.CapRolesManage); err != nil {
		return domain.NewAppError(403, "assigning a role that can manage roles or members requires the \""+domain.CapabilityLabel(domain.CapRolesManage)+"\" permission")
	}
	return nil
}

func (uc *workspaceUseCase) evictSession(ctx context.Context, userID, orgID uuid.UUID) {
	if uc.sessions == nil {
		return
	}
	uc.sessions.EvictOrgSession(ctx, userID, orgID)
}

func (uc *workspaceUseCase) ListMembers(ctx context.Context, orgID uuid.UUID) ([]domain.MemberInfo, error) {
	orgUsers, err := uc.authRepo.ListMembersByOrgID(ctx, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	// Batch the "last active" lookup for the whole member set — one GROUP BY, not
	// a query per row (U4). Absent = no live session.
	userIDs := make([]uuid.UUID, 0, len(orgUsers))
	for _, ou := range orgUsers {
		userIDs = append(userIDs, ou.UserID)
	}
	lastActive, err := uc.authRepo.LatestSessionActivityByUsers(ctx, userIDs)
	if err != nil {
		return nil, domain.ErrInternal
	}

	members := make([]domain.MemberInfo, 0, len(orgUsers))
	for _, ou := range orgUsers {
		roleName := "viewer"
		if ou.Role != nil {
			roleName = ou.Role.Name
		}
		m := domain.MemberInfo{
			UserID:   ou.UserID,
			RoleID:   ou.RoleID,
			Role:     roleName,
			Status:   ou.Status,
			JoinedAt: ou.JoinedAt,
		}
		if ou.User != nil {
			m.Email = ou.User.Email
			m.FirstName = ou.User.FirstName
			m.LastName = ou.User.LastName
			m.FullName = ou.User.FullName
			m.AvatarURL = ou.User.AvatarURL
			m.EmailVerified = ou.User.EmailVerifiedAt != nil
			m.TwoFactorEnabled = ou.User.TotpEnabledAt != nil
		}
		if t, ok := lastActive[ou.UserID]; ok {
			tt := t
			m.LastActiveAt = &tt
		}
		members = append(members, m)
	}
	return members, nil
}

// ListTeammates returns the active members the caller shares a team (group) with —
// the exact set a 'team'-scoped role may assign records to (U6.1).
//
// It is derived from the SAME self-join the row predicate uses, so the assignee
// picker cannot offer someone whose records the caller would then be unable to see.
// A caller in no group is their own only teammate.
func (uc *workspaceUseCase) ListTeammates(ctx context.Context, orgID, userID uuid.UUID) ([]domain.MemberInfo, error) {
	all, err := uc.ListMembers(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if uc.groups == nil {
		return all, nil
	}
	ids, err := uc.groups.TeammateIDs(ctx, orgID, userID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	allowed := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	out := make([]domain.MemberInfo, 0, len(ids))
	for _, m := range all {
		if allowed[m.UserID] && m.Status == domain.StatusActive {
			out = append(out, m)
		}
	}
	return out, nil
}

// GetMemberDetail assembles the member drawer payload (U4): identity + groups +
// owned-record counts + live sessions. 404s a non-member of this org.
func (uc *workspaceUseCase) GetMemberDetail(ctx context.Context, orgID, targetUserID uuid.UUID) (*domain.MemberDetail, error) {
	ou, err := uc.authRepo.GetOrgUser(ctx, targetUserID, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if ou == nil {
		return nil, domain.ErrNotMember
	}
	user, err := uc.authRepo.GetUserByID(ctx, targetUserID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}

	roleName := "viewer"
	if ou.Role != nil {
		roleName = ou.Role.Name
	}
	member := domain.MemberInfo{
		UserID: user.ID, Email: user.Email, FirstName: user.FirstName, LastName: user.LastName,
		FullName: user.FullName, AvatarURL: user.AvatarURL,
		RoleID: ou.RoleID, Role: roleName, Status: ou.Status, JoinedAt: ou.JoinedAt,
		EmailVerified: user.EmailVerifiedAt != nil,
	}

	detail := &domain.MemberDetail{Member: member, Groups: []domain.MemberGroup{}, Sessions: []domain.SessionInfo{}}

	// Owned-record counts (the offboarding preview). Best-effort — a counting
	// failure shouldn't blank the whole drawer.
	if uc.offboard != nil {
		if contacts, deals, custom, err := uc.offboard.CountOwnedRecords(ctx, orgID, targetUserID); err == nil {
			detail.OwnedContacts, detail.OwnedDeals, detail.OwnedCustom = contacts, deals, custom
		}
	}

	// Groups the member belongs to (names resolved from the org's group list).
	if uc.groups != nil {
		if ids, err := uc.groups.GroupIDsForUser(ctx, orgID, targetUserID); err == nil && len(ids) > 0 {
			if all, err := uc.groups.List(ctx, orgID); err == nil {
				nameByID := make(map[uuid.UUID]string, len(all))
				for _, g := range all {
					nameByID[g.ID] = g.Name
				}
				for _, id := range ids {
					if name, ok := nameByID[id]; ok {
						detail.Groups = append(detail.Groups, domain.MemberGroup{ID: id, Name: name})
					}
				}
			}
		}
	}

	// Live sessions for the force-sign-out list.
	if tokens, err := uc.authRepo.ListActiveRefreshTokens(ctx, targetUserID); err == nil {
		for _, t := range tokens {
			s := domain.SessionInfo{ID: t.ID, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt}
			if t.DeviceLabel != nil {
				s.DeviceLabel = *t.DeviceLabel
			}
			if t.IP != nil {
				s.IP = *t.IP
			}
			detail.Sessions = append(detail.Sessions, s)
		}
	}
	return detail, nil
}

// ForceSignOutMember is the admin "sign this person out everywhere" action (U4):
// revoke all their refresh tokens AND bump their token version so outstanding
// access tokens die immediately, then evict this org's cached session. Refuses
// to target the caller (self-service lives in the personal sessions UI) or the
// owner (protected like every other admin action against the owner).
func (uc *workspaceUseCase) ForceSignOutMember(ctx context.Context, orgID, callerUserID, targetUserID uuid.UUID) error {
	if callerUserID == targetUserID {
		return domain.NewAppError(400, "use your own device list to sign yourself out")
	}
	ou, err := uc.authRepo.GetOrgUser(ctx, targetUserID, orgID)
	if err != nil {
		return domain.ErrInternal
	}
	if ou == nil {
		return domain.ErrNotMember
	}
	if ou.Role != nil && domain.IsOwnerRole(ou.Role) {
		return domain.NewAppError(403, "the workspace owner can't be force–signed-out")
	}

	if err := uc.authRepo.RevokeAllUserRefreshTokens(ctx, targetUserID); err != nil {
		return domain.ErrInternal
	}
	if err := uc.authRepo.IncrementUserTokenVersion(ctx, targetUserID); err != nil {
		return domain.ErrInternal
	}
	uc.evictSession(ctx, targetUserID, orgID)

	actor := callerUserID
	recordAdminEvent(ctx, uc.authRepo, orgID, "member.force_signed_out", &actor,
		map[string]interface{}{"target_user_id": targetUserID.String()})
	return nil
}

// GetCurrentWorkspace returns the Workspace General page payload (U4): the org's
// identity + defaults, its active-member count, and whether the caller owns it.
func (uc *workspaceUseCase) GetCurrentWorkspace(ctx context.Context, orgID, callerUserID uuid.UUID) (*domain.WorkspaceDetail, error) {
	org, err := uc.authRepo.GetOrganizationByID(ctx, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if org == nil {
		return nil, domain.NewAppError(404, "workspace not found")
	}
	members, err := uc.authRepo.ListMembersByOrgID(ctx, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	var active int64
	for _, m := range members {
		if m.Status != domain.StatusDeleted {
			active++
		}
	}
	isOwner := false
	if ou, err := uc.authRepo.GetOrgUser(ctx, callerUserID, orgID); err == nil && ou != nil {
		isOwner = domain.IsOwnerRole(ou.Role)
	}
	return &domain.WorkspaceDetail{
		ID: org.ID, Name: org.Name, Type: org.Type,
		Currency: org.Currency, Locale: org.Locale, Timezone: org.Timezone,
		RequireTwoFactor: org.RequireTwoFactor,
		MemberCount:      active, IsOwner: isOwner, CreatedAt: org.CreatedAt,
	}, nil
}

// UpdateWorkspace writes the editable workspace fields (U4). Only provided
// (non-nil) fields change; a name, if provided, must be non-blank.
func (uc *workspaceUseCase) UpdateWorkspace(ctx context.Context, orgID uuid.UUID, in domain.UpdateWorkspaceInput) error {
	org, err := uc.authRepo.GetOrganizationByID(ctx, orgID)
	if err != nil {
		return domain.ErrInternal
	}
	if org == nil {
		return domain.NewAppError(404, "workspace not found")
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return domain.NewAppError(400, "workspace name can't be empty")
		}
		org.Name = name
	}
	if in.Currency != nil {
		org.Currency = strings.TrimSpace(*in.Currency)
	}
	if in.Locale != nil {
		org.Locale = strings.TrimSpace(*in.Locale)
	}
	if in.Timezone != nil {
		org.Timezone = strings.TrimSpace(*in.Timezone)
	}
	// The 2FA policy (U6.4). Turning it ON confines every member who hasn't enrolled
	// to the enrollment screen on their next token mint — that is the intended teeth,
	// and the FE warns before saving.
	if in.RequireTwoFactor != nil {
		org.RequireTwoFactor = *in.RequireTwoFactor
	}
	if err := uc.authRepo.UpdateOrganization(ctx, org); err != nil {
		return domain.ErrInternal
	}
	recordAdminEvent(ctx, uc.authRepo, orgID, "workspace.updated", nil,
		map[string]interface{}{"name": org.Name, "require_two_factor": org.RequireTwoFactor})
	return nil
}

// countActiveOwners returns how many ACTIVE members of the org hold the owner
// role — the last-owner guard for leave/transfer/delete.
func (uc *workspaceUseCase) countActiveOwners(ctx context.Context, orgID uuid.UUID) (int, error) {
	members, err := uc.authRepo.ListMembersByOrgID(ctx, orgID)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range members {
		if members[i].Status == domain.StatusActive && members[i].Role != nil && domain.IsOwnerRole(members[i].Role) {
			n++
		}
	}
	return n, nil
}

// LeaveWorkspace removes the caller's own membership (U4). The sole owner can't
// leave — they'd orphan an ownerless org; they must transfer ownership or delete
// the workspace first.
func (uc *workspaceUseCase) LeaveWorkspace(ctx context.Context, orgID, callerUserID uuid.UUID) error {
	ou, err := uc.authRepo.GetOrgUser(ctx, callerUserID, orgID)
	if err != nil {
		return domain.ErrInternal
	}
	if ou == nil {
		return domain.ErrNotMember
	}
	if domain.IsOwnerRole(ou.Role) {
		owners, err := uc.countActiveOwners(ctx, orgID)
		if err != nil {
			return domain.ErrInternal
		}
		if owners <= 1 {
			return domain.NewAppError(409, "you're the only owner — transfer ownership to someone else or delete the workspace before leaving")
		}
	}
	if err := uc.authRepo.DeleteOrgUser(ctx, callerUserID, orgID); err != nil {
		return domain.ErrInternal
	}
	uc.evictSession(ctx, callerUserID, orgID)
	actor := callerUserID
	recordAdminEvent(ctx, uc.authRepo, orgID, "member.left", &actor, nil)
	return nil
}

// DeleteWorkspace soft-deletes the whole workspace (U4). Owner-only, verified
// here (not just at the route). Membership resolution excludes soft-deleted
// orgs, so every member's session falls back to another workspace (or the
// zero-workspace page) on their next request.
func (uc *workspaceUseCase) DeleteWorkspace(ctx context.Context, orgID, callerUserID uuid.UUID) error {
	ou, err := uc.authRepo.GetOrgUser(ctx, callerUserID, orgID)
	if err != nil {
		return domain.ErrInternal
	}
	if ou == nil || !domain.IsOwnerRole(ou.Role) {
		return domain.NewAppError(403, "only the workspace owner can delete it")
	}
	if err := uc.authRepo.SoftDeleteOrganization(ctx, orgID); err != nil {
		return domain.ErrInternal
	}
	uc.evictSession(ctx, callerUserID, orgID)
	actor := callerUserID
	recordAdminEvent(ctx, uc.authRepo, orgID, "workspace.deleted", &actor, nil)
	return nil
}

func hashInviteToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func (uc *workspaceUseCase) InviteMember(ctx context.Context, orgID uuid.UUID, input domain.InviteMemberInput) (*domain.MemberInfo, *string, error) {
	input.Email = normalizeEmail(input.Email)
	role, err := uc.resolveAssignableRole(ctx, orgID, input.RoleID)
	if err != nil {
		return nil, nil, err
	}
	// Ownership is granted only through the transfer-ownership flow. Without
	// this, anyone holding members.invite could mint a god-mode member.
	if domain.IsOwnerRole(role) {
		return nil, nil, domain.NewAppError(403, "the owner role is granted via ownership transfer, not invites")
	}
	// Escalation guard #2: inviting into a role that can manage roles/members
	// requires the caller to hold roles.manage (P6).
	if err := uc.guardRoleAssignment(ctx, orgID, role); err != nil {
		return nil, nil, err
	}

	existing, err := uc.authRepo.GetOrgUserByEmail(ctx, input.Email, orgID)
	if err != nil && err.Error() != "record not found" {
		return nil, nil, domain.NewAppError(500, "GetOrgUser error: "+err.Error())
	}
	if existing != nil && existing.Status != domain.StatusDeleted {
		return nil, nil, domain.ErrAlreadyMember
	}

	// 256-bit CSPRNG token (P2) — uuid.New() carried only ~122 bits. The hash
	// lookup on accept is agnostic to the raw format, so old outstanding
	// uuid-based invites keep working through the 7-day overlap.
	rawToken, err := generateSecureToken()
	if err != nil {
		return nil, nil, domain.ErrInternal
	}
	tokenHash := hashInviteToken(rawToken)
	now := time.Now()

	// Dedupe (U4): a still-open invite for this email — expired or not — is
	// re-minted in place (fresh token, extended expiry, possibly a new role)
	// rather than stacked as a second row, so the invitee has exactly one live
	// link and the panel shows one entry. A brand-new email inserts as before.
	if existingInv, err := uc.authRepo.GetPendingInvitationByEmail(ctx, orgID, input.Email); err == nil && existingInv != nil {
		existingInv.RoleID = role.ID
		existingInv.TokenHash = tokenHash
		existingInv.ExpiresAt = now.Add(inviteTokenDuration)
		existingInv.ResentAt = &now
		if err := uc.authRepo.UpdateOrgInvitation(ctx, existingInv); err != nil {
			return nil, nil, domain.NewAppError(500, "UpdateOrgInvitation error: "+err.Error())
		}
	} else {
		inv := &domain.OrgInvitation{
			Email:     input.Email,
			OrgID:     orgID,
			RoleID:    role.ID,
			TokenHash: tokenHash,
			ExpiresAt: now.Add(inviteTokenDuration),
			Status:    "pending",
		}
		if err := uc.authRepo.CreateOrgInvitation(ctx, inv); err != nil {
			return nil, nil, domain.NewAppError(500, "CreateOrgInvitation error: "+err.Error())
		}
	}

	uc.sendInviteEmail(ctx, input.Email, rawToken, orgID)

	var debugToken *string
	if debugTokensEnabled(uc.appEnv) {
		debugToken = &rawToken
	}

	recordAdminEvent(ctx, uc.authRepo, orgID, "member.invited", nil,
		map[string]interface{}{"email": input.Email, "role": role.Name})

	return &domain.MemberInfo{
		Email:  input.Email,
		RoleID: role.ID,
		Role:   role.Name,
		Status: domain.StatusInvited,
	}, debugToken, nil
}

// sendInviteEmail fires the invitation email off the request path on a detached
// context (the fire-and-forget lesson): the invitation row is committed, so a
// slow or failed send must not 500 the invite — resend is the retry path. The
// workspace/inviter names are resolved synchronously on the request context
// (U0.7 — the subject used to greet invitees with the org's raw UUID); the send
// itself stays fire-and-forget.
func (uc *workspaceUseCase) sendInviteEmail(ctx context.Context, email, rawToken string, orgID uuid.UUID) {
	link := fmt.Sprintf("%s/accept-invite?token=%s", uc.frontendURL, rawToken)
	orgName, inviterName := uc.inviteEmailNames(ctx, orgID)
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := uc.mailer.SendInvite(bg, email, link, orgName, inviterName); err != nil {
			log.Printf("invite: failed to send invitation email to %s: %v", email, err)
		}
	}()
}

// inviteEmailNames resolves the workspace display name and the inviter's name
// for the invite email. Failures degrade to a generic phrase / empty inviter —
// never to a UUID.
func (uc *workspaceUseCase) inviteEmailNames(ctx context.Context, orgID uuid.UUID) (orgName, inviterName string) {
	orgName = "your team's workspace"
	if org, err := uc.authRepo.GetOrganizationByID(ctx, orgID); err == nil && org != nil && strings.TrimSpace(org.Name) != "" {
		orgName = org.Name
	}
	if caller, ok := domain.CallerFromContext(ctx); ok {
		if u, err := uc.authRepo.GetUserByID(ctx, caller.UserID); err == nil && u != nil {
			inviterName = strings.TrimSpace(u.FullName)
			if inviterName == "" {
				inviterName = strings.TrimSpace(u.FirstName + " " + u.LastName)
			}
		}
	}
	return orgName, inviterName
}

// AcceptInvite joins the invitee to the org in ONE transaction (P2): it UPSERTs
// the org_users row (reinstating a previously-removed/suspended member instead
// of blind-inserting against a possible tombstone) and, for a brand-new non-OAuth
// invitee, sets the password they chose on the accept page — so an invited user
// is no longer created PASSWORDLESS with no way in. An existing account (or one
// that will "Continue with Google") accepts without a password.
func (uc *workspaceUseCase) AcceptInvite(ctx context.Context, input domain.AcceptInviteInput) (*domain.AcceptInviteResult, error) {
	tokenHash := hashInviteToken(input.Token)
	inv, err := uc.authRepo.GetOrgInvitationByTokenHash(ctx, tokenHash)
	if err != nil || inv == nil {
		return nil, domain.NewAppError(400, "this invitation link is invalid or has expired")
	}
	if inv.Status != "pending" || inv.RevokedAt != nil || time.Now().After(inv.ExpiresAt) {
		return nil, domain.NewAppError(400, "this invitation link is no longer valid")
	}

	// A supplied password must pass policy BEFORE any write, so a weak password
	// can't leave a half-created account behind.
	var newPasswordHash *string
	if input.Password != "" {
		if err := validatePassword(input.Password); err != nil {
			return nil, err
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcryptCost)
		if err != nil {
			return nil, domain.ErrInternal
		}
		s := string(hash)
		newPasswordHash = &s
	}

	user, err := uc.authRepo.GetUserByEmail(ctx, inv.Email)
	if err != nil {
		return nil, domain.ErrInternal
	}

	createUser := user == nil
	if createUser {
		first := input.FirstName
		if first == "" {
			first = inv.Email
		}
		fullName := first
		if input.LastName != "" {
			fullName = first + " " + input.LastName
		}
		user = &domain.User{
			OrgID:        inv.OrgID,
			Email:        normalizeEmail(inv.Email),
			FirstName:    first,
			LastName:     input.LastName,
			FullName:     fullName,
			PasswordHash: newPasswordHash, // may be nil (Google-only invitee)
		}
	} else if user.PasswordHash != nil {
		// Existing account already has a password — never overwrite it from the
		// invite-accept form (that would be an account-takeover primitive). The
		// password field is ignored; membership is still (re)granted.
		newPasswordHash = nil
	}

	if err := uc.authRepo.AcceptInvitation(ctx, inv, user, createUser, newPasswordHash); err != nil {
		return nil, domain.ErrInternal
	}

	recordAdminEvent(ctx, uc.authRepo, inv.OrgID, "member.invite_accepted", &user.ID,
		map[string]interface{}{"email": inv.Email})
	// Auto-login only a brand-new account (createUser): an existing account is
	// re-authenticated normally, so a leaked invite link can't mint a session over
	// a whole pre-existing multi-workspace account (U4 review).
	return &domain.AcceptInviteResult{UserID: user.ID, OrgID: inv.OrgID, AutoLogin: createUser}, nil
}

// GetInvitationPreview resolves a raw invite token to the public accept-page
// metadata without consuming it (U4). A bad/unknown token is not an error — it
// returns Status "invalid" so the page shows a clean "link is invalid" state
// rather than a 500. Expired/revoked/accepted are distinguished so the page can
// tailor its copy (and offer a Resend hint for expired).
func (uc *workspaceUseCase) GetInvitationPreview(ctx context.Context, token string) (*domain.InvitationPreview, error) {
	if strings.TrimSpace(token) == "" {
		return &domain.InvitationPreview{Status: "invalid"}, nil
	}
	inv, err := uc.authRepo.GetOrgInvitationByTokenHash(ctx, hashInviteToken(token))
	if err != nil || inv == nil {
		return &domain.InvitationPreview{Status: "invalid"}, nil
	}

	status := "valid"
	switch {
	case inv.RevokedAt != nil || inv.Status == "revoked":
		status = "revoked"
	case inv.Status == "accepted":
		status = "accepted"
	case inv.Status != "pending":
		status = "invalid"
	case time.Now().After(inv.ExpiresAt):
		status = "expired"
	}

	preview := &domain.InvitationPreview{
		Email:   inv.Email,
		OrgName: "your team's workspace",
		Status:  status,
	}
	if org, err := uc.authRepo.GetOrganizationByID(ctx, inv.OrgID); err == nil && org != nil && strings.TrimSpace(org.Name) != "" {
		preview.OrgName = org.Name
	}
	if role, err := uc.authRepo.GetRoleByID(ctx, inv.RoleID); err == nil && role != nil {
		preview.RoleName = role.Name
	}
	if user, err := uc.authRepo.GetUserByEmail(ctx, inv.Email); err == nil && user != nil {
		preview.HasAccount = true
	}
	return preview, nil
}

// ListMyInvitations returns the currently-acceptable invitations addressed to the
// authenticated user's own account email, across all workspaces (U4 item 6) — the
// post-OAuth / zero-workspace "you've been invited to X" consent surface. The repo
// query already excludes expired/revoked invites and soft-deleted workspaces; org
// and role names are resolved here for display.
func (uc *workspaceUseCase) ListMyInvitations(ctx context.Context, userID uuid.UUID) ([]domain.IncomingInvitation, error) {
	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if user == nil {
		return nil, domain.ErrUserNotFound
	}
	// Consent-by-email requires a PROVEN email. A brand-new signup is active but
	// email-unverified (soft-gate), and anyone can register a not-yet-taken address
	// without controlling that inbox — so surfacing (and later accepting) invites by
	// a bare email match would let such an account hijack someone else's invite.
	// Google-first invitees are created verified, so they pass; an unverified account
	// simply has no consent-acceptable invites here and must use the emailed link
	// (whose single-use token is itself the proof of inbox control).
	if user.EmailVerifiedAt == nil {
		return []domain.IncomingInvitation{}, nil
	}
	invs, err := uc.authRepo.ListValidInvitationsByEmail(ctx, user.Email)
	if err != nil {
		return nil, domain.ErrInternal
	}
	out := make([]domain.IncomingInvitation, 0, len(invs))
	for i := range invs {
		inv := invs[i]
		org, err := uc.authRepo.GetOrganizationByID(ctx, inv.OrgID)
		if err != nil || org == nil {
			continue // workspace vanished between the JOIN and here — skip defensively
		}
		roleName := ""
		if role, err := uc.authRepo.GetRoleByID(ctx, inv.RoleID); err == nil && role != nil {
			roleName = role.Name
		}
		out = append(out, domain.IncomingInvitation{
			ID:        inv.ID,
			OrgID:     inv.OrgID,
			OrgName:   org.Name,
			RoleName:  roleName,
			ExpiresAt: inv.ExpiresAt,
		})
	}
	return out, nil
}

// AcceptMyInvitation accepts one of the caller's OWN pending invitations by id (U4
// item 6). Authorization is the email match: the invitation's addressee email must
// equal the authenticated account's email — the invite id is not a secret, so the
// email match (not id possession) is what grants the join. It reuses the same
// one-transaction AcceptInvitation as the link flow but never creates a user or sets
// a password (the account already exists and is authenticated). Returns the joined
// org so the handler can mint a session scoped to it.
func (uc *workspaceUseCase) AcceptMyInvitation(ctx context.Context, userID, invitationID uuid.UUID) (uuid.UUID, error) {
	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil {
		return uuid.Nil, domain.ErrInternal
	}
	if user == nil {
		return uuid.Nil, domain.ErrUserNotFound
	}
	// The email match is the whole authorization, so it is only sound when the
	// account's email is PROVEN. An unverified account (a password signup that never
	// confirmed its inbox) could otherwise claim any not-yet-registered address and
	// hijack an invite addressed to it — the emailed-link flow proves inbox control
	// via its single-use token; this by-email flow proves it via a verified email.
	// Google-first invitees are created verified and pass.
	if user.EmailVerifiedAt == nil {
		return uuid.Nil, domain.NewAppError(403, "verify your email to accept invitations here, or open the link in your invitation email")
	}
	inv, err := uc.authRepo.GetOrgInvitationByIDUnscoped(ctx, invitationID)
	if err != nil {
		return uuid.Nil, domain.ErrInternal
	}
	if inv == nil {
		return uuid.Nil, domain.NewAppError(404, "invitation not found")
	}
	if !strings.EqualFold(normalizeEmail(inv.Email), normalizeEmail(user.Email)) {
		return uuid.Nil, domain.NewAppError(403, "this invitation was not sent to your account")
	}
	if inv.Status != "pending" || inv.RevokedAt != nil || time.Now().After(inv.ExpiresAt) {
		return uuid.Nil, domain.NewAppError(400, "this invitation is no longer valid")
	}
	// The workspace must still exist — GetOrganizationByID returns nil for a
	// soft-deleted org, so a deleted workspace's stale invite can't be accepted.
	if org, err := uc.authRepo.GetOrganizationByID(ctx, inv.OrgID); err != nil || org == nil {
		return uuid.Nil, domain.NewAppError(400, "this workspace no longer exists")
	}
	if err := uc.authRepo.AcceptInvitation(ctx, inv, user, false, nil); err != nil {
		return uuid.Nil, domain.ErrInternal
	}
	recordAdminEvent(ctx, uc.authRepo, inv.OrgID, "member.invite_accepted", &user.ID,
		map[string]interface{}{"email": inv.Email, "via": "consent"})
	return inv.OrgID, nil
}

// ListInvitations returns the org's still-actionable (pending) invitations for
// the members panel (P2).
func (uc *workspaceUseCase) ListInvitations(ctx context.Context, orgID uuid.UUID) ([]domain.InvitationInfo, error) {
	// Open (not just unexpired) invitations, so an expired invite is shown with a
	// Resend badge instead of vanishing (U4). Status is computed to "expired" when
	// past its window; the row stays 'pending' in the DB until resent or revoked.
	invites, err := uc.authRepo.ListOpenInvitations(ctx, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	now := time.Now()
	out := make([]domain.InvitationInfo, 0, len(invites))
	for i := range invites {
		inv := invites[i]
		roleName := ""
		if role, err := uc.authRepo.GetRoleByID(ctx, inv.RoleID); err == nil && role != nil {
			roleName = role.Name
		}
		status := inv.Status
		if status == "pending" && now.After(inv.ExpiresAt) {
			status = "expired"
		}
		out = append(out, domain.InvitationInfo{
			ID:        inv.ID,
			Email:     inv.Email,
			RoleID:    inv.RoleID,
			Role:      roleName,
			Status:    status,
			ExpiresAt: inv.ExpiresAt,
			CreatedAt: inv.CreatedAt,
			ResentAt:  inv.ResentAt,
		})
	}
	return out, nil
}

// ResendInvitation re-mints a fresh 256-bit token (extending the expiry) and
// re-sends the invite email (P2). The old token is invalidated by the new hash.
func (uc *workspaceUseCase) ResendInvitation(ctx context.Context, orgID, invitationID uuid.UUID) (*string, error) {
	inv, err := uc.authRepo.GetOrgInvitationByID(ctx, invitationID, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if inv == nil {
		return nil, domain.NewAppError(404, "invitation not found")
	}
	if inv.Status != "pending" || inv.RevokedAt != nil {
		return nil, domain.NewAppError(409, "only a pending invitation can be resent")
	}

	rawToken, err := generateSecureToken()
	if err != nil {
		return nil, domain.ErrInternal
	}
	now := time.Now()
	inv.TokenHash = hashInviteToken(rawToken)
	inv.ExpiresAt = now.Add(inviteTokenDuration)
	inv.ResentAt = &now
	if err := uc.authRepo.UpdateOrgInvitation(ctx, inv); err != nil {
		return nil, domain.ErrInternal
	}

	uc.sendInviteEmail(ctx, inv.Email, rawToken, orgID)
	recordAdminEvent(ctx, uc.authRepo, orgID, "member.invite_resent", nil,
		map[string]interface{}{"email": inv.Email})

	var debugToken *string
	if debugTokensEnabled(uc.appEnv) {
		debugToken = &rawToken
	}
	return debugToken, nil
}

// RevokeInvitation kills a pending invitation so its token can no longer be
// accepted (P2).
func (uc *workspaceUseCase) RevokeInvitation(ctx context.Context, orgID, invitationID uuid.UUID) error {
	inv, err := uc.authRepo.GetOrgInvitationByID(ctx, invitationID, orgID)
	if err != nil {
		return domain.ErrInternal
	}
	if inv == nil {
		return domain.NewAppError(404, "invitation not found")
	}
	if inv.Status != "pending" {
		return domain.NewAppError(409, "only a pending invitation can be revoked")
	}
	now := time.Now()
	inv.Status = "revoked"
	inv.RevokedAt = &now
	if err := uc.authRepo.UpdateOrgInvitation(ctx, inv); err != nil {
		return domain.ErrInternal
	}
	recordAdminEvent(ctx, uc.authRepo, orgID, "member.invite_revoked", nil,
		map[string]interface{}{"email": inv.Email})
	return nil
}

// SendMemberResetLink emails a target member a password-reset link on an admin's
// behalf (P2). Accounts are global (one users row across workspaces), so the
// admin never sees or sets the password — this only triggers the same self-serve
// email the user could request themselves. Guardrails: the target must be a
// member of the sending org; the self-serve per-email cooldown applies; and a
// per-target daily cap across all orgs blocks cross-workspace harassment. The
// raw token is never returned in any env.
func (uc *workspaceUseCase) SendMemberResetLink(ctx context.Context, orgID, callerUserID, targetUserID uuid.UUID, meta domain.RequestMeta) error {
	ou, err := uc.authRepo.GetOrgUser(ctx, targetUserID, orgID)
	if err != nil {
		return domain.ErrInternal
	}
	if ou == nil {
		return domain.ErrNotMember
	}

	user, err := uc.authRepo.GetUserByID(ctx, targetUserID)
	if err != nil || user == nil {
		return domain.ErrUserNotFound
	}

	// Per-target daily cap first: a shared user must be protected even if each
	// individual org stays under the cooldown.
	if n, err := uc.authRepo.CountAdminResetTokensSince(ctx, targetUserID, time.Now().Add(-24*time.Hour)); err == nil && n >= adminResetLinkDailyCap {
		return domain.NewAppError(429, "a reset link was already sent to this member several times today — please try again tomorrow")
	}

	// Per-email cooldown (shared with the self-serve flow): don't stack links.
	if latest, err := uc.authRepo.GetLatestPasswordResetToken(ctx, targetUserID); err == nil && latest != nil &&
		time.Since(latest.CreatedAt) < passwordResetRequestCooldown {
		return domain.NewAppError(429, "a reset link was just sent to this member — please wait a moment before sending another")
	}

	// Only the newest link may work.
	if err := uc.authRepo.VoidActivePasswordResetTokens(ctx, targetUserID); err != nil {
		return domain.ErrInternal
	}

	rawToken, err := generateSecureToken()
	if err != nil {
		return domain.ErrInternal
	}
	initiatedBy := callerUserID
	prt := &domain.PasswordResetToken{
		UserID:      targetUserID,
		TokenHash:   hashToken(rawToken),
		ExpiresAt:   time.Now().Add(passwordResetTokenDuration),
		InitiatedBy: &initiatedBy,
	}
	if err := uc.authRepo.CreatePasswordResetToken(ctx, prt); err != nil {
		return domain.ErrInternal
	}

	// Send off the request path; audit the send in the acting org AND write a
	// user-level (org-NULL) event the target can see in their own security log.
	link := fmt.Sprintf("%s/reset-password?token=%s", uc.frontendURL, rawToken)
	email := user.Email
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := uc.mailer.SendPasswordReset(bg, email, link); err != nil {
			log.Printf("admin reset-link: failed to send to %s: %v", email, err)
			uc.writeUserAuthEvent(bg, "reset_email_failed", nil, targetUserID, meta,
				map[string]interface{}{"reason": err.Error(), "initiated_by_admin": true})
		}
	}()

	recordAdminEvent(ctx, uc.authRepo, orgID, "password.reset_link_sent_by_admin", &targetUserID,
		map[string]interface{}{"target_email": email})
	uc.writeUserAuthEvent(ctx, "password.reset_link_received", &callerUserID, targetUserID, meta, nil)
	return nil
}

// writeUserAuthEvent records a user-level (org-NULL) security event about the
// target — visible to the target in their own account activity, independent of
// any one workspace. Best-effort, like the other audit writes.
func (uc *workspaceUseCase) writeUserAuthEvent(ctx context.Context, eventType string, actorID *uuid.UUID, targetID uuid.UUID, meta domain.RequestMeta, metadata map[string]interface{}) {
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		raw = []byte("{}")
	}
	e := &domain.AuthEvent{
		Category:  "security",
		EventType: eventType,
		ActorID:   actorID,
		TargetID:  &targetID,
		Metadata:  domain.JSON(raw),
	}
	if meta.IP != "" {
		ip := meta.IP
		e.IP = &ip
	}
	if meta.UserAgent != "" {
		ua := meta.UserAgent
		e.UserAgent = &ua
	}
	if err := uc.authRepo.WriteAuthEvent(ctx, e); err != nil {
		log.Printf("auth_events: failed to record security/%s: %v", eventType, err)
	}
}

func (uc *workspaceUseCase) UpdateMemberRole(ctx context.Context, orgID uuid.UUID, targetUserID uuid.UUID, input domain.UpdateMemberRoleInput) error {
	ou, err := uc.authRepo.GetOrgUser(ctx, targetUserID, orgID)
	if err != nil || ou == nil {
		return domain.ErrNotMember
	}

	oldRole := ""
	if ou.Role != nil {
		oldRole = ou.Role.Name
	}

	if domain.IsOwnerRole(ou.Role) {
		count, _ := uc.authRepo.CountOrgUsersByRole(ctx, orgID, ou.RoleID, domain.StatusActive)
		if count <= 1 {
			return domain.ErrCannotRemoveSuperAdmin
		}
	}

	role, err := uc.resolveAssignableRole(ctx, orgID, input.RoleID)
	if err != nil {
		return err
	}
	// members.manage must not be able to mint an owner — that would be a
	// straight privilege escalation. TransferOwnership is the only owner path.
	if domain.IsOwnerRole(role) {
		return domain.NewAppError(403, "ownership is granted via the transfer-ownership endpoint")
	}
	// Escalation guard #2: assigning a role that can manage roles/members requires
	// the caller to hold roles.manage (P6).
	if err := uc.guardRoleAssignment(ctx, orgID, role); err != nil {
		return err
	}

	if err := uc.authRepo.UpdateOrgUserRole(ctx, targetUserID, orgID, role.ID); err != nil {
		return err
	}
	uc.evictSession(ctx, targetUserID, orgID)

	recordAdminEvent(ctx, uc.authRepo, orgID, "member.role_changed", &targetUserID,
		map[string]interface{}{"old_role": oldRole, "new_role": role.Name})
	return nil
}

func (uc *workspaceUseCase) SuspendMember(ctx context.Context, orgID uuid.UUID, targetUserID uuid.UUID) error {
	ou, err := uc.authRepo.GetOrgUser(ctx, targetUserID, orgID)
	if err != nil || ou == nil {
		return domain.ErrNotMember
	}
	if domain.IsOwnerRole(ou.Role) {
		count, _ := uc.authRepo.CountOrgUsersByRole(ctx, orgID, ou.RoleID, domain.StatusActive)
		if count <= 1 {
			return domain.ErrCannotRemoveSuperAdmin
		}
	}
	if err := uc.authRepo.UpdateOrgUserStatus(ctx, targetUserID, orgID, domain.StatusSuspended); err != nil {
		return err
	}
	uc.evictSession(ctx, targetUserID, orgID)
	recordAdminEvent(ctx, uc.authRepo, orgID, "member.suspended", &targetUserID, nil)
	return nil
}

func (uc *workspaceUseCase) ReinstateMember(ctx context.Context, orgID uuid.UUID, targetUserID uuid.UUID) error {
	if err := uc.authRepo.UpdateOrgUserStatus(ctx, targetUserID, orgID, domain.StatusActive); err != nil {
		return err
	}
	// Evict so the reinstated member gets back in immediately, not after TTL.
	uc.evictSession(ctx, targetUserID, orgID)
	recordAdminEvent(ctx, uc.authRepo, orgID, "member.reinstated", &targetUserID, nil)
	return nil
}

// TransferOwnership atomically demotes the current owner to admin and promotes
// the target to owner. Caller must be the current owner: the route's
// members.manage gate is NOT enough — this endpoint mints god-mode, so it gets
// the strictest check in the file (P10 P0).
func (uc *workspaceUseCase) TransferOwnership(ctx context.Context, orgID uuid.UUID, callerUserID, targetUserID uuid.UUID) error {
	caller, err := uc.authRepo.GetOrgUser(ctx, callerUserID, orgID)
	if err != nil || caller == nil {
		return domain.ErrNotMember
	}
	if caller.Status != domain.StatusActive || !domain.IsOwnerRole(caller.Role) {
		return domain.NewAppError(403, "only the current owner can transfer ownership")
	}
	if callerUserID == targetUserID {
		return domain.NewAppError(400, "you already own this workspace")
	}

	target, err := uc.authRepo.GetOrgUser(ctx, targetUserID, orgID)
	if err != nil || target == nil {
		return domain.ErrNotMember
	}
	if target.Status != domain.StatusActive {
		return domain.NewAppError(409, "ownership can only be transferred to an active member")
	}

	ownerRole, err := uc.authRepo.GetRoleByName(ctx, domain.RoleOwner, nil)
	if err != nil || ownerRole == nil {
		return domain.ErrInternal
	}
	adminRole, err := uc.authRepo.GetRoleByName(ctx, domain.RoleAdmin, nil)
	if err != nil || adminRole == nil {
		return domain.ErrInternal
	}

	if err := uc.authRepo.TransferOrgOwnership(ctx, orgID, callerUserID, targetUserID, ownerRole.ID, adminRole.ID); err != nil {
		return err
	}
	uc.evictSession(ctx, callerUserID, orgID)
	uc.evictSession(ctx, targetUserID, orgID)

	recordAdminEvent(ctx, uc.authRepo, orgID, "member.ownership_transferred", &targetUserID,
		map[string]interface{}{"from": callerUserID.String(), "to": targetUserID.String()})
	return nil
}

func (uc *workspaceUseCase) RemoveMember(ctx context.Context, orgID uuid.UUID, targetUserID uuid.UUID, input domain.RemoveMemberInput) error {
	ou, err := uc.authRepo.GetOrgUser(ctx, targetUserID, orgID)
	if err != nil || ou == nil {
		return domain.ErrNotMember
	}
	if domain.IsOwnerRole(ou.Role) {
		count, _ := uc.authRepo.CountOrgUsersByRole(ctx, orgID, ou.RoleID, domain.StatusActive)
		if count <= 1 {
			return domain.ErrCannotRemoveSuperAdmin
		}
	}

	// The member's owned records decide whether a strategy is required: removing
	// someone who owns nothing needs no ceremony, while owned data must be
	// explicitly transferred or released — and the transfer now actually runs
	// (U0.2; this replaced the mock that validated the input then ignored it).
	var ownedContacts, ownedDeals, ownedCustom int64
	if uc.offboard != nil {
		var err error
		ownedContacts, ownedDeals, ownedCustom, err = uc.offboard.CountOwnedRecords(ctx, orgID, targetUserID)
		if err != nil {
			return domain.ErrInternal
		}
	}
	if ownedContacts+ownedDeals+ownedCustom > 0 {
		switch input.Strategy {
		case "transfer":
			if input.ReassignToUserID == nil {
				return &domain.ReassignmentRequiredError{Contacts: ownedContacts, Deals: ownedDeals, Custom: ownedCustom}
			}
			newOwner := *input.ReassignToUserID
			if newOwner == targetUserID {
				return domain.NewAppError(400, "records cannot be reassigned to the member being removed")
			}
			recipient, err := uc.authRepo.GetOrgUser(ctx, newOwner, orgID)
			if err != nil {
				return domain.ErrInternal
			}
			if recipient == nil || recipient.Status != domain.StatusActive {
				return domain.NewAppError(400, "records can only be reassigned to an active member of this workspace")
			}
			if err := uc.offboard.ReassignOwnedRecords(ctx, orgID, targetUserID, newOwner); err != nil {
				return domain.ErrInternal
			}
		case "unassign":
			if err := uc.offboard.UnassignOwnedRecords(ctx, orgID, targetUserID); err != nil {
				return domain.ErrInternal
			}
		default:
			return &domain.ReassignmentRequiredError{Contacts: ownedContacts, Deals: ownedDeals, Custom: ownedCustom}
		}
	}

	// Revoke the member's org-scoped grants (record shares to them, report
	// shares targeting them, group memberships) so a later re-invite starts
	// clean instead of silently restoring old access.
	if uc.offboard != nil {
		if err := uc.offboard.RevokeUserGrants(ctx, orgID, targetUserID); err != nil {
			return domain.ErrInternal
		}
	}

	if err := uc.authRepo.DeleteOrgUser(ctx, targetUserID, orgID); err != nil {
		return err
	}
	uc.evictSession(ctx, targetUserID, orgID)
	recordAdminEvent(ctx, uc.authRepo, orgID, "member.removed", &targetUserID,
		map[string]interface{}{
			"strategy":       input.Strategy,
			"owned_contacts": ownedContacts,
			"owned_deals":    ownedDeals,
		})
	return nil
}
