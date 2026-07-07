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

// secCtx builds a full caller identity for the RecordService security tests
// (replacing the removed domain.WithCaller name-only bridge). RoleID is a fresh
// id: the fakeAuthorizer keys its decision off slug:action, not the role, so the
// id value is irrelevant — only "a caller is present" (a user call, not a trusted
// in-process one) and the exact UserID (asserted as the audit actor) matter.
func secCtx(role string, userID uuid.UUID) context.Context {
	return domain.WithCallerIdentity(context.Background(), domain.Caller{
		Role: role, UserID: userID, RoleID: uuid.New(),
	})
}

type authzCall struct {
	slug   string
	action domain.RecordAction
}

// fakeAuthorizer records every Authorize/Audit call and can deny by "slug:action".
// masks lets a test inject Field-Level Security restrictions per slug (P5b).
type fakeAuthorizer struct {
	deny      map[string]bool
	masks     map[string]domain.FieldMask
	calls     []authzCall
	audits    []domain.AuditEntry
	maskCalls int
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

func (f *fakeAuthorizer) FieldMask(_ context.Context, _ uuid.UUID, slug string) domain.FieldMask {
	f.maskCalls++
	if f.masks != nil {
		return f.masks[slug]
	}
	return domain.FieldMask{}
}

func (f *fakeAuthorizer) last() authzCall { return f.calls[len(f.calls)-1] }

// secCustomUC is a custom-object fake that can return DISTINCT prior vs updated
// records, so the audit diff has something real to compare. (The shared
// fakeCustomObjUC returns one record for both, which would diff to empty.)
type secCustomUC struct {
	domain.CustomObjectUseCase
	def          *domain.CustomObjectDef
	prior        *domain.CustomObjectRecord
	updated      *domain.CustomObjectRecord
	created      *domain.CustomObjectRecord
	deleted      bool
	createCalled bool
	updateCalled bool
}

func (f *secCustomUC) GetDefBySlug(context.Context, uuid.UUID, string) (*domain.CustomObjectDef, error) {
	return f.def, nil
}
func (f *secCustomUC) GetRecord(context.Context, uuid.UUID, uuid.UUID) (*domain.CustomObjectRecord, error) {
	return f.prior, nil
}
func (f *secCustomUC) ListRecords(context.Context, uuid.UUID, string, domain.RecordFilter) ([]domain.CustomObjectRecord, int64, error) {
	if f.prior == nil {
		return nil, 0, nil
	}
	return []domain.CustomObjectRecord{*f.prior}, 1, nil
}
func (f *secCustomUC) CreateRecord(context.Context, uuid.UUID, uuid.UUID, string, domain.CreateRecordInput) (*domain.CustomObjectRecord, error) {
	f.createCalled = true
	return f.created, nil
}
func (f *secCustomUC) UpdateRecord(context.Context, uuid.UUID, string, uuid.UUID, domain.UpdateRecordInput) (*domain.CustomObjectRecord, error) {
	f.updateCalled = true
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

	ctx := secCtx(domain.RoleViewer, uuid.New())
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

	ctx := secCtx("contractor", uuid.New())
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
	ctx := secCtx(domain.RoleManager, uuid.New())
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
	ctx := secCtx(domain.RoleManager, actor)

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
	ctx := secCtx(domain.RoleManager, uuid.New())

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
	ctx := secCtx(domain.RoleManager, uuid.New())

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
// Field-Level Security enforcement at the RecordService boundary (P5b)
// ============================================================

func newFLSCustom(data string) *secCustomUC {
	defID, recID := uuid.New(), uuid.New()
	return &secCustomUC{
		def:     &domain.CustomObjectDef{ID: defID},
		prior:   &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, Data: domain.JSON(data)},
		updated: &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, Data: domain.JSON(data)},
		created: &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, Data: domain.JSON(data)},
	}
}

func hiddenMask(slug, key string) *fakeAuthorizer {
	return &fakeAuthorizer{masks: map[string]domain.FieldMask{
		slug: {Hidden: map[string]bool{key: true}},
	}}
}

// A hidden field is stripped from a single-record read response — server-side, not
// just the UI (plan §7.4).
func TestRecordService_FLS_StripsHiddenFieldOnGet(t *testing.T) {
	custom := newFLSCustom(`{"name":"P","value":999}`)
	svc := newSecService(custom, nil, hiddenMask("project", "value"))
	ctx := secCtx(domain.RoleViewer, uuid.New())

	rec, err := svc.Get(ctx, uuid.New(), "project", custom.prior.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := rec.Fields["value"]; ok {
		t.Fatal("hidden field 'value' must be stripped from the Get response")
	}
	if rec.Fields["name"] != "P" {
		t.Fatalf("visible field 'name' should survive, got %+v", rec.Fields)
	}
}

// The strip also applies to every record in a list page.
func TestRecordService_FLS_StripsHiddenFieldOnList(t *testing.T) {
	custom := newFLSCustom(`{"name":"P","value":999}`)
	svc := newSecService(custom, nil, hiddenMask("project", "value"))
	ctx := secCtx(domain.RoleViewer, uuid.New())

	list, err := svc.List(ctx, uuid.New(), "project", domain.RecordListInput{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.Records) != 1 {
		t.Fatalf("expected one record, got %d", len(list.Records))
	}
	if _, ok := list.Records[0].Fields["value"]; ok {
		t.Fatal("hidden field 'value' must be stripped from every list record")
	}
}

// A write that touches a hidden field is rejected (403) and never reaches storage
// or the audit trail (plan P5b "reject writes to them").
func TestRecordService_FLS_RejectsWriteToHiddenField_OnUpdate(t *testing.T) {
	custom := newFLSCustom(`{"value":1}`)
	authz := hiddenMask("project", "value")
	svc := newSecService(custom, nil, authz)
	ctx := secCtx(domain.RoleViewer, uuid.New())

	_, err := svc.Update(ctx, uuid.New(), "project", custom.prior.ID, domain.RecordWriteInput{
		Fields: map[string]interface{}{"value": 2},
	})
	assertForbidden(t, err, "writing a hidden field")
	if custom.updateCalled {
		t.Fatal("a rejected write must not reach the usecase")
	}
	if len(authz.audits) != 0 {
		t.Fatalf("a rejected write must not be audited, got %d", len(authz.audits))
	}
}

// Read-only is the weaker restriction: visible on read, rejected on write.
func TestRecordService_FLS_RejectsWriteToReadOnlyField_OnCreate(t *testing.T) {
	custom := newFLSCustom(`{}`)
	authz := &fakeAuthorizer{masks: map[string]domain.FieldMask{
		"project": {ReadOnly: map[string]bool{"locked": true}},
	}}
	svc := newSecService(custom, nil, authz)
	ctx := secCtx(domain.RoleSales, uuid.New())

	_, err := svc.Create(ctx, uuid.New(), uuid.New(), "project", domain.RecordWriteInput{
		Fields: map[string]interface{}{"locked": "x"},
	})
	assertForbidden(t, err, "writing a read-only field")
	if custom.createCalled {
		t.Fatal("a rejected create must not reach the usecase")
	}
}

// Writing an allowed field succeeds, but a co-resident hidden field is still
// stripped from the write echo — so a creator can't read a field hidden from them.
func TestRecordService_FLS_StripsHiddenFieldFromWriteResponse(t *testing.T) {
	custom := newFLSCustom(`{"name":"P","value":42}`)
	svc := newSecService(custom, nil, hiddenMask("project", "value"))
	ctx := secCtx(domain.RoleManager, uuid.New())

	rec, err := svc.Create(ctx, uuid.New(), uuid.New(), "project", domain.RecordWriteInput{
		Fields: map[string]interface{}{"name": "P"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !custom.createCalled {
		t.Fatal("an allowed create should reach the usecase")
	}
	if _, ok := rec.Fields["value"]; ok {
		t.Fatal("hidden field must be stripped from the create response")
	}
	if rec.Fields["name"] != "P" {
		t.Fatalf("visible field should remain, got %+v", rec.Fields)
	}
}

// With no mask configured FLS is a complete no-op: every field survives and the
// record is untouched (zero overhead when unused).
func TestRecordService_FLS_EmptyMask_LeavesRecordIntact(t *testing.T) {
	custom := newFLSCustom(`{"name":"P","value":1}`)
	svc := newSecService(custom, nil, &fakeAuthorizer{}) // no masks
	ctx := secCtx(domain.RoleViewer, uuid.New())

	rec, err := svc.Get(ctx, uuid.New(), "project", custom.prior.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := rec.Fields["value"]; !ok {
		t.Fatal("an unrestricted object must keep all fields")
	}
	if rec.Fields["name"] != "P" {
		t.Fatalf("unexpected fields: %+v", rec.Fields)
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
