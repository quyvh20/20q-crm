package integrations

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository is the integrations persistence layer. Every query is org-scoped;
// the only exception is the capture-time token probe, which resolves the org FROM
// the credential (there is no caller to scope by).
type Repository struct {
	db *gorm.DB
}

// NewRepository builds the repository.
func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// ── Lead sources ─────────────────────────────────────────────────────────────

// CreateSource inserts a source.
func (r *Repository) CreateSource(ctx context.Context, s *LeadSource) error {
	return r.db.WithContext(ctx).Create(s).Error
}

// UpdateSource saves a source's mutable fields. It is org-guarded by the caller's
// prior GetSource, and Save writes by primary key.
func (r *Repository) UpdateSource(ctx context.Context, s *LeadSource) error {
	return r.db.WithContext(ctx).Save(s).Error
}

// GetSource returns one source by id within an org, or (nil, nil) when absent.
func (r *Repository) GetSource(ctx context.Context, orgID, id uuid.UUID) (*LeadSource, error) {
	var s LeadSource
	err := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).First(&s).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// ListSources returns an org's sources, newest first.
func (r *Repository) ListSources(ctx context.Context, orgID uuid.UUID) ([]LeadSource, error) {
	var out []LeadSource
	err := r.db.WithContext(ctx).Where("org_id = ?", orgID).Order("created_at DESC").Find(&out).Error
	return out, err
}

// SoftDeleteSource retires a source. Soft, not hard: hard-deleting would orphan or
// cascade away its ledger rows — the very history the source exists to explain.
// Token lookups exclude soft-deleted rows via gorm's DeletedAt, so the credential
// dies immediately even though the record remains.
func (r *Repository) SoftDeleteSource(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).Delete(&LeadSource{}).Error
}

// FindSourceByTokenHash resolves a capture credential to its source.
//
// Deliberately NOT org-scoped: the credential IS the org claim — there is no
// caller to scope by. The returned source's OrgID is therefore the authority for
// everything downstream, and callers must never take an org from the request.
// Soft-deleted sources are excluded, so deleting a source revokes its key.
func (r *Repository) FindSourceByTokenHash(ctx context.Context, hash string) (*LeadSource, error) {
	if hash == "" {
		return nil, nil
	}
	var s LeadSource
	err := r.db.WithContext(ctx).Where("token_hash = ?", hash).First(&s).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// TouchSourceUsed stamps last_used_at and resets the failure counter. Best-effort:
// a failure here must never fail a lead that was already written.
func (r *Repository) TouchSourceUsed(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Model(&LeadSource{}).Where("id = ?", id).
		Updates(map[string]any{"last_used_at": time.Now(), "consecutive_failures": 0}).Error
}

// CountCreatedToday counts records this source has created since UTC midnight —
// the daily cap's backstop. Indexed by (source_id, created_at).
func (r *Repository) CountCreatedToday(ctx context.Context, sourceID uuid.UUID, now time.Time) (int64, error) {
	midnight := now.UTC().Truncate(24 * time.Hour)
	var n int64
	err := r.db.WithContext(ctx).Model(&IntegrationEvent{}).
		Where("source_id = ? AND created_at >= ? AND outcome = ?", sourceID, midnight, OutcomeCreated).
		Count(&n).Error
	return n, err
}

// ── Events ───────────────────────────────────────────────────────────────────

// InsertEventDeduped inserts a delivery, returning inserted=false when the
// provider already delivered it.
//
// Dedupe rides the partial unique indexes on (source_id, provider_event_id) and
// (connection_id, provider_event_id). Two indexes, not one, because Postgres
// treats NULLs as DISTINCT: a provider webhook resolves its connection but not yet
// its source, so a single source-scoped index would never fire for it and every
// retry would create a duplicate contact — on the highest-volume channel.
//
// An event with no ProviderEventID cannot dedupe (the index predicate excludes
// NULLs) and always inserts; that is correct — without a stable delivery id there
// is nothing to be idempotent against.
func (r *Repository) InsertEventDeduped(ctx context.Context, e *IntegrationEvent) (inserted bool, err error) {
	if e.ProviderEventID == nil || *e.ProviderEventID == "" {
		return true, r.db.WithContext(ctx).Create(e).Error
	}
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(e)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// FindEventByProviderID returns a prior delivery with this id for the source, used
// to answer a redelivery with the original result instead of re-running it.
func (r *Repository) FindEventByProviderID(ctx context.Context, sourceID uuid.UUID, providerEventID string) (*IntegrationEvent, error) {
	var e IntegrationEvent
	err := r.db.WithContext(ctx).
		Where("source_id = ? AND provider_event_id = ?", sourceID, providerEventID).
		Order("created_at ASC").First(&e).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

// FinishEvent records a delivery's outcome. The result id is persisted BEFORE any
// side effect fires, so a crash between the write and the notification cannot make
// a retry create a second contact.
func (r *Repository) FinishEvent(ctx context.Context, e *IntegrationEvent) error {
	now := time.Now()
	e.ProcessedAt = &now
	return r.db.WithContext(ctx).Save(e).Error
}

// ListEvents returns a source's recent deliveries, newest first — the ledger view.
func (r *Repository) ListEvents(ctx context.Context, orgID uuid.UUID, sourceID *uuid.UUID, limit int) ([]IntegrationEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("org_id = ?", orgID)
	if sourceID != nil {
		q = q.Where("source_id = ?", *sourceID)
	}
	var out []IntegrationEvent
	err := q.Order("created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}
