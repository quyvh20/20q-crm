package repository

import (
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// TestSeedTemplateName covers the lineage resolution behind P6 new-object seeding
// of custom roles, without a DB: a system role is its own template; a custom role
// resolves through its denormalized template_key or one hop through
// seeded_from_role_id; a lineage-less legacy role resolves to "" (left unseeded).
func TestSeedTemplateName(t *testing.T) {
	viewer := domain.Role{ID: uuid.New(), Name: domain.RoleViewer, IsSystem: true}
	admin := domain.Role{ID: uuid.New(), Name: domain.RoleAdmin, IsSystem: true}

	tkViewer := domain.RoleViewer
	customA := domain.Role{ID: uuid.New(), Name: "Read Plus", TemplateKey: &tkViewer} // denormalized template

	adminID := admin.ID
	customB := domain.Role{ID: uuid.New(), Name: "Ops", SeededFromRoleID: &adminID} // one hop to a system role

	customAID := customA.ID
	customC := domain.Role{ID: uuid.New(), Name: "Read Plus Plus", SeededFromRoleID: &customAID} // hop to a template-bearing custom role

	legacy := domain.Role{ID: uuid.New(), Name: "Legacy"} // no lineage

	byID := map[uuid.UUID]domain.Role{
		viewer.ID: viewer, admin.ID: admin, customA.ID: customA,
		customB.ID: customB, customC.ID: customC, legacy.ID: legacy,
	}

	cases := []struct {
		role domain.Role
		want string
	}{
		{viewer, domain.RoleViewer},
		{admin, domain.RoleAdmin},
		{customA, domain.RoleViewer},
		{customB, domain.RoleAdmin},
		{customC, domain.RoleViewer},
		{legacy, ""},
	}
	for _, c := range cases {
		if got := seedTemplateName(c.role, byID); got != c.want {
			t.Errorf("seedTemplateName(%s) = %q, want %q", c.role.Name, got, c.want)
		}
	}
}
