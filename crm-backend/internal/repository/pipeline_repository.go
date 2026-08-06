package repository

import (
	"context"
	"errors"
	"hash/fnv"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type pipelineRepository struct {
	db *gorm.DB
}

func NewPipelineRepository(db *gorm.DB) domain.PipelineRepository {
	return &pipelineRepository{db: db}
}

func (r *pipelineRepository) List(ctx context.Context, orgID uuid.UUID) ([]domain.Pipeline, error) {
	var out []domain.Pipeline
	// Name is the tie-break because position is not unique — without it two
	// pipelines sharing a position swap places between requests and the
	// switcher reorders itself under the user.
	err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("position ASC, name ASC").
		Find(&out).Error
	return out, err
}

func (r *pipelineRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Pipeline, error) {
	var p domain.Pipeline
	err := r.db.WithContext(ctx).Where("id = ? AND org_id = ?", id, orgID).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *pipelineRepository) GetDefault(ctx context.Context, orgID uuid.UUID) (*domain.Pipeline, error) {
	var p domain.Pipeline
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND is_default", orgID).
		First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *pipelineRepository) Create(ctx context.Context, p *domain.Pipeline) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Create(p).Error
}

func (r *pipelineRepository) Update(ctx context.Context, p *domain.Pipeline) error {
	return r.db.WithContext(ctx).Save(p).Error
}

// SoftDelete removes the board AND its stages, in one transaction.
//
// Taking the stages too is not tidiness. No stage query joins `pipelines`, so a
// stage whose board is soft-deleted stays indistinguishable from a live one:
// it keeps passing the lead-ingest liveness check, keeps appearing in the
// workflow stage picker, and a deal created onto it is stamped with a deleted
// pipeline_id — landing on no board at all, and (because a cross-pipeline move
// is refused) unable to be moved back off it.
func (r *pipelineRepository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("org_id = ? AND pipeline_id = ?", orgID, id).
			Delete(&domain.PipelineStage{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND org_id = ?", id, orgID).
			Delete(&domain.Pipeline{}).Error
	})
}

// SetDefault promotes one pipeline and demotes every other in the org.
//
// ONE transaction, demote-then-promote. The partial unique index
// uq_pipelines_org_default means any other ordering — or splitting this into
// two statements outside a transaction — surfaces as a constraint violation
// mid-flight rather than a silent double-default. That is the index doing its
// job, but it would still be a 500 for the user, so the order matters.
func (r *pipelineRepository) SetDefault(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.Pipeline{}).
			Where("org_id = ? AND id <> ? AND is_default", orgID, id).
			Update("is_default", false).Error; err != nil {
			return err
		}
		res := tx.Model(&domain.Pipeline{}).
			Where("org_id = ? AND id = ?", orgID, id).
			Update("is_default", true)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return domain.ErrPipelineNotFound
		}
		return nil
	})
}

// CountOpenDeals counts LIVE deals on a pipeline. Soft-deleted deals are
// excluded on purpose: a deleted deal must not block deleting the board it
// used to sit on.
func (r *pipelineRepository) CountOpenDeals(ctx context.Context, orgID, id uuid.UUID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&domain.Deal{}).
		Where("org_id = ? AND pipeline_id = ? AND deleted_at IS NULL", orgID, id).
		Count(&n).Error
	return n, err
}

// EnsureDefault returns the org's default pipeline, creating it if absent.
//
// This is the safety net for an org the boot backfill could not have reached:
// one created by an OLD instance during a rolling deploy, after the new
// instance already ran its backfill. Without it that org has stages with a NULL
// pipeline_id and no board to put them on.
//
// The fast path is a plain indexed read with no lock. Only a miss takes the
// advisory lock, re-checks under it, and inserts — the same
// check/lock/re-check shape EnsureSystemObjects uses. pg_advisory_xact_lock
// releases at commit, so no unlock path can be forgotten.
func (r *pipelineRepository) EnsureDefault(ctx context.Context, orgID uuid.UUID) (*domain.Pipeline, error) {
	if p, err := r.GetDefault(ctx, orgID); err != nil || p != nil {
		return p, err
	}

	var out *domain.Pipeline
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// hashtext() is not stable across major Postgres versions; hashing the
		// uuid in Go keeps the lock key reproducible regardless of server.
		h := fnv.New64a()
		_, _ = h.Write([]byte("pipeline-default:" + orgID.String()))
		key := int64(h.Sum64())
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(?)`, key).Error; err != nil {
			return err
		}
		// Re-check under the lock: the racing caller may have created it while
		// we waited, and inserting a second default would violate
		// uq_pipelines_org_default.
		var existing domain.Pipeline
		err := tx.Where("org_id = ? AND is_default", orgID).First(&existing).Error
		if err == nil {
			out = &existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		p := &domain.Pipeline{ID: uuid.New(), OrgID: orgID, Name: "Sales Pipeline", IsDefault: true}
		if err := tx.Create(p).Error; err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, err
}
