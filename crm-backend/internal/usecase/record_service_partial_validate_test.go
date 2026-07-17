package usecase

import (
	"context"
	"encoding/json"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// A partial update must be validated against the record's RESULTING state, not
// against the delta the request happens to carry.
//
// This was a live bug, reproduced against a real stack before the fix: an org with
// a required custom field `industry` could not save a change to `notes` —
// "custom_fields.industry is required" — even though industry was stored on the
// row. fieldvalidate's required-check ranges over the ORG'S DEFINITIONS, so any
// required field absent from the delta reads as missing. The custom-object path
// has merged-then-validated since it shipped; the system-object path never did.

// GetFieldDefs on the recording fake — the partial-write path asks for the org's
// defs to type-check per key. Returning none makes it a no-op, which is what the
// presence-relaxation tests want: they assert the STRICT validator is not consulted.
func (f *recordingSettingsUC) GetFieldDefs(_ context.Context, _ uuid.UUID, _ string) ([]domain.CustomFieldDef, error) {
	return nil, nil
}

// Update on the contact fake — the sibling fakes declare Create/Delete/GetByID;
// this is the first test in the package to exercise the contact UPDATE path.
func (f *fakeContactUC) Update(_ context.Context, _, _ uuid.UUID, _ domain.UpdateContactInput) (*domain.Contact, error) {
	return f.ret, nil
}

// contactWithCustom builds a service whose stored contact carries `stored` in its
// custom_fields blob, and returns the recorder that captures what the validator saw.
func contactWithCustom(t *testing.T, stored map[string]any) (domain.RecordService, *recordingSettingsUC, uuid.UUID) {
	t.Helper()
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal stored: %v", err)
	}
	id := uuid.New()
	contact := &fakeContactUC{ret: &domain.Contact{
		ID:           id,
		FirstName:    "Ada",
		CustomFields: domain.JSON(raw),
	}}
	settings := &recordingSettingsUC{}
	svc := NewRecordService(&fakeCustomObjUC{}, settings, contact, &fakeCompanyUC{}, &fakeDealUC{}, nil, nil, nil)
	return svc, settings, id
}

// validatedKeys decodes what the validator was actually handed.
func validatedKeys(t *testing.T, r *recordingSettingsUC) map[string]any {
	t.Helper()
	if len(r.gotFields) == 0 {
		return nil
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(r.gotFields), &got); err != nil {
		t.Fatalf("unmarshal validated fields: %v", err)
	}
	return got
}

// TestUpdate_PartialCustomFields_ValidatesMergedState is the regression. The
// validator must see the stored field the caller never mentioned — otherwise it
// reports a required field as missing while it sits on the row.
func TestUpdate_PartialCustomFields_ValidatesMergedState(t *testing.T) {
	svc, settings, id := contactWithCustom(t, map[string]any{"industry": "Software"})

	_, err := svc.Update(context.Background(), uuid.New(), "contact", id, domain.RecordWriteInput{
		Fields: map[string]interface{}{"notes": "just a note"},
	})
	if err != nil {
		t.Fatalf("a partial custom-field edit must not fail: %v", err)
	}

	got := validatedKeys(t, settings)
	if got["industry"] != "Software" {
		t.Errorf("validator must see the STORED industry (the required-check ranges over org defs, not the payload); got %+v", got)
	}
	if got["notes"] != "just a note" {
		t.Errorf("validator must see the incoming delta too; got %+v", got)
	}
}

// TestUpdate_PartialCustomFields_DeltaWins pins that the merge is an overlay, not
// a union — an edit to an existing key must be validated as its NEW value, or a
// type change would be checked against the old one and slip through.
func TestUpdate_PartialCustomFields_DeltaWins(t *testing.T) {
	svc, settings, id := contactWithCustom(t, map[string]any{"industry": "Software"})

	_, err := svc.Update(context.Background(), uuid.New(), "contact", id, domain.RecordWriteInput{
		Fields: map[string]interface{}{"industry": "Hardware"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := validatedKeys(t, settings); got["industry"] != "Hardware" {
		t.Errorf("the delta must win over the stored value; got %+v", got)
	}
}

// TestUpdate_NativeOnlyEdit_SkipsCustomValidation pins the existing early-return:
// an edit touching no custom keys must not drag the whole definition set into a
// validation it has no business running.
func TestUpdate_NativeOnlyEdit_SkipsCustomValidation(t *testing.T) {
	svc, settings, id := contactWithCustom(t, map[string]any{"industry": "Software"})

	_, err := svc.Update(context.Background(), uuid.New(), "contact", id, domain.RecordWriteInput{
		Fields: map[string]interface{}{"first_name": "Ada"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if settings.gotFields != nil {
		t.Errorf("a native-only edit must not invoke custom-field validation; validator saw %s", settings.gotFields)
	}
}

// TestUpdate_MergeDoesNotLeakNativeKeys guards the merge base: prior.Fields carries
// native columns AND flattened custom fields, so a careless merge would hand
// first_name/email to the custom-field validator, which would then reject them as
// unknown-typed or (worse) accept and confuse the required-check.
func TestUpdate_MergeDoesNotLeakNativeKeys(t *testing.T) {
	svc, settings, id := contactWithCustom(t, map[string]any{"industry": "Software"})

	_, err := svc.Update(context.Background(), uuid.New(), "contact", id, domain.RecordWriteInput{
		Fields: map[string]interface{}{"notes": "x"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := validatedKeys(t, settings)
	for _, native := range []string{"first_name", "last_name", "email", "phone", "company", "owner_user_id"} {
		if _, leaked := got[native]; leaked {
			t.Errorf("native key %q must never reach the custom-field validator; got %+v", native, got)
		}
	}
}

// TestUpdate_ValidationErrorStillPropagates pins that the fix removes a FALSE
// positive without removing the true one: a genuinely invalid value still fails.
func TestUpdate_ValidationErrorStillPropagates(t *testing.T) {
	svc, settings, id := contactWithCustom(t, map[string]any{"industry": "Software"})
	settings.validateErr = domain.NewAppError(400, "custom_fields.industry is required")

	_, err := svc.Update(context.Background(), uuid.New(), "contact", id, domain.RecordWriteInput{
		Fields: map[string]interface{}{"industry": ""},
	})
	if err == nil {
		t.Fatal("a real validation failure must still reject the write")
	}
}

// TestCreate_StillEnforcesRequiredAcrossDefs pins that CREATE keeps the strict
// path: a create has no prior state, so checking required across the definition
// set is exactly right there — the fix must not leak leniency into it.
func TestCreate_StillEnforcesRequiredAcrossDefs(t *testing.T) {
	contact := &fakeContactUC{ret: &domain.Contact{ID: uuid.New(), FirstName: "Ada"}}
	settings := &recordingSettingsUC{validateErr: domain.NewAppError(400, "custom_fields.industry is required")}
	svc := NewRecordService(&fakeCustomObjUC{}, settings, contact, &fakeCompanyUC{}, &fakeDealUC{}, nil, nil, nil)

	_, err := svc.Create(context.Background(), uuid.New(), uuid.New(), "contact", domain.RecordWriteInput{
		Fields: map[string]interface{}{"first_name": "Ada", "notes": "x"},
	})
	if err == nil {
		t.Fatal("create must still enforce required custom fields across the definition set")
	}
}

// ── Partial writes (domain.WithPartialWrite) ────────────────────────────────
//
// An inbound lead is not a form submission. An admin marking a custom field
// "required" means "a human filling this in must provide it" — they cannot mean
// "silently reject leads that lack it", which is what the required-check does,
// because it ranges over the org's DEFINITIONS rather than the payload.

// TestPartialWrite_CreateSkipsRequiredPresence pins the create path: a lead that
// lacks the org's required custom field must still land.
func TestPartialWrite_CreateSkipsRequiredPresence(t *testing.T) {
	contact := &fakeContactUC{ret: &domain.Contact{ID: uuid.New(), FirstName: "Ada"}}
	// The strict path would reject this write; the partial path must not consult it.
	settings := &recordingSettingsUC{validateErr: domain.NewAppError(400, "custom_fields.industry is required")}
	svc := NewRecordService(&fakeCustomObjUC{}, settings, contact, &fakeCompanyUC{}, &fakeDealUC{}, nil, nil, nil)

	ctx := domain.WithPartialWrite(context.Background())
	_, err := svc.Create(ctx, uuid.New(), uuid.New(), "contact", domain.RecordWriteInput{
		Fields: map[string]interface{}{"first_name": "Ada", "lead_source": "integration:api"},
	})
	if err != nil {
		t.Fatalf("a partial write must not be rejected for a missing required field: %v", err)
	}
}

// TestPartialWrite_UpdateSkipsRequiredPresence is the regression for a bug found
// by running it live: the partial-write branch existed on the create path but not
// on the update path, so an ingested lead updating an existing contact still 400'd
// with "custom_fields.industry is required". The merged view cannot rescue it —
// merging supplies fields the RECORD has, and this record never had one.
func TestPartialWrite_UpdateSkipsRequiredPresence(t *testing.T) {
	svc, settings, id := contactWithCustom(t, map[string]any{"utm_source": "google"})
	settings.validateErr = domain.NewAppError(400, "custom_fields.industry is required")

	ctx := domain.WithPartialWrite(context.Background())
	_, err := svc.Update(ctx, uuid.New(), "contact", id, domain.RecordWriteInput{
		Fields: map[string]interface{}{"utm_source": "newsletter"},
	})
	if err != nil {
		t.Fatalf("a partial update must not be rejected for a missing required field: %v", err)
	}
}

// TestPartialWrite_StillTypeChecks pins that this relaxes PRESENCE only. A wrong
// value is still wrong — otherwise the flag would be a validation bypass rather
// than a statement about what kind of write this is.
func TestPartialWrite_StillTypeChecks(t *testing.T) {
	contact := &fakeContactUC{ret: &domain.Contact{ID: uuid.New(), FirstName: "Ada"}}
	settings := &fieldDefSettingsUC{defs: []domain.CustomFieldDef{
		{Key: "tier", Type: "select", EntityType: "contact", Options: []string{"gold", "silver"}},
	}}
	svc := NewRecordService(&fakeCustomObjUC{}, settings, contact, &fakeCompanyUC{}, &fakeDealUC{}, nil, nil, nil)

	ctx := domain.WithPartialWrite(context.Background())
	_, err := svc.Create(ctx, uuid.New(), uuid.New(), "contact", domain.RecordWriteInput{
		Fields: map[string]interface{}{"first_name": "Ada", "tier": "platinum"}, // not an option
	})
	if err == nil {
		t.Fatal("a partial write must still reject a value that violates its field's type")
	}
}

// fieldDefSettingsUC serves real defs so the per-key validator has something to
// check against (the recording fake returns none, which short-circuits).
type fieldDefSettingsUC struct {
	domain.OrgSettingsUseCase
	defs []domain.CustomFieldDef
}

func (f *fieldDefSettingsUC) GetFieldDefs(_ context.Context, _ uuid.UUID, _ string) ([]domain.CustomFieldDef, error) {
	return f.defs, nil
}
func (f *fieldDefSettingsUC) ValidateCustomFields(_ context.Context, _ uuid.UUID, _ string, _ domain.JSON) error {
	return nil
}
