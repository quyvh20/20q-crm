package domain

import (
	"context"
	"mime/multipart"

	"github.com/google/uuid"
)

// ============================================================
// Auth DTOs (Data Transfer Objects)
// ============================================================

type RegisterInput struct {
	OrgName   string `json:"org_name" binding:"required,min=2"`
	Email     string `json:"email" binding:"required,email"`
	Password  string `json:"password" binding:"required,min=8"`
	FirstName string `json:"first_name" binding:"required,min=1"`
	LastName  string `json:"last_name"`
}

type LoginInput struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type RefreshInput struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type AuthResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	User         User   `json:"user"`
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

// ============================================================
// Repository Interfaces
// ============================================================

type AuthRepository interface {
	CreateOrganization(ctx context.Context, org *Organization) error
	CreateUser(ctx context.Context, user *User) error
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (*User, error)
	GetUserByGoogleID(ctx context.Context, googleID string) (*User, error)
	UpdateUser(ctx context.Context, user *User) error

	CreateRefreshToken(ctx context.Context, token *RefreshToken) error
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, tokenID uuid.UUID) error
	RevokeAllUserRefreshTokens(ctx context.Context, userID uuid.UUID) error
}

// ============================================================
// UseCase Interfaces
// ============================================================

type AuthUseCase interface {
	Register(ctx context.Context, input RegisterInput) (*AuthResponse, error)
	Login(ctx context.Context, input LoginInput) (*AuthResponse, error)
	RefreshToken(ctx context.Context, refreshToken string) (*AuthResponse, error)
	Logout(ctx context.Context, refreshToken string) error
	GetMe(ctx context.Context, userID uuid.UUID) (*User, error)
	GoogleLogin(ctx context.Context, code string) (*AuthResponse, error)
	GetGoogleAuthURL(state string) string
}

// ============================================================
// Contact DTOs
// ============================================================

type ContactFilter struct {
	Q           string      `form:"q"`
	CompanyID   *uuid.UUID  `form:"company_id"`
	TagIDs      []uuid.UUID `form:"tag_ids"`
	OwnerUserID *uuid.UUID  `form:"owner_user_id"`
	Cursor      string      `form:"cursor"`
	Limit       int         `form:"limit"`
}

type ImportResult struct {
	Created      int      `json:"created"`
	Skipped      int      `json:"skipped"`
	Errors       int      `json:"errors"`
	ErrorDetails []string `json:"error_details,omitempty"`
}

type CreateContactInput struct {
	FirstName    string     `json:"first_name" binding:"required,min=1"`
	LastName     string     `json:"last_name"`
	Email        *string    `json:"email"`
	Phone        *string    `json:"phone"`
	CompanyID    *uuid.UUID `json:"company_id"`
	OwnerUserID  *uuid.UUID `json:"owner_user_id"`
	CustomFields JSON       `json:"custom_fields"`
	TagIDs       []uuid.UUID `json:"tag_ids"`
}

type UpdateContactInput struct {
	FirstName    *string    `json:"first_name"`
	LastName     *string    `json:"last_name"`
	Email        *string    `json:"email"`
	Phone        *string    `json:"phone"`
	CompanyID    *uuid.UUID `json:"company_id"`
	OwnerUserID  *uuid.UUID `json:"owner_user_id"`
	CustomFields *JSON      `json:"custom_fields"`
	TagIDs       *[]uuid.UUID `json:"tag_ids"`
}

// ============================================================
// Contact Repository Interface
// ============================================================

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
}

// ============================================================
// Contact UseCase Interface
// ============================================================

type ContactUseCase interface {
	List(ctx context.Context, orgID uuid.UUID, f ContactFilter) ([]Contact, string, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Contact, error)
	Create(ctx context.Context, orgID uuid.UUID, input CreateContactInput) (*Contact, error)
	Update(ctx context.Context, orgID, id uuid.UUID, input UpdateContactInput) (*Contact, error)
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	BulkImport(ctx context.Context, orgID uuid.UUID, file multipart.File, filename string) (*ImportResult, error)
	Count(ctx context.Context, orgID uuid.UUID) (int64, error)
}
