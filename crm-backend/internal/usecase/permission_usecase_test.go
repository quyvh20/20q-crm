package usecase

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// Fakes
// ============================================================

type fakePermRepo struct {
	access      map[string]map[string]domain.ObjectAccess
	loadCalls   int
	ensureCalls int
	upserts     []domain.ObjectPermission
	audits      []*domain.ObjectAudit
	roles       []domain.Role
	perms       []domain.ObjectPermission

	// FLS (P5b)
	fieldAccess    map[string]map[string]map[string]string // role → slug → key → level
	loadFieldCalls int
	fieldPerms     []domain.FieldPermission
	fieldUpserts   []domain.FieldPermission
	fieldDeletes   []string // "slug:key"

	// Capabilities (P3)
	capabilities  map[string]map[string]bool // role → capability → true
	loadCapCalls  int
}

func (f *fakePermRepo) LoadOrgCapabilities(context.Context, uuid.UUID) (map[string]map[string]bool, error) {
	f.loadCapCalls++
	if f.capabilities == nil {
		return map[string]map[string]bool{}, nil
	}
	return f.capabilities, nil
}

func (f *fakePermRepo) EnsureDefaults(context.Context, uuid.UUID) error { f.ensureCalls++; return nil }
func (f *fakePermRepo) LoadOrgAccess(context.Context, uuid.UUID) (map[string]map[string]domain.ObjectAccess, error) {
	f.loadCalls++
	if f.access == nil {
		return map[string]map[string]domain.ObjectAccess{}, nil
	}
	return f.access, nil
}
func (f *fakePermRepo) ListRoles(context.Context, uuid.UUID) ([]domain.Role, error) {
	return f.roles, nil
}
func (f *fakePermRepo) ListPermissions(context.Context, uuid.UUID) ([]domain.ObjectPermission, error) {
	return f.perms, nil
}
func (f *fakePermRepo) UpsertPermission(_ context.Context, p domain.ObjectPermission) error {
	f.upserts = append(f.upserts, p)
	return nil
}
func (f *fakePermRepo) WriteAudit(_ context.Context, a *domain.ObjectAudit) error {
	f.audits = append(f.audits, a)
	return nil
}
func (f *fakePermRepo) ListAudit(context.Context, uuid.UUID, string, uuid.UUID, int) ([]domain.AuditView, error) {
	return nil, nil
}
func (f *fakePermRepo) LoadOrgFieldAccess(context.Context, uuid.UUID) (map[string]map[string]map[string]string, error) {
	f.loadFieldCalls++
	if f.fieldAccess == nil {
		return map[string]map[string]map[string]string{}, nil
	}
	return f.fieldAccess, nil
}
func (f *fakePermRepo) ListFieldPermissions(context.Context, uuid.UUID, string) ([]domain.FieldPermission, error) {
	return f.fieldPerms, nil
}
func (f *fakePermRepo) UpsertFieldPermission(_ context.Context, p domain.FieldPermission) error {
	f.fieldUpserts = append(f.fieldUpserts, p)
	return nil
}
func (f *fakePermRepo) DeleteFieldPermission(_ context.Context, _, _ uuid.UUID, slug, fieldKey string) error {
	f.fieldDeletes = append(f.fieldDeletes, slug+":"+fieldKey)
	return nil
}

type fakeRegistryUC struct {
	objects []domain.ObjectSummary
	schema  *domain.ObjectDescriptor
}

func (f *fakeRegistryUC) ListObjects(context.Context, uuid.UUID) ([]domain.ObjectSummary, error) {
	return f.objects, nil
}
func (f *fakeRegistryUC) GetSchema(context.Context, uuid.UUID, string) (*domain.ObjectDescriptor, error) {
	return f.schema, nil
}
func (f *fakeRegistryUC) SetNumberPrefix(context.Context, uuid.UUID, string, string) error {
	return nil
}
func (f *fakeRegistryUC) ListIncomingRelations(context.Context, uuid.UUID, string) ([]domain.IncomingRelation, error) {
	return nil, nil
}

func callerCtx(role string) context.Context {
	return domain.WithCaller(context.Background(), role, uuid.New())
}

// ============================================================
// HasCapability — the default-deny system-capability decision (P3, D5)
// ============================================================

func TestHasCapability_NoCaller_AllowsTrustedInternalCall(t *testing.T) {
	uc := NewPermissionUseCase(&fakePermRepo{}, &fakeRegistryUC{})
	if err := uc.HasCapability(context.Background(), uuid.New(), domain.CapMembersManage); err != nil {
		t.Fatalf("expected trusted call to be allowed, got %v", err)
	}
}

func TestHasCapability_Owner_BypassesEvenWithEmptyTable(t *testing.T) {
	// Empty capability map: owner must still pass every capability check, so an
	// empty table can never lock the owner out (plan risk register).
	uc := NewPermissionUseCase(&fakePermRepo{}, &fakeRegistryUC{})
	for _, cap := range domain.AllCapabilities {
		if err := uc.HasCapability(callerCtx(domain.RoleOwner), uuid.New(), cap); err != nil {
			t.Fatalf("owner should bypass capability %s, got %v", cap, err)
		}
	}
}

func TestHasCapability_CustomRole_PassesAndFailsCorrectly(t *testing.T) {
	// A custom "Support Agent" role granted only audit.view flows through the SAME
	// gate as a system role — the headline P3 behavior (I1 fixed).
	repo := &fakePermRepo{capabilities: map[string]map[string]bool{
		"Support Agent": {domain.CapAuditView: true},
	}}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})

	if err := uc.HasCapability(callerCtx("Support Agent"), uuid.New(), domain.CapAuditView); err != nil {
		t.Fatalf("Support Agent with audit.view should pass, got %v", err)
	}
	err := uc.HasCapability(callerCtx("Support Agent"), uuid.New(), domain.CapMembersManage)
	assertForbidden(t, err, "Support Agent lacking members.manage")
}

func TestHasCapability_DefaultDeny_WhenRoleAbsent(t *testing.T) {
	uc := NewPermissionUseCase(&fakePermRepo{}, &fakeRegistryUC{})
	err := uc.HasCapability(callerCtx("brand_new_role"), uuid.New(), domain.CapRolesManage)
	assertForbidden(t, err, "a role with no capability rows")
}

// ============================================================
// Authorize — the default-deny OLS decision
// ============================================================

func TestAuthorize_NoCaller_AllowsTrustedInternalCall(t *testing.T) {
	uc := NewPermissionUseCase(&fakePermRepo{}, &fakeRegistryUC{})
	// No caller in context => trusted in-process call (automation/AI/seed).
	if err := uc.Authorize(context.Background(), uuid.New(), "deal", domain.ActionDelete); err != nil {
		t.Fatalf("expected trusted call to be allowed, got %v", err)
	}
}

func TestAuthorize_Owner_Bypasses(t *testing.T) {
	// Empty access map: owner must still be allowed everything.
	uc := NewPermissionUseCase(&fakePermRepo{}, &fakeRegistryUC{})
	for _, action := range []domain.RecordAction{domain.ActionRead, domain.ActionCreate, domain.ActionEdit, domain.ActionDelete} {
		if err := uc.Authorize(callerCtx(domain.RoleOwner), uuid.New(), "deal", action); err != nil {
			t.Fatalf("owner should bypass OLS for %s, got %v", action, err)
		}
	}
}

func TestAuthorize_EnforcesPerActionBits(t *testing.T) {
	repo := &fakePermRepo{access: map[string]map[string]domain.ObjectAccess{
		domain.RoleViewer: {"deal": {Read: true}}, // read-only
	}}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})

	if err := uc.Authorize(callerCtx(domain.RoleViewer), uuid.New(), "deal", domain.ActionRead); err != nil {
		t.Fatalf("viewer should read deals, got %v", err)
	}
	err := uc.Authorize(callerCtx(domain.RoleViewer), uuid.New(), "deal", domain.ActionCreate)
	assertForbidden(t, err, "viewer creating a deal")
}

func TestAuthorize_DefaultDeny_WhenNoRow(t *testing.T) {
	// sales_rep has rows for "deal" but none for the custom "project" object.
	repo := &fakePermRepo{access: map[string]map[string]domain.ObjectAccess{
		domain.RoleSales: {"deal": {Read: true, Create: true, Edit: true}},
	}}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})

	err := uc.Authorize(callerCtx(domain.RoleSales), uuid.New(), "project", domain.ActionRead)
	assertForbidden(t, err, "default-deny on an object with no rows")

	// And a role absent from the map entirely is denied too.
	err = uc.Authorize(callerCtx("contractor"), uuid.New(), "deal", domain.ActionRead)
	assertForbidden(t, err, "default-deny for an unknown role")
}

// ============================================================
// Cache — load once, invalidate to refresh
// ============================================================

func TestAuthorize_CachesPerOrg_AndInvalidates(t *testing.T) {
	org := uuid.New()
	repo := &fakePermRepo{access: map[string]map[string]domain.ObjectAccess{
		domain.RoleManager: {"deal": {Read: true}},
	}}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})

	for i := 0; i < 3; i++ {
		if err := uc.Authorize(callerCtx(domain.RoleManager), org, "deal", domain.ActionRead); err != nil {
			t.Fatalf("expected allow, got %v", err)
		}
	}
	if repo.loadCalls != 1 {
		t.Fatalf("expected access loaded once and cached, loadCalls=%d", repo.loadCalls)
	}

	uc.Invalidate(org)
	if err := uc.Authorize(callerCtx(domain.RoleManager), org, "deal", domain.ActionRead); err != nil {
		t.Fatalf("expected allow after invalidate, got %v", err)
	}
	if repo.loadCalls != 2 {
		t.Fatalf("expected reload after invalidate, loadCalls=%d", repo.loadCalls)
	}
}

func TestSetPermission_UpsertsAndBustsCache(t *testing.T) {
	org := uuid.New()
	repo := &fakePermRepo{access: map[string]map[string]domain.ObjectAccess{
		domain.RoleManager: {"deal": {Read: true}},
	}}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})

	// Warm the cache.
	_ = uc.Authorize(callerCtx(domain.RoleManager), org, "deal", domain.ActionRead)
	if repo.loadCalls != 1 {
		t.Fatalf("expected warm load, loadCalls=%d", repo.loadCalls)
	}

	roleID := uuid.New()
	if err := uc.SetPermission(context.Background(), org, domain.SetPermissionInput{
		RoleID: roleID, ObjectSlug: "deal", CanRead: true, CanEdit: true,
	}); err != nil {
		t.Fatalf("SetPermission: %v", err)
	}
	if len(repo.upserts) != 1 || repo.upserts[0].RoleID != roleID || !repo.upserts[0].CanEdit {
		t.Fatalf("expected one upsert with the edit bit, got %+v", repo.upserts)
	}

	// Cache was busted, so the next authorize reloads.
	_ = uc.Authorize(callerCtx(domain.RoleManager), org, "deal", domain.ActionRead)
	if repo.loadCalls != 2 {
		t.Fatalf("expected reload after SetPermission, loadCalls=%d", repo.loadCalls)
	}
}

func TestSetPermission_RejectsMissingFields(t *testing.T) {
	uc := NewPermissionUseCase(&fakePermRepo{}, &fakeRegistryUC{})
	err := uc.SetPermission(context.Background(), uuid.New(), domain.SetPermissionInput{ObjectSlug: "deal"})
	assertStatus(t, err, 400, "missing role_id")
}

// ============================================================
// Audit + Grid
// ============================================================

func TestAudit_WritesRow(t *testing.T) {
	repo := &fakePermRepo{}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})
	org, actor, rec := uuid.New(), uuid.New(), uuid.New()

	uc.Audit(context.Background(), domain.AuditEntry{
		OrgID: org, ActorID: actor, ObjectSlug: "deal", RecordID: rec,
		Action:  domain.ActionEdit,
		Changes: map[string]interface{}{"value": map[string]interface{}{"old": 1, "new": 2}},
	})

	if len(repo.audits) != 1 {
		t.Fatalf("expected one audit row, got %d", len(repo.audits))
	}
	a := repo.audits[0]
	if a.ObjectSlug != "deal" || a.RecordID != rec || a.Action != "edit" {
		t.Fatalf("audit row fields wrong: %+v", a)
	}
	if a.ActorID == nil || *a.ActorID != actor {
		t.Fatalf("expected actor %s, got %v", actor, a.ActorID)
	}
}

func TestListRecordAudit_RequiresReadAccess(t *testing.T) {
	repo := &fakePermRepo{access: map[string]map[string]domain.ObjectAccess{
		domain.RoleViewer: {"deal": {Read: true}},
	}}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})

	// A role with no read on "project" can't inspect its audit trail.
	_, err := uc.ListRecordAudit(callerCtx(domain.RoleViewer), uuid.New(), "project", uuid.New())
	assertForbidden(t, err, "audit for an object you can't read")

	// With read access the call goes through to the repo (which returns empty here).
	if _, err := uc.ListRecordAudit(callerCtx(domain.RoleViewer), uuid.New(), "deal", uuid.New()); err != nil {
		t.Fatalf("expected audit allowed for a readable object, got %v", err)
	}
}

func TestGetGrid_AssemblesObjectsRolesMatrix(t *testing.T) {
	org := uuid.New()
	ownerID := uuid.New()
	repo := &fakePermRepo{
		roles: []domain.Role{
			{ID: ownerID, Name: domain.RoleOwner, IsSystem: true},
			{ID: uuid.New(), Name: domain.RoleViewer, IsSystem: true},
		},
		perms: []domain.ObjectPermission{
			{RoleID: ownerID, ObjectSlug: "deal", CanRead: true, CanCreate: true, CanEdit: true, CanDelete: true},
		},
	}
	reg := &fakeRegistryUC{objects: []domain.ObjectSummary{
		{Slug: "deal", Label: "Deal", IsSystem: true},
		{Slug: "project", Label: "Project", IsSystem: false},
	}}
	uc := NewPermissionUseCase(repo, reg)

	grid, err := uc.GetGrid(context.Background(), org)
	if err != nil {
		t.Fatalf("GetGrid: %v", err)
	}
	if len(grid.Objects) != 2 || len(grid.Roles) != 2 || len(grid.Matrix) != 1 {
		t.Fatalf("grid shape wrong: %d objects, %d roles, %d cells", len(grid.Objects), len(grid.Roles), len(grid.Matrix))
	}
	// The owner role is flagged so the UI can lock its row.
	var sawOwnerFlag bool
	for _, r := range grid.Roles {
		if r.Name == domain.RoleOwner && r.IsOwner {
			sawOwnerFlag = true
		}
	}
	if !sawOwnerFlag {
		t.Fatalf("expected owner role flagged IsOwner")
	}
	if grid.Matrix[0].ObjectSlug != "deal" || !grid.Matrix[0].Delete {
		t.Fatalf("matrix cell wrong: %+v", grid.Matrix[0])
	}
}

// ============================================================
// Field-Level Security (P5b)
// ============================================================

func TestFieldMask_NoCaller_ReturnsEmpty(t *testing.T) {
	repo := &fakePermRepo{fieldAccess: map[string]map[string]map[string]string{
		domain.RoleViewer: {"deal": {"value": "hidden"}},
	}}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})
	// No caller => trusted in-process call: never masked, so automation/AI see all.
	if m := uc.FieldMask(context.Background(), uuid.New(), "deal"); !m.Empty() {
		t.Fatalf("trusted call should get an empty mask, got %+v", m)
	}
}

func TestFieldMask_Owner_Bypasses(t *testing.T) {
	repo := &fakePermRepo{fieldAccess: map[string]map[string]map[string]string{
		domain.RoleOwner: {"deal": {"value": "hidden"}},
	}}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})
	if m := uc.FieldMask(callerCtx(domain.RoleOwner), uuid.New(), "deal"); !m.Empty() {
		t.Fatalf("owner should bypass FLS, got %+v", m)
	}
}

func TestFieldMask_HiddenAndReadLevels(t *testing.T) {
	repo := &fakePermRepo{fieldAccess: map[string]map[string]map[string]string{
		domain.RoleViewer: {"deal": {"value": "hidden", "stage": "read", "title": "edit"}},
	}}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})

	m := uc.FieldMask(callerCtx(domain.RoleViewer), uuid.New(), "deal")
	if !m.IsHidden("value") {
		t.Fatal("value should be hidden")
	}
	if m.CanWrite("value") {
		t.Fatal("a hidden field must not be writable")
	}
	if m.IsHidden("stage") {
		t.Fatal("a read-only field is visible, not hidden")
	}
	if m.CanWrite("stage") {
		t.Fatal("a read-only field must not be writable")
	}
	// A stored 'edit' level (shouldn't normally exist) and an unmentioned field are
	// both fully accessible.
	if !m.CanWrite("title") || m.IsHidden("title") {
		t.Fatal("edit-level field should be fully accessible")
	}
	if !m.CanWrite("other") {
		t.Fatal("an unrestricted field must be writable")
	}
}

func TestFieldMask_NoRows_ReturnsEmpty_AndSharesOLSCache(t *testing.T) {
	org := uuid.New()
	// OLS rows exist but no FLS rows — the common case.
	repo := &fakePermRepo{access: map[string]map[string]domain.ObjectAccess{
		domain.RoleSales: {"deal": {Read: true}},
	}}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})

	if m := uc.FieldMask(callerCtx(domain.RoleSales), org, "deal"); !m.Empty() {
		t.Fatalf("no FLS rows => empty mask, got %+v", m)
	}
	// FieldMask shares the OLS cache entry: it loads field access exactly once and
	// reuses the warm entry across calls (zero overhead when unused).
	_ = uc.FieldMask(callerCtx(domain.RoleSales), org, "deal")
	_ = uc.Authorize(callerCtx(domain.RoleSales), org, "deal", domain.ActionRead)
	if repo.loadFieldCalls != 1 {
		t.Fatalf("expected field access loaded once and cached, loadFieldCalls=%d", repo.loadFieldCalls)
	}
	if repo.loadCalls != 1 {
		t.Fatalf("expected OLS access loaded once and shared, loadCalls=%d", repo.loadCalls)
	}
}

func TestSetFieldPermission_Restrict_Upserts_AndBustsCache(t *testing.T) {
	org := uuid.New()
	repo := &fakePermRepo{}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})
	roleID := uuid.New()

	// Warm the cache.
	_ = uc.FieldMask(callerCtx(domain.RoleViewer), org, "deal")
	if repo.loadFieldCalls != 1 {
		t.Fatalf("expected warm load, loadFieldCalls=%d", repo.loadFieldCalls)
	}

	if err := uc.SetFieldPermission(context.Background(), org, domain.SetFieldPermissionInput{
		RoleID: roleID, ObjectSlug: "deal", FieldKey: "value", Level: "hidden",
	}); err != nil {
		t.Fatalf("SetFieldPermission: %v", err)
	}
	if len(repo.fieldUpserts) != 1 || repo.fieldUpserts[0].Level != "hidden" || repo.fieldUpserts[0].FieldKey != "value" {
		t.Fatalf("expected one hidden upsert, got %+v", repo.fieldUpserts)
	}
	// Cache busted → next mask reloads.
	_ = uc.FieldMask(callerCtx(domain.RoleViewer), org, "deal")
	if repo.loadFieldCalls != 2 {
		t.Fatalf("expected reload after SetFieldPermission, loadFieldCalls=%d", repo.loadFieldCalls)
	}
}

func TestSetFieldPermission_EditLevel_DeletesRow(t *testing.T) {
	repo := &fakePermRepo{}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})

	// 'edit' is the default → stored as the absence of a row, so it deletes.
	if err := uc.SetFieldPermission(context.Background(), uuid.New(), domain.SetFieldPermissionInput{
		RoleID: uuid.New(), ObjectSlug: "deal", FieldKey: "value", Level: "edit",
	}); err != nil {
		t.Fatalf("SetFieldPermission(edit): %v", err)
	}
	if len(repo.fieldUpserts) != 0 {
		t.Fatalf("edit level must not upsert a row, got %+v", repo.fieldUpserts)
	}
	if len(repo.fieldDeletes) != 1 || repo.fieldDeletes[0] != "deal:value" {
		t.Fatalf("expected a delete of deal:value, got %+v", repo.fieldDeletes)
	}
}

func TestSetFieldPermission_RejectsBadInput(t *testing.T) {
	uc := NewPermissionUseCase(&fakePermRepo{}, &fakeRegistryUC{})

	// Missing field_key.
	err := uc.SetFieldPermission(context.Background(), uuid.New(), domain.SetFieldPermissionInput{
		RoleID: uuid.New(), ObjectSlug: "deal", Level: "hidden",
	})
	assertStatus(t, err, 400, "missing field_key")

	// Unknown level.
	err = uc.SetFieldPermission(context.Background(), uuid.New(), domain.SetFieldPermissionInput{
		RoleID: uuid.New(), ObjectSlug: "deal", FieldKey: "value", Level: "secret",
	})
	assertStatus(t, err, 400, "invalid level")
}

func TestGetFieldGrid_AssemblesFieldsRolesMatrix(t *testing.T) {
	org := uuid.New()
	roleID := uuid.New()
	repo := &fakePermRepo{
		roles: []domain.Role{
			{ID: uuid.New(), Name: domain.RoleOwner, IsSystem: true},
			{ID: roleID, Name: domain.RoleViewer, IsSystem: true},
		},
		fieldPerms: []domain.FieldPermission{
			{RoleID: roleID, ObjectSlug: "deal", FieldKey: "value", Level: "hidden"},
		},
	}
	reg := &fakeRegistryUC{schema: &domain.ObjectDescriptor{
		Slug: "deal", Label: "Deal",
		Fields: []domain.FieldDescriptor{
			{Key: "title", Label: "Title", Type: "text", IsSystem: true},
			{Key: "value", Label: "Amount", Type: "number", IsSystem: true},
		},
	}}
	uc := NewPermissionUseCase(repo, reg)

	grid, err := uc.GetFieldGrid(context.Background(), org, "deal")
	if err != nil {
		t.Fatalf("GetFieldGrid: %v", err)
	}
	if grid.Slug != "deal" || len(grid.Fields) != 2 || len(grid.Roles) != 2 || len(grid.Matrix) != 1 {
		t.Fatalf("grid shape wrong: %+v", grid)
	}
	if grid.Matrix[0].FieldKey != "value" || grid.Matrix[0].Level != "hidden" || grid.Matrix[0].RoleID != roleID {
		t.Fatalf("matrix cell wrong: %+v", grid.Matrix[0])
	}
	var sawOwner bool
	for _, r := range grid.Roles {
		if r.Name == domain.RoleOwner && r.IsOwner {
			sawOwner = true
		}
	}
	if !sawOwner {
		t.Fatal("expected owner role flagged IsOwner so the UI can lock its column")
	}
}

// ============================================================
// Helpers
// ============================================================

func assertForbidden(t *testing.T, err error, what string) {
	t.Helper()
	assertStatus(t, err, 403, what)
}

func assertStatus(t *testing.T, err error, code int, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error, got nil", what)
	}
	appErr, ok := err.(*domain.AppError)
	if !ok {
		t.Fatalf("%s: expected *domain.AppError, got %T (%v)", what, err, err)
	}
	if appErr.Code != code {
		t.Fatalf("%s: expected status %d, got %d (%s)", what, code, appErr.Code, appErr.Message)
	}
}
