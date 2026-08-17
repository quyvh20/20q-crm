package usecase

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// ============================================================
// Fake ListViewRepository — records whether persistence was reached, so a test
// can prove a gate (OLS / name / FLS / compile / owner / cap) rejected BEFORE
// any repo call. Real SQL is a thin gorm wrapper; nothing here to re-prove.
// ============================================================

type fakeListViewRepo struct {
	views        map[uuid.UUID]*domain.ListView
	count        int64
	createCalled bool
	updateCalled bool
	deleteCalled bool
}

func newFakeListViewRepo() *fakeListViewRepo {
	return &fakeListViewRepo{views: map[uuid.UUID]*domain.ListView{}}
}

// List mirrors the real predicate (own OR shared) so visibility tests exercise
// the same contract the SQL implements.
func (f *fakeListViewRepo) List(_ context.Context, orgID uuid.UUID, slug string, userID uuid.UUID) ([]domain.ListView, error) {
	var out []domain.ListView
	for _, v := range f.views {
		if v.OrgID == orgID && v.ObjectSlug == slug && (v.OwnerUserID == userID || v.Shared) {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (f *fakeListViewRepo) GetByID(_ context.Context, orgID, id uuid.UUID) (*domain.ListView, error) {
	v, ok := f.views[id]
	if !ok || v.OrgID != orgID {
		return nil, nil
	}
	return v, nil
}

func (f *fakeListViewRepo) Create(_ context.Context, v *domain.ListView) error {
	f.createCalled = true
	if v.ID == uuid.Nil {
		v.ID = uuid.New()
	}
	f.views[v.ID] = v
	return nil
}

func (f *fakeListViewRepo) Update(_ context.Context, v *domain.ListView) error {
	f.updateCalled = true
	f.views[v.ID] = v
	return nil
}

func (f *fakeListViewRepo) SoftDelete(_ context.Context, _ uuid.UUID, id uuid.UUID) (bool, error) {
	f.deleteCalled = true
	if _, ok := f.views[id]; !ok {
		return false, nil
	}
	delete(f.views, id)
	return true, nil
}

func (f *fakeListViewRepo) CountForObject(_ context.Context, _ uuid.UUID, _ string) (int64, error) {
	return f.count, nil
}

// ============================================================
// Env
// ============================================================

// listViewTestRegistry seeds a contact def with a typed column (email), a jsonb
// text field, and a jsonb number field ("salary" — the one tests hide via FLS),
// so the dry-run compile resolves both storage kinds.
func listViewTestRegistry() *fakeRegistryRepo {
	contactDefID := uuid.MustParse("cccccccc-0000-0000-0000-0000000010a1")
	table := "contacts"
	return &fakeRegistryRepo{
		defs: []domain.ObjectDef{{
			ID: contactDefID, Slug: "contact", Label: "Contact", LabelPlural: "Contacts",
			IsSystem: true, Storage: "table", RecordTable: &table,
		}},
		fields: map[uuid.UUID][]domain.ObjectField{
			contactDefID: {
				{Key: "email", Label: "Email", Type: "text", StorageKind: "column", MapsToColumn: reportStrPtr("email"), IsSystem: true},
				{Key: "lead_source", Label: "Lead Source", Type: "text", StorageKind: "jsonb"},
				{Key: "salary", Label: "Salary", Type: "number", StorageKind: "jsonb"},
			},
		},
	}
}

type listViewEnv struct {
	uc    domain.ListViewUseCase
	repo  *fakeListViewRepo
	authz *fakeAuthorizer
	orgID uuid.UUID
	owner uuid.UUID
}

func newListViewEnv() *listViewEnv {
	repo := newFakeListViewRepo()
	authz := &fakeAuthorizer{}
	return &listViewEnv{
		uc:    NewListViewUseCase(repo, listViewTestRegistry(), authz),
		repo:  repo,
		authz: authz,
		orgID: uuid.New(),
		owner: uuid.New(),
	}
}

func lvInput(name string, def string) domain.ListViewInput {
	in := domain.ListViewInput{}
	if name != "" {
		in.Name = reportStrPtr(name)
	}
	if def != "" {
		in.Definition = []byte(def)
	}
	return in
}

// ============================================================
// Create gates
// ============================================================

func TestListView_CreateNameRequired(t *testing.T) {
	env := newListViewEnv()
	// Absent, and whitespace-only (which must trim to empty, not save as " ").
	for _, in := range []domain.ListViewInput{lvInput("", ""), {Name: reportStrPtr("   ")}} {
		_, err := env.uc.Create(context.Background(), env.orgID, env.owner, "contact", in)
		if code := appErrCode(t, err); code != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400 for a missing name", code)
		}
	}
	// 121 runes (not bytes — the varchar(120) bound counts characters).
	_, err := env.uc.Create(context.Background(), env.orgID, env.owner, "contact", lvInput(strings.Repeat("é", 121), ""))
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 for an over-long name", code)
	}
	if env.repo.createCalled {
		t.Fatal("a name-rejected create must not reach the repo")
	}
}

func TestListView_CreateOLSDenied(t *testing.T) {
	env := newListViewEnv()
	env.authz.deny = map[string]bool{"contact:read": true}
	_, err := env.uc.Create(context.Background(), env.orgID, env.owner, "contact", lvInput("Mine", ""))
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", code)
	}
	if env.repo.createCalled {
		t.Fatal("an OLS-denied create must not reach the repo")
	}
}

func TestListView_CreateHiddenFieldFilterRejected(t *testing.T) {
	env := newListViewEnv()
	env.authz.masks = map[string]domain.FieldMask{"contact": {Hidden: map[string]bool{"salary": true}}}
	// Nested group, proving the walk reaches rule leaves, not just the root.
	def := `{"filter":{"op":"AND","rules":[{"field":"email","operator":"eq","value":"a@b.com"},{"op":"OR","rules":[{"field":"salary","operator":"gt","value":100}]}]}}`
	_, err := env.uc.Create(context.Background(), env.orgID, env.owner, "contact", lvInput("Sneaky", def))
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 for a hidden-field filter", code)
	}
	if env.repo.createCalled {
		t.Fatal("an FLS-rejected view must never be persisted — a shared view would leak an oracle on the hidden values")
	}
}

func TestListView_CreateInvalidFilterASTRejected(t *testing.T) {
	env := newListViewEnv()
	for name, def := range map[string]string{
		"unknown field": `{"filter":{"field":"no_such_field","operator":"eq","value":"x"}}`,
		"bad operator":  `{"filter":{"field":"email","operator":"regex","value":"x"}}`,
		"not json":      `{not json`,
	} {
		_, err := env.uc.Create(context.Background(), env.orgID, env.owner, "contact", lvInput("Broken", def))
		if code := appErrCode(t, err); code != http.StatusBadRequest {
			t.Fatalf("%s: code = %d, want 400", name, code)
		}
	}
	if env.repo.createCalled {
		t.Fatal("a view that cannot compile must not be persisted")
	}
}

func TestListView_CreateUnknownObjectRejected(t *testing.T) {
	env := newListViewEnv()
	// The catalog build is what discovers the slug — a filter forces it.
	def := `{"filter":{"field":"email","operator":"eq","value":"x"}}`
	_, err := env.uc.Create(context.Background(), env.orgID, env.owner, "no_such_object", lvInput("X", def))
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 for an unknown object", code)
	}
}

func TestListView_CreateScalarValidation(t *testing.T) {
	env := newListViewEnv()
	for name, def := range map[string]string{
		"bad sort_order": `{"sort_order":"sideways"}`,
		"non-uuid tag":   `{"tags":["not-a-uuid"]}`,
		"oversized":      `{"q":"` + strings.Repeat("a", maxListViewDefinitionBytes) + `"}`,
	} {
		_, err := env.uc.Create(context.Background(), env.orgID, env.owner, "contact", lvInput("X", def))
		if code := appErrCode(t, err); code != http.StatusBadRequest {
			t.Fatalf("%s: code = %d, want 400", name, code)
		}
	}
	if env.repo.createCalled {
		t.Fatal("scalar-invalid definitions must not be persisted")
	}
}

func TestListView_CreateValid(t *testing.T) {
	env := newListViewEnv()
	shared := true
	in := domain.ListViewInput{
		Name:       reportStrPtr("  Hot leads  "),
		Shared:     &shared,
		Definition: []byte(`{"filter":{"op":"AND","rules":[{"field":"email","operator":"contains","value":"@acme"},{"field":"salary","operator":"gte","value":50}]},"q":"x","tags":["` + uuid.New().String() + `"],"sort_by":"created_at","sort_order":"desc"}`),
	}
	v, err := env.uc.Create(context.Background(), env.orgID, env.owner, "contact", in)
	if err != nil {
		t.Fatalf("valid create failed: %v", err)
	}
	if v.Name != "Hot leads" {
		t.Fatalf("name = %q, want trimmed %q", v.Name, "Hot leads")
	}
	if !v.Shared || v.OwnerUserID != env.owner || v.ObjectSlug != "contact" {
		t.Fatalf("stored view fields wrong: %+v", v)
	}
	if !env.repo.createCalled {
		t.Fatal("valid create should reach the repo")
	}
}

func TestListView_CreateCapEnforced(t *testing.T) {
	env := newListViewEnv()
	env.repo.count = 100
	_, err := env.uc.Create(context.Background(), env.orgID, env.owner, "contact", lvInput("One too many", ""))
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 at the cap", code)
	}
	if env.repo.createCalled {
		t.Fatal("a capped create must not reach the repo")
	}
	env.repo.count = 99
	if _, err := env.uc.Create(context.Background(), env.orgID, env.owner, "contact", lvInput("The last slot", "")); err != nil {
		t.Fatalf("create under the cap should succeed: %v", err)
	}
}

// ============================================================
// Owner-only update/delete — `shared` widens who can USE a view, never who can
// rewrite it, so a shared view stays read-only to everyone but its owner.
// ============================================================

func TestListView_UpdateDeleteOwnerOnly(t *testing.T) {
	env := newListViewEnv()
	view := &domain.ListView{
		OrgID: env.orgID, OwnerUserID: env.owner, ObjectSlug: "contact",
		Name: "Team view", Shared: true, Definition: []byte(`{}`),
	}
	if err := env.repo.Create(context.Background(), view); err != nil {
		t.Fatal(err)
	}
	env.repo.createCalled, env.repo.updateCalled, env.repo.deleteCalled = false, false, false
	stranger := uuid.New()

	_, err := env.uc.Update(context.Background(), env.orgID, stranger, view.ID, lvInput("Hijacked", ""))
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Fatalf("non-owner update code = %d, want 403 even on a shared view", code)
	}
	if env.repo.updateCalled {
		t.Fatal("a non-owner update must not reach the repo")
	}

	if err := env.uc.Delete(context.Background(), env.orgID, stranger, view.ID); appErrCode(t, err) != http.StatusForbidden {
		t.Fatalf("non-owner delete err = %v, want 403", err)
	}
	if env.repo.deleteCalled {
		t.Fatal("a non-owner delete must not reach the repo")
	}

	// The owner can do both; the partial update leaves unnamed fields alone.
	updated, err := env.uc.Update(context.Background(), env.orgID, env.owner, view.ID, lvInput("Renamed", ""))
	if err != nil {
		t.Fatalf("owner update failed: %v", err)
	}
	if updated.Name != "Renamed" || !updated.Shared {
		t.Fatalf("partial update mangled the view: %+v", updated)
	}
	if err := env.uc.Delete(context.Background(), env.orgID, env.owner, view.ID); err != nil {
		t.Fatalf("owner delete failed: %v", err)
	}
}

func TestListView_UpdateRevalidatesDefinition(t *testing.T) {
	env := newListViewEnv()
	view := &domain.ListView{
		OrgID: env.orgID, OwnerUserID: env.owner, ObjectSlug: "contact",
		Name: "Mine", Definition: []byte(`{}`),
	}
	if err := env.repo.Create(context.Background(), view); err != nil {
		t.Fatal(err)
	}
	env.repo.updateCalled = false
	// A field hidden AFTER the view was saved must not be sneakable in via edit.
	env.authz.masks = map[string]domain.FieldMask{"contact": {Hidden: map[string]bool{"salary": true}}}
	in := domain.ListViewInput{Definition: []byte(`{"filter":{"field":"salary","operator":"gt","value":1}}`)}
	_, err := env.uc.Update(context.Background(), env.orgID, env.owner, view.ID, in)
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 for a hidden-field filter on update", code)
	}
	if env.repo.updateCalled {
		t.Fatal("an FLS-rejected update must not be persisted")
	}
}

func TestListView_UpdateDeleteNotFound(t *testing.T) {
	env := newListViewEnv()
	if _, err := env.uc.Update(context.Background(), env.orgID, env.owner, uuid.New(), lvInput("X", "")); appErrCode(t, err) != http.StatusNotFound {
		t.Fatalf("update of a missing view err = %v, want 404", err)
	}
	if err := env.uc.Delete(context.Background(), env.orgID, env.owner, uuid.New()); appErrCode(t, err) != http.StatusNotFound {
		t.Fatalf("delete of a missing view err = %v, want 404", err)
	}
}

// ============================================================
// List visibility
// ============================================================

func TestListView_ListOwnPlusShared(t *testing.T) {
	env := newListViewEnv()
	other := uuid.New()
	seed := []*domain.ListView{
		{OrgID: env.orgID, OwnerUserID: env.owner, ObjectSlug: "contact", Name: "Mine private"},
		{OrgID: env.orgID, OwnerUserID: other, ObjectSlug: "contact", Name: "Theirs shared", Shared: true},
		{OrgID: env.orgID, OwnerUserID: other, ObjectSlug: "contact", Name: "Theirs private"},
	}
	for _, v := range seed {
		if err := env.repo.Create(context.Background(), v); err != nil {
			t.Fatal(err)
		}
	}
	views, err := env.uc.List(context.Background(), env.orgID, env.owner, "contact")
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatalf("got %d views, want 2 (own + shared, never a stranger's private)", len(views))
	}
	for _, v := range views {
		if v.Name == "Theirs private" {
			t.Fatal("another user's private view leaked into List")
		}
	}
}

func TestListView_ListOmitsSharedViewsTouchingCallerHiddenFields(t *testing.T) {
	// Save-time FLS checks the AUTHOR's mask; this reader's mask is narrower.
	// A shared definition names its filter fields and constants verbatim, so
	// listing it to a role that has the field hidden leaks the field's
	// existence plus the org's threshold — the reader must not receive the
	// view at all. Their OWN views pass regardless: they authored the
	// definition, and hiding it with no edit path would strand it.
	env := newListViewEnv()
	other := uuid.New()
	salaryDef := datatypes.JSON(`{"filter":{"op":"AND","rules":[{"field":"salary","operator":"gte","value":100000}]}}`)
	seed := []*domain.ListView{
		{OrgID: env.orgID, OwnerUserID: other, ObjectSlug: "contact", Name: "Big salaries", Shared: true, Definition: salaryDef},
		{OrgID: env.orgID, OwnerUserID: other, ObjectSlug: "contact", Name: "Gmail folk", Shared: true,
			Definition: datatypes.JSON(`{"filter":{"op":"AND","rules":[{"field":"email","operator":"contains","value":"@gmail"}]}}`)},
		{OrgID: env.orgID, OwnerUserID: env.owner, ObjectSlug: "contact", Name: "My salary view", Definition: salaryDef},
	}
	for _, v := range seed {
		if err := env.repo.Create(context.Background(), v); err != nil {
			t.Fatal(err)
		}
	}

	env.authz.masks = map[string]domain.FieldMask{"contact": {Hidden: map[string]bool{"salary": true}}}
	views, err := env.uc.List(context.Background(), env.orgID, env.owner, "contact")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, v := range views {
		names[v.Name] = true
	}
	if names["Big salaries"] {
		t.Fatal("a shared view filtering on a caller-hidden field leaked its definition")
	}
	if !names["Gmail folk"] || !names["My salary view"] {
		t.Fatalf("over-filtered: got %v, want the visible shared view and the caller's own view", names)
	}
}

func TestListView_ListOLSDenied(t *testing.T) {
	env := newListViewEnv()
	env.authz.deny = map[string]bool{"contact:read": true}
	if _, err := env.uc.List(context.Background(), env.orgID, env.owner, "contact"); appErrCode(t, err) != http.StatusForbidden {
		t.Fatalf("list err = %v, want 403", err)
	}
}
