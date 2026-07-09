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
