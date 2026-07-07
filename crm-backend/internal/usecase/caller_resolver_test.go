package usecase

import (
	"context"
	"errors"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeResolverAuthRepo satisfies domain.AuthRepository by embedding it (nil) and
// overriding only GetOrgUser — the sole method NewCallerResolver uses. Any other
// call would panic, which is the intent (the resolver must not touch anything else).
type fakeResolverAuthRepo struct {
	domain.AuthRepository
	ou  *domain.OrgUser
	err error
}

func (f fakeResolverAuthRepo) GetOrgUser(context.Context, uuid.UUID, uuid.UUID) (*domain.OrgUser, error) {
	return f.ou, f.err
}

func TestCallerResolver_ResolveCaller(t *testing.T) {
	orgID := uuid.New()
	userID := uuid.New()
	roleID := uuid.New()

	t.Run("active member resolves to full identity", func(t *testing.T) {
		r := NewCallerResolver(fakeResolverAuthRepo{ou: &domain.OrgUser{
			UserID: userID, OrgID: orgID, RoleID: roleID, Status: domain.StatusActive,
			Role: &domain.Role{ID: roleID, Name: "sales_rep", DataScope: domain.DataScopeOwn},
		}})
		c, err := r.ResolveCaller(context.Background(), orgID, userID)
		require.NoError(t, err)
		assert.Equal(t, userID, c.UserID)
		assert.Equal(t, roleID, c.RoleID)
		assert.Equal(t, "sales_rep", c.Role)
		assert.Equal(t, domain.DataScopeOwn, c.DataScope)
		assert.False(t, c.IsOwner)
	})

	t.Run("owner role sets IsOwner", func(t *testing.T) {
		r := NewCallerResolver(fakeResolverAuthRepo{ou: &domain.OrgUser{
			UserID: userID, OrgID: orgID, RoleID: roleID, Status: domain.StatusActive,
			Role: &domain.Role{ID: roleID, Name: "owner", IsOwner: true, DataScope: domain.DataScopeAll},
		}})
		c, err := r.ResolveCaller(context.Background(), orgID, userID)
		require.NoError(t, err)
		assert.True(t, c.IsOwner)
	})

	t.Run("suspended membership is rejected (fail closed)", func(t *testing.T) {
		r := NewCallerResolver(fakeResolverAuthRepo{ou: &domain.OrgUser{
			UserID: userID, OrgID: orgID, RoleID: roleID, Status: "suspended",
			Role: &domain.Role{ID: roleID, Name: "admin", DataScope: domain.DataScopeAll},
		}})
		_, err := r.ResolveCaller(context.Background(), orgID, userID)
		require.Error(t, err)
	})

	t.Run("non-member is rejected", func(t *testing.T) {
		r := NewCallerResolver(fakeResolverAuthRepo{ou: nil})
		_, err := r.ResolveCaller(context.Background(), orgID, userID)
		require.Error(t, err)
	})

	t.Run("repo error propagates", func(t *testing.T) {
		r := NewCallerResolver(fakeResolverAuthRepo{err: errors.New("db down")})
		_, err := r.ResolveCaller(context.Background(), orgID, userID)
		require.Error(t, err)
	})

	t.Run("nil user id is rejected", func(t *testing.T) {
		r := NewCallerResolver(fakeResolverAuthRepo{})
		_, err := r.ResolveCaller(context.Background(), orgID, uuid.Nil)
		require.Error(t, err)
	})
}
