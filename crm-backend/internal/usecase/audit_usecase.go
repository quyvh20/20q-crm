package usecase

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// auditUseCase reads the admin/auth audit log for the transparency UI (P4). It is
// read-only — auth_events is append-only and written best-effort from the auth and
// admin usecases; nothing here mutates.
type auditUseCase struct {
	repo domain.AuthRepository
}

func NewAuditUseCase(repo domain.AuthRepository) domain.AuditUseCase {
	return &auditUseCase{repo: repo}
}

// auditListMaxLimit caps a single page (and the CSV export). The audit log is
// indexed by (org_id, created_at); large exports paginate rather than pulling the
// whole table in one query.
const auditListMaxLimit = 10000

func (uc *auditUseCase) ListEvents(ctx context.Context, orgID uuid.UUID, f domain.AuthEventFilter) ([]domain.AuthEventView, int64, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > auditListMaxLimit {
		f.Limit = auditListMaxLimit
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	events, total, err := uc.repo.ListAuthEvents(ctx, orgID, f)
	if err != nil {
		return nil, 0, domain.ErrInternal
	}
	return events, total, nil
}
