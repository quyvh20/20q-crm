package integrations

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// The wire fixtures below are Google's OWN documented payloads, kept verbatim —
// including the Samples page's "Google_key" capitalization (capital G), which
// differs from the production sample's "google_key" on the SAME docs site. That
// inconsistency is the reason the envelope decodes through encoding/json (whose
// field matching falls back to case-insensitive): an exact-case lookup would 401
// every advertiser's "Send test data" click and the integration would be dead on
// arrival while all our tests passed.
const googleTestSampleJSON = `{
  "lead_id": "TeSter-123-ID",
  "user_column_data": [
    {"column_name": "Full Name", "string_value": "FirstName LastName", "column_id": "FULL_NAME"},
    {"column_name": "User Phone", "string_value": "1-650-555-0123", "column_id": "PHONE_NUMBER"},
    {"column_name": "User Email", "string_value": "test@example.com", "column_id": "EMAIL"}
  ],
  "api_version": "1.0",
  "form_id": 0,
  "campaign_id": 0,
  "Google_key": "xfdgdgsgfchgvhgfchg",
  "is_test": true,
  "gcl_id": "",
  "adgroup_id": 0,
  "creative_id": 0
}`

const googleProdSampleJSON = `{
  "lead_id": "CiQADm3ZceLBnPn5Rr_dKMu8DE",
  "user_column_data": [
    {"column_name": "First Name", "string_value": "Ada", "column_id": "FIRST_NAME"},
    {"column_name": "Last Name", "string_value": "Lovelace", "column_id": "LAST_NAME"},
    {"column_name": "User Email", "string_value": "ada@example.com", "column_id": "EMAIL"},
    {"column_name": "What is your budget?", "string_value": "10k-50k", "column_id": "budget_question_id"}
  ],
  "api_version": "1.0",
  "form_id": 123456789,
  "campaign_id": 543212345,
  "google_key": "xfdgdgsgfchgvhgfchg",
  "is_test": false,
  "gcl_id": "Cj0KCQjw-example",
  "adgroup_id": 20000000000,
  "creative_id": 30000000000
}`

func TestGooglePayload_DecodesBothDocumentedKeyCasings(t *testing.T) {
	var testP googlePayload
	if err := json.Unmarshal([]byte(googleTestSampleJSON), &testP); err != nil {
		t.Fatal(err)
	}
	if testP.GoogleKey != "xfdgdgsgfchgvhgfchg" {
		t.Fatalf("the Samples page spells it Google_key and the decode must still find it, got %q", testP.GoogleKey)
	}
	if !testP.IsTest {
		t.Fatal("is_test lost")
	}
	if testP.LeadID != "TeSter-123-ID" {
		t.Fatalf("lead_id = %q", testP.LeadID)
	}

	var prodP googlePayload
	if err := json.Unmarshal([]byte(googleProdSampleJSON), &prodP); err != nil {
		t.Fatal(err)
	}
	if prodP.GoogleKey != "xfdgdgsgfchgvhgfchg" || prodP.IsTest {
		t.Fatalf("production sample decode: key=%q is_test=%v", prodP.GoogleKey, prodP.IsTest)
	}
	if prodP.CampaignID != 543212345 || prodP.GclID == "" {
		t.Fatalf("ad metadata lost: campaign=%d gcl=%q", prodP.CampaignID, prodP.GclID)
	}
}

// Unknown fields must never fail the decode — Google documents that parsers which
// reject unknown fields will break as the payload evolves.
func TestGooglePayload_IgnoresUnknownFields(t *testing.T) {
	var p googlePayload
	err := json.Unmarshal([]byte(`{"lead_id":"x","some_future_field":{"nested":true},"lead_source":"LEAD_FORM"}`), &p)
	if err != nil {
		t.Fatalf("an unknown field must not break the parse: %v", err)
	}
}

func TestFlattenGoogleColumns(t *testing.T) {
	var p googlePayload
	if err := json.Unmarshal([]byte(googleProdSampleJSON), &p); err != nil {
		t.Fatal(err)
	}
	fields := flattenGoogleColumns(p.UserColumnData)

	// column_id is the stable key; column_name is documented-deprecated.
	if fields["EMAIL"] != "ada@example.com" || fields["FIRST_NAME"] != "Ada" {
		t.Fatalf("standard columns must key by column_id: %v", fields)
	}
	// A custom question rides its own id — unmapped, it quarantines and becomes an
	// observed key the admin can one-click map. That is the L2 flow, not a gap.
	if fields["budget_question_id"] != "10k-50k" {
		t.Fatalf("custom question lost: %v", fields)
	}

	// column_name is the fallback when column_id is absent (older payloads).
	fields = flattenGoogleColumns([]googleColumn{{ColumnName: "Legacy Question", Value: "yes"}})
	if fields["Legacy Question"] != "yes" {
		t.Fatalf("column_name fallback: %v", fields)
	}
	// A column with neither name nor id has no addressable key; dropping it beats
	// inventing one.
	if got := flattenGoogleColumns([]googleColumn{{Value: "orphan"}}); len(got) != 0 {
		t.Fatalf("keyless column must drop: %v", got)
	}
}

// Zero-valued ids are ABSENT, not "0": Google's test posts send 0 for ids the test
// cannot know, and a stored "0" would read as a real id in the delivery log.
func TestGoogleLeadContext_ZeroIsAbsent(t *testing.T) {
	var testP googlePayload
	_ = json.Unmarshal([]byte(googleTestSampleJSON), &testP)
	ctx := googleLeadContext(&testP)
	if len(ctx) != 0 {
		t.Fatalf("a test payload's zeroed metadata must vanish, got %v", ctx)
	}

	var prodP googlePayload
	_ = json.Unmarshal([]byte(googleProdSampleJSON), &prodP)
	ctx = googleLeadContext(&prodP)
	if ctx["campaign_id"] != "543212345" || ctx["gcl_id"] != "Cj0KCQjw-example" || ctx["form_id"] != "123456789" {
		t.Fatalf("production metadata must persist: %v", ctx)
	}
}

// gcl_id must land in the seeded gclid attribution field — Google sends it as a
// discrete value, not inside a page_url for the query parser to find.
func TestAttributionValues_DirectGclID(t *testing.T) {
	src := &LeadSource{Kind: KindGoogleAds, Name: "Spring Campaign"}
	vals := attributionValues(src, parseLeadContext(map[string]any{"gcl_id": "Cj0Kabc"}))
	if vals["gclid"] != "Cj0Kabc" {
		t.Fatalf("a direct gcl_id must stamp gclid, got %v", vals)
	}
	if vals["lead_source"] != "integration:google_ads" {
		t.Fatalf("lead_source = %q", vals["lead_source"])
	}
}

// The seed map must survive the same validation an admin-saved map gets — a seed
// that ValidateFieldMap rejects would fail every google_ads source creation.
func TestGoogleSeedFieldMap_ValidatesAgainstContactSchema(t *testing.T) {
	allow := buildTestAllowlist(t)
	if problems := ValidateFieldMap(googleSeedFieldMap(), allow); len(problems) > 0 {
		t.Fatalf("the seed map must validate clean: %v", problems)
	}
}

// The seed exercises the real mapping on Google's own documented test payload:
// FULL_NAME splits, EMAIL maps, PHONE_NUMBER maps (and is then stripped by the
// provider-test coercion — asserted separately below).
func TestGoogleSeedFieldMap_AppliesToDocumentedTestPayload(t *testing.T) {
	var p googlePayload
	_ = json.Unmarshal([]byte(googleTestSampleJSON), &p)
	mapped, failures := googleSeedFieldMap().Apply(flattenGoogleColumns(p.UserColumnData))
	if len(failures) != 0 {
		t.Fatalf("the documented test payload must map clean: %v", failures)
	}
	if mapped["first_name"] != "FirstName" || mapped["last_name"] != "LastName" {
		t.Fatalf("FULL_NAME must split: %v", mapped)
	}
	if mapped["email"] != "test@example.com" || mapped["phone"] != "1-650-555-0123" {
		t.Fatalf("identity columns must map: %v", mapped)
	}
}

// ── Provider-test identity coercion ──────────────────────────────────────────

func TestCoerceProviderTestIdentity(t *testing.T) {
	src := &LeadSource{ID: uuid.New()}

	t.Run("provider test: email coerced, phone stripped", func(t *testing.T) {
		fields := map[string]any{"email": "test@example.com", "phone": "1-650-555-0123", "first_name": "FirstName"}
		coerceProviderTestIdentity(src, TestOriginProvider, fields)
		if fields["email"] != testLeadEmail(src) {
			// Without coercion the advertiser's SECOND test click hard-fails: the
			// sample email collides with the unique index and the provenance guard
			// refuses the winner.
			t.Fatalf("identity must be the synthetic address, got %v", fields["email"])
		}
		if _, still := fields["phone"]; still {
			// Google's dummy number on a phone-matching source's test contact would
			// be a standing match target for any later lead with the same digits.
			t.Fatal("phone must be stripped")
		}
		if fields["first_name"] != "FirstName" {
			t.Fatal("non-identity fields must survive — they are what the mapping test proves")
		}
	})

	t.Run("admin test and real leads: untouched", func(t *testing.T) {
		for _, origin := range []string{TestOriginAdmin, TestOriginNone} {
			fields := map[string]any{"email": "someone@customer.com", "phone": "123"}
			coerceProviderTestIdentity(src, origin, fields)
			if fields["email"] != "someone@customer.com" || fields["phone"] != "123" {
				t.Fatalf("origin %q must not be coerced: %v", origin, fields)
			}
		}
	})
}

// ── Phone-match identity conflict (the amendment shipped with the seed) ──────

// A single phone match is a merge ONLY when identities agree. The lead's email
// already failed to match anything; if the phone-matched contact holds a
// DIFFERENT email, these are two people sharing a handset, and merging would
// silently absorb a new person into a stranger's record.
func TestFindMatch_PhoneHitWithConflictingEmailRefusesToMerge(t *testing.T) {
	contactWith := func(email string) domain.Contact {
		c := domain.Contact{ID: uuid.New(), FirstName: "Existing"}
		if email != "" {
			c.Email = &email
		}
		return c
	}
	src := testSource(t, "", `["email","phone"]`)
	lead := map[string]any{"phone": "5551234567"}

	svc := &LeadIngestService{matcher: &stubMatcher{byPhone: []domain.Contact{contactWith("owner@company.com")}}}
	res, err := svc.findMatch(context.Background(), src, lead, "newperson@elsewhere.com")
	if err != nil {
		t.Fatal(err)
	}
	if res.Contact != nil {
		t.Fatal("a conflicting identity must refuse the merge — a visible duplicate beats a silent wrong merge")
	}
	if !res.Ambiguous || res.AmbiguityNote == "" {
		t.Fatalf("the refusal must be disclosed on the delivery: %+v", res)
	}

	// The enrichment case still merges: the matched contact has no email yet, so
	// filling it in is the phone-only shape this matching exists for.
	svc = &LeadIngestService{matcher: &stubMatcher{byPhone: []domain.Contact{contactWith("")}}}
	res, err = svc.findMatch(context.Background(), src, lead, "newperson@elsewhere.com")
	if err != nil {
		t.Fatal(err)
	}
	if res.Contact == nil || res.MatchedOn != MatchPhone {
		t.Fatalf("an email-less phone match is enrichment and must merge: %+v", res)
	}

	// Same email on both sides is the same person — merge.
	svc = &LeadIngestService{matcher: &stubMatcher{byPhone: []domain.Contact{contactWith("newperson@elsewhere.com")}}}
	res, err = svc.findMatch(context.Background(), src, lead, "newperson@elsewhere.com")
	if err != nil {
		t.Fatal(err)
	}
	if res.Contact == nil {
		t.Fatalf("an agreeing identity must merge: %+v", res)
	}

	// An email-LESS lead (the phone-only form) merges on the phone hit regardless
	// of the contact's own email — there is no identity to conflict.
	svc = &LeadIngestService{matcher: &stubMatcher{byPhone: []domain.Contact{contactWith("owner@company.com")}}}
	res, err = svc.findMatch(context.Background(), src, lead, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Contact == nil {
		t.Fatalf("a phone-only lead has no conflicting identity and must merge: %+v", res)
	}
}

// ── Error-shape helpers ──────────────────────────────────────────────────────

func TestGoogleKeyMismatchErrorNeverEchoesInput(t *testing.T) {
	// The mismatch ledger text is a fixed constant. If someone "improves" it to
	// include the received key, an attacker's probe value lands in a field admins
	// read. The constant being referenced here is the whole assertion.
	if strings.Contains(googleKeyMismatchError, "%") {
		t.Fatal("the mismatch error must be a fixed string, never a format template")
	}
}

// The pre-auth ledger row stores the ENVELOPE shape, key redacted. Flattened
// attacker-chosen column names would seed the admin's mapping picker (observed
// keys samples raw_payload's top level) from outside the credential.
func TestRedactedEnvelope(t *testing.T) {
	var p googlePayload
	_ = json.Unmarshal([]byte(googleProdSampleJSON), &p)
	env := redactedEnvelope(&p)
	if env["google_key"] != "(redacted)" {
		t.Fatalf("the received key must never be stored: %v", env["google_key"])
	}
	if _, top := env["budget_question_id"]; top {
		t.Fatal("column keys must stay NESTED on a pre-auth row — top-level keys feed the mapping picker")
	}
	raw, _ := json.Marshal(env)
	if strings.Contains(string(raw), "xfdgdgsgfchgvhgfchg") {
		t.Fatal("the received key leaked into the stored envelope")
	}
}
