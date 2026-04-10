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
	BulkDeleteByIDs(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) (int64, error)
	BulkAssignTag(ctx context.Context, orgID uuid.UUID, contactIDs []uuid.UUID, tagID uuid.UUID) (int64, error)
}

// BulkAction DTOs
type BulkActionInput struct {
	Action     string      `json:"action" binding:"required"` // "delete" | "assign_tag"
	ContactIDs []uuid.UUID `json:"contact_ids" binding:"required,min=1"`
	TagID      *uuid.UUID  `json:"tag_id"` // required when action == "assign_tag"
}

type BulkActionResult struct {
	Affected int    `json:"affected"`
	Message  string `json:"message"`
}


type ContactUseCase interface {
	List(ctx context.Context, orgID uuid.UUID, f ContactFilter) ([]Contact, string, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Contact, error)
	Create(ctx context.Context, orgID uuid.UUID, input CreateContactInput) (*Contact, error)
	Update(ctx context.Context, orgID, id uuid.UUID, input UpdateContactInput) (*Contact, error)
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	BulkImport(ctx context.Context, orgID uuid.UUID, file multipart.File, filename string, conflictMode string) (*ImportResult, error)
	BulkAction(ctx context.Context, orgID uuid.UUID, input BulkActionInput) (*BulkActionResult, error)
	Count(ctx context.Context, orgID uuid.UUID) (int64, error)
}

// ============================================================
// Company DTOs
// ============================================================

type CompanyFilter struct {
	Q      string `form:"q"`
	Cursor string `form:"cursor"`
	Limit  int    `form:"limit"`
}

type CreateCompanyInput struct {
	Name         string     `json:"name" binding:"required,min=1"`
	Industry     *string    `json:"industry"`
	Website      *string    `json:"website"`
	CustomFields JSON       `json:"custom_fields"`
}

type UpdateCompanyInput struct {
	Name         *string    `json:"name"`
	Industry     *string    `json:"industry"`
	Website      *string    `json:"website"`
	CustomFields *JSON      `json:"custom_fields"`
}

// ============================================================
// Company Repository Interface
// ============================================================

type CompanyRepository interface {
	List(ctx context.Context, orgID uuid.UUID, f CompanyFilter) ([]Company, string, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Company, error)
	Create(ctx context.Context, c *Company) error
	Update(ctx context.Context, c *Company) error
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) error
	Count(ctx context.Context, orgID uuid.UUID) (int64, error)
}

// ============================================================
// Company UseCase Interface
// ============================================================

type CompanyUseCase interface {
	List(ctx context.Context, orgID uuid.UUID, f CompanyFilter) ([]Company, string, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Company, error)
	Create(ctx context.Context, orgID uuid.UUID, input CreateCompanyInput) (*Company, error)
	Update(ctx context.Context, orgID, id uuid.UUID, input UpdateCompanyInput) (*Company, error)
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	Count(ctx context.Context, orgID uuid.UUID) (int64, error)
}

// ============================================================
// Tag DTOs
// ============================================================

type CreateTagInput struct {
	Name  string `json:"name" binding:"required,min=1"`
	Color string `json:"color"`
}

type UpdateTagInput struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

// ============================================================
// Tag Repository Interface
// ============================================================

type TagRepository interface {
	List(ctx context.Context, orgID uuid.UUID) ([]Tag, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Tag, error)
	Create(ctx context.Context, t *Tag) error
	Update(ctx context.Context, t *Tag) error
	Delete(ctx context.Context, orgID, id uuid.UUID) error
}

// ============================================================
// Tag UseCase Interface
// ============================================================

type TagUseCase interface {
	List(ctx context.Context, orgID uuid.UUID) ([]Tag, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Tag, error)
	Create(ctx context.Context, orgID uuid.UUID, input CreateTagInput) (*Tag, error)
	Update(ctx context.Context, orgID, id uuid.UUID, input UpdateTagInput) (*Tag, error)
	Delete(ctx context.Context, orgID, id uuid.UUID) error
}

// ============================================================
// Deal DTOs
// ============================================================

type DealFilter struct {
	Q           string     `form:"q"`
	StageID     *uuid.UUID `form:"stage_id"`
	OwnerUserID *uuid.UUID `form:"owner_user_id"`
	ContactID   *uuid.UUID `form:"contact_id"`
	Cursor      string     `form:"cursor"`
	Limit       int        `form:"limit"`
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

// ============================================================
// Deal Repository Interface
// ============================================================

type DealRepository interface {
	List(ctx context.Context, orgID uuid.UUID, f DealFilter) ([]Deal, string, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Deal, error)
	Create(ctx context.Context, d *Deal) error
	Update(ctx context.Context, d *Deal) error
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) error
	Count(ctx context.Context, orgID uuid.UUID) (int64, error)
	Forecast(ctx context.Context, orgID uuid.UUID) ([]ForecastRow, error)
}

// ============================================================
// Deal UseCase Interface
// ============================================================

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

// ============================================================
// PipelineStage DTOs
// ============================================================

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

// ============================================================
// PipelineStage Repository Interface
// ============================================================

type PipelineStageRepository interface {
	List(ctx context.Context, orgID uuid.UUID) ([]PipelineStage, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*PipelineStage, error)
	Create(ctx context.Context, s *PipelineStage) error
	Update(ctx context.Context, s *PipelineStage) error
	CountByOrg(ctx context.Context, orgID uuid.UUID) (int64, error)
}

// ============================================================
// PipelineStage UseCase Interface
// ============================================================

type PipelineStageUseCase interface {
	List(ctx context.Context, orgID uuid.UUID) ([]PipelineStage, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*PipelineStage, error)
	Create(ctx context.Context, orgID uuid.UUID, input CreateStageInput) (*PipelineStage, error)
	Update(ctx context.Context, orgID, id uuid.UUID, input UpdateStageInput) (*PipelineStage, error)
}

// ============================================================
// Activity DTOs
// ============================================================

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

// ============================================================
// Activity Repository Interface
// ============================================================

type ActivityRepository interface {
	List(ctx context.Context, orgID uuid.UUID, f ActivityFilter) ([]Activity, error)
	Create(ctx context.Context, a *Activity) error
}

// ============================================================
// Activity UseCase Interface
// ============================================================

type ActivityUseCase interface {
	List(ctx context.Context, orgID uuid.UUID, f ActivityFilter) ([]Activity, error)
	Create(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, input CreateActivityInput) (*Activity, error)
}

// ============================================================
// Task DTOs
// ============================================================

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
	Priority   string     `json:"priority"` // low, medium, high
}

type UpdateTaskInput struct {
	Title      *string    `json:"title"`
	AssignedTo *uuid.UUID `json:"assigned_to"`
	DueAt      *string    `json:"due_at"`
	Priority   *string    `json:"priority"`
	Completed  *bool      `json:"completed"`
}

// ============================================================
// Task Repository Interface
// ============================================================

type TaskRepository interface {
	List(ctx context.Context, orgID uuid.UUID, f TaskFilter) ([]Task, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Task, error)
	Create(ctx context.Context, t *Task) error
	Update(ctx context.Context, t *Task) error
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) error
}

// ============================================================
// Task UseCase Interface
// ============================================================

type TaskUseCase interface {
	List(ctx context.Context, orgID uuid.UUID, f TaskFilter) ([]Task, error)
	Create(ctx context.Context, orgID uuid.UUID, input CreateTaskInput) (*Task, error)
	Update(ctx context.Context, orgID uuid.UUID, id uuid.UUID, input UpdateTaskInput) (*Task, error)
	Delete(ctx context.Context, orgID uuid.UUID, id uuid.UUID) error
}

// ============================================================
// User Listing (for assignee dropdowns)
// ============================================================

type UserRepository interface {
	ListByOrgID(ctx context.Context, orgID uuid.UUID) ([]User, error)
}

