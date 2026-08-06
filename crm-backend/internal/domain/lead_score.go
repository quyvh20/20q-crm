package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrLeadScoreTruncated marks a rescore that stopped at its per-org round cap
// with work still to do. It lives in domain so the usecase can distinguish
// "incomplete but progressing" from a hard failure without importing the
// repository package.
var ErrLeadScoreTruncated = errors.New("lead scoring: rescore hit its per-org round cap; the remaining contacts resume on the next pass")

// IsLeadScoreTruncated reports the round-cap sentinel.
func IsLeadScoreTruncated(err error) bool { return errors.Is(err, ErrLeadScoreTruncated) }

// ============================================================
// Lead scoring (R9.4)
// ============================================================
//
// A lead score is a 0–100 integer on a contact, recomputed from org-defined
// rules over data the platform already collects: contact fields ("field fit")
// and marketing engagement (M9's event ledger).
//
// The whole engine is ONE set-based SQL UPDATE per org. That is a deliberate
// architectural constraint rather than an optimisation: the obvious
// alternative — a batch SQL path plus an in-memory evaluator on the write
// path — gives two implementations of "what does this rule mean", and they
// drift. A contact scored 40 by the sweep and 30 by the write hook is a bug
// nobody can reproduce. With only a SQL engine that class of bug cannot exist.
//
// It also means automation's EvaluateConditions is deliberately NOT reused: its
// EvalContext is a frozen trigger snapshot with no database handle, so it
// cannot express an aggregate at all, and its nil-guard makes `neq` return
// FALSE for a missing field — the opposite of what "score +10 if industry is
// not retail" must do for a contact with no industry.

// Lead-scoring rule kinds.
const (
	// LeadRuleKindField tests contact fields. Its Condition is a SegmentFilter,
	// compiled by the same M5 machinery segments use, against the same report
	// field catalog — so anything segmentable is scorable, for free.
	LeadRuleKindField = "field"
	// LeadRuleKindEngagement tests the marketing event ledger. Its Condition is
	// a LeadEngagementCondition, compiled to a correlated EXISTS.
	//
	// A separate KIND rather than a new SegmentFilter leaf, deliberately: a new
	// leaf would have to be taught to SegmentFilter.Touched (the FLS walker —
	// an unrecognised leaf is silently skipped, i.e. unchecked), to the
	// compiler, and to CompileAudienceQuery, and it would leak engagement
	// semantics into every ordinary segment. A second kind is strictly smaller
	// and cannot regress M5.
	LeadRuleKindEngagement = "engagement"
)

// LeadScoreMin/Max bound the final score. Rules may carry negative points —
// "hard bounced: −40" is among the highest-signal rules available — and the
// SUM is clamped, not each rule, which keeps the model order-independent.
const (
	LeadScoreMin = 0
	LeadScoreMax = 100
)

// MaxLeadScoringRules caps rules per org. The compiler emits one CASE arm per
// rule into a single UPDATE, so this bounds statement size and planning time.
const MaxLeadScoringRules = 50

// Scorable engagement events.
//
// Deliberately NOT including email.opened in v1. M9's own analytics already
// documents that opens are dominated by Apple/Gmail prefetch and treats clicks
// as the headline metric; a rule weighting raw opens scores the recipient's
// mail client, not the lead. Adding opens later requires the same 10-second
// de-prefetch guard M9 uses, not a bare event match.
const (
	LeadEventClicked   = "email.clicked"
	LeadEventBounced   = "email.bounced"
	LeadEventComplaint = "email.complained"
)

// ValidLeadEvents is the closed vocabulary an engagement rule may name.
var ValidLeadEvents = map[string]bool{
	LeadEventClicked:   true,
	LeadEventBounced:   true,
	LeadEventComplaint: true,
}

// LeadEngagementCondition is an engagement rule's payload: "at least MinCount
// <Event> events in the last WindowDays days".
type LeadEngagementCondition struct {
	Event      string `json:"event"`
	WindowDays int    `json:"window_days"`
	MinCount   int    `json:"min_count"`
}

// LeadScoringRule is one org-defined scoring rule.
//
// Condition is raw JSON because its shape depends on Kind. The usecase decodes
// and validates it per kind before it ever reaches the compiler.
type LeadScoringRule struct {
	ID        uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID     uuid.UUID      `gorm:"type:uuid;not null;index" json:"org_id"`
	Name      string         `gorm:"size:120;not null" json:"name"`
	Kind      string         `gorm:"size:16;not null;default:'field'" json:"kind"`
	Points    int            `gorm:"not null;default:0" json:"points"`
	Condition JSON           `gorm:"type:jsonb;not null;default:'{}'" json:"condition"`
	Enabled   bool           `gorm:"not null;default:true" json:"enabled"`
	Position  int            `gorm:"not null;default:0" json:"position"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (LeadScoringRule) TableName() string { return "lead_scoring_rules" }

type CreateLeadScoringRuleInput struct {
	Name      string `json:"name" binding:"required,min=1"`
	Kind      string `json:"kind" binding:"required"`
	Points    int    `json:"points"`
	Condition JSON   `json:"condition"`
	Enabled   *bool  `json:"enabled"`
	Position  *int   `json:"position"`
}

type UpdateLeadScoringRuleInput struct {
	Name      *string `json:"name"`
	Points    *int    `json:"points"`
	Condition *JSON   `json:"condition"`
	Enabled   *bool   `json:"enabled"`
	Position  *int    `json:"position"`
}

// LeadScoringRepository is the persistence port. RecomputeOrg is the engine.
type LeadScoringRepository interface {
	ListRules(ctx context.Context, orgID uuid.UUID) ([]LeadScoringRule, error)
	GetRule(ctx context.Context, orgID, id uuid.UUID) (*LeadScoringRule, error)
	CreateRule(ctx context.Context, r *LeadScoringRule) error
	UpdateRule(ctx context.Context, r *LeadScoringRule) error
	DeleteRule(ctx context.Context, orgID, id uuid.UUID) error
	CountRules(ctx context.Context, orgID uuid.UUID) (int64, error)
	// RecomputeOrg rescores every contact in the org. It returns the rows it
	// wrote, a human-readable reason per rule it had to SKIP, and an error.
	//
	// Skipping rather than failing is the point: a rule can stop compiling
	// through an ordinary unrelated action — an admin deletes the custom field
	// it tests — and aborting the whole expression would freeze every contact's
	// score workspace-wide, permanently and with nothing on screen to say so.
	// One broken rule must cost only its own points.
	RecomputeOrg(ctx context.Context, orgID uuid.UUID, catalog []ReportField, rules []LeadScoringRule) (rows int64, skipped []string, err error)
	// OrgsDueForRescore claims orgs whose scores are stale. Driven from
	// organizations (not from a DISTINCT over a child table) so an org whose
	// rules were all just deleted still gets one pass to zero its scores.
	OrgsDueForRescore(ctx context.Context, staleness time.Duration, limit int) ([]uuid.UUID, error)
	MarkOrgRescored(ctx context.Context, orgID uuid.UUID) error
}

// LeadScoringUseCase is the application port.
type LeadScoringUseCase interface {
	ListRules(ctx context.Context, orgID uuid.UUID) ([]LeadScoringRule, error)
	CreateRule(ctx context.Context, orgID uuid.UUID, in CreateLeadScoringRuleInput) (*LeadScoringRule, error)
	UpdateRule(ctx context.Context, orgID, id uuid.UUID, in UpdateLeadScoringRuleInput) (*LeadScoringRule, error)
	DeleteRule(ctx context.Context, orgID, id uuid.UUID) error
	// RecomputeOrgNow rescores one org immediately, for the "apply now" button
	// and for the fire-and-forget rescore after a rule changes.
	RecomputeOrgNow(ctx context.Context, orgID uuid.UUID) (int64, error)
	// RunRescorePass is the sweep's entry point: claim stale orgs, rescore each.
	RunRescorePass(ctx context.Context) (int, error)
}
