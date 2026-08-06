package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ============================================================
// Pipelines (R9.3)
// ============================================================
//
// Before this, an org had exactly ONE pipeline and it was implicit: the org's
// entire pipeline_stages row set, ordered by position. Every stage query was
// keyed on org_id alone. A Pipeline makes that implicit board explicit, so an
// org can run more than one sales motion (new business vs renewals, or one per
// product line) without their stages colliding on a single ladder.
//
// Deliberately NOT a registry object. Five branch sites key on the `stage`
// registry field having an EMPTY target_slug, and registering stages/pipelines
// would route them through the OLS/FLS chokepoint (P5a/P5b) — a far larger
// blast radius than this feature. Pipeline reaches reports as a VIRTUAL deal
// field instead (see reportDealVirtualFields).

// Pipeline is one board: an ordered set of stages a deal moves through.
type Pipeline struct {
	ID    uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID uuid.UUID `gorm:"type:uuid;not null;index" json:"org_id"`
	Name  string    `gorm:"size:100;not null" json:"name"`
	// Position orders the pipeline switcher. Not unique — two pipelines may
	// share a position and simply tie-break by name.
	Position int `gorm:"not null;default:0" json:"position"`
	// IsDefault marks the org's fallback board: where a stage or deal lands
	// when nothing names a pipeline, and what the backfill assigns existing
	// rows to. Exactly one per org, enforced at the DB by the partial unique
	// index uq_pipelines_org_default — not by application code, because two
	// instances booting simultaneously would both pass an application check.
	//
	// Chosen over an organizations.default_pipeline_id column: a nullable FK
	// on organizations cannot express "exactly one", and organizations is a
	// hot table this feature has no other reason to touch.
	IsDefault bool           `gorm:"not null;default:false" json:"is_default"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Pipeline) TableName() string { return "pipelines" }

type CreatePipelineInput struct {
	Name string `json:"name" binding:"required,min=1"`
	// Position is optional; the usecase appends to the end when omitted.
	Position *int `json:"position"`
	// SeedDefaultStages fills a brand-new pipeline with the standard five
	// stages. Without it the pipeline is created empty, which is a legitimate
	// choice (the caller intends to define its own ladder) but renders an
	// empty board until stages exist.
	SeedDefaultStages bool `json:"seed_default_stages"`
}

type UpdatePipelineInput struct {
	Name     *string `json:"name"`
	Position *int    `json:"position"`
	// IsDefault promotes this pipeline to the org default, demoting whichever
	// pipeline currently holds it. Demotion-to-nothing is not expressible on
	// purpose: an org must always have exactly one default.
	IsDefault *bool `json:"is_default"`
}

type PipelineRepository interface {
	List(ctx context.Context, orgID uuid.UUID) ([]Pipeline, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Pipeline, error)
	// GetDefault returns the org's default pipeline, or (nil, nil) when the
	// org has none yet (an org created by an older instance mid-deploy, which
	// the boot backfill could not have reached).
	GetDefault(ctx context.Context, orgID uuid.UUID) (*Pipeline, error)
	Create(ctx context.Context, p *Pipeline) error
	Update(ctx context.Context, p *Pipeline) error
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) error
	// SetDefault promotes one pipeline and demotes every other in the org, in
	// ONE transaction — the partial unique index makes any non-atomic ordering
	// a constraint violation rather than a silent double-default.
	SetDefault(ctx context.Context, orgID, id uuid.UUID) error
	// CountOpenDeals reports how many live deals sit on this pipeline. Deleting
	// a pipeline with deals is refused rather than cascaded: stage deletion
	// already demonstrates the failure mode (it is a soft delete, so the
	// deals.stage_id ON DELETE SET NULL never fires and deals orphan at a
	// tombstone id). Repeating that at pipeline scale strands whole boards.
	CountOpenDeals(ctx context.Context, orgID, id uuid.UUID) (int64, error)
	// EnsureDefault lazily creates the org's default pipeline if it has none,
	// under an advisory lock so concurrent callers cannot double-insert. It is
	// the safety net for orgs the boot backfill missed.
	EnsureDefault(ctx context.Context, orgID uuid.UUID) (*Pipeline, error)
}

type PipelineUseCase interface {
	List(ctx context.Context, orgID uuid.UUID) ([]Pipeline, error)
	Create(ctx context.Context, orgID uuid.UUID, in CreatePipelineInput) (*Pipeline, error)
	Update(ctx context.Context, orgID, id uuid.UUID, in UpdatePipelineInput) (*Pipeline, error)
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	// ResolveDefault returns the org's default pipeline id, creating the
	// pipeline if the org somehow has none. Every "no pipeline was named"
	// code path funnels through here so the fallback is defined in exactly
	// one place.
	ResolveDefault(ctx context.Context, orgID uuid.UUID) (*Pipeline, error)
}
