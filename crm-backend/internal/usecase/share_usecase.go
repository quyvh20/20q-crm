package usecase

import (
	"context"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// shareUseCase creates/revokes/lists record shares — the escape hatch that lets a
// row-scoped role ('own' or 'team') reach specific records outside its scope
// (U6.2). A record can be granted to a user, a role, or a group, at 'view' or
// 'edit', exactly like a report.
//
// The gate is visibility: Share/Unshare/List all begin by fetching the record
// through RecordService, which is data-scope aware. So a row-scoped role can only
// share the records it can already reach, while an 'all'-scoped role (manager /
// admin / owner) can share any record in the workspace. There is no separate
// "sharing" capability to grant or forget.
type shareUseCase struct {
	records  domain.RecordService
	shares   domain.RecordShareRepository
	idents   domain.ShareIdentityRepository
	groups   domain.UserGroupRepository
	roles    domain.RoleRepository
	authRepo domain.AuthRepository
	authz    domain.RecordAuthorizer
}

func NewShareUseCase(
	records domain.RecordService,
	shares domain.RecordShareRepository,
	idents domain.ShareIdentityRepository,
	groups domain.UserGroupRepository,
	roles domain.RoleRepository,
	authRepo domain.AuthRepository,
	authz domain.RecordAuthorizer,
) domain.ShareUseCase {
	return &shareUseCase{records: records, shares: shares, idents: idents, groups: groups, roles: roles, authRepo: authRepo, authz: authz}
}

// Share grants recordID (of object slug) to a user, role, or group. It verifies
// the caller can see the record, validates the target exists in this org, then
// upserts the grant and audits it.
func (uc *shareUseCase) Share(ctx context.Context, orgID, actorID uuid.UUID, slug string, recordID uuid.UUID, in domain.ShareRecordInput) (*domain.RecordShare, error) {
	rec, err := uc.records.Get(ctx, orgID, slug, recordID)
	if err != nil {
		return nil, err // not visible under the caller's scope → 403/404 from RecordService
	}
	// Re-sharing is a MANAGE act, not a read act. Gating it on mere visibility was a
	// privilege-escalation path: anyone the record was shared with at 'view' could
	// re-share it to themselves — or to a role/group they belong to — at 'edit', and
	// hand themselves write access to a record they were only ever meant to look at.
	if err := uc.requireManage(ctx, orgID, actorID, slug, recordID, rec); err != nil {
		return nil, err
	}

	if !domain.IsShareTarget(in.TargetType) {
		return nil, domain.NewAppError(http.StatusBadRequest, "target_type must be 'user', 'role' or 'group'")
	}
	if in.TargetID == uuid.Nil {
		return nil, domain.NewAppError(http.StatusBadRequest, "target_id is required")
	}
	level := in.Level
	if level == "" {
		level = domain.ShareLevelView
	}
	// The level is ENFORCED, not decorative: 'view' grants visibility only, 'edit'
	// also grants writes (RecordAccessPredicate with RequireEdit). 'comment' is a
	// report level and is rejected here rather than stored as a no-op.
	if !domain.IsStorableRecordShareLevel(level) {
		return nil, domain.NewAppError(http.StatusBadRequest, "level must be 'view' or 'edit'")
	}

	var granteeUserID *uuid.UUID
	switch in.TargetType {
	case domain.ShareTargetUser:
		// Never share to yourself, and never share to the record's own owner: both
		// are no-ops that read as real grants in the share list.
		if in.TargetID == actorID {
			return nil, domain.NewAppError(http.StatusBadRequest, "you already have access to this record")
		}
		if rec != nil && rec.OwnerUserID != nil && *rec.OwnerUserID == in.TargetID {
			return nil, domain.NewAppError(http.StatusBadRequest, "that person already owns this record")
		}
		grantee, err := uc.authRepo.GetOrgUser(ctx, in.TargetID, orgID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		if grantee == nil || grantee.Status != domain.StatusActive {
			return nil, domain.NewAppError(http.StatusBadRequest, "that person is not an active member of this workspace")
		}
		id := in.TargetID
		granteeUserID = &id
	case domain.ShareTargetRole:
		// GetInOrg accepts the org's own custom roles and the global system roles,
		// and nothing else — so a share can never name a role from another workspace.
		role, err := uc.roles.GetInOrg(ctx, orgID, in.TargetID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		if role == nil {
			return nil, domain.NewAppError(http.StatusBadRequest, "that role does not exist in this workspace")
		}
	case domain.ShareTargetGroup:
		exists, err := uc.groups.ExistsInOrg(ctx, orgID, in.TargetID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		if !exists {
			return nil, domain.NewAppError(http.StatusBadRequest, "that group does not exist in this workspace")
		}
	}

	share := &domain.RecordShare{
		OrgID:           orgID,
		RecordType:      slug,
		RecordID:        recordID,
		TargetType:      in.TargetType,
		TargetID:        in.TargetID,
		GranteeUserID:   granteeUserID, // legacy mirror, user targets only
		PermissionLevel: level,
		CreatedBy:       &actorID,
	}
	if err := uc.shares.Upsert(ctx, share); err != nil {
		return nil, domain.ErrInternal
	}

	uc.authz.Audit(ctx, domain.AuditEntry{
		OrgID:      orgID,
		ActorID:    actorID,
		ObjectSlug: slug,
		RecordID:   recordID,
		Action:     domain.ActionEdit,
		Changes: map[string]interface{}{
			"shared_with_type": in.TargetType,
			"shared_with_id":   in.TargetID.String(),
			"level":            level,
		},
	})
	return share, nil
}

// Unshare revokes a share by id (scoped to the addressed record), after
// confirming the caller can see the record.
func (uc *shareUseCase) Unshare(ctx context.Context, orgID, actorID uuid.UUID, slug string, recordID, shareID uuid.UUID) error {
	rec, err := uc.records.Get(ctx, orgID, slug, recordID)
	if err != nil {
		return err
	}
	// Revoking is a manage act too — otherwise a view-shared grantee could delete
	// every other share on the record, including the ones the owner made.
	if err := uc.requireManage(ctx, orgID, actorID, slug, recordID, rec); err != nil {
		return err
	}
	affected, err := uc.shares.DeleteByID(ctx, orgID, shareID, slug, recordID)
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

// requireManage is the gate on granting and revoking. Only someone with MANAGE on
// the record may change who else can reach it: its owner, or a caller whose role
// reaches the whole workspace (owner role / 'all' scope). A share — at any level —
// grants access to the RECORD, never the power to redistribute it.
func (uc *shareUseCase) requireManage(ctx context.Context, orgID, actorID uuid.UUID, slug string, recordID uuid.UUID, rec *domain.UniformRecord) error {
	caller, hasCaller := domain.CallerFromContext(ctx)
	if hasCaller && (caller.IsOwner || caller.DataScope == domain.DataScopeAll) {
		return nil
	}
	if rec != nil && rec.OwnerUserID != nil && *rec.OwnerUserID == actorID {
		return nil
	}
	// An unowned record has no one to manage it but the workspace's all-scoped roles.
	return domain.NewAppError(http.StatusForbidden,
		"only the record's owner can change who it's shared with")
}

// List returns a record's shares, after confirming the caller can see the record.
func (uc *shareUseCase) List(ctx context.Context, orgID uuid.UUID, slug string, recordID uuid.UUID) ([]domain.RecordShareView, error) {
	if _, err := uc.records.Get(ctx, orgID, slug, recordID); err != nil {
		return nil, err
	}
	return uc.shares.ListByRecord(ctx, orgID, slug, recordID)
}

// EffectiveLevel answers "what may this caller do with this record?" — the record
// twin of the report level resolver. It is what the UI keys its Edit/Share buttons
// off, so it must agree with the enforcement predicate:
//
//	manage — the record's owner, or an 'all'-scoped caller (they can reach and
//	         re-share anything in the workspace)
//	edit   — an 'edit' share matched by user, role, or group
//	view   — a 'view' share, or a teammate's record under 'team' scope
//	none   — the record is not visible at all
func (uc *shareUseCase) EffectiveLevel(ctx context.Context, orgID, userID uuid.UUID, slug string, recordID uuid.UUID) (string, error) {
	rec, err := uc.records.Get(ctx, orgID, slug, recordID)
	if err != nil {
		// Not visible under the caller's scope — no level at all.
		return domain.ShareLevelNone, nil
	}

	caller, hasCaller := domain.CallerFromContext(ctx)
	if hasCaller && (caller.IsOwner || caller.DataScope == domain.DataScopeAll) {
		return domain.ShareLevelManage, nil
	}
	if rec != nil && rec.OwnerUserID != nil && *rec.OwnerUserID == userID {
		return domain.ShareLevelManage, nil
	}

	ident, err := uc.idents.GetShareIdentity(ctx, orgID, userID)
	if err != nil {
		return domain.ShareLevelNone, domain.ErrInternal
	}
	level, err := uc.shares.BestLevelFor(ctx, orgID, slug, recordID, ident)
	if err != nil {
		return domain.ShareLevelNone, domain.ErrInternal
	}
	if level == domain.ShareLevelNone {
		// Visible but unshared and unowned ⇒ reachable through team scope: readable,
		// not writable (a teammate's record is not yours to edit).
		return domain.ShareLevelView, nil
	}
	return level, nil
}

// ListSharedWithMe backs the "Shared with me" view: records someone else owns that
// have been shared to the caller — directly, via their role, or via a group.
//
// A share does not override Object-Level Security: if the caller's role cannot read
// the object at all, its records must not surface here. That filter is resolved to
// a slug whitelist and pushed INTO the query rather than applied to the page after
// the fact — post-filtering a page would leave the total count (and therefore the
// pager) counting rows the caller is never shown.
func (uc *shareUseCase) ListSharedWithMe(ctx context.Context, orgID, userID uuid.UUID, slug string, limit, offset int) ([]domain.SharedRecordView, int64, error) {
	ident, err := uc.idents.GetShareIdentity(ctx, orgID, userID)
	if err != nil {
		return nil, 0, domain.ErrInternal
	}

	types, err := uc.shares.SharedRecordTypes(ctx, orgID, ident)
	if err != nil {
		return nil, 0, domain.ErrInternal
	}
	allowed := make([]string, 0, len(types))
	for _, t := range types {
		if slug != "" && t != slug {
			continue
		}
		if uc.authz.Authorize(ctx, orgID, t, domain.ActionRead) == nil {
			allowed = append(allowed, t)
		}
	}
	if len(allowed) == 0 {
		return []domain.SharedRecordView{}, 0, nil
	}

	rows, total, err := uc.shares.ListSharedWithMe(ctx, orgID, ident, allowed, limit, offset)
	if err != nil {
		return nil, 0, domain.ErrInternal
	}
	return rows, total, nil
}
