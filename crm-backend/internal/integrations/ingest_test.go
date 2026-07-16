package integrations

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// fakeSchema serves a contact-shaped descriptor: the four writable system fields,
// a relation, and a custom field — the exact mix the allowlist must sort out.
type fakeSchema struct{ desc *domain.ObjectDescriptor }

func (f *fakeSchema) GetSchema(_ context.Context, _ uuid.UUID, _ string) (*domain.ObjectDescriptor, error) {
	return f.desc, nil
}

func contactSchema() *fakeSchema {
	return &fakeSchema{desc: &domain.ObjectDescriptor{
		Slug: "contact",
		Fields: []domain.FieldDescriptor{
			{Key: "first_name", Type: "text", IsSystem: true},
			{Key: "last_name", Type: "text", IsSystem: true},
			{Key: "email", Type: "text", IsSystem: true},
			{Key: "phone", Type: "text", IsSystem: true},
			{Key: "company", Type: "relation", TargetSlug: "company", IsSystem: true},
			{Key: "tier", Type: "select", IsSystem: false}, // an org custom field
		},
	}}
}

func buildTestAllowlist(t *testing.T) *Allowlist {
	t.Helper()
	a, err := BuildAllowlist(context.Background(), contactSchema(), uuid.New(), "contact")
	if err != nil {
		t.Fatalf("BuildAllowlist: %v", err)
	}
	return a
}

// TestAllowlist_PermitsOnlyWritableSystemFields pins what a stranger may write.
// This is the whole security control: ingestion writes callerless, so OLS returns
// nil and the FLS mask is empty — RecordService writes whatever it is handed, and
// nothing below this package rejects anything.
func TestAllowlist_PermitsOnlyWritableSystemFields(t *testing.T) {
	a := buildTestAllowlist(t)
	want := map[string]bool{"first_name": true, "last_name": true, "email": true, "phone": true}
	got := map[string]bool{}
	for _, k := range a.Keys() {
		got[k] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("%q should be writable", k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("%q must NOT be writable", k)
		}
	}
}

func TestAllowlist_Apply(t *testing.T) {
	a := buildTestAllowlist(t)

	t.Run("ownership and relations are quarantined, never written", func(t *testing.T) {
		// owner_user_id would let a caller steal or strip ownership (the adapter
		// reads a present-null as UNASSIGN); company would 400 the whole lead,
		// since the adapter runs it through uuidField and a lead carries a NAME.
		allowed, quarantined := a.Apply(map[string]any{
			"email":         "a@b.com",
			"owner_user_id": uuid.New().String(),
			"company":       "Acme Inc",
		})
		if _, ok := allowed["owner_user_id"]; ok {
			t.Error("owner_user_id must never be writable from a payload")
		}
		if _, ok := allowed["company"]; ok {
			t.Error("company (a relation) must never be writable from a payload")
		}
		if _, ok := quarantined["owner_user_id"]; !ok {
			t.Error("owner_user_id should be quarantined (recorded), not dropped")
		}
		if _, ok := quarantined["company"]; !ok {
			t.Error("company should be quarantined so its value stays visible")
		}
		if allowed["email"] != "a@b.com" {
			t.Error("a legitimate field must still pass")
		}
	})

	t.Run("unknown keys are quarantined, not written", func(t *testing.T) {
		// Without this, they land in contacts.custom_fields with a 200:
		// fieldvalidate explicitly allows unknown keys and customFieldsJSON sweeps
		// every non-native key into the blob.
		_, quarantined := a.Apply(map[string]any{"email": "a@b.com", "ssn": "123-45-6789"})
		if _, ok := quarantined["ssn"]; !ok {
			t.Error("an unknown key must be quarantined")
		}
	})

	t.Run("custom fields are quarantined in L1", func(t *testing.T) {
		// Deliberate scope cut: one custom key makes RecordService demand EVERY
		// required custom field in the org, 400ing the lead.
		allowed, quarantined := a.Apply(map[string]any{"email": "a@b.com", "tier": "gold"})
		if _, ok := allowed["tier"]; ok {
			t.Error("custom fields are L2; they must not reach the write in L1")
		}
		if _, ok := quarantined["tier"]; !ok {
			t.Error("a custom field must be recorded, not dropped — L2 will map it")
		}
	})

	t.Run("empty strings are quarantined because they OVERWRITE", func(t *testing.T) {
		// "" is not "absent" to the adapter: strPtr returns &"" and the usecase
		// assigns it, so {"last_name":""} would blank a real name.
		allowed, _ := a.Apply(map[string]any{"email": "a@b.com", "last_name": "   "})
		if _, ok := allowed["last_name"]; ok {
			t.Error("a blank value must not reach the write")
		}
	})
}

func TestNormalizeEmail(t *testing.T) {
	// Normalizing before the write is what makes the case-insensitive match and the
	// case-SENSITIVE unique index agree — without it, "John@X.com" and "john@x.com"
	// both insert, no 23505 fires, and the upsert loop never engages.
	for _, tc := range []struct{ in, want string }{
		{"  Bob@Example.COM ", "bob@example.com"},
		{"already@lower.com", "already@lower.com"},
		{"   ", ""},
	} {
		if got := normalizeEmail(tc.in); got != tc.want {
			t.Errorf("normalizeEmail(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSynthesizeFirstName(t *testing.T) {
	// The adapter 400s a blank (trimmed) first_name, so an email-only lead — the
	// common shape from ad platforms — needs one or it is rejected outright.
	for _, tc := range []struct{ email, want string }{
		{"ada@lovelace.dev", "ada"},
		{"", "Lead"},
		{"@nolocal.com", "Lead"},
	} {
		if got := synthesizeFirstName(tc.email); got != tc.want {
			t.Errorf("synthesizeFirstName(%q) = %q, want %q", tc.email, got, tc.want)
		}
	}
	if got := synthesizeFirstName("  "); got == "" {
		t.Error("synthesis must never return a blank — the adapter rejects it")
	}
}

func TestUpdateFields_Policies(t *testing.T) {
	existingLast := "Lovelace"
	existing := &domain.Contact{FirstName: "Ada", LastName: existingLast}
	incoming := map[string]any{"first_name": "A.", "last_name": "Byron", "phone": "+15550100"}
	svc := &LeadIngestService{}

	t.Run("fill_blank_only protects human work", func(t *testing.T) {
		// A rep fixes a name; the lead resubmits the form. Under overwrite the fix
		// is silently gone — so the safe policy is the default.
		out := svc.updateFields(&LeadSource{UpdatePolicy: UpdatePolicyFillBlankOnly}, incoming, existing)
		if _, ok := out["first_name"]; ok {
			t.Error("must not overwrite a populated first_name")
		}
		if _, ok := out["last_name"]; ok {
			t.Error("must not overwrite a populated last_name")
		}
		if out["phone"] != "+15550100" {
			t.Error("must fill a blank phone")
		}
	})

	t.Run("overwrite takes everything", func(t *testing.T) {
		out := svc.updateFields(&LeadSource{UpdatePolicy: UpdatePolicyOverwrite}, incoming, existing)
		if out["first_name"] != "A." || out["last_name"] != "Byron" {
			t.Errorf("overwrite should replace populated fields: %+v", out)
		}
	})

	t.Run("ownership can never ride in on an update", func(t *testing.T) {
		// Belt and braces with the allowlist: a present owner_user_id reassigns, and
		// a null one UNASSIGNS — stripping the rep off the record with a 200.
		out := svc.updateFields(&LeadSource{UpdatePolicy: UpdatePolicyOverwrite},
			map[string]any{"owner_user_id": uuid.New().String(), "company": "Acme"}, existing)
		if _, ok := out["owner_user_id"]; ok {
			t.Error("owner_user_id must never be updatable from a payload")
		}
		if _, ok := out["company"]; ok {
			t.Error("company must never be updatable from a payload")
		}
	})
}

// TestSynthesizedNameNeverReachesUpdate is the structural guard for the highest-
// impact bug the plan review caught: synthesis in the shared mapping step would
// have made every email-only re-submission overwrite the contact's real first_name
// with the email local-part. Synthesis lives in create() only; updateFields sends
// exactly what it was handed.
func TestSynthesizedNameNeverReachesUpdate(t *testing.T) {
	svc := &LeadIngestService{}
	existing := &domain.Contact{FirstName: "Ada"}
	// An email-only lead: no first_name at all.
	out := svc.updateFields(&LeadSource{UpdatePolicy: UpdatePolicyOverwrite},
		map[string]any{"email": "ada@lovelace.dev"}, existing)
	if _, ok := out["first_name"]; ok {
		t.Fatal("update must never carry a first_name the lead did not send")
	}
}
