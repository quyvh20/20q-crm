package usecase

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// Fakes specific to OLS + audit
// ============================================================

type authzCall struct {
	slug   string
	action domain.RecordAction
}

// fakeAuthorizer records every Authorize/Audit call and can deny by "slug:action".
type fakeAuthorizer struct {
	deny   map[string]bool
	calls  []authzCall
	audits []domain.AuditEntry
}

func (f *fakeAuthorizer) Authorize(_ context.Context, _ uuid.UUID, slug string, action domain.RecordAction) error {
	f.calls = append(f.calls, authzCall{slug, action})
	if f.deny[slug+":"+string(action)] {
		return domain.NewAppError(403, "denied")
	}
	return nil
}

func (f *fakeAuthorizer) Audit(_ context.Context, e domain.AuditEntry) {
	f.audits = append(f.audits, e)
}

func (f *fakeAuthorizer) last() authzCall { return f.calls[len(f.calls)-1] }

// secCustomUC is a custom-object fake that can return DISTINCT prior vs updated
// records, so the audit diff has something real to compare. (The shared
// fakeCustomObjUC returns one record for both, which would diff to empty.)
type secCustomUC struct {
	domain.CustomObjectUseCase
	def     *domain.CustomObjectDef
	prior   *domain.CustomObjectRecord
	updated *domain.CustomObjectRecord
	created *domain.CustomObjectRecord
	deleted bool
}

func (f *secCustomUC) GetDefBySlug(context.Context, uuid.UUID, string) (*domain.CustomObjectDef, error) {
	return f.def, nil
}
func (f *secCustomUC) GetRecord(context.Context, uuid.UUID, uuid.UUID) (*domain.CustomObjectRecord, error) {
	return f.prior, nil
}
func (f *secCustomUC) CreateRecord(context.Context, uuid.UUID, uuid.UUID, string, domain.CreateRecordInput) (*domain.CustomObjectRecord, error) {
	return f.created, nil
}
func (f *secCustomUC) UpdateRecord(context.Context, uuid.UUID, string, uuid.UUID, domain.UpdateRecordInput) (*domain.CustomObjectRecord, error) {
	return f.updated, nil
}
func (f *secCustomUC) DeleteRecord(context.Context, uuid.UUID, uuid.UUID) error {
	f.deleted = true
	return nil
}

func newSecService(custom domain.CustomObjectUseCase, deal *fakeDealUC, authz domain.RecordAuthorizer) domain.RecordService {
	if custom == nil {
		custom = &fakeCustomObjUC{}
	}
	if deal == nil {
		deal = &fakeDealUC{}
	}
	return NewRecordService(custom, &recordingSettingsUC{}, &contactAdapterStubUC{}, &companyAdapterStubUC{}, deal, nil, nil, authz)
}

// ============================================================
// OLS enforcement at the RecordService boundary
// ============================================================

func TestRecordService_DeniedCreate_DoesNotReachUsecase(t *testing.T) {
	deal := &fakeDealUC{ret: &domain.Deal{ID: uuid.New(), Title: "X"}}
	authz := &fakeAuthorizer{deny: map[string]bool{"deal:create": true}}
	svc := newSecService(nil, deal, authz)

	ctx := domain.WithCaller(context.Background(), domain.RoleViewer, uuid.New())
	_, err := svc.Create(ctx, uuid.New(), uuid.New(), "deal", domain.RecordWriteInput{
		Fields: map[string]interface{}{"title": "X"},
	})

	assertForbidden(t, err, "create denied by OLS")
	if deal.createCalled {
		t.Fatal("a denied create must not reach the deal usecase")
	}
	if len(authz.audits) != 0 {
		t.Fatalf("a denied write must not be audited, got %d audits", len(authz.audits))
	}
}

func TestRecordService_DeniedRead_BlocksList(t *testing.T) {
	authz := &fakeAuthorizer{deny: map[string]bool{"deal:read": true}}
	svc := newSecService(nil, &fakeDealUC{}, authz)

	ctx := domain.WithCaller(context.Background(), "contractor", uuid.New())
	_, err := svc.List(ctx, uuid.New(), "deal", domain.RecordListInput{})
	assertForbidden(t, err, "list denied by OLS")
}

func TestRecordService_AuthorizesActionPerVerb(t *testing.T) {
	defID, recID := uuid.New(), uuid.New()
	custom := &secCustomUC{
		def:     &domain.CustomObjectDef{ID: defID},
		prior:   &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, Data: domain.JSON(`{"name":"P"}`)},
		updated: &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, Data: domain.JSON(`{"name":"P2"}`)},
		created: &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, Data: domain.JSON(`{"name":"P"}`)},
	}
	authz := &fakeAuthorizer{}
	svc := newSecService(custom, nil, authz)
	ctx := domain.WithCaller(context.Background(), domain.RoleManager, uuid.New())
	org := uuid.New()

	cases := []struct {
		name   string
		run    func() error
		action domain.RecordAction
	}{
		{"get", func() error { _, e := svc.Get(ctx, org, "project", recID); return e }, domain.ActionRead},
		{"create", func() error {
			_, e := svc.Create(ctx, org, uuid.New(), "project", domain.RecordWriteInput{Fields: map[string]interface{}{"name": "P"}})
			return e
		}, domain.ActionCreate},
		{"update", func() error {
			_, e := svc.Update(ctx, org, "project", recID, domain.RecordWriteInput{Fields: map[string]interface{}{"name": "P2"}})
			return e
		}, domain.ActionEdit},
		{"delete", func() error { return svc.Delete(ctx, org, "project", recID) }, domain.ActionDelete},
	}
	for _, tc := range cases {
		if err := tc.run(); err != nil {
			t.Fatalf("%s: unexpected error %v", tc.name, err)
		}
		got := authz.last()
		if got.slug != "project" || got.action != tc.action {
			t.Fatalf("%s: expected authorize(project, %s), got (%s, %s)", tc.name, tc.action, got.slug, got.action)
		}
	}
}

// ============================================================
// Audit trail
// ============================================================

func TestRecordService_AuditsCreate(t *testing.T) {
	defID, recID := uuid.New(), uuid.New()
	custom := &secCustomUC{
		def:     &domain.CustomObjectDef{ID: defID},
		created: &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, Data: domain.JSON(`{"name":"Acme"}`)},
	}
	authz := &fakeAuthorizer{}
	svc := newSecService(custom, nil, authz)
	actor := uuid.New()
	ctx := domain.WithCaller(context.Background(), domain.RoleManager, actor)

	if _, err := svc.Create(ctx, uuid.New(), actor, "project", domain.RecordWriteInput{
		Fields: map[string]interface{}{"name": "Acme"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	e := lastAudit(t, authz)
	if e.Action != domain.ActionCreate || e.ObjectSlug != "project" || e.RecordID != recID {
		t.Fatalf("create audit fields wrong: %+v", e)
	}
	if e.ActorID != actor {
		t.Fatalf("expected actor %s, got %s", actor, e.ActorID)
	}
	newVal := changeNew(t, e.Changes, "name")
	if newVal != "Acme" {
		t.Fatalf("expected create diff new=Acme, got %v", newVal)
	}
}

func TestRecordService_AuditsUpdate_WithFieldDiff(t *testing.T) {
	defID, recID := uuid.New(), uuid.New()
	custom := &secCustomUC{
		def:     &domain.CustomObjectDef{ID: defID},
		prior:   &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, Data: domain.JSON(`{"status":"open"}`)},
		updated: &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, Data: domain.JSON(`{"status":"closed"}`)},
	}
	authz := &fakeAuthorizer{}
	svc := newSecService(custom, nil, authz)
	ctx := domain.WithCaller(context.Background(), domain.RoleManager, uuid.New())

	if _, err := svc.Update(ctx, uuid.New(), "project", recID, domain.RecordWriteInput{
		Fields: map[string]interface{}{"status": "closed"},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	e := lastAudit(t, authz)
	if e.Action != domain.ActionEdit {
		t.Fatalf("expected edit audit, got %s", e.Action)
	}
	diff, ok := e.Changes["status"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a status diff, got %+v", e.Changes)
	}
	if diff["old"] != "open" || diff["new"] != "closed" {
		t.Fatalf("expected status open→closed, got %+v", diff)
	}
}

func TestRecordService_AuditsDelete(t *testing.T) {
	defID, recID := uuid.New(), uuid.New()
	custom := &secCustomUC{
		def:   &domain.CustomObjectDef{ID: defID},
		prior: &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID},
	}
	authz := &fakeAuthorizer{}
	svc := newSecService(custom, nil, authz)
	ctx := domain.WithCaller(context.Background(), domain.RoleManager, uuid.New())

	if err := svc.Delete(ctx, uuid.New(), "project", recID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !custom.deleted {
		t.Fatal("expected the record to be deleted")
	}
	e := lastAudit(t, authz)
	if e.Action != domain.ActionDelete || e.RecordID != recID {
		t.Fatalf("delete audit wrong: %+v", e)
	}
}

// ============================================================
// Helpers
// ============================================================

func lastAudit(t *testing.T, a *fakeAuthorizer) domain.AuditEntry {
	t.Helper()
	if len(a.audits) == 0 {
		t.Fatal("expected an audit entry, got none")
	}
	return a.audits[len(a.audits)-1]
}

func changeNew(t *testing.T, changes map[string]interface{}, key string) interface{} {
	t.Helper()
	cell, ok := changes[key].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a diff cell for %q, got %+v", key, changes)
	}
	return cell["new"]
}
