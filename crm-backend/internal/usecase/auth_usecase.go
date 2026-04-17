package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"crm-backend/internal/domain"
	"crm-backend/pkg/config"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	bcryptCost           = 12
	accessTokenDuration  = 15 * time.Minute
	refreshTokenDuration = 7 * 24 * time.Hour
	refreshTokenBytes    = 32
)

type authUseCase struct {
	authRepo    domain.AuthRepository
	cfg         *config.Config
	oauthConfig *oauth2.Config
}

func NewAuthUseCase(repo domain.AuthRepository, cfg *config.Config) domain.AuthUseCase {
	var oauthCfg *oauth2.Config
	if cfg.GoogleClientID != "" {
		oauthCfg = &oauth2.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  cfg.GoogleRedirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		}
	}
	return &authUseCase{
		authRepo:    repo,
		cfg:         cfg,
		oauthConfig: oauthCfg,
	}
}

func (uc *authUseCase) Register(ctx context.Context, input domain.RegisterInput) (*domain.AuthResponse, error) {
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
		Role:         "admin",
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

	accessToken, err := uc.generateAccessToken(user.ID, org.ID, ownerRole.Name)
	if err != nil {
		return nil, domain.NewAppError(500, "Access token err: " + err.Error())
	}

	refreshToken, err := uc.createRefreshToken(ctx, user.ID)
	if err != nil {
		return nil, domain.NewAppError(500, "Refresh token err: " + err.Error())
	}

	workspaces := []domain.WorkspaceInfo{
		{
			OrgID:   org.ID,
			OrgName: org.Name,
			OrgType: org.Type,
			Role:    ownerRole.Name,
			Status:  domain.StatusActive,
		},
	}

	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         *user,
		Workspaces:   workspaces,
	}, nil
}

func (uc *authUseCase) Login(ctx context.Context, input domain.LoginInput) (*domain.AuthResponse, error) {
	user, err := uc.authRepo.GetUserByEmail(ctx, input.Email)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if user == nil || user.PasswordHash == nil {
		return nil, domain.ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(input.Password)); err != nil {
		return nil, domain.ErrInvalidCredentials
	}

	orgUsers, err := uc.authRepo.ListOrgsByUserID(ctx, user.ID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	workspaces := make([]domain.WorkspaceInfo, 0, len(orgUsers))
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
		workspaces = append(workspaces, domain.WorkspaceInfo{
			OrgID:   ou.OrgID,
			OrgName: name,
			OrgType: orgType,
			Role:    roleName,
			Status:  ou.Status,
		})
	}

	var activeOrgID uuid.UUID
	var activeRole string
	if len(orgUsers) > 0 {
		activeOrgID = orgUsers[0].OrgID
		if orgUsers[0].Role != nil {
			activeRole = orgUsers[0].Role.Name
		} else {
			activeRole = "viewer"
		}
	}

	accessToken, err := uc.generateAccessToken(user.ID, activeOrgID, activeRole)
	if err != nil {
		return nil, domain.ErrInternal
	}

	refreshToken, err := uc.createRefreshToken(ctx, user.ID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         *user,
		Workspaces:   workspaces,
	}, nil
}

func (uc *authUseCase) SwitchWorkspace(ctx context.Context, userID uuid.UUID, input domain.SwitchWorkspaceInput) (*domain.AuthResponse, error) {
	ou, err := uc.authRepo.GetOrgUser(ctx, userID, input.OrgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if ou == nil || ou.Status != "active" {
		return nil, domain.ErrNotMember
	}

	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}

	roleName := "viewer"
	if ou.Role != nil {
		roleName = ou.Role.Name
	}
	accessToken, err := uc.generateAccessToken(userID, input.OrgID, roleName)
	if err != nil {
		return nil, domain.ErrInternal
	}

	refreshToken, err := uc.createRefreshToken(ctx, userID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	orgUsers, _ := uc.authRepo.ListOrgsByUserID(ctx, userID)
	workspaces := make([]domain.WorkspaceInfo, 0, len(orgUsers))
	for _, o := range orgUsers {
		name := ""
		orgType := "company"
		if o.Org != nil {
			name = o.Org.Name
			orgType = o.Org.Type
		}
		roleName := "viewer"
		if o.Role != nil {
			roleName = o.Role.Name
		}
		workspaces = append(workspaces, domain.WorkspaceInfo{
			OrgID:   o.OrgID,
			OrgName: name,
			OrgType: orgType,
			Role:    roleName,
			Status:  o.Status,
		})
	}

	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         *user,
		Workspaces:   workspaces,
	}, nil
}

func (uc *authUseCase) ListWorkspaces(ctx context.Context, userID uuid.UUID) ([]domain.WorkspaceInfo, error) {
	orgUsers, err := uc.authRepo.ListOrgsByUserID(ctx, userID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	workspaces := make([]domain.WorkspaceInfo, 0, len(orgUsers))
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
		workspaces = append(workspaces, domain.WorkspaceInfo{
			OrgID:   ou.OrgID,
			OrgName: name,
			OrgType: orgType,
			Role:    roleName,
			Status:  ou.Status,
		})
	}
	return workspaces, nil
}

func (uc *authUseCase) RefreshToken(ctx context.Context, refreshToken string) (*domain.AuthResponse, error) {
	tokenHash := hashToken(refreshToken)

	storedToken, err := uc.authRepo.GetRefreshTokenByHash(ctx, tokenHash)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if storedToken == nil {
		return nil, domain.ErrInvalidToken
	}

	if storedToken.RevokedAt != nil {
		return nil, domain.ErrTokenRevoked
	}

	if time.Now().After(storedToken.ExpiresAt) {
		return nil, domain.ErrTokenExpired
	}

	_ = uc.authRepo.RevokeRefreshToken(ctx, storedToken.ID)

	user, err := uc.authRepo.GetUserByID(ctx, storedToken.UserID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}

	orgUsers, _ := uc.authRepo.ListOrgsByUserID(ctx, user.ID)
	var activeOrgID uuid.UUID
	var activeRole string
	if len(orgUsers) > 0 {
		activeOrgID = orgUsers[0].OrgID
		if orgUsers[0].Role != nil {
			activeRole = orgUsers[0].Role.Name
		} else {
			activeRole = "viewer"
		}
	}

	accessToken, err := uc.generateAccessToken(user.ID, activeOrgID, activeRole)
	if err != nil {
		return nil, domain.ErrInternal
	}

	newRefreshToken, err := uc.createRefreshToken(ctx, user.ID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	workspaces := make([]domain.WorkspaceInfo, 0, len(orgUsers))
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
		workspaces = append(workspaces, domain.WorkspaceInfo{
			OrgID:   ou.OrgID,
			OrgName: name,
			OrgType: orgType,
			Role:    roleName,
			Status:  ou.Status,
		})
	}

	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		User:         *user,
		Workspaces:   workspaces,
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
				Role:      "admin",
				GoogleID:  &googleUser.ID,
				AvatarURL: &googleUser.Picture,
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
		}
	}

	fullUser, _ := uc.authRepo.GetUserByID(ctx, user.ID)
	if fullUser != nil {
		user = fullUser
	}

	orgUsers, _ := uc.authRepo.ListOrgsByUserID(ctx, user.ID)
	var activeOrgID uuid.UUID
	var activeRole string
	if len(orgUsers) > 0 {
		activeOrgID = orgUsers[0].OrgID
		if orgUsers[0].Role != nil {
			activeRole = orgUsers[0].Role.Name
		} else {
			activeRole = "viewer"
		}
	}

	accessToken, err := uc.generateAccessToken(user.ID, activeOrgID, activeRole)
	if err != nil {
		return nil, domain.ErrInternal
	}

	refreshTokenStr, err := uc.createRefreshToken(ctx, user.ID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	workspaces := make([]domain.WorkspaceInfo, 0, len(orgUsers))
	for _, o := range orgUsers {
		name := ""
		orgType := "company"
		if o.Org != nil {
			name = o.Org.Name
			orgType = o.Org.Type
		}
		roleName := "viewer"
		if o.Role != nil {
			roleName = o.Role.Name
		}
		workspaces = append(workspaces, domain.WorkspaceInfo{
			OrgID:   o.OrgID,
			OrgName: name,
			OrgType: orgType,
			Role:    roleName,
			Status:  o.Status,
		})
	}

	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshTokenStr,
		User:         *user,
		Workspaces:   workspaces,
	}, nil
}

type JWTClaims struct {
	UserID uuid.UUID `json:"user_id"`
	OrgID  uuid.UUID `json:"org_id"`
	Role   string    `json:"role"`
	jwt.RegisteredClaims
}

func (uc *authUseCase) generateAccessToken(userID, orgID uuid.UUID, role string) (string, error) {
	claims := JWTClaims{
		UserID: userID,
		OrgID:  orgID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(accessTokenDuration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "20q-crm",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(uc.cfg.JWTSecret))
}

func (uc *authUseCase) createRefreshToken(ctx context.Context, userID uuid.UUID) (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	rawToken := hex.EncodeToString(b)

	tokenHash := hashToken(rawToken)
	rt := &domain.RefreshToken{
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(refreshTokenDuration),
	}
	if err := uc.authRepo.CreateRefreshToken(ctx, rt); err != nil {
		return "", err
	}

	return rawToken, nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
