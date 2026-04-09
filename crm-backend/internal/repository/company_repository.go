package repository

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type companyRepository struct {
	db *gorm.DB
}

func NewCompanyRepository(db *gorm.DB) domain.CompanyRepository {
	return &companyRepository{db: db}
}

// ============================================================
// List — keyset pagination + optional name search
// ============================================================

func (r *companyRepository) List(ctx context.Context, orgID uuid.UUID, f domain.CompanyFilter) ([]domain.Company, string, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	query := r.db.WithContext(ctx).
		Where("companies.org_id = ?", orgID)

	// Full-text name filter
	if f.Q != "" {
		q := "%" + strings.ToLower(f.Q) + "%"
		query = query.Where("LOWER(companies.name) LIKE ?", q)
	}

	// Keyset cursor: base64(id)
	if f.Cursor != "" {
		decoded, err := base64.StdEncoding.DecodeString(f.Cursor)
		if err == nil {
			query = query.Where("companies.id < ?", string(decoded))
		}
	}

	var companies []domain.Company
	if err := query.
		Order("companies.created_at DESC, companies.id DESC").
		Limit(limit + 1).
		Find(&companies).Error; err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(companies) > limit {
		companies = companies[:limit]
		last := companies[len(companies)-1]
		nextCursor = base64.StdEncoding.EncodeToString([]byte(last.ID.String()))
	}

	return companies, nextCursor, nil
}

// ============================================================
// GetByID
// ============================================================

func (r *companyRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Company, error) {
	var company domain.Company
	err := r.db.WithContext(ctx).
		Where("id = ? AND org_id = ?", id, orgID).
		First(&company).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &company, err
}

// ============================================================
// Create
// ============================================================

func (r *companyRepository) Create(ctx context.Context, c *domain.Company) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Create(c).Error
}

// ============================================================
// Update
// ============================================================

func (r *companyRepository) Update(ctx context.Context, c *domain.Company) error {
	return r.db.WithContext(ctx).Save(c).Error
}

// ============================================================
// SoftDelete
// ============================================================

func (r *companyRepository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Where("id = ? AND org_id = ?", id, orgID).
		Delete(&domain.Company{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("company not found")
	}
	return nil
}

// ============================================================
// Count
// ============================================================

func (r *companyRepository) Count(ctx context.Context, orgID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&domain.Company{}).
		Where("org_id = ?", orgID).
		Count(&count).Error
	return count, err
}
