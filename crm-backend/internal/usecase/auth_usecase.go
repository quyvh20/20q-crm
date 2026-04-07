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
	bcryptCost          = 12
	accessTokenDuration = 15 * time.Minute
	refreshTokenDuration = 7 * 24 * time.Hour
	refreshTokenBytes   = 32
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

// ============================================================
// Register
// ============================================================

func (uc *authUseCase) Register(ctx context.Context, input domain.RegisterInput) (*domain.AuthResponse, error) {
	// Check if email already exists
	existing, err := uc.authRepo.GetUserByEmail(ctx, input.Email)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if existing != nil {
		return nil, domain.ErrEmailAlreadyExists
	}

	// Create organization
	org := &domain.Organization{Name: input.OrgName}
	if err := uc.authRepo.CreateOrganization(ctx, org); err != nil {
		return nil, domain.ErrInternal
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcryptCost)
	if err != nil {
		return nil, domain.ErrInternal
	}
	hashStr := string(hash)

	// Create admin user
	user := &domain.User{
		OrgID:        org.ID,
		Email:        input.Email,
		PasswordHash: &hashStr,
		FirstName:    input.FirstName,
		LastName:     input.LastName,
		Role:         "admin",
	}
	if err := uc.authRepo.CreateUser(ctx, user); err != nil {
		return nil, domain.ErrInternal
	}

	// Generate tokens
	accessToken, err := uc.generateAccessToken(user)
	if err != nil {
		return nil, domain.ErrInternal
	}

	refreshToken, err := uc.createRefreshToken(ctx, user.ID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	user.Organization = *org

	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         *user,
	}, nil
}

// ============================================================
// Login
// ============================================================

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

	accessToken, err := uc.generateAccessToken(user)
	if err != nil {
		return nil, domain.ErrInternal
	}

	refreshToken, err := uc.createRefreshToken(ctx, user.ID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	// Load org
	fullUser, _ := uc.authRepo.GetUserByID(ctx, user.ID)
	if fullUser != nil {
		user = fullUser
	}

	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         *user,
	}, nil
}

// ============================================================
// Refresh Token
// ============================================================

func (uc *authUseCase) RefreshToken(ctx context.Context, refreshToken string) (*domain.AuthResponse, error) {
	// Hash the incoming token to find it in DB
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

	// Revoke old token (rotation)
	_ = uc.authRepo.RevokeRefreshToken(ctx, storedToken.ID)

	// Get user
	user, err := uc.authRepo.GetUserByID(ctx, storedToken.UserID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}

	// Issue new tokens
	accessToken, err := uc.generateAccessToken(user)
	if err != nil {
		return nil, domain.ErrInternal
	}

	newRefreshToken, err := uc.createRefreshToken(ctx, user.ID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		User:         *user,
	}, nil
}

// ============================================================
// Logout
// ============================================================

func (uc *authUseCase) Logout(ctx context.Context, refreshToken string) error {
	tokenHash := hashToken(refreshToken)
	storedToken, err := uc.authRepo.GetRefreshTokenByHash(ctx, tokenHash)
	if err != nil || storedToken == nil {
		return nil // silently succeed even if token not found
	}
	return uc.authRepo.RevokeRefreshToken(ctx, storedToken.ID)
}

// ============================================================
// Get Me
// ============================================================

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

// ============================================================
// Google OAuth
// ============================================================

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

	// Exchange code for token
	token, err := uc.oauthConfig.Exchange(ctx, code)
	if err != nil {
		return nil, domain.NewAppError(http.StatusBadRequest, "failed to exchange authorization code")
	}

	// Get user info from Google
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

	// Try to find existing user by Google ID
	user, err := uc.authRepo.GetUserByGoogleID(ctx, googleUser.ID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	if user == nil {
		// Try to find by email (link accounts)
		user, err = uc.authRepo.GetUserByEmail(ctx, googleUser.Email)
		if err != nil {
			return nil, domain.ErrInternal
		}

		if user != nil {
			// Link Google account to existing user
			user.GoogleID = &googleUser.ID
			if googleUser.Picture != "" {
				user.AvatarURL = &googleUser.Picture
			}
			if err := uc.authRepo.UpdateUser(ctx, user); err != nil {
				return nil, domain.ErrInternal
			}
		} else {
			// Create new org + user
			org := &domain.Organization{Name: fmt.Sprintf("%s's Org", googleUser.GivenName)}
			if err := uc.authRepo.CreateOrganization(ctx, org); err != nil {
				return nil, domain.ErrInternal
			}

			user = &domain.User{
				OrgID:     org.ID,
				Email:     googleUser.Email,
				FirstName: googleUser.GivenName,
				LastName:  googleUser.FamilyName,
				Role:      "admin",
				GoogleID:  &googleUser.ID,
				AvatarURL: &googleUser.Picture,
			}
			if err := uc.authRepo.CreateUser(ctx, user); err != nil {
				return nil, domain.ErrInternal
			}
		}
	}

	// Generate tokens
	fullUser, _ := uc.authRepo.GetUserByID(ctx, user.ID)
	if fullUser != nil {
		user = fullUser
	}

	accessToken, err := uc.generateAccessToken(user)
	if err != nil {
		return nil, domain.ErrInternal
	}

	refreshTokenStr, err := uc.createRefreshToken(ctx, user.ID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	return &domain.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshTokenStr,
		User:         *user,
	}, nil
}

// ============================================================
// Token Helpers
// ============================================================

type JWTClaims struct {
	UserID uuid.UUID `json:"user_id"`
	OrgID  uuid.UUID `json:"org_id"`
	Role   string    `json:"role"`
	jwt.RegisteredClaims
}

func (uc *authUseCase) generateAccessToken(user *domain.User) (string, error) {
	claims := JWTClaims{
		UserID: user.ID,
		OrgID:  user.OrgID,
		Role:   user.Role,
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
	// Generate random token
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	rawToken := hex.EncodeToString(b)

	// Store hash in DB
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
