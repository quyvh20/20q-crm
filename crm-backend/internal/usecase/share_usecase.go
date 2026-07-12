package usecase

import (
	"context"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// shareUseCase creates/revokes/lists record shares — the escape hatch that lets
// an 'own'-scoped role reach specific records it doesn't own (I2). Ownership is
// enforced by reusing RecordService.Get, which is data-scope-aware: the caller can
// only act on a record it can already see, so an 'own'-scoped role shares only its
// own records while an 'all'-scoped role (manager/admin) can share any.
type shareUseCase struct {
	records  domain.RecordService
	shares   domain.RecordShareRepository
	authRepo domain.AuthRepository
	authz    domain.RecordAuthorizer
}

func NewShareUseCase(records domain.RecordService, shares domain.RecordShareRepository, authRepo domain.AuthRepository, authz domain.RecordAuthorizer) domain.ShareUseCase {
	return &shareUseCase{records: records, shares: shares, authRepo: authRepo, authz: authz}
}

// Share grants recordID (of object slug) to a workspace member. It verifies the
// caller can see the record (ownership/scope gate), the grantee is an active
// member of the org, then upserts the grant idempotently and audits it.
func (uc *shareUseCase) Share(ctx context.Context, orgID, actorID uuid.UUID, slug string, recordID uuid.UUID, in domain.ShareRecordInput) (*domain.RecordShare, error) {
	if _, err := uc.records.Get(ctx, orgID, slug, recordID); err != nil {
		return nil, err // not visible under the caller's scope → 403/404 from RecordService
	}
	if in.GranteeUserID == uuid.Nil {
		return nil, domain.NewAppError(http.StatusBadRequest, "grantee_user_id is required")
	}

	grantee, err := uc.authRepo.GetOrgUser(ctx, in.GranteeUserID, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if grantee == nil || grantee.Status != domain.StatusActive {
		return nil, domain.NewAppError(http.StatusBadRequest, "grantee is not an active member of this workspace")
	}

	// The level is ENFORCED (U0.4): 'read' grants visibility only, 'edit' also
	// grants writes (applyWriteScopeFromCtx / requireWriteVisible). Anything else
	// is rejected rather than stored as a decorative string.
	level := in.PermissionLevel
	if level == "" {
		level = "read"
	}
	if level != "read" && level != "edit" {
		return nil, domain.NewAppError(http.StatusBadRequest, "permission_level must be 'read' or 'edit'")
	}

	exists, err := uc.shares.ExistsForGrantee(ctx, slug, recordID, in.GranteeUserID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	share := &domain.RecordShare{
		RecordType:      slug,
		RecordID:        recordID,
		GranteeUserID:   in.GranteeUserID,
		PermissionLevel: level,
		CreatedBy:       &actorID,
	}
	if exists {
		// Idempotent: a repeat grant returns the existing state without a dup row.
		return share, nil
	}
	if err := uc.shares.Create(ctx, share); err != nil {
		return nil, domain.ErrInternal
	}

	uc.authz.Audit(ctx, domain.AuditEntry{
		OrgID:      orgID,
		ActorID:    actorID,
		ObjectSlug: slug,
		RecordID:   recordID,
		Action:     domain.ActionEdit,
		Changes:    map[string]interface{}{"shared_with": in.GranteeUserID.String()},
	})
	return share, nil
}

// Unshare revokes a share by id (scoped to the addressed record), after
// confirming the caller can see the record.
func (uc *shareUseCase) Unshare(ctx context.Context, orgID, actorID uuid.UUID, slug string, recordID, shareID uuid.UUID) error {
	if _, err := uc.records.Get(ctx, orgID, slug, recordID); err != nil {
		return err
	}
	affected, err := uc.shares.DeleteByID(ctx, shareID, slug, recordID)
	if err != nil {
		return domain.ErrInternal
	}
	if affected == 0 {
		return domain.NewAppError(http.StatusNotFound, "share not found")
	}
	uc.authz.Audit(ctx, domain.AuditEntry{
		OrgID:      orgID,
		ActorID:    actorID,
		ObjectSlug: slug,
		RecordID:   recordID,
		Action:     domain.ActionEdit,
		Changes:    map[string]interface{}{"unshared": shareID.String()},
	})
	return nil
}

// List returns a record's shares, after confirming the caller can see the record.
func (uc *shareUseCase) List(ctx context.Context, orgID uuid.UUID, slug string, recordID uuid.UUID) ([]domain.ShareView, error) {
	if _, err := uc.records.Get(ctx, orgID, slug, recordID); err != nil {
		return nil, err
	}
	return uc.shares.ListByRecord(ctx, slug, recordID)
}
