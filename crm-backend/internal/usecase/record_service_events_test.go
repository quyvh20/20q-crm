package usecase

import (
	"context"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// record_service_events_test.go covers the A2 uniform-event closure: every system
// object (contact/company/deal) now fires its own create/update/delete from the
// uniform write path with the automation-shaped payload, and deletes fire for
// custom objects too. Company had no automation wiring at all before A2.
//
// fakeContactUC / fakeCompanyUC / fakeDealUC are declared in the sibling
// record_service_links_test.go / record_service_test.go; here we add the
// write methods those tests didn't need.

func (f *fakeContactUC) Create(_ context.Context, _ uuid.UUID, _ domain.CreateContactInput) (*domain.Contact, error) {
	return f.ret, nil
}
func (f *fakeContactUC) Delete(_ context.Context, _, _ uuid.UUID) error { return nil }

func (f *fakeCompanyUC) Create(_ context.Context, _ uuid.UUID, _ domain.CreateCompanyInput) (*domain.Company, error) {
	return f.ret, nil
}

// awaitEvent captures the next emitted event or fails after a timeout.
func awaitEvent(t *testing.T, svc domain.RecordService, act func() error) (string, map[string]any) {
	t.Helper()
	var gotType string
	var gotPayload map[string]any
	done := make(chan struct{})
	svc.SetEventEmitter(func(_ context.Context, _ uuid.UUID, eventType string, payload map[string]any) {
		gotType = eventType
		gotPayload = payload
		close(done)
	})
	if err := act(); err != nil {
		t.Fatalf("action: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automation event was not fired")
	}
	return gotType, gotPayload
}

func TestCreate_Contact_FiresEventWithAutomationShape(t *testing.T) {
	ownerID := uuid.New()
	companyID := uuid.New()
	contact := &fakeContactUC{ret: &domain.Contact{
		ID:           uuid.New(),
		FirstName:    "Ada",
		LastName:     "Lovelace",
		OwnerUserID:  &ownerID,
		CompanyID:    &companyID,
		CustomFields: domain.JSON(`{"tier":"gold"}`),
	}}
	svc := NewRecordService(&fakeCustomObjUC{}, &recordingSettingsUC{}, contact, &fakeCompanyUC{}, &fakeDealUC{}, nil, nil, nil)

	gotType, payload := awaitEvent(t, svc, func() error {
		_, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "contact", domain.RecordWriteInput{
			Fields: map[string]interface{}{"first_name": "Ada"},
		})
		return err
	})

	if gotType != "contact_created" {
		t.Errorf("event type = %q, want contact_created", gotType)
	}
	cm, ok := payload["contact"].(map[string]any)
	if !ok {
		t.Fatalf("payload.contact missing: %+v", payload)
	}
	// Automation shape mirrors the delivery contactToMap: owner_user_id (not
	// owner_id) and custom fields flattened as custom_fields.<k>.
	if cm["owner_user_id"] != ownerID.String() {
		t.Errorf("owner_user_id = %v, want %s", cm["owner_user_id"], ownerID)
	}
	if cm["company_id"] != companyID.String() {
		t.Errorf("company_id = %v, want %s", cm["company_id"], companyID)
	}
	if cm["custom_fields.tier"] != "gold" {
		t.Errorf("custom field not flattened: %+v", cm)
	}
}

func TestCreate_Company_FiresEvent(t *testing.T) {
	industry := "Software"
	company := &fakeCompanyUC{ret: &domain.Company{
		ID:       uuid.New(),
		Name:     "Acme Inc",
		Industry: &industry,
	}}
	svc := NewRecordService(&fakeCustomObjUC{}, &recordingSettingsUC{}, &fakeContactUC{}, company, &fakeDealUC{}, nil, nil, nil)

	gotType, payload := awaitEvent(t, svc, func() error {
		_, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "company", domain.RecordWriteInput{
			Fields: map[string]interface{}{"name": "Acme Inc"},
		})
		return err
	})

	if gotType != "company_created" {
		t.Errorf("event type = %q, want company_created (company had no automation before A2)", gotType)
	}
	cm, ok := payload["company"].(map[string]any)
	if !ok || cm["name"] != "Acme Inc" || cm["industry"] != "Software" {
		t.Errorf("company automation map wrong: %+v", payload["company"])
	}
}

func TestDelete_Contact_FiresDeletedEvent(t *testing.T) {
	contact := &fakeContactUC{ret: &domain.Contact{ID: uuid.New(), FirstName: "Ada"}}
	svc := NewRecordService(&fakeCustomObjUC{}, &recordingSettingsUC{}, contact, &fakeCompanyUC{}, &fakeDealUC{}, nil, nil, nil)

	gotType, payload := awaitEvent(t, svc, func() error {
		return svc.Delete(context.Background(), uuid.New(), "contact", uuid.New())
	})

	if gotType != "contact_deleted" {
		t.Errorf("event type = %q, want contact_deleted", gotType)
	}
	if payload["contact"] == nil {
		t.Errorf("delete payload should carry the snapshot: %+v", payload)
	}
}

// ── L0: write source + enrollment suppression ────────────────────────────────
//
// All three emitters stamp trigger.source from the context instead of the
// hardcoded "crm_ui" literal they carried before, and translate
// domain.WithAutomationSuppressed into the payload flag the automation engine
// reads. Each emitter gets BOTH cases, deliberately:
//
//   - the suppressed/custom-source case (the new behavior), and
//   - an unsuppressed control asserting the default source AND the ABSENCE of the
//     suppression key.
//
// The control is the load-bearing half. A positive-only assertion cannot detect an
// always-on flag, and an emitter that stamped _suppressed unconditionally would
// silently kill enrollment for every write on that path — leads rotting unenrolled,
// the exact failure this subsystem exists to prevent. Absence-of-key is also what
// pins L0's hard requirement: an ordinary write's payload stays byte-identical to
// what shipped before.
//
// Emitter → covering tests:
//
//	fireLifecycleEvent (_system) → TestEvent_WriteSourceDefaultsToCrmUI (+ variants)
//	fireStageChanged   (_system) → TestEvent_DealStageChanged_*
//	fireEvent          (here)    → TestEvent_CustomObject_*

// triggerSourceOf digs trigger.source out of an emitted payload.
func triggerSourceOf(t *testing.T, payload map[string]any) string {
	t.Helper()
	trig, ok := payload["trigger"].(map[string]any)
	if !ok {
		t.Fatalf("payload.trigger missing: %+v", payload)
	}
	s, _ := trig["source"].(string)
	return s
}

func newContactSvc(t *testing.T) domain.RecordService {
	t.Helper()
	contact := &fakeContactUC{ret: &domain.Contact{ID: uuid.New(), FirstName: "Ada"}}
	return NewRecordService(&fakeCustomObjUC{}, &recordingSettingsUC{}, contact, &fakeCompanyUC{}, &fakeDealUC{}, nil, nil, nil)
}

func createContact(svc domain.RecordService, ctx context.Context) func() error {
	return func() error {
		_, err := svc.Create(ctx, uuid.New(), uuid.New(), "contact", domain.RecordWriteInput{
			Fields: map[string]interface{}{"first_name": "Ada"},
		})
		return err
	}
}

// TestEvent_WriteSourceDefaultsToCrmUI is the regression that gates L0's "zero
// behavior change" claim: every write before L0 hardcoded "crm_ui", so a caller
// that never sets a source must still produce exactly that — otherwise a live
// workflow conditioning on trigger.source changes behavior on deploy.
func TestEvent_WriteSourceDefaultsToCrmUI(t *testing.T) {
	svc := newContactSvc(t)

	_, payload := awaitEvent(t, svc, createContact(svc, context.Background()))

	if got := triggerSourceOf(t, payload); got != domain.DefaultWriteSource {
		t.Errorf("trigger.source = %q, want %q", got, domain.DefaultWriteSource)
	}
	if _, present := payload[domain.AutomationSuppressedPayloadKey]; present {
		t.Errorf("an unsuppressed write must not carry the suppression key at all: %+v", payload)
	}
}

func TestEvent_WriteSourceFromContext(t *testing.T) {
	svc := newContactSvc(t)
	ctx := domain.WithWriteSource(context.Background(), "integration:google_ads")

	_, payload := awaitEvent(t, svc, createContact(svc, ctx))

	if got := triggerSourceOf(t, payload); got != "integration:google_ads" {
		t.Errorf("trigger.source = %q, want integration:google_ads", got)
	}
}

// TestEvent_EmptyWriteSourceKeepsDefault pins WithWriteSource's guard: an empty
// source must not blank the default into "".
func TestEvent_EmptyWriteSourceKeepsDefault(t *testing.T) {
	svc := newContactSvc(t)
	ctx := domain.WithWriteSource(context.Background(), "")

	_, payload := awaitEvent(t, svc, createContact(svc, ctx))

	if got := triggerSourceOf(t, payload); got != domain.DefaultWriteSource {
		t.Errorf("trigger.source = %q, want %q", got, domain.DefaultWriteSource)
	}
}

func TestEvent_SuppressionStampsPayloadFlag(t *testing.T) {
	svc := newContactSvc(t)
	ctx := domain.WithAutomationSuppressed(context.Background())

	_, payload := awaitEvent(t, svc, createContact(svc, ctx))

	if payload[domain.AutomationSuppressedPayloadKey] != true {
		t.Errorf("suppressed write should stamp %s=true: %+v", domain.AutomationSuppressedPayloadKey, payload)
	}
}

// newProjectSvc builds a service whose custom-object writes emit project_created.
func newProjectSvc(t *testing.T) domain.RecordService {
	t.Helper()
	defID := uuid.New()
	custom := &fakeCustomObjUC{
		def: &domain.CustomObjectDef{ID: defID, Slug: "project"},
		rec: &domain.CustomObjectRecord{ID: uuid.New(), ObjectDefID: defID, DisplayName: "Apollo", Data: domain.JSON(`{"status":"active"}`)},
	}
	return NewRecordService(custom, &recordingSettingsUC{}, &fakeContactUC{}, &fakeCompanyUC{}, &fakeDealUC{}, nil, nil, nil)
}

func createProject(svc domain.RecordService, ctx context.Context) func() error {
	return func() error {
		_, err := svc.Create(ctx, uuid.New(), uuid.New(), "project", domain.RecordWriteInput{
			Fields: map[string]interface{}{"status": "active"},
		})
		return err
	}
}

// TestEvent_CustomObject_Suppressed covers the emitter in record_service.go — a
// hardcoded "crm_ui" site in a different file from the other two, used by the
// custom-object write path. Missing it would leave any non-contact target
// reporting the wrong origin.
func TestEvent_CustomObject_Suppressed(t *testing.T) {
	svc := newProjectSvc(t)
	ctx := domain.WithAutomationSuppressed(domain.WithWriteSource(context.Background(), "integration:api"))

	gotType, payload := awaitEvent(t, svc, createProject(svc, ctx))

	if gotType != "project_created" {
		t.Errorf("event type = %q, want project_created", gotType)
	}
	if got := triggerSourceOf(t, payload); got != "integration:api" {
		t.Errorf("custom-object trigger.source = %q, want integration:api", got)
	}
	if payload[domain.AutomationSuppressedPayloadKey] != true {
		t.Errorf("custom-object suppressed write should stamp the flag: %+v", payload)
	}
}

// TestEvent_CustomObject_UnsuppressedControl is the control for the test above:
// without it, an emitter that stamped the flag unconditionally would kill
// enrollment for every custom-object write and the suite would stay green.
func TestEvent_CustomObject_UnsuppressedControl(t *testing.T) {
	svc := newProjectSvc(t)

	_, payload := awaitEvent(t, svc, createProject(svc, context.Background()))

	if got := triggerSourceOf(t, payload); got != domain.DefaultWriteSource {
		t.Errorf("custom-object trigger.source = %q, want %q", got, domain.DefaultWriteSource)
	}
	if _, present := payload[domain.AutomationSuppressedPayloadKey]; present {
		t.Errorf("an unsuppressed custom-object write must not carry the suppression key at all: %+v", payload)
	}
}

// stageMoveSvc builds a deal service whose Update(stage) routes through
// ChangeStage and therefore fires deal_stage_changed via fireStageChanged. A
// stage-ONLY move sets nonStage=false, so exactly one event fires and awaitEvent's
// single close(done) is safe.
func stageMoveSvc(t *testing.T) (domain.RecordService, uuid.UUID, uuid.UUID) {
	t.Helper()
	oldStage, newStage, dealID := uuid.New(), uuid.New(), uuid.New()
	deal := &fakeDealUC{
		getRet: &domain.Deal{ID: dealID, Title: "Acme", StageID: &oldStage},
		ret:    &domain.Deal{ID: dealID, Title: "Acme", StageID: &newStage},
	}
	return newTestService(nil, nil, deal), dealID, newStage
}

func moveStage(svc domain.RecordService, ctx context.Context, dealID, newStage uuid.UUID) func() error {
	return func() error {
		_, err := svc.Update(ctx, uuid.New(), "deal", dealID, domain.RecordWriteInput{
			Fields: map[string]interface{}{"stage": newStage.String()},
		})
		return err
	}
}

// TestEvent_DealStageChanged_Suppressed pins the third emitter, fireStageChanged.
// It is the only one that builds its own payload (extra old_stage_id/new_stage_id
// keys) rather than sharing a construction site, so it is the one most likely to
// drift back to a hardcoded source unnoticed — and deal_stage_changed is the
// highest-volume trigger in a CRM, so a lost suppression stamp here is exactly the
// enrollment storm the flag exists to prevent.
func TestEvent_DealStageChanged_Suppressed(t *testing.T) {
	svc, dealID, newStage := stageMoveSvc(t)
	ctx := domain.WithAutomationSuppressed(domain.WithWriteSource(context.Background(), "integration:google_ads"))

	gotType, payload := awaitEvent(t, svc, moveStage(svc, ctx, dealID, newStage))

	if gotType != "deal_stage_changed" {
		t.Errorf("event type = %q, want deal_stage_changed", gotType)
	}
	if got := triggerSourceOf(t, payload); got != "integration:google_ads" {
		t.Errorf("stage-change trigger.source = %q, want integration:google_ads", got)
	}
	if payload[domain.AutomationSuppressedPayloadKey] != true {
		t.Errorf("suppressed stage move should stamp the flag: %+v", payload)
	}
}

func TestEvent_DealStageChanged_UnsuppressedControl(t *testing.T) {
	svc, dealID, newStage := stageMoveSvc(t)

	_, payload := awaitEvent(t, svc, moveStage(svc, context.Background(), dealID, newStage))

	if got := triggerSourceOf(t, payload); got != domain.DefaultWriteSource {
		t.Errorf("stage-change trigger.source = %q, want %q", got, domain.DefaultWriteSource)
	}
	if _, present := payload[domain.AutomationSuppressedPayloadKey]; present {
		t.Errorf("an unsuppressed stage move must not carry the suppression key at all: %+v", payload)
	}
}

func TestDelete_CustomObject_FiresDeletedEvent(t *testing.T) {
	defID := uuid.New()
	recID := uuid.New()
	custom := &fakeCustomObjUC{
		def: &domain.CustomObjectDef{ID: defID, Slug: "project"},
		rec: &domain.CustomObjectRecord{ID: recID, ObjectDefID: defID, DisplayName: "Apollo", Data: domain.JSON(`{"status":"active"}`)},
	}
	svc := NewRecordService(custom, &recordingSettingsUC{}, &fakeContactUC{}, &fakeCompanyUC{}, &fakeDealUC{}, nil, nil, nil)

	gotType, payload := awaitEvent(t, svc, func() error {
		return svc.Delete(context.Background(), uuid.New(), "project", recID)
	})

	if gotType != "project_deleted" {
		t.Errorf("event type = %q, want project_deleted", gotType)
	}
	proj, ok := payload["project"].(map[string]any)
	if !ok || proj["status"] != "active" {
		t.Errorf("deleted snapshot wrong: %+v", payload["project"])
	}
}
