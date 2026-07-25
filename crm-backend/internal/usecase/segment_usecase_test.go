package usecase

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// ============================================================
// Fake SegmentStore — records whether the persistence/query layer was reached,
// so a test can prove a gate (OLS / FLS / tag ownership / type) rejected BEFORE
// any store call. It never runs real SQL; the compiled-SQL discipline is proven
// in repository/segment_sql_test.go and the Docker integration test.
// ============================================================

type fakeSegStore struct {
	segs          map[uuid.UUID]*domain.Segment
	createCalled  bool
	updateCalled  bool
	setCountCalls int
	addN          int
	dynCount      int
	staticCount   int
	tagOK         bool
	validateErr   error
}

func newFakeSegStore() *fakeSegStore {
	return &fakeSegStore{segs: map[uuid.UUID]*domain.Segment{}, tagOK: true}
}

func (f *fakeSegStore) CreateSegment(_ context.Context, s *domain.Segment) error {
	f.createCalled = true
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	f.segs[s.ID] = s
	return nil
}
func (f *fakeSegStore) GetSegmentByID(_ context.Context, _ uuid.UUID, id uuid.UUID) (*domain.Segment, error) {
	return f.segs[id], nil
}
func (f *fakeSegStore) ListSegmentsByOrg(_ context.Context, _ uuid.UUID) ([]domain.Segment, error) {
	out := make([]domain.Segment, 0, len(f.segs))
	for _, s := range f.segs {
		out = append(out, *s)
	}
	return out, nil
}
func (f *fakeSegStore) UpdateSegment(_ context.Context, s *domain.Segment) error {
	f.updateCalled = true
	f.segs[s.ID] = s
	return nil
}
func (f *fakeSegStore) SoftDeleteSegment(_ context.Context, _ uuid.UUID, id uuid.UUID) (bool, error) {
	if _, ok := f.segs[id]; !ok {
		return false, nil
	}
	delete(f.segs, id)
	return true, nil
}
func (f *fakeSegStore) SetSegmentCount(_ context.Context, _, _ uuid.UUID, _ int) error {
	f.setCountCalls++
	return nil
}
func (f *fakeSegStore) ValidateDefinition(_ []domain.ReportField, _ domain.SegmentFilter) error {
	return f.validateErr
}
func (f *fakeSegStore) CountDynamic(_ context.Context, _ uuid.UUID, _ []domain.ReportField, _ domain.SegmentFilter) (int, error) {
	return f.dynCount, nil
}
func (f *fakeSegStore) PreviewDynamic(_ context.Context, _ uuid.UUID, _ []domain.ReportField, _ domain.SegmentFilter, _ int) ([]domain.SegmentContactRow, error) {
	return nil, nil
}
func (f *fakeSegStore) CountStatic(_ context.Context, _, _ uuid.UUID) (int, error) {
	return f.staticCount, nil
}
func (f *fakeSegStore) PreviewStatic(_ context.Context, _, _ uuid.UUID, _ int) ([]domain.SegmentContactRow, error) {
	return nil, nil
}
func (f *fakeSegStore) AddStaticMembers(_ context.Context, _, _ uuid.UUID, _ []uuid.UUID, _ string) (int, error) {
	return f.addN, nil
}
func (f *fakeSegStore) RemoveStaticMember(_ context.Context, _, _, _ uuid.UUID) (bool, error) {
	return true, nil
}
func (f *fakeSegStore) TagBelongsToOrg(_ context.Context, _, _ uuid.UUID) (bool, error) {
	return f.tagOK, nil
}
func (f *fakeSegStore) CompileAudienceQuery(_ uuid.UUID, _ []domain.ReportField, includes, excludes []domain.ResolvedSegment) (string, []any, error) {
	// Minimal stand-in: the real SQL is proven in the repository + Docker tests.
	return "SELECT NULL::uuid AS contact_id, NULL::text AS email_normalized WHERE false", nil, nil
}

// ============================================================
// Env
// ============================================================

func segTestRegistry() *fakeRegistryRepo {
	contactDefID := uuid.MustParse("cccccccc-0000-0000-0000-00000000c0a1")
	table := "contacts"
	return &fakeRegistryRepo{
		defs: []domain.ObjectDef{{
			ID: contactDefID, Slug: "contact", Label: "Contact", LabelPlural: "Contacts",
			IsSystem: true, Storage: "table", RecordTable: &table,
		}},
		fields: map[uuid.UUID][]domain.ObjectField{
			contactDefID: {
				{Key: "email", Label: "Email", Type: "text", StorageKind: "column", MapsToColumn: reportStrPtr("email"), IsSystem: true},
				{Key: "first_name", Label: "First", Type: "text", StorageKind: "column", MapsToColumn: reportStrPtr("first_name"), IsSystem: true},
				{Key: "lead_source", Label: "Lead Source", Type: "text", StorageKind: "jsonb"},
				{Key: "score", Label: "Score", Type: "number", StorageKind: "jsonb"},
			},
		},
	}
}

type segEnv struct {
	uc    domain.SegmentUseCase
	store *fakeSegStore
	authz *fakeAuthorizer
	orgID uuid.UUID
}

func newSegEnv() *segEnv {
	store := newFakeSegStore()
	authz := &fakeAuthorizer{}
	// redis nil → count cache degrades to compute-live; SetSegmentCount (durable) is
	// the observable proxy for "this count was cacheable".
	uc := NewSegmentUseCase(store, segTestRegistry(), authz, nil)
	return &segEnv{uc: uc, store: store, authz: authz, orgID: uuid.New()}
}

func dynInput(name string, def string) domain.SegmentInput {
	return domain.SegmentInput{Name: name, Type: domain.SegmentTypeDynamic, Definition: []byte(def)}
}

// ============================================================
// Security ordering — the strict allowlist + FLS are load-bearing (ingest writes
// callerless, so marketing.manage alone must not become a data-read primitive).
// ============================================================

func TestSegment_CreateOLSDenied(t *testing.T) {
	env := newSegEnv()
	env.authz.deny = map[string]bool{"contact:read": true}
	_, err := env.uc.Create(context.Background(), env.orgID, uuid.New(), dynInput("X", `{"field":"email","operator":"eq","value":"a@b.com"}`))
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", code)
	}
	if env.store.createCalled {
		t.Fatal("a denied create must not reach the store")
	}
}

func TestSegment_CreateFLSHiddenFieldRejected(t *testing.T) {
	env := newSegEnv()
	env.authz.masks = map[string]domain.FieldMask{"contact": {Hidden: map[string]bool{"email": true}}}
	_, err := env.uc.Create(context.Background(), env.orgID, uuid.New(), dynInput("X", `{"field":"email","operator":"eq","value":"a@b.com"}`))
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 for a hidden-field filter", code)
	}
	if env.store.createCalled {
		t.Fatal("an FLS-rejected create must not reach the store")
	}
}

func TestSegment_CreateTagOwnershipEnforced(t *testing.T) {
	env := newSegEnv()
	tag := uuid.New().String()
	def := `{"tag_id":"` + tag + `"}`

	// A tag id that does not belong to the org is a 400 (no cross-org probing) and
	// must not be persisted.
	env.store.tagOK = false
	_, err := env.uc.Create(context.Background(), env.orgID, uuid.New(), dynInput("X", def))
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Fatalf("unknown-tag code = %d, want 400", code)
	}
	if env.store.createCalled {
		t.Fatal("an unknown-tag segment must not be persisted")
	}

	// An org-owned tag is accepted.
	env.store.tagOK = true
	if _, err := env.uc.Create(context.Background(), env.orgID, uuid.New(), dynInput("X", def)); err != nil {
		t.Fatalf("org-owned tag should be accepted: %v", err)
	}
	if !env.store.createCalled {
		t.Fatal("a valid tag segment should be created")
	}
}

func TestSegment_CreateInvalidTypeRejected(t *testing.T) {
	env := newSegEnv()
	_, err := env.uc.Create(context.Background(), env.orgID, uuid.New(), domain.SegmentInput{Name: "X", Type: "weird"})
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", code)
	}
}

func TestSegment_CreateInvalidJSONRejected(t *testing.T) {
	env := newSegEnv()
	_, err := env.uc.Create(context.Background(), env.orgID, uuid.New(), dynInput("X", `{not json`))
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", code)
	}
	if env.store.createCalled {
		t.Fatal("an unparseable definition must not be persisted")
	}
}

func TestSegment_CreateCompileErrorIs400(t *testing.T) {
	env := newSegEnv()
	env.store.validateErr = errSegCompile
	_, err := env.uc.Create(context.Background(), env.orgID, uuid.New(), dynInput("X", `{"field":"email","operator":"eq","value":"x"}`))
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 for a compile-check failure", code)
	}
	if env.store.createCalled {
		t.Fatal("a compile-check failure must not be persisted")
	}
}

var errSegCompile = &segTestErr{"segment: unknown field \"salary\""}

type segTestErr struct{ s string }

func (e *segTestErr) Error() string { return e.s }

func TestSegment_MissingNameRejected(t *testing.T) {
	env := newSegEnv()
	_, err := env.uc.Create(context.Background(), env.orgID, uuid.New(), dynInput("   ", `{}`))
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 for a blank name", code)
	}
}

// ============================================================
// Update re-validates FLS (a field hidden AFTER save must not be re-savable).
// ============================================================

func TestSegment_UpdateFLSReRejects(t *testing.T) {
	env := newSegEnv()
	seg := &domain.Segment{ID: uuid.New(), OrgID: env.orgID, Type: domain.SegmentTypeDynamic, Definition: datatypes.JSON(`{}`)}
	env.store.segs[seg.ID] = seg
	env.authz.masks = map[string]domain.FieldMask{"contact": {Hidden: map[string]bool{"email": true}}}
	_, err := env.uc.Update(context.Background(), env.orgID, seg.ID, dynInput("X", `{"field":"email","operator":"eq","value":"x"}`))
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", code)
	}
}

// ============================================================
// Type guard — static membership only applies to a static list.
// ============================================================

func TestSegment_AddStaticMembersRejectsDynamic(t *testing.T) {
	env := newSegEnv()
	dyn := &domain.Segment{ID: uuid.New(), OrgID: env.orgID, Type: domain.SegmentTypeDynamic, Definition: datatypes.JSON(`{}`)}
	env.store.segs[dyn.ID] = dyn
	_, err := env.uc.AddStaticMembers(context.Background(), env.orgID, dyn.ID, []uuid.UUID{uuid.New()})
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Fatalf("adding members to a dynamic segment: code = %d, want 400", code)
	}

	stat := &domain.Segment{ID: uuid.New(), OrgID: env.orgID, Type: domain.SegmentTypeStatic, Definition: datatypes.JSON(`{}`)}
	env.store.segs[stat.ID] = stat
	env.store.addN = 3
	n, err := env.uc.AddStaticMembers(context.Background(), env.orgID, stat.ID, []uuid.UUID{uuid.New(), uuid.New(), uuid.New()})
	if err != nil {
		t.Fatalf("adding members to a static list: %v", err)
	}
	if n != 3 {
		t.Fatalf("added = %d, want 3", n)
	}
}

// ============================================================
// Count cache is org-wide-only — a row-scoped caller's count is caller-specific
// and must NEVER be written to the shared cache (cross-user count leak). The
// durable SetSegmentCount is the observable proxy for "cacheable".
// ============================================================

func segCtxOwner(userID uuid.UUID) context.Context {
	return domain.WithCallerIdentity(context.Background(), domain.Caller{UserID: userID, RoleID: uuid.New(), IsOwner: true})
}

func segCtxRowScoped(userID uuid.UUID) context.Context {
	return domain.WithCallerIdentity(context.Background(), domain.Caller{UserID: userID, RoleID: uuid.New(), DataScope: domain.DataScopeOwn})
}

func TestSegment_CountCachedOnlyForOrgWideCaller(t *testing.T) {
	env := newSegEnv()
	seg := &domain.Segment{ID: uuid.New(), OrgID: env.orgID, Type: domain.SegmentTypeDynamic, Definition: datatypes.JSON(`{}`)}
	env.store.segs[seg.ID] = seg
	env.store.dynCount = 42

	// Org-wide (owner) caller → count is shareable → durable cache write happens.
	if _, err := env.uc.Count(segCtxOwner(uuid.New()), env.orgID, seg.ID); err != nil {
		t.Fatalf("owner count: %v", err)
	}
	if env.store.setCountCalls != 1 {
		t.Fatalf("org-wide count must be cached (setCountCalls=%d, want 1)", env.store.setCountCalls)
	}

	// Row-scoped caller → count is caller-specific → must NOT touch the shared cache.
	if _, err := env.uc.Count(segCtxRowScoped(uuid.New()), env.orgID, seg.ID); err != nil {
		t.Fatalf("row-scoped count: %v", err)
	}
	if env.store.setCountCalls != 1 {
		t.Fatalf("a row-scoped count must not be cached (setCountCalls=%d, want still 1)", env.store.setCountCalls)
	}
}

// ============================================================
// Preview re-checks FLS at run time (field hidden after the segment was saved).
// ============================================================

func TestSegment_PreviewRunTimeFLS(t *testing.T) {
	env := newSegEnv()
	seg := &domain.Segment{ID: uuid.New(), OrgID: env.orgID, Type: domain.SegmentTypeDynamic,
		Definition: datatypes.JSON(`{"field":"email","operator":"eq","value":"x"}`)}
	env.store.segs[seg.ID] = seg
	env.authz.masks = map[string]domain.FieldMask{"contact": {Hidden: map[string]bool{"email": true}}}
	_, err := env.uc.Preview(context.Background(), env.orgID, seg.ID, 50)
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 — a field hidden after save must not leak via preview", code)
	}
}

// ============================================================
// Dry-run preview (the builder's live count) runs the full validation path.
// ============================================================

func TestSegment_PreviewDefinitionOLSDenied(t *testing.T) {
	env := newSegEnv()
	env.authz.deny = map[string]bool{"contact:read": true}
	_, err := env.uc.PreviewDefinition(context.Background(), env.orgID, []byte(`{"field":"email","operator":"eq","value":"x"}`), 50)
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", code)
	}
}

func TestSegment_PreviewDefinitionFLSHidden(t *testing.T) {
	env := newSegEnv()
	env.authz.masks = map[string]domain.FieldMask{"contact": {Hidden: map[string]bool{"email": true}}}
	_, err := env.uc.PreviewDefinition(context.Background(), env.orgID, []byte(`{"field":"email","operator":"eq","value":"x"}`), 50)
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 — a dry-run must not probe a hidden field", code)
	}
}

func TestSegment_PreviewDefinitionRuns(t *testing.T) {
	env := newSegEnv()
	env.store.dynCount = 7
	res, err := env.uc.PreviewDefinition(context.Background(), env.orgID, []byte(`{"field":"lead_source","operator":"eq","value":"web"}`), 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Count != 7 {
		t.Fatalf("count = %d, want 7", res.Count)
	}
	// A dry-run never persists a durable count.
	if env.store.setCountCalls != 0 {
		t.Fatalf("a dry-run must not write the durable count cache (calls=%d)", env.store.setCountCalls)
	}
}

// ============================================================
// Review fixes: Delete OLS gate, name length, Get OLS + count scrub + definition
// restriction, Update rejects a restricted editor.
// ============================================================

func TestSegment_DeleteOLSDenied(t *testing.T) {
	env := newSegEnv()
	seg := &domain.Segment{ID: uuid.New(), OrgID: env.orgID, Type: domain.SegmentTypeDynamic, Definition: datatypes.JSON(`{}`)}
	env.store.segs[seg.ID] = seg
	env.authz.deny = map[string]bool{"contact:read": true}
	err := env.uc.Delete(context.Background(), env.orgID, seg.ID)
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", code)
	}
}

func TestSegment_NameTooLongRejected(t *testing.T) {
	env := newSegEnv()
	long := strings.Repeat("a", 161)
	if code := appErrCode(t, mustErr(env.uc.Create(context.Background(), env.orgID, uuid.New(), dynInput(long, `{}`)))); code != http.StatusBadRequest {
		t.Fatalf("create long name code = %d, want 400", code)
	}
	seg := &domain.Segment{ID: uuid.New(), OrgID: env.orgID, Type: domain.SegmentTypeDynamic, Definition: datatypes.JSON(`{}`)}
	env.store.segs[seg.ID] = seg
	if code := appErrCode(t, mustErr(env.uc.Update(context.Background(), env.orgID, seg.ID, dynInput(long, `{}`)))); code != http.StatusBadRequest {
		t.Fatalf("update long name code = %d, want 400", code)
	}
}

func TestSegment_GetOLSDenied(t *testing.T) {
	env := newSegEnv()
	seg := &domain.Segment{ID: uuid.New(), OrgID: env.orgID, Type: domain.SegmentTypeDynamic, Definition: datatypes.JSON(`{}`)}
	env.store.segs[seg.ID] = seg
	env.authz.deny = map[string]bool{"contact:read": true}
	if code := appErrCode(t, mustErrSeg(env.uc.Get(context.Background(), env.orgID, seg.ID))); code != http.StatusForbidden {
		t.Fatalf("Get code = %d, want 403", code)
	}
	if _, err := env.uc.List(context.Background(), env.orgID); appErrCode(t, err) != http.StatusForbidden {
		t.Fatal("List must also 403 without contact:read")
	}
}

func TestSegment_GetScrubsOrgWideCountForRowScopedCaller(t *testing.T) {
	env := newSegEnv()
	now := time.Now()
	seg := &domain.Segment{ID: uuid.New(), OrgID: env.orgID, Type: domain.SegmentTypeDynamic,
		Definition: datatypes.JSON(`{}`), CountCached: 99, CountCachedAt: &now}
	env.store.segs[seg.ID] = seg

	// Row-scoped caller must not see the org-wide cached aggregate.
	got, err := env.uc.Get(segCtxRowScoped(uuid.New()), env.orgID, seg.ID)
	if err != nil {
		t.Fatalf("row-scoped Get: %v", err)
	}
	if got.CountCached != 0 || got.CountCachedAt != nil {
		t.Fatalf("row-scoped caller must see a scrubbed count, got %d / %v", got.CountCached, got.CountCachedAt)
	}
	// Org-wide caller keeps it. (Note: the fake mutates the shared pointer, so re-seed.)
	env.store.segs[seg.ID] = &domain.Segment{ID: seg.ID, OrgID: env.orgID, Type: domain.SegmentTypeDynamic,
		Definition: datatypes.JSON(`{}`), CountCached: 99, CountCachedAt: &now}
	got, err = env.uc.Get(segCtxOwner(uuid.New()), env.orgID, seg.ID)
	if err != nil {
		t.Fatalf("owner Get: %v", err)
	}
	if got.CountCached != 99 {
		t.Fatalf("org-wide caller should see the cached count, got %d", got.CountCached)
	}
}

func TestSegment_GetRestrictsHiddenFieldDefinition(t *testing.T) {
	env := newSegEnv()
	seg := &domain.Segment{ID: uuid.New(), OrgID: env.orgID, Type: domain.SegmentTypeDynamic,
		Definition: datatypes.JSON(`{"field":"email","operator":"eq","value":"x"}`)}
	env.store.segs[seg.ID] = seg
	env.authz.masks = map[string]domain.FieldMask{"contact": {Hidden: map[string]bool{"email": true}}}
	got, err := env.uc.Get(context.Background(), env.orgID, seg.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.DefinitionRestricted {
		t.Fatal("a definition filtering on a hidden field must be flagged restricted")
	}
	if string(got.Definition) != "{}" {
		t.Fatalf("the restricted definition must be blanked, got %s", string(got.Definition))
	}
}

func TestSegment_UpdateRejectsRestrictedEditor(t *testing.T) {
	env := newSegEnv()
	seg := &domain.Segment{ID: uuid.New(), OrgID: env.orgID, Type: domain.SegmentTypeDynamic,
		Definition: datatypes.JSON(`{"field":"email","operator":"eq","value":"x"}`)}
	env.store.segs[seg.ID] = seg
	env.authz.masks = map[string]domain.FieldMask{"contact": {Hidden: map[string]bool{"email": true}}}
	// Even a name-only edit (blanked definition from the FE) must be refused, so it
	// can't silently drop the hidden condition.
	_, err := env.uc.Update(context.Background(), env.orgID, seg.ID, domain.SegmentInput{Name: "Renamed", Type: "dynamic", Definition: []byte(`{}`)})
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", code)
	}
}

func mustErr[T any](_ T, err error) error       { return err }
func mustErrSeg(_ *domain.Segment, err error) error { return err }

// ============================================================
// ListFields applies FLS.
// ============================================================

func TestSegment_ListFieldsStripsHidden(t *testing.T) {
	env := newSegEnv()
	env.authz.masks = map[string]domain.FieldMask{"contact": {Hidden: map[string]bool{"email": true}}}
	fields, err := env.uc.ListFields(context.Background(), env.orgID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range fields {
		if f.Key == "email" {
			t.Fatal("a hidden field leaked into the segment field catalog")
		}
	}
	// A visible field is present.
	var sawFirst bool
	for _, f := range fields {
		if f.Key == "first_name" {
			sawFirst = true
		}
	}
	if !sawFirst {
		t.Fatal("expected first_name in the catalog")
	}
}
