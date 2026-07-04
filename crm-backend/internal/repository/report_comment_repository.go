package repository

import (
	"context"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// reportCommentRepository persists a report's comment thread. Authorization is
// the usecase's job (via the report usecase's ResolveAccess); this layer only
// reads/writes rows and resolves author display names.
type reportCommentRepository struct {
	db *gorm.DB
}

func NewReportCommentRepository(db *gorm.DB) domain.ReportCommentRepository {
	return &reportCommentRepository{db: db}
}

func (r *reportCommentRepository) Create(ctx context.Context, c *domain.ReportComment) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// ListByReport returns non-deleted comments oldest-first with the author's
// display name resolved (same expression the share repo uses). CanDelete is
// left false — the usecase computes it per caller.
func (r *reportCommentRepository) ListByReport(ctx context.Context, orgID, reportID uuid.UUID) ([]domain.ReportCommentView, error) {
	// Scan through a struct so GORM uses uuid.UUID's Scanner: a bare uuid dest is
	// treated as [16]byte and fails on the driver's string value. author_id is
	// nullable → *uuid.UUID.
	type row struct {
		ID         uuid.UUID
		AuthorID   *uuid.UUID
		Body       string
		CreatedAt  time.Time
		AuthorName string
	}
	var rows []row
	err := r.db.WithContext(ctx).Raw(`
		SELECT c.id, c.author_id, c.body, c.created_at,
		       COALESCE(NULLIF(u.full_name,''), NULLIF(TRIM(u.first_name||' '||u.last_name),''), u.email) AS author_name
		FROM report_comments c LEFT JOIN users u ON u.id = c.author_id
		WHERE c.org_id = ? AND c.report_id = ? AND c.deleted_at IS NULL
		ORDER BY c.created_at ASC`, orgID, reportID).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]domain.ReportCommentView, 0, len(rows))
	for _, rw := range rows {
		name := rw.AuthorName
		if name == "" {
			name = "(removed)"
		}
		out = append(out, domain.ReportCommentView{
			ID: rw.ID, AuthorID: rw.AuthorID, AuthorName: name, Body: rw.Body, CreatedAt: rw.CreatedAt,
		})
	}
	return out, nil
}

func (r *reportCommentRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.ReportComment, error) {
	var c domain.ReportComment
	err := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).First(&c).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (r *reportCommentRepository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		Delete(&domain.ReportComment{})
	return res.RowsAffected, res.Error
}
