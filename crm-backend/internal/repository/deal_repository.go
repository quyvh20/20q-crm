package repository

import (
	"context"
	"fmt"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type dealRepository struct {
	db *gorm.DB
}

func NewDealRepository(db *gorm.DB) domain.DealRepository {
	return &dealRepository{db: db}
}

// dealSortColumns is the whitelist behind DealFilter.SortBy — the only thing
// between a caller-supplied sort_by and an fmt.Sprintf into Order().
//
// created_at and title are NOT NULL in the schema. value and probability are NOT —
// migrations/000002_schema.up.sql declares `value NUMERIC(15,2) DEFAULT 0` and
// `probability INT DEFAULT 0 CHECK (…)`, DEFAULT without NOT NULL, and no boot
// guard in main.go adds one. (An earlier version of this comment asserted the
// opposite; TestKeysetWhitelists_AgreeWithSchemaNullability now reads
// information_schema and the migration so the claim cannot go stale again.)
//
// They carry nullAs rather than nullable because domain.Deal holds them as float64
// and int, which cannot represent NULL: a NULL scans back as 0, so the cursor
// minted from such a row says 0 whatever the column held, and the ORDER BY has to
// agree. `nullable: true` looks like the fix and is not — it is worse than the bug
// it appears to close: the null arms switch on a nil cursor value that a
// non-pointer field never produces, so DESC still dead-stops after the null block
// (2 of 8 rows), and ASC starts REPEATING already-served rows once a null block
// straddles a page boundary, because the "…OR value IS NULL" arm re-admits the
// whole non-null head. Both were reproduced against real Postgres; see
// TestPagination_Deals_NullNumericColumnsWalkExactlyOnce.
//
// Consequence worth knowing: a NULL-valued deal now sorts as 0 rather than under
// Postgres's default NULL placement. That matches what the row already presents
// everywhere else — the API serialises Value 0 for it and the UI renders $0 — so a
// deal shown as $0 no longer jumps to the top of a highest-value-first list.
var dealSortColumns = map[string]keysetColumn{
	"created_at":  {col: "deals.created_at", kind: keysetTime},
	"value":       {col: "deals.value", kind: keysetNumber, nullAs: "0"},
	"probability": {col: "deals.probability", kind: keysetNumber, nullAs: "0"},
	"title":       {col: "deals.title", kind: keysetText},
}

// dealSortValue reads the sort column off the last row of a page so the cursor
// carries the same value the ORDER BY compared. The default arm must match
// newKeysetOrdering's defaultKey below.
func dealSortValue(d *domain.Deal, key string) any {
	switch key {
	case "value":
		return d.Value
	case "probability":
		return d.Probability
	case "title":
		return d.Title
	default:
		return d.CreatedAt
	}
}

// ============================================================
// List — keyset pagination + filters
// ============================================================

func (r *dealRepository) List(ctx context.Context, orgID uuid.UUID, f domain.DealFilter) ([]domain.Deal, string, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	query := applyScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "deals", "deal").
		Preload("Contact").
		Preload("Company").
		Preload("Stage").
		Preload("Owner")

	if f.Q != "" {
		q := "%" + strings.ToLower(f.Q) + "%"
		query = query.Where("LOWER(deals.title) LIKE ?", q)
	}
	if f.StageID != nil {
		query = query.Where("deals.stage_id = ?", *f.StageID)
	}
	if f.OwnerUserID != nil {
		query = query.Where("deals.owner_user_id = ?", *f.OwnerUserID)
	}
	if f.ContactID != nil {
		query = query.Where("deals.contact_id = ?", *f.ContactID)
	}
	if f.CompanyID != nil {
		query = query.Where("deals.company_id = ?", *f.CompanyID)
	}
	if f.PipelineID != nil {
		query = query.Where("deals.pipeline_id = ?", *f.PipelineID)
	}
	for k, v := range f.CustomFilters {
		if k == "" || v == "" {
			continue
		}
		query = query.Where("deals.custom_fields ->> ? = ?", k, v)
	}

	// ── Sort + keyset cursor ─────────────────────────────────────────────────
	// One ordering object emits BOTH the predicate and the ORDER BY. They used to
	// be written 22 lines apart and disagreed — the cursor compared ids while the
	// sort compared created_at — which repeated and skipped rows on every "Load
	// more". See keyset_cursor.go.
	ord := newKeysetOrdering(dealSortColumns, "created_at", "deals.id", f.SortBy, f.SortOrder)
	query = ord.applyCursor(query, f.Cursor)

	var deals []domain.Deal
	if err := query.
		Order(ord.orderClause()).
		Limit(limit + 1).
		Find(&deals).Error; err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(deals) > limit {
		deals = deals[:limit]
		last := deals[len(deals)-1]
		nextCursor = ord.nextCursor(dealSortValue(&last, ord.key), last.ID)
	}

	return deals, nextCursor, nil
}

// ============================================================
// GetByID
// ============================================================

func (r *dealRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Deal, error) {
	var deal domain.Deal
	err := applyScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "deals", "deal").
		Where("deals.id = ?", id).
		Preload("Contact").
		Preload("Company").
		Preload("Stage").
		Preload("Owner").
		First(&deal).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &deal, err
}

// ============================================================
// Create
// ============================================================

func (r *dealRepository) Create(ctx context.Context, d *domain.Deal) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Create(d).Error
}

// ============================================================
// Update
// ============================================================

func (r *dealRepository) Update(ctx context.Context, d *domain.Deal) error {
	// Updates writes by primary key with no scope, so enforce write-level row
	// scope first: read visibility is not write access (U0.4).
	if err := requireWriteVisible(r.db, ctx, d.OrgID, "deals", "deal", d.ID); err != nil {
		return err
	}
	return r.db.WithContext(ctx).
		Model(d).
		// This allow-list is authoritative: a column missing from it is silently
		// NOT written, so the caller's update appears to succeed and does nothing.
		// pipeline_id (R9.3) rides alongside stage_id because every stage writer
		// sets the two together.
		Select(
			"title", "contact_id", "company_id", "stage_id", "pipeline_id",
			"value", "probability", "owner_user_id",
			"expected_close_at", "is_won", "is_lost", "closed_at",
			"custom_fields", "updated_at",
		).
		Updates(d).Error
}

// ============================================================
// SoftDelete
// ============================================================

func (r *dealRepository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) error {
	result := applyWriteScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "deals", "deal").
		Where("deals.id = ?", id).
		Delete(&domain.Deal{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("deal not found")
	}
	return nil
}

// ============================================================
// Count
// ============================================================

func (r *dealRepository) Count(ctx context.Context, orgID uuid.UUID) (int64, error) {
	var count int64
	err := applyScopeFromCtx(r.db.WithContext(ctx).Model(&domain.Deal{}), ctx, orgID, "deals", "deal").
		Count(&count).Error
	return count, err
}

// ============================================================
// Forecast — 12-month rolling revenue forecast
// ============================================================

func (r *dealRepository) Forecast(ctx context.Context, orgID uuid.UUID) ([]domain.ForecastRow, error) {
	var rows []domain.ForecastRow
	sql := `
		SELECT
			TO_CHAR(expected_close_at, 'YYYY-MM') AS month,
			COALESCE(SUM(value * probability / 100.0), 0) AS expected_revenue,
			COUNT(*) AS deals_count
		FROM deals
		WHERE org_id = ?
		  AND is_won = false
		  AND is_lost = false
		  AND deleted_at IS NULL
		  AND expected_close_at IS NOT NULL
		  AND expected_close_at >= DATE_TRUNC('month', NOW())
		  AND expected_close_at < DATE_TRUNC('month', NOW()) + INTERVAL '12 months'
		GROUP BY TO_CHAR(expected_close_at, 'YYYY-MM')
		ORDER BY month ASC
	`
	err := r.db.WithContext(ctx).Raw(sql, orgID).Scan(&rows).Error
	return rows, err
}
