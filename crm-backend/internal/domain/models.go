package domain

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
)

// ============================================================
// JSON type for GORM JSONB support
// ============================================================

type JSON json.RawMessage

func (j JSON) Value() (driver.Value, error) {
	if len(j) == 0 {
		return "null", nil
	}
	return string(j), nil
}

func (j *JSON) Scan(value interface{}) error {
	if value == nil {
		*j = JSON("null")
		return nil
	}
	switch v := value.(type) {
	case []byte:
		*j = JSON(v)
	case string:
		*j = JSON(v)
	default:
		return fmt.Errorf("cannot scan type %T into JSON", value)
	}
	return nil
}

func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return []byte(j), nil
}

func (j *JSON) UnmarshalJSON(data []byte) error {
	if data == nil {
		return fmt.Errorf("JSON: UnmarshalJSON on nil pointer")
	}
	*j = JSON(data)
	return nil
}

// ============================================================
// Core Entities
// ============================================================

type Organization struct {
	ID                   uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	Name                 string         `gorm:"size:255;not null" json:"name"`
	PlanTier             string         `gorm:"size:50;not null;default:'free'" json:"plan_tier"`
	PaddleSubscriptionID *string        `gorm:"size:255" json:"paddle_subscription_id,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
	DeletedAt            gorm.DeletedAt `gorm:"index" json:"-"`
}

type User struct {
	ID           uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID        uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	Email        string         `gorm:"size:255;uniqueIndex;not null" json:"email"`
	PasswordHash *string        `gorm:"size:255" json:"-"`
	FirstName    string         `gorm:"size:100;not null" json:"first_name"`
	LastName     string         `gorm:"size:100;not null;default:''" json:"last_name"`
	Role         string         `gorm:"type:user_role;not null;default:'viewer'" json:"role"`
	AvatarURL    *string        `gorm:"type:text" json:"avatar_url,omitempty"`
	GoogleID     *string        `gorm:"size:255" json:"-"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`

	Organization Organization `gorm:"foreignKey:OrgID" json:"organization,omitempty"`
}

type RefreshToken struct {
	ID        uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null" json:"user_id"`
	TokenHash string     `gorm:"size:255;not null" json:"-"`
	ExpiresAt time.Time  `gorm:"not null" json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// ============================================================
// CRM Entities
// ============================================================

type Company struct {
	ID           uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID        uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	Name         string         `gorm:"size:255;not null" json:"name"`
	Industry     *string        `gorm:"size:100" json:"industry,omitempty"`
	Website      *string        `gorm:"size:500" json:"website,omitempty"`
	CustomFields JSON           `gorm:"type:jsonb;default:'{}'" json:"custom_fields"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

type Contact struct {
	ID           uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID        uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	FirstName    string         `gorm:"size:100;not null" json:"first_name"`
	LastName     string         `gorm:"size:100;not null;default:''" json:"last_name"`
	Email        *string        `gorm:"size:255" json:"email,omitempty"`
	Phone        *string        `gorm:"size:50" json:"phone,omitempty"`
	CompanyID    *uuid.UUID     `gorm:"type:uuid" json:"company_id,omitempty"`
	OwnerUserID  *uuid.UUID     `gorm:"type:uuid" json:"owner_user_id,omitempty"`
	CustomFields JSON           `gorm:"type:jsonb;default:'{}'" json:"custom_fields"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`

	Company *Company `gorm:"foreignKey:CompanyID" json:"company,omitempty"`
	Owner   *User    `gorm:"foreignKey:OwnerUserID" json:"owner,omitempty"`
	Tags    []Tag    `gorm:"many2many:contact_tags" json:"tags,omitempty"`

	// Vector embedding for semantic search (not serialised to JSON)
	Embedding *pgvector.Vector `gorm:"type:vector(768)" json:"-"`
}

type PipelineStage struct {
	ID        uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID     uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	Name      string         `gorm:"size:100;not null" json:"name"`
	Position  int            `gorm:"not null;default:0" json:"position"`
	Color     string         `gorm:"size:20;default:'#3B82F6'" json:"color"`
	IsWon     bool           `gorm:"not null;default:false" json:"is_won"`
	IsLost    bool           `gorm:"not null;default:false" json:"is_lost"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type Deal struct {
	ID              uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID           uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	Title           string         `gorm:"size:255;not null" json:"title"`
	ContactID       *uuid.UUID     `gorm:"type:uuid" json:"contact_id,omitempty"`
	CompanyID       *uuid.UUID     `gorm:"type:uuid" json:"company_id,omitempty"`
	StageID         *uuid.UUID     `gorm:"type:uuid" json:"stage_id,omitempty"`
	Value           float64        `gorm:"type:numeric(15,2);default:0" json:"value"`
	Probability     int            `gorm:"default:0" json:"probability"`
	OwnerUserID     *uuid.UUID     `gorm:"type:uuid" json:"owner_user_id,omitempty"`
	ExpectedCloseAt *time.Time     `json:"expected_close_at,omitempty"`
	IsWon           bool           `gorm:"not null;default:false" json:"is_won"`
	IsLost          bool           `gorm:"not null;default:false" json:"is_lost"`
	ClosedAt        *time.Time     `json:"closed_at,omitempty"`
	CustomFields    JSON           `gorm:"type:jsonb;default:'{}'" json:"custom_fields"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"-"`

	Contact *Contact       `gorm:"foreignKey:ContactID" json:"contact,omitempty"`
	Company *Company       `gorm:"foreignKey:CompanyID" json:"company,omitempty"`
	Stage   *PipelineStage `gorm:"foreignKey:StageID" json:"stage,omitempty"`
	Owner   *User          `gorm:"foreignKey:OwnerUserID" json:"owner,omitempty"`
}

type Activity struct {
	ID              uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID           uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	Type            string         `gorm:"type:activity_type;not null" json:"type"`
	DealID          *uuid.UUID     `gorm:"type:uuid" json:"deal_id,omitempty"`
	ContactID       *uuid.UUID     `gorm:"type:uuid" json:"contact_id,omitempty"`
	UserID          *uuid.UUID     `gorm:"type:uuid" json:"user_id,omitempty"`
	Title           *string        `gorm:"size:255" json:"title,omitempty"`
	Body            *string        `gorm:"type:text" json:"body,omitempty"`
	DurationMinutes *int           `json:"duration_minutes,omitempty"`
	OccurredAt      time.Time      `gorm:"not null;default:now()" json:"occurred_at"`
	Sentiment       *string        `gorm:"type:text" json:"sentiment,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"-"`
}

type Task struct {
	ID          uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID       uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	Title       string         `gorm:"size:255;not null" json:"title"`
	DealID      *uuid.UUID     `gorm:"type:uuid" json:"deal_id,omitempty"`
	ContactID   *uuid.UUID     `gorm:"type:uuid" json:"contact_id,omitempty"`
	AssignedTo  *uuid.UUID     `gorm:"type:uuid" json:"assigned_to,omitempty"`
	DueAt       *time.Time     `json:"due_at,omitempty"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
	Priority    string         `gorm:"size:20;not null;default:'medium'" json:"priority"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

type Tag struct {
	ID        uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID     uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	Name      string         `gorm:"size:100;not null" json:"name"`
	Color     string         `gorm:"size:20;default:'#6B7280'" json:"color"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type VoiceNote struct {
	ID              uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID           uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	DealID          *uuid.UUID     `gorm:"type:uuid" json:"deal_id,omitempty"`
	ContactID       *uuid.UUID     `gorm:"type:uuid" json:"contact_id,omitempty"`
	FileURL         *string        `gorm:"type:text" json:"file_url,omitempty"`
	DurationSeconds *int           `json:"duration_seconds,omitempty"`
	Status          string         `gorm:"size:50;not null;default:'pending'" json:"status"`
	Transcript      *string        `gorm:"type:text" json:"transcript,omitempty"`
	Summary         *string        `gorm:"type:text" json:"summary,omitempty"`
	KeyPoints       JSON           `gorm:"type:jsonb;default:'[]'" json:"key_points"`
	ActionItems     JSON           `gorm:"type:jsonb;default:'[]'" json:"action_items"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"-"`
}

// ============================================================
// Custom Field Definitions (stored as JSONB in OrgSettings)
// ============================================================

type CustomFieldDef struct {
	Key        string   `json:"key"`                    // machine key, e.g. "budget"
	Label      string   `json:"label"`                  // display label, e.g. "Budget ($)"
	Type       string   `json:"type"`                   // "text" | "number" | "date" | "select" | "boolean" | "url"
	EntityType string   `json:"entity_type"`            // "contact" | "company" | "deal"
	Options    []string `json:"options,omitempty"`       // for "select" type only
	Required   bool     `json:"required"`               // whether field is mandatory
	Position   int      `json:"position"`               // display order
}

// ValidFieldTypes lists the allowed custom field types.
var ValidFieldTypes = map[string]bool{
	"text": true, "number": true, "date": true,
	"select": true, "boolean": true, "url": true,
}

// ValidEntityTypes lists the entity types that support custom fields.
var ValidEntityTypes = map[string]bool{
	"contact": true, "company": true, "deal": true,
}

// ============================================================
// Custom Object Definitions & Records
// ============================================================

type CustomObjectDef struct {
	ID          uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID       uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	Slug        string         `gorm:"size:100;not null" json:"slug"`
	Label       string         `gorm:"size:255;not null" json:"label"`
	LabelPlural string         `gorm:"size:255;not null" json:"label_plural"`
	Icon        string         `gorm:"size:50;default:'📦'" json:"icon"`
	Fields      JSON           `gorm:"type:jsonb;default:'[]'" json:"fields"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

type CustomObjectRecord struct {
	ID          uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID       uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	ObjectDefID uuid.UUID      `gorm:"type:uuid;not null" json:"object_def_id"`
	DisplayName string         `gorm:"size:500" json:"display_name"`
	Data        JSON           `gorm:"type:jsonb;default:'{}'" json:"data"`
	ContactID   *uuid.UUID     `gorm:"type:uuid" json:"contact_id,omitempty"`
	DealID      *uuid.UUID     `gorm:"type:uuid" json:"deal_id,omitempty"`
	CreatedBy   *uuid.UUID     `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
	// Preloaded relations
	Contact *Contact `gorm:"foreignKey:ContactID" json:"contact,omitempty"`
	Deal    *Deal    `gorm:"foreignKey:DealID" json:"deal,omitempty"`
}

// ============================================================
// System / Configuration Entities
// ============================================================

type SystemTemplate struct {
	ID              uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	Slug            string    `gorm:"size:100;uniqueIndex;not null" json:"slug"`
	Name            string    `gorm:"size:255;not null" json:"name"`
	PipelineStages  JSON      `gorm:"type:jsonb;default:'[]'" json:"pipeline_stages"`
	CustomFieldDefs JSON      `gorm:"type:jsonb;default:'[]'" json:"custom_field_defs"`
	AIContext       *string   `gorm:"type:text" json:"ai_context,omitempty"`
	AutomationRules JSON      `gorm:"type:jsonb;default:'[]'" json:"automation_rules"`
	KBTemplates     JSON      `gorm:"type:jsonb;default:'{}'" json:"kb_templates"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type OrgSettings struct {
	OrgID                uuid.UUID `gorm:"type:uuid;primaryKey" json:"org_id"`
	IndustryTemplateSlug *string   `gorm:"size:100" json:"industry_template_slug,omitempty"`
	AIContextOverride    *string   `gorm:"type:text" json:"ai_context_override,omitempty"`
	CustomFieldDefs      JSON      `gorm:"type:jsonb;default:'[]'" json:"custom_field_defs"`
	OnboardingCompleted  bool      `gorm:"not null;default:false" json:"onboarding_completed"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type Workflow struct {
	ID         uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID      uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	Name       string         `gorm:"size:255;not null" json:"name"`
	IsActive   bool           `gorm:"not null;default:false" json:"is_active"`
	Trigger    JSON           `gorm:"type:jsonb;default:'{}'" json:"trigger"`
	Conditions JSON           `gorm:"type:jsonb;default:'[]'" json:"conditions"`
	Actions    JSON           `gorm:"type:jsonb;default:'[]'" json:"actions"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

type WorkflowRun struct {
	ID          uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	WorkflowID  uuid.UUID  `gorm:"type:uuid;not null" json:"workflow_id"`
	Status      string     `gorm:"size:50;not null;default:'running'" json:"status"`
	ContextData JSON       `gorm:"type:jsonb;default:'{}'" json:"context_data"`
	StartedAt   time.Time  `gorm:"not null;default:now()" json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	ErrorMsg    *string    `gorm:"type:text" json:"error_msg,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ============================================================
// AI Token Usage (audit trail)
// ============================================================

type AITokenUsage struct {
	ID           uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID        uuid.UUID `gorm:"type:uuid;not null;index" json:"org_id"`
	UserID       uuid.UUID `gorm:"type:uuid;not null" json:"user_id"`
	Model        string    `gorm:"size:100;not null" json:"model"`
	Provider     string    `gorm:"size:50;not null" json:"provider"`
	Feature      string    `gorm:"size:100;not null" json:"feature"`
	InputTokens  int       `gorm:"not null;default:0" json:"input_tokens"`
	OutputTokens int       `gorm:"not null;default:0" json:"output_tokens"`
	CostUSD      float64   `gorm:"type:numeric(10,6);default:0" json:"cost_usd"`
	CreatedAt    time.Time `json:"created_at"`
}

// ============================================================
// Knowledge Base
// ============================================================

type KnowledgeBaseEntry struct {
	ID        uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID     uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	Section   string         `gorm:"type:text;not null" json:"section"`
	Title     string         `gorm:"type:text;not null" json:"title"`
	Content   string         `gorm:"type:text;not null" json:"content"`
	IsActive  bool           `gorm:"default:true" json:"is_active"`
	CreatedBy *uuid.UUID     `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (KnowledgeBaseEntry) TableName() string { return "knowledge_base" }

// ValidKBSections lists the allowed knowledge base section keys.
var ValidKBSections = map[string]string{
	"company":     "Company Info",
	"products":    "Products & Services",
	"playbook":    "Sales Playbook",
	"process":     "Our Process",
	"competitors": "Competitive Advantages",
}
