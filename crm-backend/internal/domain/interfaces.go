package domain

import (
	"context"
	"mime/multipart"

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
}

type RefreshInput struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type SwitchWorkspaceInput struct {
	OrgID uuid.UUID `json:"org_id" binding:"required"`
}

type WorkspaceInfo struct {
	OrgID   uuid.UUID `json:"org_id"`
	OrgName string    `json:"org_name"`
	OrgType string    `json:"org_type"`
	Role    string    `json:"role"`
	Status  string    `json:"status"`
}

type AuthResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	User         User            `json:"user"`
	Workspaces   []WorkspaceInfo `json:"workspaces"`
}

type InviteMemberInput struct {
	Email string `json:"email" binding:"required,email"`
	Role  string `json:"role" binding:"required"`
}

type UpdateMemberRoleInput struct {
	Role string `json:"role" binding:"required"`
}

type MemberInfo struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	FullName  string    `json:"full_name"`
	AvatarURL *string   `json:"avatar_url,omitempty"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
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
	RevokeRefreshToken(ctx context.Context, tokenID uuid.UUID) error
	RevokeAllUserRefreshTokens(ctx context.Context, userID uuid.UUID) error

	CreateOrgUser(ctx context.Context, ou *OrgUser) error
	GetOrgUser(ctx context.Context, userID, orgID uuid.UUID) (*OrgUser, error)
	ListOrgsByUserID(ctx context.Context, userID uuid.UUID) ([]OrgUser, error)
	ListMembersByOrgID(ctx context.Context, orgID uuid.UUID) ([]OrgUser, error)
	UpdateOrgUserRole(ctx context.Context, userID, orgID uuid.UUID, role string) error
	DeleteOrgUser(ctx context.Context, userID, orgID uuid.UUID) error
	GetOrgUserByEmail(ctx context.Context, email string, orgID uuid.UUID) (*OrgUser, error)
}

type AuthUseCase interface {
	Register(ctx context.Context, input RegisterInput) (*AuthResponse, error)
	Login(ctx context.Context, input LoginInput) (*AuthResponse, error)
	RefreshToken(ctx context.Context, refreshToken string) (*AuthResponse, error)
	Logout(ctx context.Context, refreshToken string) error
	GetMe(ctx context.Context, userID uuid.UUID) (*User, error)
	GoogleLogin(ctx context.Context, code string) (*AuthResponse, error)
	GetGoogleAuthURL(state string) string
	SwitchWorkspace(ctx context.Context, userID uuid.UUID, input SwitchWorkspaceInput) (*AuthResponse, error)
	ListWorkspaces(ctx context.Context, userID uuid.UUID) ([]WorkspaceInfo, error)
}

type WorkspaceUseCase interface {
	ListMembers(ctx context.Context, orgID uuid.UUID) ([]MemberInfo, error)
	InviteMember(ctx context.Context, orgID uuid.UUID, input InviteMemberInput) (*MemberInfo, error)
	UpdateMemberRole(ctx context.Context, orgID uuid.UUID, targetUserID uuid.UUID, input UpdateMemberRoleInput) error
	RemoveMember(ctx context.Context, orgID uuid.UUID, targetUserID uuid.UUID) error
}

type ContactFilter struct {
	Q           string      `form:"q"`
	Semantic    bool        `form:"semantic"`
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
	CountByOrg(ctx context.Context, orgID uuid.UUID) (int64, error)
}

type PipelineStageUseCase interface {
	List(ctx context.Context, orgID uuid.UUID) ([]PipelineStage, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*PipelineStage, error)
	Create(ctx context.Context, orgID uuid.UUID, input CreateStageInput) (*PipelineStage, error)
	Update(ctx context.Context, orgID, id uuid.UUID, input UpdateStageInput) (*PipelineStage, error)
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
	Required   bool     `json:"required"`
	Position   *int     `json:"position"`
}

type UpdateFieldDefInput struct {
	Label    *string  `json:"label"`
	Type     *string  `json:"type"`
	Options  []string `json:"options"`
	Required *bool    `json:"required"`
	Position *int     `json:"position"`
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
}

type UpdateObjectDefInput struct {
	Label       *string `json:"label"`
	LabelPlural *string `json:"label_plural"`
	Icon        *string `json:"icon"`
	Fields      JSON    `json:"fields"`
}

type CreateRecordInput struct {
	Data      JSON       `json:"data" binding:"required"`
	ContactID *uuid.UUID `json:"contact_id"`
	DealID    *uuid.UUID `json:"deal_id"`
}

type UpdateRecordInput struct {
	Data        JSON       `json:"data"`
	DisplayName *string    `json:"display_name"`
	ContactID   *uuid.UUID `json:"contact_id"`
	DealID      *uuid.UUID `json:"deal_id"`
}

type RecordFilter struct {
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
	Q      string `json:"q"`
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

type KnowledgeBaseUseCase interface {
	ListSections(ctx context.Context, orgID uuid.UUID) ([]KnowledgeBaseEntry, error)
	GetSection(ctx context.Context, orgID uuid.UUID, section string) (*KnowledgeBaseEntry, error)
	UpsertSection(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, section string, input UpsertKBInput) (*KnowledgeBaseEntry, error)
	GetAIPrompt(ctx context.Context, orgID uuid.UUID) (string, error)
}
