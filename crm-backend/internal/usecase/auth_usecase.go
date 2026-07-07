package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"crm-backend/internal/domain"
	"crm-backend/pkg/config"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	bcryptCost           = 12
	accessTokenDuration  = 2 * time.Hour
	refreshTokenDuration = 7 * 24 * time.Hour
	refreshTokenBytes    = 32

	// Account-recovery token lifetimes (P1). Reset is short-lived; verification
	// is longer since it may sit in an inbox. Resend is throttled per user.
	passwordResetTokenDuration     = time.Hour
	emailVerificationTokenDuration = 24 * time.Hour
	resendVerificationCooldown     = time.Minute
	// Per-email cooldown between forgot-password requests (P10 P1): the only
	// other guard is 30 req/min/IP, which no-ops without Redis — without this,
	// one caller can bomb a victim's inbox with reset emails.
	passwordResetRequestCooldown = time.Minute

	// Per-email login throttle (P2). After loginFailThreshold failures within
	// loginFailWindow, each further failure sets an exponential lockout starting
	// at loginLockBase and doubling, capped at loginLockCap. A successful login
	// clears the counter. Backed by Redis; no-op when Redis is absent.
	loginFailThreshold = 5
	loginFailWindow    = 15 * time.Minute
	loginLockBase      = 30 * time.Second
	loginLockCap       = 15 * time.Minute
)

type authUseCase struct {
	authRepo    domain.AuthRepository
	stageRepo   domain.PipelineStageRepository
	cfg         *config.Config
	oauthConfig *oauth2.Config
	mailer      domain.Mailer
	appEnv      string
	// redisClient backs per-email login throttling and session-cache eviction on
	// token_version bumps (P2). Nil in dev without Redis → those paths no-op.
	redisClient *redis.Client
}

var defaultPipelineStages = []struct {
	Name  string
	Color string
	IsWon bool
	IsLost bool
	Pos  int
}{
	{"Lead In",     "#6366F1", false, false, 0},
	{"Qualified",   "#3B82F6", false, false, 1},
	{"Proposal",    "#F59E0B", false, false, 2},
	{"Negotiation", "#EF4444", false, false, 3},
	{"Closed Won",  "#10B981", true,  false, 4},
}

func NewAuthUseCase(repo domain.AuthRepository, stageRepo domain.PipelineStageRepository, cfg *config.Config, mailer domain.Mailer, appEnv string, redisClient *redis.Client) domain.AuthUseCase {
	var oauthCfg *oauth2.Config
	if cfg.GoogleClientID != "" {
		// The OAuth callback must return to the FRONTEND origin so it flows back
		// through the same /api proxy as every other request — that is what keeps
		// the session cookie first-party. A cross-site callback host (e.g. the API
		// domain directly) sets a third-party cookie the browser then refuses to
		// send, which loops login forever. Derive it from FrontendURL when set so
		// no separate GOOGLE_REDIRECT_URL has to be kept in sync with the proxy;
		// falls back to the explicit config for setups without a FrontendURL.
		redirectURL := cfg.GoogleRedirectURL
		if cfg.FrontendURL != "" {
			redirectURL = strings.TrimRight(cfg.FrontendURL, "/") + "/api/auth/google/callback"
		}
		oauthCfg = &oauth2.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		}
	}
	return &authUseCase{
		authRepo:    repo,
		stageRepo:   stageRepo,
		cfg:         cfg,
		oauthConfig: oauthCfg,
		mailer:      mailer,
		appEnv:      appEnv,
		redisClient: redisClient,
	}
}

func (uc *authUseCase) seedDefaultStages(ctx context.Context, orgID uuid.UUID) {
	// Only seed if no stages exist
	count, err := uc.stageRepo.CountByOrg(ctx, orgID)
	if err != nil || count > 0 {
		return
	}
	for _, s := range defaultPipelineStages {
		stage := &domain.PipelineStage{
			OrgID:    orgID,
			Name:     s.Name,
			Color:    s.Color,
			Position: s.Pos,
			IsWon:    s.IsWon,
			IsLost:   s.IsLost,
		}
		_ = uc.stageRepo.Create(ctx, stage)
	}
}

func (uc *authUseCase) Register(ctx context.Context, input domain.RegisterInput) (*domain.AuthResponse, error) {
	input.Email = normalizeEmail(input.Email)
	existing, err := uc.authRepo.GetUserByEmail(ctx, input.Email)
	if err != nil {
		return nil, domain.NewAppError(500, "Get user err: " + err.Error())
	}
	if existing != nil {
		return nil, domain.ErrEmailAlreadyExists
	}

	orgType := input.OrgType
	if orgType == "" {
		orgType = "company"
	}
	org := &domain.Organization{Name: input.OrgName, Type: orgType}
	if err := uc.authRepo.CreateOrganization(ctx, org); err != nil {
		return nil, domain.NewAppError(500, "Create org err: " + err.Error())
	}

	// Seed default pipeline stages for the new organization
	uc.seedDefaultStages(ctx, org.ID)

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcryptCost)
	if err != nil {
		return nil, domain.NewAppError(500, "Hash err: " + err.Error())
	}
	hashStr := string(hash)

	fullName := input.FirstName
	if input.LastName != "" {
		fullName = input.FirstName + " " + input.LastName
	}

	user := &domain.User{
		OrgID:        org.ID,
		Email:        input.Email,
		PasswordHash: &hashStr,
		FirstName:    input.FirstName,
		LastName:     input.LastName,
		FullName:     fullName,
	}
	if err := uc.authRepo.CreateUser(ctx, user); err != nil {
		return nil, domain.NewAppError(500, "Create user err: " + err.Error())
	}

	ownerRole, err := uc.authRepo.GetRoleByName(ctx, domain.RoleOwner, nil)
	if err != nil {
		return nil, domain.NewAppError(500, "Get role err: " + err.Error())
	}

	ou := &domain.OrgUser{
		UserID: user.ID,
		OrgID:  org.ID,
		RoleID: ownerRole.ID,
		Status: domain.StatusActive,
	}
	if err := uc.authRepo.CreateOrgUser(ctx, ou); err != nil {
		return nil, domain.NewAppError(500, "Create org user err: " + err.Error())
	}

	// Soft-gate email verification (plan D2): the account is fully active and
	// logged in immediately; we just email a verification link and drive a
	// banner off User.EmailVerifiedAt. Best-effort — never fail registration if
	// the email can't be sent (the user can resend from the banner).
	if _, err := uc.issueVerificationEmail(ctx, user); err != nil {
		log.Printf("register: failed to issue verification email for %s: %v", user.Email, err)
	}
	uc.recordAuthEvent(ctx, "auth", "user.registered", orgPtr(org.ID), &user.ID, &user.ID, domain.RequestMeta{}, nil)

	accessToken, err := uc.generateAccessToken(user.ID, org.ID, ownerRole, user.TokenVersion)
	if err != nil {
		return nil, domain.NewAppError(500, "Access token err: " + err.Error())
	}

	refreshToken, err := uc.createRefreshToken(ctx, user.ID, domain.RequestMeta{}, nil)
	if err != nil {
		return nil, domain.NewAppError(500, "Refresh token err: " + err.Error())
	}

	workspaces := []domain.WorkspaceInfo{
		{
			OrgID:       org.ID,
			OrgName:     org.Name,
			OrgType:     org.Type,
			Role:        ownerRole.Name,
			Status:      domain.StatusActive,
			MemberCount: 1,
		},
	}

	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         *user,
		Workspaces:   workspaces,
		ActiveOrgID:  org.ID,
		NeedsChooser: false,
	}, nil
}

func (uc *authUseCase) Login(ctx context.Context, input domain.LoginInput, meta domain.RequestMeta) (*domain.AuthResponse, error) {
	// Per-email lockout check first — a locked account never reaches bcrypt, so a
	// brute-force run costs the attacker a 429 instead of a hash comparison.
	if wait := uc.loginLockRemaining(ctx, input.Email); wait > 0 {
		uc.recordAuthEvent(ctx, "security", "login.throttled", nil, nil, nil, meta,
			map[string]interface{}{"email": input.Email})
		return nil, &domain.AppError{
			Code:       http.StatusTooManyRequests,
			Message:    domain.ErrTooManyLoginAttempts.Message,
			RetryAfter: int(wait.Seconds()) + 1,
		}
	}

	user, err := uc.authRepo.GetUserByEmail(ctx, input.Email)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if user == nil || user.PasswordHash == nil {
		uc.registerLoginFailure(ctx, input.Email)
		uc.recordAuthEvent(ctx, "auth", "login.failed", nil, nil, nil, meta,
			map[string]interface{}{"email": input.Email, "reason": "no_such_user"})
		return nil, domain.ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(input.Password)); err != nil {
		uc.registerLoginFailure(ctx, input.Email)
		uc.recordAuthEvent(ctx, "auth", "login.failed", orgPtr(user.OrgID), nil, &user.ID, meta,
			map[string]interface{}{"email": input.Email, "reason": "bad_password"})
		return nil, domain.ErrInvalidCredentials
	}

	// Success — clear the failure counter so the next login starts fresh.
	uc.clearLoginFailures(ctx, input.Email)

	orgUsers, err := uc.authRepo.ListOrgsByUserID(ctx, user.ID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	// R2 server-side org selection (P3): explicit org_id → valid default → sole →
	// deterministic first. An explicit org the user can't access is a hard 403,
	// never a silent fallback into some other org.
	sel, needsChooser, explicitMiss := uc.resolveActiveOrg(ctx, user, orgUsers, input.OrgID)
	if explicitMiss {
		return nil, domain.NewAppError(http.StatusForbidden, "you are not an active member of the requested workspace")
	}

	var activeOrgID uuid.UUID
	var activeRole *domain.Role
	if sel != nil {
		activeOrgID = sel.OrgID
		activeRole = sel.Role
	}

	accessToken, err := uc.generateAccessToken(user.ID, activeOrgID, activeRole, user.TokenVersion)
	if err != nil {
		return nil, domain.ErrInternal
	}

	// Compare this sign-in against existing sessions BEFORE minting the new one, so
	// a genuinely new device/IP triggers an alert (P4). Attribute it to the active
	// workspace (not the user's legacy home org) so it lands in the right audit log.
	uc.maybeAlertNewDevice(ctx, user, activeOrgID, meta)

	refreshToken, err := uc.createRefreshToken(ctx, user.ID, meta, nil)
	if err != nil {
		return nil, domain.ErrInternal
	}

	uc.recordAuthEvent(ctx, "auth", "login.success", orgPtr(activeOrgID), &user.ID, &user.ID, meta, nil)

	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         *user,
		Workspaces:   uc.buildWorkspaces(ctx, orgUsers),
		ActiveOrgID:  activeOrgID,
		DefaultOrgID: user.DefaultOrgID,
		NeedsChooser: needsChooser,
	}, nil
}

// resolveActiveOrg runs the R2 selection chain over already-loaded memberships
// and, as a side effect, self-clears a stored default that no longer resolves to
// an active membership (P3). Returns the chosen membership (nil ⇒ no active
// workspace when explicitMiss is false), whether the SPA needs the chooser, and
// whether an explicitly requested org was NOT an active membership. It mutates
// user.DefaultOrgID in place when it clears the stored one so the response echoes
// the corrected value.
func (uc *authUseCase) resolveActiveOrg(ctx context.Context, user *domain.User, orgUsers []domain.OrgUser, requested *uuid.UUID) (sel *domain.OrgUser, needsChooser, explicitMiss bool) {
	var defaultInvalid bool
	sel, needsChooser, defaultInvalid, explicitMiss = selectActiveOrg(orgUsers, requested, user.DefaultOrgID)
	if defaultInvalid {
		_ = uc.authRepo.SetUserDefaultOrg(ctx, user.ID, nil)
		user.DefaultOrgID = nil
	}
	return sel, needsChooser, explicitMiss
}

func (uc *authUseCase) SwitchWorkspace(ctx context.Context, userID uuid.UUID, input domain.SwitchWorkspaceInput, meta domain.RequestMeta, currentRefreshToken string) (*domain.AuthResponse, error) {
	ou, err := uc.authRepo.GetOrgUser(ctx, userID, input.OrgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if ou == nil || ou.Status != domain.StatusActive {
		return nil, domain.ErrNotMember
	}

	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}

	// "Make this my default": persist so the chooser isn't shown again (P3).
	if input.SetDefault {
		orgID := input.OrgID
		if err := uc.authRepo.SetUserDefaultOrg(ctx, userID, &orgID); err != nil {
			return nil, domain.ErrInternal
		}
		user.DefaultOrgID = &orgID
	}

	accessToken, err := uc.generateAccessToken(userID, input.OrgID, ou.Role, user.TokenVersion)
	if err != nil {
		return nil, domain.ErrInternal
	}

	// Switch hygiene (P3): revoke the refresh token the caller presented so the
	// switch doesn't orphan a live 7-day credential, and mint the successor with
	// real device meta (the old path stamped blank device rows).
	if currentRefreshToken != "" {
		if st, _ := uc.authRepo.GetRefreshTokenByHash(ctx, hashToken(currentRefreshToken)); st != nil {
			_ = uc.authRepo.RevokeRefreshToken(ctx, st.ID)
		}
	}
	refreshToken, err := uc.createRefreshToken(ctx, userID, meta, nil)
	if err != nil {
		return nil, domain.ErrInternal
	}

	orgUsers, _ := uc.authRepo.ListOrgsByUserID(ctx, userID)
	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         *user,
		Workspaces:   uc.buildWorkspaces(ctx, orgUsers),
		ActiveOrgID:  input.OrgID,
		DefaultOrgID: user.DefaultOrgID,
		NeedsChooser: false,
	}, nil
}

func (uc *authUseCase) ListWorkspaces(ctx context.Context, userID uuid.UUID) ([]domain.WorkspaceInfo, error) {
	orgUsers, err := uc.authRepo.ListOrgsByUserID(ctx, userID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	return uc.buildWorkspaces(ctx, orgUsers), nil
}

func (uc *authUseCase) RefreshToken(ctx context.Context, input domain.RefreshInput, meta domain.RequestMeta) (*domain.AuthResponse, error) {
	tokenHash := hashToken(input.RefreshToken)

	// Look up the row regardless of state so we can tell "never issued" from
	// "already rotated/revoked" — the latter is a reuse/theft signal.
	storedToken, err := uc.authRepo.GetRefreshTokenByHashAny(ctx, tokenHash)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if storedToken == nil {
		return nil, domain.ErrInvalidToken
	}

	// Reuse detection: a revoked token presented again. Distinguish genuine theft
	// from a deliberately-ended session. A token revoked because it was ROTATED
	// has a successor in the chain — replaying it means the old token was captured,
	// so nuke every session and alert. A token revoked by the user's own action
	// (logout, revoke-device, sign-out-everywhere, password reset) has NO successor;
	// replaying it is just that device coming back after the user cut it off — fail
	// it closed with a plain 401, without nuking the user's other sessions or
	// firing a false "your token was stolen" alarm (P4).
	if storedToken.RevokedAt != nil {
		if hasSuccessor, _ := uc.authRepo.RefreshTokenHasSuccessor(ctx, storedToken.ID); !hasSuccessor {
			return nil, domain.ErrInvalidToken
		}
		_ = uc.authRepo.RevokeAllUserRefreshTokens(ctx, storedToken.UserID)
		uc.bumpTokenVersion(ctx, storedToken.UserID)
		var orgID *uuid.UUID
		if u, _ := uc.authRepo.GetUserByID(ctx, storedToken.UserID); u != nil {
			orgID = orgPtr(u.OrgID)
			if err := uc.mailer.SendSecurityAlert(ctx, u.Email, "Suspicious sign-in activity",
				"A previously-used sign-in token for your Guerrilla CRM account was replayed, which can indicate the token was stolen. We ended all active sessions as a precaution. Please sign in again, and reset your password if you don't recognize this activity."); err != nil {
				log.Printf("refresh: failed to send token-reuse alert to %s: %v", u.Email, err)
			}
		}
		uc.recordAuthEvent(ctx, "security", "token.reuse", orgID, &storedToken.UserID, &storedToken.UserID, meta,
			map[string]interface{}{"refresh_token_id": storedToken.ID.String()})
		return nil, domain.ErrTokenReuse
	}

	if time.Now().After(storedToken.ExpiresAt) {
		return nil, domain.ErrTokenExpired
	}

	user, err := uc.authRepo.GetUserByID(ctx, storedToken.UserID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}

	orgUsers, _ := uc.authRepo.ListOrgsByUserID(ctx, user.ID)

	// R2 fail-closed selection (P3). Run it BEFORE rotating the presented token so
	// a 409 leaves the caller's refresh cookie intact — the SPA can retry a plain
	// (no-org) refresh into its default/first org and route to the chooser. The old
	// silent orgUsers[0] fallback could flip a multi-org user into another org on a
	// cookie-only refresh; now an explicit org that isn't an active membership is a
	// hard 409 ORG_UNAVAILABLE.
	sel, needsChooser, explicitMiss := uc.resolveActiveOrg(ctx, user, orgUsers, input.OrgID)
	if explicitMiss {
		return nil, &domain.OrgUnavailableError{Workspaces: uc.buildWorkspaces(ctx, orgUsers)}
	}

	// Rotate: revoke the presented token and mint a successor linked to it.
	_ = uc.authRepo.RevokeRefreshToken(ctx, storedToken.ID)

	var activeOrgID uuid.UUID
	var activeRole *domain.Role
	if sel != nil {
		activeOrgID = sel.OrgID
		activeRole = sel.Role
	}

	accessToken, err := uc.generateAccessToken(user.ID, activeOrgID, activeRole, user.TokenVersion)
	if err != nil {
		return nil, domain.ErrInternal
	}

	newRefreshToken, err := uc.createRefreshToken(ctx, user.ID, meta, &storedToken.ID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		User:         *user,
		Workspaces:   uc.buildWorkspaces(ctx, orgUsers),
		ActiveOrgID:  activeOrgID,
		DefaultOrgID: user.DefaultOrgID,
		NeedsChooser: needsChooser,
	}, nil
}

func (uc *authUseCase) Logout(ctx context.Context, refreshToken string) error {
	tokenHash := hashToken(refreshToken)
	storedToken, err := uc.authRepo.GetRefreshTokenByHash(ctx, tokenHash)
	if err != nil || storedToken == nil {
		return nil
	}
	return uc.authRepo.RevokeRefreshToken(ctx, storedToken.ID)
}

func (uc *authUseCase) GetMe(ctx context.Context, userID uuid.UUID) (*domain.User, error) {
	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if user == nil {
		return nil, domain.ErrUserNotFound
	}
	return user, nil
}

func (uc *authUseCase) GetGoogleAuthURL(state string) string {
	if uc.oauthConfig == nil {
		return ""
	}
	return uc.oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

func (uc *authUseCase) GoogleLogin(ctx context.Context, code string) (*domain.AuthResponse, error) {
	if uc.oauthConfig == nil {
		return nil, domain.NewAppError(http.StatusServiceUnavailable, "Google OAuth not configured")
	}

	token, err := uc.oauthConfig.Exchange(ctx, code)
	if err != nil {
		return nil, domain.NewAppError(http.StatusBadRequest, "failed to exchange authorization code")
	}

	client := uc.oauthConfig.Client(ctx, token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		return nil, domain.ErrInternal
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, domain.ErrInternal
	}

	var googleUser domain.GoogleUserInfo
	if err := json.Unmarshal(body, &googleUser); err != nil {
		return nil, domain.ErrInternal
	}
	// Normalize so a Google account whose email differs only by case from an
	// existing local account links to it instead of forking a new one (P2).
	googleUser.Email = normalizeEmail(googleUser.Email)

	user, err := uc.authRepo.GetUserByGoogleID(ctx, googleUser.ID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	if user == nil {
		user, err = uc.authRepo.GetUserByEmail(ctx, googleUser.Email)
		if err != nil {
			return nil, domain.ErrInternal
		}

		if user != nil {
			user.GoogleID = &googleUser.ID
			if googleUser.Picture != "" {
				user.AvatarURL = &googleUser.Picture
			}
			// Google has already verified this email, so a linked local account
			// is verified too — never soft-gate an OAuth user.
			if user.EmailVerifiedAt == nil && googleUser.VerifiedEmail {
				now := time.Now()
				user.EmailVerifiedAt = &now
			}
			if err := uc.authRepo.UpdateUser(ctx, user); err != nil {
				return nil, domain.ErrInternal
			}
		} else {
			org := &domain.Organization{
				Name: fmt.Sprintf("%s's Workspace", googleUser.GivenName),
				Type: "personal",
			}
			if err := uc.authRepo.CreateOrganization(ctx, org); err != nil {
				return nil, domain.ErrInternal
			}

			fullName := googleUser.GivenName
			if googleUser.FamilyName != "" {
				fullName = googleUser.GivenName + " " + googleUser.FamilyName
			}

			user = &domain.User{
				OrgID:     org.ID,
				Email:     googleUser.Email,
				FirstName: googleUser.GivenName,
				LastName:  googleUser.FamilyName,
				FullName:  fullName,
				GoogleID:  &googleUser.ID,
				AvatarURL: &googleUser.Picture,
			}
			// Google-provided emails are pre-verified.
			if googleUser.VerifiedEmail {
				now := time.Now()
				user.EmailVerifiedAt = &now
			}
			if err := uc.authRepo.CreateUser(ctx, user); err != nil {
				return nil, domain.ErrInternal
			}

			ownerRole, err := uc.authRepo.GetRoleByName(ctx, domain.RoleOwner, nil)
			if err != nil {
				return nil, domain.ErrInternal
			}

			ou := &domain.OrgUser{
				UserID: user.ID,
				OrgID:  org.ID,
				RoleID: ownerRole.ID,
				Status: domain.StatusActive,
			}
			if err := uc.authRepo.CreateOrgUser(ctx, ou); err != nil {
				return nil, domain.ErrInternal
			}

			// Seed default pipeline stages for the new organization
			uc.seedDefaultStages(ctx, org.ID)
		}
	}

	fullUser, _ := uc.authRepo.GetUserByID(ctx, user.ID)
	if fullUser != nil {
		user = fullUser
	}

	orgUsers, _ := uc.authRepo.ListOrgsByUserID(ctx, user.ID)

	// Same R2 selection chain as password login (P3): a returning multi-org Google
	// user lands on their default, or is asked to choose. No explicit org on OAuth.
	sel, needsChooser, _ := uc.resolveActiveOrg(ctx, user, orgUsers, nil)
	var activeOrgID uuid.UUID
	var activeRole *domain.Role
	if sel != nil {
		activeOrgID = sel.OrgID
		activeRole = sel.Role
	}

	accessToken, err := uc.generateAccessToken(user.ID, activeOrgID, activeRole, user.TokenVersion)
	if err != nil {
		return nil, domain.ErrInternal
	}

	refreshTokenStr, err := uc.createRefreshToken(ctx, user.ID, domain.RequestMeta{}, nil)
	if err != nil {
		return nil, domain.ErrInternal
	}

	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshTokenStr,
		User:         *user,
		Workspaces:   uc.buildWorkspaces(ctx, orgUsers),
		ActiveOrgID:  activeOrgID,
		DefaultOrgID: user.DefaultOrgID,
		NeedsChooser: needsChooser,
	}, nil
}

type JWTClaims struct {
	UserID uuid.UUID `json:"user_id"`
	OrgID  uuid.UUID `json:"org_id"`
	Role   string    `json:"role"`
	// RoleID and DataScope thread the caller's role identity end-to-end (P3), so
	// every authorization layer keys off role_id and the row scope is data, not a
	// hardcoded name check. Absent in pre-P3 tokens (decode to zero); the
	// middleware re-resolves both authoritatively from org_users when Redis is
	// available, so a stale/empty claim self-heals on the next request.
	RoleID    uuid.UUID `json:"rid,omitempty"`
	DataScope string    `json:"ds,omitempty"`
	// TokenVersion mirrors users.token_version at mint time. The middleware
	// rejects the token if it no longer matches, giving instant global session
	// invalidation (P2). Absent in pre-P2 tokens → decodes to 0 → matches the
	// default column, so old sessions survive a deploy.
	TokenVersion int `json:"tv"`
	jwt.RegisteredClaims
}

// --- Session / device management (P4) ---

// ListSessions returns the caller's live sessions (one per device), marking the
// one making this request as Current by matching the presented refresh token's
// hash. It never returns the token itself.
func (uc *authUseCase) ListSessions(ctx context.Context, userID uuid.UUID, currentRefreshToken string) ([]domain.SessionInfo, error) {
	tokens, err := uc.authRepo.ListActiveRefreshTokens(ctx, userID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	var currentHash string
	if currentRefreshToken != "" {
		currentHash = hashToken(currentRefreshToken)
	}
	out := make([]domain.SessionInfo, 0, len(tokens))
	for _, t := range tokens {
		si := domain.SessionInfo{
			ID:         t.ID,
			CreatedAt:  t.CreatedAt,
			LastUsedAt: t.LastUsedAt,
			Current:    currentHash != "" && t.TokenHash == currentHash,
		}
		if t.DeviceLabel != nil {
			si.DeviceLabel = *t.DeviceLabel
		}
		if t.IP != nil {
			si.IP = *t.IP
		}
		out = append(out, si)
	}
	return out, nil
}

// RevokeSession revokes one of the caller's own sessions (a lost/unknown device).
// Scoped to the owner, so a caller cannot revoke another user's session. It
// revokes the device's refresh token AND bumps token_version so the revoked
// device's still-valid access token is rejected on its next request instead of
// lingering for the access-token TTL — the "Revoke" control reads as an instant
// cut-off, which matters for a security action ("revoke a device you don't
// recognize"). The user's other devices silently re-mint an access token from
// their own valid refresh tokens; the revoked device, having lost its refresh
// token, cannot.
func (uc *authUseCase) RevokeSession(ctx context.Context, userID, orgID, sessionID uuid.UUID) error {
	n, err := uc.authRepo.RevokeRefreshTokenForUser(ctx, sessionID, userID)
	if err != nil {
		return domain.ErrInternal
	}
	if n == 0 {
		return domain.NewAppError(http.StatusNotFound, "session not found")
	}
	uc.bumpTokenVersion(ctx, userID) // kill the revoked device's access token now, not in ≤2h
	recordSecurityEvent(ctx, uc.authRepo, orgID, "session.revoked", &userID,
		map[string]interface{}{"session_id": sessionID.String()})
	return nil
}

// SignOutEverywhere revokes every refresh token and bumps token_version (killing
// all outstanding access tokens instantly), then mints a fresh session for the
// current device so the caller stays signed in here while every other device is
// logged out. Returns the new access + refresh tokens for the handler to set.
func (uc *authUseCase) SignOutEverywhere(ctx context.Context, userID, orgID uuid.UUID, currentRefreshToken string) (*domain.AuthResponse, error) {
	if err := uc.authRepo.RevokeAllUserRefreshTokens(ctx, userID); err != nil {
		return nil, domain.ErrInternal
	}
	uc.bumpTokenVersion(ctx, userID) // increments token_version + evicts session cache

	// Reload after the bump so the new access token carries the current version.
	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}

	orgUsers, _ := uc.authRepo.ListOrgsByUserID(ctx, userID)
	activeOrgID := orgID
	var activeRole *domain.Role
	for _, ou := range orgUsers {
		if ou.OrgID == orgID {
			activeRole = ou.Role
			break
		}
	}
	if activeRole == nil && len(orgUsers) > 0 {
		activeOrgID = orgUsers[0].OrgID
		activeRole = orgUsers[0].Role
	}

	accessToken, err := uc.generateAccessToken(user.ID, activeOrgID, activeRole, user.TokenVersion)
	if err != nil {
		return nil, domain.ErrInternal
	}
	meta, _ := domain.RequestMetaFromContext(ctx)
	refreshToken, err := uc.createRefreshToken(ctx, user.ID, meta, nil)
	if err != nil {
		return nil, domain.ErrInternal
	}

	recordSecurityEvent(ctx, uc.authRepo, activeOrgID, "session.signed_out_others", &userID, nil)

	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         *user,
		Workspaces:   uc.buildWorkspaces(ctx, orgUsers),
		ActiveOrgID:  activeOrgID,
		DefaultOrgID: user.DefaultOrgID,
		NeedsChooser: false,
	}, nil
}

// maybeAlertNewDevice emails a security alert when a login arrives from a device
// (browser/OS label) and IP not seen among the user's existing live sessions.
// Best-effort and off the request path; the first-ever device is not alerted (the
// login itself is the signal). Called from Login BEFORE the new refresh token is
// minted, so the new session isn't compared against itself.
func (uc *authUseCase) maybeAlertNewDevice(ctx context.Context, user *domain.User, activeOrgID uuid.UUID, meta domain.RequestMeta) {
	if uc.mailer == nil || (meta.UserAgent == "" && meta.IP == "") {
		return
	}
	existing, err := uc.authRepo.ListActiveRefreshTokens(ctx, user.ID)
	if err != nil || len(existing) == 0 {
		return
	}
	label := deviceLabelFromUA(meta.UserAgent)
	for _, t := range existing {
		// Recognized only when BOTH the exact device (full User-Agent) AND the IP
		// match an existing session. Matching on either alone silences real
		// account-takeover sign-ins: a shared/NAT IP collides across unrelated
		// people, and the coarse "Chrome on Windows" label collides across
		// unrelated machines. So a new device OR a new network both alert.
		sameDevice := t.UserAgent != nil && meta.UserAgent != "" && *t.UserAgent == meta.UserAgent
		sameIP := t.IP != nil && meta.IP != "" && *t.IP == meta.IP
		if sameDevice && sameIP {
			return // known device on a known network
		}
	}

	uc.recordAuthEvent(ctx, "security", "login.new_device", orgPtr(activeOrgID), &user.ID, &user.ID, meta,
		map[string]interface{}{"device": label, "ip": meta.IP})

	email := user.Email
	ip := meta.IP
	go func() {
		where := label
		if ip != "" {
			where += " (" + ip + ")"
		}
		msg := fmt.Sprintf("Your Guerrilla CRM account was just signed in from a new device: %s. If this was you, no action is needed. If you don't recognize it, reset your password and use Settings → Security to sign out of other sessions.", where)
		if err := uc.mailer.SendSecurityAlert(context.Background(), email, "New sign-in to your account", msg); err != nil {
			log.Printf("login: failed to send new-device alert to %s: %v", email, err)
		}
	}()
}

// generateAccessToken mints an access token for the caller's active role. role
// may be nil (an unresolved/zero membership — e.g. the zero-workspace login that
// binds to uuid.Nil), in which case it mints a least-privilege token: no role id
// (so OLS default-denies) and the NARROWEST 'own' data scope. P9 fixed the prior
// fail-open here, which minted the WIDER 'all' scope for a role-less caller —
// exploitable on a nil-role membership over a real org, where reads have no OLS
// route gate and fall through to the repository scope filter.
func (uc *authUseCase) generateAccessToken(userID, orgID uuid.UUID, role *domain.Role, tokenVersion int) (string, error) {
	roleName := domain.RoleViewer
	roleID := uuid.Nil
	dataScope := domain.DataScopeOwn
	if role != nil {
		roleName = role.Name
		roleID = role.ID
		dataScope = domain.DataScopeAll
		if role.DataScope == domain.DataScopeOwn {
			dataScope = domain.DataScopeOwn
		}
	}
	claims := JWTClaims{
		UserID:       userID,
		OrgID:        orgID,
		Role:         roleName,
		RoleID:       roleID,
		DataScope:    dataScope,
		TokenVersion: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(accessTokenDuration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "20q-crm",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(uc.cfg.JWTSecret))
}

// createRefreshToken mints a hashed refresh token row. meta stamps the device/IP
// for the sessions UI; rotatedFrom links it to its predecessor in the rotation
// chain (nil for a fresh login). P2.
func (uc *authUseCase) createRefreshToken(ctx context.Context, userID uuid.UUID, meta domain.RequestMeta, rotatedFrom *uuid.UUID) (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	rawToken := hex.EncodeToString(b)

	now := time.Now()
	tokenHash := hashToken(rawToken)
	rt := &domain.RefreshToken{
		UserID:      userID,
		TokenHash:   tokenHash,
		ExpiresAt:   now.Add(refreshTokenDuration),
		LastUsedAt:  &now,
		RotatedFrom: rotatedFrom,
	}
	if meta.IP != "" {
		ip := meta.IP
		rt.IP = &ip
	}
	if meta.UserAgent != "" {
		ua := meta.UserAgent
		rt.UserAgent = &ua
		label := deviceLabelFromUA(ua)
		rt.DeviceLabel = &label
	}
	if err := uc.authRepo.CreateRefreshToken(ctx, rt); err != nil {
		return "", err
	}

	return rawToken, nil
}

// deviceLabelFromUA extracts a short human label ("Chrome on macOS") from a
// User-Agent string, best-effort. It never parses untrusted input into anything
// but a display string, and falls back to a trimmed UA when it can't tell.
func deviceLabelFromUA(ua string) string {
	if ua == "" {
		return "Unknown device"
	}
	browser := "Browser"
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "OPR/") || strings.Contains(ua, "Opera"):
		browser = "Opera"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	os := ""
	switch {
	case strings.Contains(ua, "Windows"):
		os = "Windows"
	case strings.Contains(ua, "Mac OS X") || strings.Contains(ua, "Macintosh"):
		os = "macOS"
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad"):
		os = "iOS"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	}
	if os != "" {
		return browser + " on " + os
	}
	if len(ua) > 60 {
		return ua[:60]
	}
	return ua
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// normalizeEmail lowercases and trims an email for storage so casing/whitespace
// can't fork one human into two accounts or silently break a reset lookup (P2).
// Paired with the case-insensitive GetUserByEmail read; writers call this.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// buildWorkspaces renders a user's memberships for an AuthResponse, resolving
// each org's active-member count in ONE query (P3). It is the single source of
// the workspaces payload — every auth path (login/refresh/switch/oauth/me) goes
// through here, so the shape can't drift between them. orgUsers comes from
// ListOrgsByUserID, which is already ACTIVE-only and deterministically ordered.
func (uc *authUseCase) buildWorkspaces(ctx context.Context, orgUsers []domain.OrgUser) []domain.WorkspaceInfo {
	orgIDs := make([]uuid.UUID, 0, len(orgUsers))
	for _, ou := range orgUsers {
		orgIDs = append(orgIDs, ou.OrgID)
	}
	counts, _ := uc.authRepo.CountActiveMembersByOrgs(ctx, orgIDs)

	ws := make([]domain.WorkspaceInfo, 0, len(orgUsers))
	for _, ou := range orgUsers {
		name := ""
		orgType := "company"
		if ou.Org != nil {
			name = ou.Org.Name
			orgType = ou.Org.Type
		}
		roleName := "viewer"
		if ou.Role != nil {
			roleName = ou.Role.Name
		}
		ws = append(ws, domain.WorkspaceInfo{
			OrgID:       ou.OrgID,
			OrgName:     name,
			OrgType:     orgType,
			Role:        roleName,
			Status:      ou.Status,
			MemberCount: counts[ou.OrgID],
		})
	}
	return ws
}

// selectActiveOrg implements the R2 server-side org-selection chain (P3) over the
// caller's ACTIVE memberships: explicit request → valid saved default → the sole
// membership → deterministic first (with the chooser). It returns the chosen
// membership (nil ⇒ zero active workspaces), whether the SPA should show the
// chooser, whether the stored default was invalid (caller self-clears it), and
// whether an explicitly requested org was NOT an active membership (login 403 /
// refresh 409). orgUsers is already ACTIVE-only, but the filter is kept as a
// defensive belt so a future caller passing mixed statuses can't misselect.
func selectActiveOrg(orgUsers []domain.OrgUser, requested, defaultOrgID *uuid.UUID) (selected *domain.OrgUser, needsChooser, defaultInvalid, explicitMiss bool) {
	active := make([]domain.OrgUser, 0, len(orgUsers))
	for i := range orgUsers {
		if orgUsers[i].Status == domain.StatusActive {
			active = append(active, orgUsers[i])
		}
	}
	find := func(id uuid.UUID) *domain.OrgUser {
		for i := range active {
			if active[i].OrgID == id {
				return &active[i]
			}
		}
		return nil
	}

	if requested != nil && *requested != uuid.Nil {
		if sel := find(*requested); sel != nil {
			return sel, false, false, false
		}
		return nil, false, false, true // asked for an org they can't access
	}

	if defaultOrgID != nil && *defaultOrgID != uuid.Nil {
		if sel := find(*defaultOrgID); sel != nil {
			return sel, false, false, false
		}
		defaultInvalid = true // stored default no longer resolves → self-clear
	}

	switch len(active) {
	case 0:
		return nil, false, defaultInvalid, false // zero-membership dead-end
	case 1:
		return &active[0], false, defaultInvalid, false
	default:
		return &active[0], true, defaultInvalid, false // first + prompt the chooser
	}
}

// generateSecureToken returns a 256-bit CSPRNG token as hex — the raw value that
// goes in an email link. Only its SHA-256 hash is ever persisted.
func generateSecureToken() (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// --- Per-email login throttle (P2) ---
//
// login:fail:{email} counts recent failures; login:lock:{email} holds the
// exponential lockout with its TTL as the remaining wait. All three helpers
// no-op when Redis is unavailable, so dev without Redis is unaffected.

func loginFailKey(email string) string { return "login:fail:" + strings.ToLower(email) }
func loginLockKey(email string) string { return "login:lock:" + strings.ToLower(email) }

// loginLockRemaining returns how long the caller must wait before another login
// attempt, or 0 if not locked out.
func (uc *authUseCase) loginLockRemaining(ctx context.Context, email string) time.Duration {
	if uc.redisClient == nil {
		return 0
	}
	ttl, err := uc.redisClient.TTL(ctx, loginLockKey(email)).Result()
	if err != nil || ttl <= 0 { // -2 (no key) / -1 (no expire) both map to "not locked"
		return 0
	}
	return ttl
}

// registerLoginFailure records one failed attempt and, past the threshold, arms
// an exponential lockout.
func (uc *authUseCase) registerLoginFailure(ctx context.Context, email string) {
	if uc.redisClient == nil {
		return
	}
	cnt, err := uc.redisClient.Incr(ctx, loginFailKey(email)).Result()
	if err != nil {
		return
	}
	if cnt == 1 {
		uc.redisClient.Expire(ctx, loginFailKey(email), loginFailWindow)
	}
	if cnt > int64(loginFailThreshold) {
		backoff := loginLockBase
		for i := int64(loginFailThreshold + 1); i < cnt && backoff < loginLockCap; i++ {
			backoff *= 2
		}
		if backoff > loginLockCap {
			backoff = loginLockCap
		}
		uc.redisClient.Set(ctx, loginLockKey(email), "1", backoff)
	}
}

// clearLoginFailures resets the throttle after a successful login.
func (uc *authUseCase) clearLoginFailures(ctx context.Context, email string) {
	if uc.redisClient == nil {
		return
	}
	uc.redisClient.Del(ctx, loginFailKey(email), loginLockKey(email))
}

// bumpTokenVersion increments the user's token_version (invalidating every
// outstanding access token) and evicts their cached sessions so the next request
// re-reads the new version from the DB — making the kill instant rather than
// TTL-bounded. Best-effort: a failure is logged, never fatal to the caller.
func (uc *authUseCase) bumpTokenVersion(ctx context.Context, userID uuid.UUID) {
	if err := uc.authRepo.IncrementUserTokenVersion(ctx, userID); err != nil {
		log.Printf("token_version: failed to bump for %s: %v", userID, err)
		return
	}
	uc.evictUserSessionCache(ctx, userID)
}

// evictUserSessionCache deletes the middleware's per-(user,org) session-cache
// entries for a user by exact key (no SCAN). Called after a token_version bump.
func (uc *authUseCase) evictUserSessionCache(ctx context.Context, userID uuid.UUID) {
	if uc.redisClient == nil {
		return
	}
	orgUsers, err := uc.authRepo.ListOrgsByUserID(ctx, userID)
	if err != nil {
		return
	}
	for _, ou := range orgUsers {
		_ = uc.redisClient.Del(ctx, SessionCacheKey(userID, ou.OrgID)).Err()
	}
}

// debugTokensEnabled gates the raw-token debug escape hatch (forgot-password /
// resend-verification / invites) on an explicit environment ALLOWLIST. The old
// `appEnv != "production"` check failed OPEN: an unset or typo'd APP_ENV on a
// production deployment handed working account-takeover tokens to any anonymous
// caller. Only exact dev/test values may enable it (P10 P1).
func debugTokensEnabled(appEnv string) bool {
	return appEnv == "development" || appEnv == "test"
}

// orgPtr converts a value org id to the pointer AuthEvent.OrgID wants (nil for
// the zero uuid, i.e. "no org").
func orgPtr(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// recordAuthEvent appends one auth/admin/security event. Best-effort, mirroring
// object_audit: a write failure is logged and swallowed so it can never fail the
// user action that triggered it.
func (uc *authUseCase) recordAuthEvent(ctx context.Context, category, eventType string, orgID, actorID, targetID *uuid.UUID, meta domain.RequestMeta, metadata map[string]interface{}) {
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		raw = []byte("{}")
	}
	e := &domain.AuthEvent{
		OrgID:     orgID,
		ActorID:   actorID,
		TargetID:  targetID,
		Category:  category,
		EventType: eventType,
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
		log.Printf("auth_events: failed to record %s/%s: %v", category, eventType, err)
	}
}

// issueVerificationEmail mints a hashed, single-use verification token and emails
// the link. Shared by register and resend. Returns the raw token so callers can
// expose a debug token in non-prod. A mail-send failure is logged, not fatal
// (the token is valid; the user can resend).
func (uc *authUseCase) issueVerificationEmail(ctx context.Context, user *domain.User) (string, error) {
	rawToken, err := generateSecureToken()
	if err != nil {
		return "", err
	}
	evt := &domain.EmailVerificationToken{
		UserID:    user.ID,
		TokenHash: hashToken(rawToken),
		ExpiresAt: time.Now().Add(emailVerificationTokenDuration),
	}
	if err := uc.authRepo.CreateEmailVerificationToken(ctx, evt); err != nil {
		return "", err
	}
	link := fmt.Sprintf("%s/verify-email?token=%s", uc.cfg.FrontendURL, rawToken)
	if err := uc.mailer.SendVerification(ctx, user.Email, link); err != nil {
		log.Printf("verification email send failed for %s: %v", user.Email, err)
	}
	return rawToken, nil
}

// ForgotPassword issues a reset token and emails the link. It ALWAYS reports
// success (no account enumeration): the caller-facing response is identical
// whether or not the email exists. A debug token is returned only in non-prod.
func (uc *authUseCase) ForgotPassword(ctx context.Context, input domain.ForgotPasswordInput, meta domain.RequestMeta) (*string, error) {
	user, err := uc.authRepo.GetUserByEmail(ctx, input.Email)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if user == nil {
		// Unknown email — succeed silently so existence can't be probed.
		return nil, nil
	}

	// Per-email cooldown. Silently succeed (identical to the unknown-email
	// branch) so the throttle itself can't be probed for account existence.
	if latest, err := uc.authRepo.GetLatestPasswordResetToken(ctx, user.ID); err == nil && latest != nil &&
		time.Since(latest.CreatedAt) < passwordResetRequestCooldown {
		return nil, nil
	}

	// Only the newest link may work: void outstanding tokens so re-requesting
	// narrows (never widens) the interception window.
	if err := uc.authRepo.VoidActivePasswordResetTokens(ctx, user.ID); err != nil {
		return nil, domain.ErrInternal
	}

	rawToken, err := generateSecureToken()
	if err != nil {
		return nil, domain.ErrInternal
	}
	prt := &domain.PasswordResetToken{
		UserID:    user.ID,
		TokenHash: hashToken(rawToken),
		ExpiresAt: time.Now().Add(passwordResetTokenDuration),
	}
	if err := uc.authRepo.CreatePasswordResetToken(ctx, prt); err != nil {
		return nil, domain.ErrInternal
	}

	// Send the email and write the audit event OFF the request path (detached
	// context — the request context is canceled once the handler returns). This
	// keeps response latency independent of whether the account exists: both the
	// existing- and unknown-email branches now return after the same fast DB work
	// instead of the existing branch blocking on the Resend round-trip, which
	// would otherwise be a timing oracle for account enumeration.
	link := fmt.Sprintf("%s/reset-password?token=%s", uc.cfg.FrontendURL, rawToken)
	email := user.Email
	orgID := orgPtr(user.OrgID)
	targetID := user.ID
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := uc.mailer.SendPasswordReset(bg, email, link); err != nil {
			log.Printf("forgot-password: failed to send reset email to %s: %v", email, err)
			// Surface delivery failures to support instead of losing them in logs:
			// a reset the user never receives looks to them like the reset is broken.
			uc.recordAuthEvent(bg, "security", "reset_email_failed", orgID, nil, &targetID, meta,
				map[string]interface{}{"reason": err.Error()})
		}
		uc.recordAuthEvent(bg, "security", "password.reset_requested", orgID, nil, &targetID, meta, nil)
	}()

	if debugTokensEnabled(uc.appEnv) {
		return &rawToken, nil
	}
	return nil, nil
}

// ResetPassword consumes a reset token, sets the new password, and invalidates
// every existing session: it revokes all of the user's refresh tokens and bumps
// token_version so outstanding access tokens are rejected immediately (P2). The
// token is single-use and short-TTL.
func (uc *authUseCase) ResetPassword(ctx context.Context, input domain.ResetPasswordInput, meta domain.RequestMeta) error {
	if err := validatePassword(input.Password); err != nil {
		return err
	}

	prt, err := uc.authRepo.GetPasswordResetTokenByHash(ctx, hashToken(input.Token))
	if err != nil {
		return domain.ErrInternal
	}
	if prt == nil || prt.UsedAt != nil || time.Now().After(prt.ExpiresAt) {
		return domain.ErrInvalidResetToken
	}

	user, err := uc.authRepo.GetUserByID(ctx, prt.UserID)
	if err != nil || user == nil {
		return domain.ErrInvalidResetToken
	}

	// Atomically claim the token BEFORE mutating the password. Exactly one caller
	// gets claimed == 1; a replay or a concurrent request gets 0 and is rejected.
	// Claiming first is fail-closed: a claimed-but-not-applied token just needs a
	// fresh reset request — strictly safer than leaving it replayable.
	claimed, err := uc.authRepo.MarkPasswordResetTokenUsed(ctx, prt.ID)
	if err != nil {
		return domain.ErrInternal
	}
	if claimed == 0 {
		return domain.ErrInvalidResetToken
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcryptCost)
	if err != nil {
		return domain.ErrInternal
	}
	hashStr := string(hash)
	user.PasswordHash = &hashStr
	// Completing a reset proves control of the inbox, so verify the email too.
	if user.EmailVerifiedAt == nil {
		now := time.Now()
		user.EmailVerifiedAt = &now
	}
	if err := uc.authRepo.UpdateUser(ctx, user); err != nil {
		return domain.ErrInternal
	}

	// Invalidate every outstanding session: revoke refresh tokens AND bump
	// token_version so already-issued access tokens are rejected immediately
	// (P2) rather than lingering for up to their 2h TTL.
	_ = uc.authRepo.RevokeAllUserRefreshTokens(ctx, user.ID)
	uc.bumpTokenVersion(ctx, user.ID)

	// Alert + audit run on a DETACHED context: the request context is canceled
	// the moment the handler returns, and this codebase has already been bitten
	// by fire-and-forget work dying with the request (the automation webhook
	// lesson). The password is committed either way — these are side effects.
	email := user.Email
	orgID := orgPtr(user.OrgID)
	targetID := user.ID
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := uc.mailer.SendSecurityAlert(bg, email, "Your password was changed",
			"Your Guerrilla CRM password was just changed. If this was you, no action is needed. If you did not do this, reset your password immediately and contact your workspace admin."); err != nil {
			log.Printf("reset-password: failed to send security alert to %s: %v", email, err)
		}
		uc.recordAuthEvent(bg, "security", "password.reset", orgID, &targetID, &targetID, meta, nil)
	}()

	return nil
}

// VerifyEmail consumes a verification token and stamps EmailVerifiedAt. Public
// (token-authenticated); idempotent if already verified.
func (uc *authUseCase) VerifyEmail(ctx context.Context, input domain.VerifyEmailInput) error {
	evt, err := uc.authRepo.GetEmailVerificationTokenByHash(ctx, hashToken(input.Token))
	if err != nil {
		return domain.ErrInternal
	}
	if evt == nil || evt.UsedAt != nil || time.Now().After(evt.ExpiresAt) {
		return domain.ErrInvalidVerifyToken
	}

	user, err := uc.authRepo.GetUserByID(ctx, evt.UserID)
	if err != nil || user == nil {
		return domain.ErrInvalidVerifyToken
	}

	// Atomically claim the token (single-use) before applying verification, so a
	// replay or concurrent request can't consume it twice.
	claimed, err := uc.authRepo.MarkEmailVerificationTokenUsed(ctx, evt.ID)
	if err != nil {
		return domain.ErrInternal
	}
	if claimed == 0 {
		return domain.ErrInvalidVerifyToken
	}

	if user.EmailVerifiedAt == nil {
		now := time.Now()
		user.EmailVerifiedAt = &now
		if err := uc.authRepo.UpdateUser(ctx, user); err != nil {
			return domain.ErrInternal
		}
	}
	uc.recordAuthEvent(ctx, "security", "email.verified", orgPtr(user.OrgID), &user.ID, &user.ID, domain.RequestMeta{}, nil)

	return nil
}

// ResendVerification re-issues a verification email for the authenticated user.
// No-op success if already verified; throttled per user (a lightweight cooldown
// until the P2 rate-limit middleware lands).
func (uc *authUseCase) ResendVerification(ctx context.Context, userID uuid.UUID, meta domain.RequestMeta) (*string, error) {
	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}
	if user.EmailVerifiedAt != nil {
		return nil, nil // already verified — nothing to do
	}

	if latest, _ := uc.authRepo.GetLatestEmailVerificationToken(ctx, userID); latest != nil &&
		time.Since(latest.CreatedAt) < resendVerificationCooldown {
		return nil, domain.ErrResendTooSoon
	}

	rawToken, err := uc.issueVerificationEmail(ctx, user)
	if err != nil {
		return nil, domain.ErrInternal
	}
	uc.recordAuthEvent(ctx, "security", "email.verification_sent", orgPtr(user.OrgID), &userID, &userID, meta, nil)

	if debugTokensEnabled(uc.appEnv) {
		return &rawToken, nil
	}
	return nil, nil
}
