package usecase

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// Fakes — each embeds its domain interface so only the methods a test exercises
// need bodies; any unexpected call panics on the nil embedded interface.
// ============================================================

type fakeDealUC struct {
	domain.DealUseCase
	created           domain.CreateDealInput
	createCalled      bool
	updated           domain.UpdateDealInput
	updateCalled      bool
	ret               *domain.Deal
	getRet            *domain.Deal // prior-state read (defaults to ret)
	changeStageCalled bool
	changeStageInput  domain.UpdateDealStageInput
	listRet           []domain.Deal
	listNext          string
	listGot           domain.DealFilter
}

func (f *fakeDealUC) Create(_ context.Context, _ uuid.UUID, in domain.CreateDealInput) (*domain.Deal, error) {
	f.created = in
	f.createCalled = true
	return f.ret, nil
}
func (f *fakeDealUC) Update(_ context.Context, _, _ uuid.UUID, in domain.UpdateDealInput) (*domain.Deal, error) {
	f.updated = in
	f.updateCalled = true
	return f.ret, nil
}
func (f *fakeDealUC) ChangeStage(_ context.Context, _, _ uuid.UUID, in domain.UpdateDealStageInput) (*domain.Deal, error) {
	f.changeStageCalled = true
	f.changeStageInput = in
	return f.ret, nil
}
func (f *fakeDealUC) GetByID(_ context.Context, _, _ uuid.UUID) (*domain.Deal, error) {
	if f.getRet != nil {
		return f.getRet, nil
	}
	return f.ret, nil
}
func (f *fakeDealUC) List(_ context.Context, _ uuid.UUID, ff domain.DealFilter) ([]domain.Deal, string, error) {
	f.listGot = ff
	return f.listRet, f.listNext, nil
}

type fakeCustomObjUC struct {
	domain.CustomObjectUseCase
	def        *domain.CustomObjectDef
	defErr     error
	rec        *domain.CustomObjectRecord
	recErr     error
	createData domain.JSON
	updateData domain.JSON
	listRet    []domain.CustomObjectRecord
	listTotal  int64
	listErr    error
	listGot    domain.RecordFilter
	deleted    bool
}

func (f *fakeCustomObjUC) GetDefBySlug(_ context.Context, _ uuid.UUID, _ string) (*domain.CustomObjectDef, error) {
	return f.def, f.defErr
}
func (f *fakeCustomObjUC) GetRecord(_ context.Context, _, _ uuid.UUID) (*domain.CustomObjectRecord, error) {
	return f.rec, f.recErr
}
func (f *fakeCustomObjUC) CreateRecord(_ context.Context, _, _ uuid.UUID, _ string, in domain.CreateRecordInput) (*domain.CustomObjectRecord, error) {
	f.createData = in.Data
	return f.rec, nil
}
func (f *fakeCustomObjUC) UpdateRecord(_ context.Context, _ uuid.UUID, _ string, _ uuid.UUID, in domain.UpdateRecordInput) (*domain.CustomObjectRecord, error) {
	f.updateData = in.Data
	return f.rec, nil
}
func (f *fakeCustomObjUC) ListRecords(_ context.Context, _ uuid.UUID, _ string, ff domain.RecordFilter) ([]domain.CustomObjectRecord, int64, error) {
	f.listGot = ff
	return f.listRet, f.listTotal, f.listErr
}
func (f *fakeCustomObjUC) DeleteRecord(_ context.Context, _, _ uuid.UUID) error {
	f.deleted = true
	return nil
}

type recordingSettingsUC struct {
	domain.OrgSettingsUseCase
	validateErr error
	gotEntity   string
	gotFields   domain.JSON
}

func (f *recordingSettingsUC) ValidateCustomFields(_ context.Context, _ uuid.UUID, entityType string, fields domain.JSON) error {
	f.gotEntity = entityType
	f.gotFields = fields
	return f.validateErr
}

func newTestService(custom *fakeCustomObjUC, settings *recordingSettingsUC, deal *fakeDealUC) domain.RecordService {
	if custom == nil {
		custom = &fakeCustomObjUC{}
	}
	if settings == nil {
		settings = &recordingSettingsUC{}
	}
	if deal == nil {
		deal = &fakeDealUC{}
	}
	// Link/tag tests use newLinkTestService; here links are absent (nil repos),
	// which the service treats as "not configured" / skip-cascade. authz nil too:
	// these dispatch/validation tests don't exercise OLS (the dedicated OLS/audit
	// tests in record_service_security_test.go wire a fake authorizer).
	return NewRecordService(custom, settings, &contactAdapterStubUC{}, &companyAdapterStubUC{}, deal, nil, nil, nil)
}

// Minimal contact/company usecase stubs (not exercised by these tests).
type contactAdapterStubUC struct{ domain.ContactUseCase }
type companyAdapterStubUC struct{ domain.CompanyUseCase }

// ============================================================
// Tests
// ============================================================

func TestGet_Deal_ProjectsUniformShape(t *testing.T) {
	stageID := uuid.New()
	closeAt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	deal := &fakeDealUC{ret: &domain.Deal{
		ID:              uuid.New(),
		Title:           "Acme renewal",
		Value:           1500,
		Probability:     40,
		StageID:         &stageID,
		ExpectedCloseAt: &closeAt,
		CustomFields:    domain.JSON(`{"region":"EU"}`),
	}}
	svc := newTestService(nil, nil, deal)

	rec, err := svc.Get(context.Background(), uuid.New(), "deal", uuid.New())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Object != "deal" || rec.Display != "Acme renewal" {
		t.Fatalf("unexpected object/display: %q %q", rec.Object, rec.Display)
	}
	if rec.Fields["title"] != "Acme renewal" {
		t.Errorf("title = %v", rec.Fields["title"])
	}
	if v, ok := rec.Fields["value"].(float64); !ok || v != 1500 {
		t.Errorf("value = %v (%T)", rec.Fields["value"], rec.Fields["value"])
	}
	if rec.Fields["stage"] != stageID.String() {
		t.Errorf("stage = %v, want %s", rec.Fields["stage"], stageID)
	}
	if rec.Fields["expected_close_at"] != "2026-01-02" {
		t.Errorf("expected_close_at = %v", rec.Fields["expected_close_at"])
	}
	if rec.Fields["region"] != "EU" {
		t.Errorf("custom field region not merged: %v", rec.Fields["region"])
	}
}

func TestGet_CustomObject_ProjectsFromData(t *testing.T) {
	defID := uuid.New()
	recID := uuid.New()
	custom := &fakeCustomObjUC{
		def: &domain.CustomObjectDef{ID: defID, Slug: "project"},
		rec: &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, DisplayName: "Apollo", Data: domain.JSON(`{"status":"active"}`)},
	}
	svc := newTestService(custom, nil, nil)

	rec, err := svc.Get(context.Background(), uuid.New(), "project", recID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Object != "project" || rec.Display != "Apollo" {
		t.Fatalf("unexpected: %+v", rec)
	}
	if rec.Fields["status"] != "active" {
		t.Errorf("status = %v", rec.Fields["status"])
	}
}

func TestGet_CustomObject_ResolvesDisplayFromLiveDef(t *testing.T) {
	// R8: the title is computed from the display field's CURRENT value at read time,
	// not the stale display_name captured at write time. Here the def's first text
	// field is "name", the record's data has name="Apollo II", but the stored
	// display_name is an old value — Get must return the live one.
	defID := uuid.New()
	recID := uuid.New()
	custom := &fakeCustomObjUC{
		def: &domain.CustomObjectDef{ID: defID, Slug: "project",
			Fields: domain.JSON(`[{"key":"name","label":"Name","type":"text"}]`)},
		rec: &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, DisplayName: "Old Name", Data: domain.JSON(`{"name":"Apollo II"}`)},
	}
	svc := newTestService(custom, nil, nil)

	rec, err := svc.Get(context.Background(), uuid.New(), "project", recID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Display != "Apollo II" {
		t.Errorf("display = %q, want the live field value 'Apollo II' (R8)", rec.Display)
	}
}

func TestGet_CustomObject_SlugMismatch404(t *testing.T) {
	custom := &fakeCustomObjUC{
		def: &domain.CustomObjectDef{ID: uuid.New(), Slug: "project"},
		// record belongs to a different object def
		rec: &domain.CustomObjectRecord{ID: uuid.New(), ObjectDefID: uuid.New()},
	}
	svc := newTestService(custom, nil, nil)

	_, err := svc.Get(context.Background(), uuid.New(), "project", uuid.New())
	appErr, ok := err.(*domain.AppError)
	if !ok || appErr.Code != 404 {
		t.Fatalf("want 404 AppError, got %v", err)
	}
}

func TestCreate_Deal_MapsFieldsAndValidates(t *testing.T) {
	companyID := uuid.New()
	deal := &fakeDealUC{ret: &domain.Deal{ID: uuid.New(), Title: "Acme"}}
	settings := &recordingSettingsUC{}
	svc := newTestService(nil, settings, deal)

	_, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "deal", domain.RecordWriteInput{
		Fields: map[string]interface{}{
			"title":             "Acme",
			"value":             float64(1000),
			"company":           companyID.String(),
			"expected_close_at": "2026-01-02",
			"region":            "EU", // admin-defined custom field
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if deal.created.Title != "Acme" || deal.created.Value != 1000 {
		t.Errorf("title/value mismatch: %+v", deal.created)
	}
	if deal.created.CompanyID == nil || *deal.created.CompanyID != companyID {
		t.Errorf("company relation not mapped: %v", deal.created.CompanyID)
	}
	if deal.created.ExpectedCloseAt == nil {
		t.Fatalf("expected_close_at not set")
	}
	if _, perr := time.Parse(time.RFC3339, *deal.created.ExpectedCloseAt); perr != nil {
		t.Errorf("expected_close_at not RFC3339: %q", *deal.created.ExpectedCloseAt)
	}
	if deal.created.CustomFields == nil {
		t.Errorf("custom field region dropped")
	}
	// validation ran against the non-native subset only
	if settings.gotEntity != "deal" {
		t.Errorf("validation entity = %q", settings.gotEntity)
	}
	var cf map[string]interface{}
	_ = json.Unmarshal(settings.gotFields, &cf)
	if cf["region"] != "EU" {
		t.Errorf("validated fields = %s", settings.gotFields)
	}
	if _, ok := cf["title"]; ok {
		t.Errorf("native field leaked into custom validation: %s", settings.gotFields)
	}
}

func TestCreate_Deal_ValidationFailureShortCircuits(t *testing.T) {
	deal := &fakeDealUC{ret: &domain.Deal{}}
	settings := &recordingSettingsUC{validateErr: domain.NewAppError(400, "custom_fields.region: bad")}
	svc := newTestService(nil, settings, deal)

	_, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "deal", domain.RecordWriteInput{
		Fields: map[string]interface{}{"title": "Acme", "region": "ZZ"},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if deal.createCalled {
		t.Error("deal usecase should not be called when validation fails")
	}
}

func TestCreate_CustomObject_MarshalsFields(t *testing.T) {
	custom := &fakeCustomObjUC{
		def: &domain.CustomObjectDef{ID: uuid.New(), Slug: "project"},
		rec: &domain.CustomObjectRecord{ID: uuid.New(), DisplayName: "Apollo", Data: domain.JSON(`{"status":"active"}`)},
	}
	svc := newTestService(custom, nil, nil)

	_, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "project", domain.RecordWriteInput{
		Fields: map[string]interface{}{"status": "active"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(custom.createData, &got); err != nil {
		t.Fatalf("createData not valid JSON: %v", err)
	}
	if got["status"] != "active" {
		t.Errorf("createData = %s", custom.createData)
	}
}

func TestList_CustomObject_EncodesOffsetCursor(t *testing.T) {
	custom := &fakeCustomObjUC{
		def:       &domain.CustomObjectDef{ID: uuid.New(), Slug: "project"},
		listRet:   []domain.CustomObjectRecord{{ID: uuid.New(), DisplayName: "A"}, {ID: uuid.New(), DisplayName: "B"}},
		listTotal: 5,
	}
	svc := newTestService(custom, nil, nil)

	page, err := svc.List(context.Background(), uuid.New(), "project", domain.RecordListInput{Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Records) != 2 {
		t.Fatalf("records = %d", len(page.Records))
	}
	if page.NextCursor == "" {
		t.Fatal("expected a next cursor (2 of 5 returned)")
	}
	// Feeding the cursor back asks the usecase for the next offset.
	if _, err := svc.List(context.Background(), uuid.New(), "project", domain.RecordListInput{Limit: 2, Cursor: page.NextCursor}); err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if custom.listGot.Offset != 2 {
		t.Errorf("second page offset = %d, want 2", custom.listGot.Offset)
	}
}

func TestList_CustomObject_NoMoreWhenExhausted(t *testing.T) {
	custom := &fakeCustomObjUC{
		def:       &domain.CustomObjectDef{ID: uuid.New(), Slug: "project"},
		listRet:   []domain.CustomObjectRecord{{ID: uuid.New()}, {ID: uuid.New()}},
		listTotal: 2,
	}
	svc := newTestService(custom, nil, nil)

	page, err := svc.List(context.Background(), uuid.New(), "project", domain.RecordListInput{Limit: 25})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page.NextCursor != "" {
		t.Errorf("expected no next cursor, got %q", page.NextCursor)
	}
}

func TestList_Deal_PassesThroughKeysetCursor(t *testing.T) {
	deal := &fakeDealUC{
		listRet:  []domain.Deal{{ID: uuid.New(), Title: "A"}},
		listNext: "opaque-keyset-cursor",
	}
	svc := newTestService(nil, nil, deal)

	page, err := svc.List(context.Background(), uuid.New(), "deal", domain.RecordListInput{Limit: 10, Q: "ac", Cursor: "incoming"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page.NextCursor != "opaque-keyset-cursor" {
		t.Errorf("cursor passthrough = %q", page.NextCursor)
	}
	if deal.listGot.Cursor != "incoming" || deal.listGot.Q != "ac" || deal.listGot.Limit != 10 {
		t.Errorf("filter not forwarded: %+v", deal.listGot)
	}
}

func TestSystemPrecedenceOverCustomSlug(t *testing.T) {
	// A rogue custom object reusing the "deal" slug must not shadow the system object.
	custom := &fakeCustomObjUC{def: &domain.CustomObjectDef{ID: uuid.New(), Slug: "deal"}}
	deal := &fakeDealUC{ret: &domain.Deal{ID: uuid.New(), Title: "Real deal"}}
	svc := newTestService(custom, nil, deal)

	rec, err := svc.Get(context.Background(), uuid.New(), "deal", uuid.New())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Display != "Real deal" {
		t.Errorf("system object did not take precedence: %q", rec.Display)
	}
}

func TestList_UnknownSlug_PropagatesNotFound(t *testing.T) {
	custom := &fakeCustomObjUC{listErr: domain.NewAppError(404, "custom object not found")}
	svc := newTestService(custom, nil, nil)

	_, err := svc.List(context.Background(), uuid.New(), "nope", domain.RecordListInput{})
	appErr, ok := err.(*domain.AppError)
	if !ok || appErr.Code != 404 {
		t.Fatalf("want 404 AppError, got %v", err)
	}
}

func TestCreate_CustomObject_FiresAutomationEvent(t *testing.T) {
	custom := &fakeCustomObjUC{
		def: &domain.CustomObjectDef{ID: uuid.New(), Slug: "project"},
		rec: &domain.CustomObjectRecord{ID: uuid.New(), DisplayName: "Apollo", Data: domain.JSON(`{"status":"active"}`)},
	}
	svc := newTestService(custom, nil, nil)

	var gotType string
	var gotPayload map[string]any
	done := make(chan struct{})
	svc.SetEventEmitter(
		func(_ context.Context, _ uuid.UUID, eventType string, payload map[string]any) {
			gotType = eventType
			gotPayload = payload
			close(done)
		})

	if _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "project", domain.RecordWriteInput{
		Fields: map[string]interface{}{"status": "active"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automation event was not fired")
	}
	if gotType != "project_created" {
		t.Errorf("event type = %q, want project_created", gotType)
	}
	if gotPayload["entity_id"] == nil {
		t.Errorf("payload missing entity_id: %+v", gotPayload)
	}
	// The record is flattened under its slug key, matching the legacy handler.
	proj, ok := gotPayload["project"].(map[string]any)
	if !ok || proj["status"] != "active" || proj["display_name"] != "Apollo" {
		t.Errorf("payload.project wrong: %+v", gotPayload["project"])
	}
}

func TestCreate_SystemObject_DoesNotFireUniformEvent(t *testing.T) {
	// System objects keep their automation on the legacy pages (plan P7), so the
	// uniform path must not double-emit a differently-shaped event for them.
	deal := &fakeDealUC{ret: &domain.Deal{ID: uuid.New(), Title: "Acme"}}
	svc := newTestService(nil, nil, deal)

	fired := make(chan struct{}, 1)
	svc.SetEventEmitter(
		func(_ context.Context, _ uuid.UUID, _ string, _ map[string]any) { fired <- struct{}{} })

	if _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "deal", domain.RecordWriteInput{
		Fields: map[string]interface{}{"title": "Acme"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	select {
	case <-fired:
		t.Error("system-object create should not fire a uniform automation event")
	case <-time.After(100 * time.Millisecond):
		// expected: no event
	}
}

func TestUpdate_DealStageChange_RoutesThroughChangeStageAndFiresEvent(t *testing.T) {
	oldStage := uuid.New()
	newStage := uuid.New()
	dealID := uuid.New()
	deal := &fakeDealUC{
		getRet: &domain.Deal{ID: dealID, Title: "Acme", StageID: &oldStage},
		ret:    &domain.Deal{ID: dealID, Title: "Acme", StageID: &newStage, IsWon: true},
	}
	svc := newTestService(nil, nil, deal)

	gotType := ""
	var gotPayload map[string]any
	done := make(chan struct{})
	svc.SetEventEmitter(func(_ context.Context, _ uuid.UUID, eventType string, payload map[string]any) {
		gotType = eventType
		gotPayload = payload
		close(done)
	})

	if _, err := svc.Update(context.Background(), uuid.New(), "deal", dealID, domain.RecordWriteInput{
		Fields: map[string]interface{}{"stage": newStage.String()},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if !deal.changeStageCalled {
		t.Fatal("a stage change must route through ChangeStage (won/lost side-effects)")
	}
	if deal.changeStageInput.StageID != newStage {
		t.Errorf("ChangeStage stage = %v, want %v", deal.changeStageInput.StageID, newStage)
	}
	if deal.updateCalled {
		t.Error("a stage-only update must not also call the plain Update path")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deal_stage_changed event was not fired")
	}
	if gotType != "deal_stage_changed" {
		t.Errorf("event type = %q, want deal_stage_changed", gotType)
	}
	if gotPayload["new_stage_id"] != newStage.String() || gotPayload["old_stage_id"] != oldStage.String() {
		t.Errorf("stage ids wrong: old=%v new=%v", gotPayload["old_stage_id"], gotPayload["new_stage_id"])
	}
	// The deal map uses the automation shape (stage_id, is_won), not uniform keys.
	dm, ok := gotPayload["deal"].(map[string]any)
	if !ok || dm["stage_id"] != newStage.String() || dm["is_won"] != true {
		t.Errorf("deal automation map wrong: %+v", gotPayload["deal"])
	}
}

func TestDelete_CustomObject_VerifiesOwnership(t *testing.T) {
	defID := uuid.New()
	recID := uuid.New()
	custom := &fakeCustomObjUC{
		def: &domain.CustomObjectDef{ID: defID, Slug: "project"},
		rec: &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID},
	}
	svc := newTestService(custom, nil, nil)

	if err := svc.Delete(context.Background(), uuid.New(), "project", recID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !custom.deleted {
		t.Error("DeleteRecord not called")
	}
}

func TestUpdate_CustomObject_FiresUpdatedEvent(t *testing.T) {
	defID := uuid.New()
	recID := uuid.New()
	custom := &fakeCustomObjUC{
		def: &domain.CustomObjectDef{ID: defID, Slug: "project"},
		rec: &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, DisplayName: "Apollo", Data: domain.JSON(`{"status":"done"}`)},
	}
	svc := newTestService(custom, nil, nil)

	var gotType string
	var gotPayload map[string]any
	done := make(chan struct{})
	svc.SetEventEmitter(func(_ context.Context, _ uuid.UUID, eventType string, payload map[string]any) {
		gotType = eventType
		gotPayload = payload
		close(done)
	})

	if _, err := svc.Update(context.Background(), uuid.New(), "project", recID, domain.RecordWriteInput{
		Fields: map[string]interface{}{"status": "done"},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// The marshalled fields reached the custom-object usecase.
	var got map[string]interface{}
	if err := json.Unmarshal(custom.updateData, &got); err != nil {
		t.Fatalf("updateData not valid JSON: %v", err)
	}
	if got["status"] != "done" {
		t.Errorf("updateData = %s", custom.updateData)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automation event was not fired")
	}
	if gotType != "project_updated" {
		t.Errorf("event type = %q, want project_updated", gotType)
	}
	proj, ok := gotPayload["project"].(map[string]any)
	if !ok || proj["status"] != "done" || proj["display_name"] != "Apollo" {
		t.Errorf("payload.project wrong: %+v", gotPayload["project"])
	}
}

func TestCreate_Deal_MapsOwnerAsNativeField(t *testing.T) {
	// owner_user_id is a native deal column: it must be threaded into the typed
	// input (parity with the legacy handler, which binds it from the body) and
	// must NOT leak into the custom_fields blob.
	ownerID := uuid.New()
	deal := &fakeDealUC{ret: &domain.Deal{ID: uuid.New(), Title: "Acme"}}
	svc := newTestService(nil, nil, deal)

	if _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "deal", domain.RecordWriteInput{
		Fields: map[string]interface{}{"title": "Acme", "owner_user_id": ownerID.String()},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if deal.created.OwnerUserID == nil || *deal.created.OwnerUserID != ownerID {
		t.Errorf("owner_user_id not mapped to the typed input: %v", deal.created.OwnerUserID)
	}
	if deal.created.CustomFields != nil {
		t.Errorf("owner_user_id leaked into custom_fields: %s", deal.created.CustomFields)
	}
}
