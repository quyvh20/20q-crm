package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// IntegrationAudienceReader answers "who should hear that this workspace's lead
// pipes are broken".
//
// It exists as its own small type because no such lookup existed anywhere: the
// permission layer is caller-oriented — domain.CapabilityChecker answers only about
// whoever is on the context — so nothing could enumerate the users in an org holding
// a capability. That is a query, not new infrastructure, which is why it lives here
// rather than growing domain.PermissionUseCase.
type IntegrationAudienceReader struct {
	db *gorm.DB
}

// NewIntegrationAudienceReader builds the reader.
func NewIntegrationAudienceReader(db *gorm.DB) *IntegrationAudienceReader {
	return &IntegrationAudienceReader{db: db}
}

// IntegrationAdmins returns the live members who should be notified about
// integration health in an org.
//
// Two legs, and the second one is not redundant:
//
//   - members whose role grants integrations.manage, and
//   - the workspace OWNER.
//
// The owner leg is load-bearing. The owner role deliberately holds no
// role_permissions rows at all — it bypasses capability checks entirely so that an
// empty or half-seeded permission table can never lock the owner out of their own
// workspace. So the natural query, "roles granting integrations.manage", silently
// excludes the one person guaranteed to care that lead capture has stopped. A
// recipient list that omits the owner would look correct in every test written
// against a seeded org and be wrong in exactly the workspaces that need it.
//
// Liveness is enforced in SQL rather than by filtering afterwards, because
// ListMembersByOrgID applies no filter at all and returns invited, suspended and
// soft-deleted rows alike — a fan-out built on it mails people who have left. Note
// org_users.deleted_at is a plain *time.Time, not gorm.DeletedAt, so GORM adds no
// soft-delete scope and the predicate has to be written by hand.
//
// Because workspace soft-delete stamps org_users.deleted_at on every member, a
// deleted workspace resolves to an empty audience for free.
func (r *IntegrationAudienceReader) IntegrationAdmins(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := r.db.WithContext(ctx).Raw(`
		SELECT DISTINCT ou.user_id
		  FROM org_users ou
		  JOIN roles ro ON ro.id = ou.role_id
		 WHERE ou.org_id = ?
		   AND ou.status = 'active'
		   AND ou.deleted_at IS NULL
		   AND (
		         ro.is_owner = TRUE
		         OR (ro.is_system = TRUE AND ro.name = ?)
		         OR EXISTS (
		              SELECT 1 FROM role_permissions rp
		               WHERE rp.role_id = ro.id AND rp.permission_code = ?
		            )
		       )`,
		orgID, domain.RoleOwner, domain.CapIntegrationsManage).Scan(&ids).Error
	if err != nil {
		return nil, err
	}
	return ids, nil
}
