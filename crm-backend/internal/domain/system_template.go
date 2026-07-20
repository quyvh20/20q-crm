package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ============================================================
// Template spec — the shapes parsed out of a SystemTemplate's JSONB columns
// ============================================================
//
// These deliberately mirror the CreateXInput types this package already exposes,
// so the apply engine is a straight translation with no field invention. Where a
// spec type diverges from its Input counterpart the reason is noted inline.
//
// None of the 2022 seed payloads parse into these. That is intentional and not a
// regression: `pipeline_stages` held bare strings (a stage is a relational row
// with position/color/is_won/is_lost, and deals reference it by UUID);
// `custom_field_defs` used `currency`/`multiselect`, which are not in
// ValidFieldTypes, and omitted the required `entity_type`; `automation_rules` was
// never populated at all. Those rows are superseded by the boot seed.

// TemplateStage is 1:1 with CreateStageInput.
//
// Position is REQUIRED and must be dense 0..N-1. CreateStageInput.Position is a
// non-pointer int with no binding tag, so an omitted position is indistinguishable
// from 0 and every stage would silently land in the same slot.
type TemplateStage struct {
	Name     string `json:"name"`
	Position int    `json:"position"`
	Color    string `json:"color"`
	IsWon    bool   `json:"is_won"`
	IsLost   bool   `json:"is_lost"`
}

// TemplateFieldDef adds a field to one of the three SYSTEM objects. EntityType is
// required and must be in ValidEntityTypes; custom-object fields use
// TemplateObjectField instead (the system-object path rejects custom slugs).
type TemplateFieldDef struct {
	EntityType  string   `json:"entity_type"`
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Options     []string `json:"options,omitempty"`
	TargetSlug  string   `json:"target_slug,omitempty"`
	ViaField    string   `json:"via_field,omitempty"`
	SourceField string   `json:"source_field,omitempty"`
	Required    bool     `json:"required"`
	Position    int      `json:"position"`
}

// TemplateObjectField is a field on a template-created custom object. It has no
// EntityType — that concept only applies to the system objects.
type TemplateObjectField struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Options     []string `json:"options,omitempty"`
	TargetSlug  string   `json:"target_slug,omitempty"`
	ViaField    string   `json:"via_field,omitempty"`
	SourceField string   `json:"source_field,omitempty"`
	Required    bool     `json:"required"`
	Position    int      `json:"position"`
}

// TemplateObjectDef is 1:1 with CreateObjectDefInput plus its fields.
//
// TargetSlug on a relation field may point at a system slug or at another object
// in the SAME template. The apply engine therefore creates every object without
// its relation fields first and adds them in a second pass — validateFieldDefs on
// the custom-object path does not validate target_slug at all, so a forward
// reference would persist silently as a dangling relation rather than erroring.
type TemplateObjectDef struct {
	Slug        string                `json:"slug"`
	Label       string                `json:"label"`
	LabelPlural string                `json:"label_plural"`
	Icon        string                `json:"icon"`
	Searchable  bool                  `json:"searchable"`
	Fields      []TemplateObjectField `json:"fields"`
}

// Template workflow activation policy.
const (
	// TemplateActivationAuto asks for the workflow to be switched on at apply time.
	// It is honoured ONLY when every action in the tree is internal-only; a tree
	// containing an outbound action is downgraded to needs-review regardless.
	TemplateActivationAuto = "auto"
	// TemplateActivationManual creates the workflow switched off.
	TemplateActivationManual = "manual"
)

// autoActivatableActions is the allow-list of action types a template workflow may
// contain and still be switched ON automatically at apply time.
//
// It is an ALLOW-list, not a deny-list, on purpose: a new action type added later
// defaults to "not auto-activatable" and someone has to think about it, rather than
// silently inheriting permission to run unattended in every customer's workspace.
//
// Deliberately excluded, with reasons:
//   - send_email, send_webhook — obviously outbound.
//   - notify_user — LOOKS internal (its own doc comment says "in-app notification"),
//     but the wired notifier is the platform NotificationUseCase, which sends real
//     email whenever the recipient's preference has email on and digest off.
//   - ai_generate — outbound network call to the AI gateway, and it spends token budget.
//   - enroll_records — internal itself, but it enrolls records into ANOTHER workflow
//     that may contain outbound actions one hop away.
//   - assign_user — mutates ownership and advances a round-robin cursor; harmless
//     technically, but reassigning a customer's records unattended is a surprise.
var autoActivatableActions = map[string]bool{
	"create_task":    true,
	"update_record":  true,
	"update_contact": true, // deprecated alias, same executor as update_record
	"log_activity":   true,
	"create_record":  true,
	"find_records":   true, // read-only
	"delay":          true, // pure control flow
}

// IsAutoActivatableAction reports whether an action type may run unattended in a
// freshly applied template.
func IsAutoActivatableAction(actionType string) bool {
	return autoActivatableActions[actionType]
}

// TemplateTrigger is the workflow's trigger. Params is left raw so the automation
// package stays the single authority on per-trigger shape.
type TemplateTrigger struct {
	Type   string `json:"type"`
	Params JSON   `json:"params,omitempty"`
}

// TemplateWorkflow is one automation shipped by a template.
//
// Steps is raw JSON decoded by the apply engine into the automation package's
// StepSpec — domain must not import automation (automation imports domain).
//
// There is deliberately no top-level `conditions` field: in a steps-based workflow
// it is silently ignored. Branching must be a `condition` step carrying
// yes_steps/no_steps, and an If/Else is a terminal fork whose branches never merge.
type TemplateWorkflow struct {
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Activation  string          `json:"activation"`
	Trigger     TemplateTrigger `json:"trigger"`
	Steps       JSON            `json:"steps"`
}

// ============================================================
// Apply results
// ============================================================

// Per-item outcome of an apply.
const (
	TemplateItemCreated = "created"
	TemplateItemSkipped = "skipped"
	TemplateItemFailed  = "failed"
	// TemplateItemNeedsReview: created, but left switched off for a human to check.
	TemplateItemNeedsReview = "needs_review"
)

// Overall outcome of an apply.
const (
	TemplateApplyApplied        = "applied"
	TemplateApplyPartial        = "partial"
	TemplateApplyAlreadyApplied = "already_applied"
	TemplateApplyFailed         = "failed"
)

type TemplateApplyItem struct {
	// Kind is one of: pipeline_stage, object_def, field_def, kb_section, workflow.
	Kind   string `json:"kind"`
	Key    string `json:"key"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	ID     string `json:"id,omitempty"`
	Error  string `json:"error,omitempty"`
}

type TemplateApplyResult struct {
	TemplateSlug string              `json:"template_slug"`
	Status       string              `json:"status"`
	SpecVersion  int                 `json:"spec_version"`
	Items        []TemplateApplyItem `json:"items"`
	// Warnings surface conditions the user must act on but which are not failures —
	// e.g. custom roles with no seed lineage, which cannot see the new objects until
	// an admin grants access by hand.
	Warnings []string `json:"warnings,omitempty"`
}

// ============================================================
// Application ledger
// ============================================================

// OrgTemplateApplication records that an org applied a template. UNIQUE on
// (org_id, template_slug) — it is what makes a second apply a reported no-op
// rather than a duplicate install.
type OrgTemplateApplication struct {
	ID           uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID        uuid.UUID `gorm:"type:uuid;not null" json:"org_id"`
	TemplateSlug string    `gorm:"size:100;not null" json:"template_slug"`
	SpecVersion  int       `gorm:"not null;default:1" json:"spec_version"`
	Status       string    `gorm:"size:32;not null" json:"status"`
	Result       JSON      `gorm:"type:jsonb;default:'{}'" json:"result"`
	AppliedBy    uuid.UUID `gorm:"type:uuid" json:"applied_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (OrgTemplateApplication) TableName() string { return "org_template_applications" }

// ============================================================
// DTOs
// ============================================================

// SystemTemplateView is the catalog row. It carries counts rather than the spec
// so the picker can render 20+ cards without shipping every payload.
type SystemTemplateView struct {
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	Description   string `json:"description"`
	Icon          string `json:"icon"`
	SortOrder     int    `json:"sort_order"`
	StageCount    int    `json:"stage_count"`
	ObjectCount   int    `json:"object_count"`
	FieldCount    int    `json:"field_count"`
	WorkflowCount int    `json:"workflow_count"`
	HasKB         bool   `json:"has_kb"`
	Applied       bool   `json:"applied"`
}

type SystemTemplateDetail struct {
	SystemTemplateView
	Stages     []TemplateStage     `json:"stages"`
	Objects    []TemplateObjectDef `json:"objects"`
	Fields     []TemplateFieldDef  `json:"fields"`
	Workflows  []TemplateWorkflow  `json:"workflows"`
	AIContext  string              `json:"ai_context"`
	KBSections map[string]string   `json:"kb_sections"`
}

// ============================================================
// Ports
// ============================================================

// SystemTemplateRepository reads the GLOBAL template catalog. Note the absence of
// an orgID on List/GetBySlug: system_templates has no org_id column and is shared
// by every workspace. The ledger methods are the org-scoped half.
type SystemTemplateRepository interface {
	List(ctx context.Context, activeOnly bool) ([]SystemTemplate, error)
	GetBySlug(ctx context.Context, slug string) (*SystemTemplate, error)
	RecordApplication(ctx context.Context, app *OrgTemplateApplication) error
	GetApplication(ctx context.Context, orgID uuid.UUID, slug string) (*OrgTemplateApplication, error)
	ListApplications(ctx context.Context, orgID uuid.UUID) ([]OrgTemplateApplication, error)
}

type SystemTemplateUseCase interface {
	List(ctx context.Context, orgID uuid.UUID) ([]SystemTemplateView, error)
	Get(ctx context.Context, orgID uuid.UUID, slug string) (*SystemTemplateDetail, error)
	ListApplied(ctx context.Context, orgID uuid.UUID) ([]OrgTemplateApplication, error)
	Apply(ctx context.Context, orgID, userID uuid.UUID, slug string, force bool) (*TemplateApplyResult, error)
}
