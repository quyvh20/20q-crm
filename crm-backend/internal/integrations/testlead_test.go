package integrations

import (
	"context"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// The test-lead flow's safety property is that a made-up lead can never touch a real
// contact. These tests exist to make that property expensive to delete.

func testSource(t *testing.T, fieldMap string, matchFields string) *LeadSource {
	t.Helper()
	if fieldMap == "" {
		fieldMap = "{}"
	}
	if matchFields == "" {
		matchFields = `["email"]`
	}
	return &LeadSource{
		ID:           uuid.New(),
		OrgID:        uuid.New(),
		Kind:         KindAPI,
		Name:         "Test Source",
		TargetSlug:   "contact",
		UpdatePolicy: UpdatePolicyFillBlankOnly,
		FieldMap:     datatypes.JSON(fieldMap),
		MatchFields:  datatypes.JSON(matchFields),
		Status:       SourceStatusActive,
	}
}

func buildPayload(t *testing.T, src *LeadSource) (map[string]any, []string, error) {
	t.Helper()
	fmap, err := ParseFieldMap(src.FieldMap)
	if err != nil {
		t.Fatalf("ParseFieldMap: %v", err)
	}
	return buildTestPayload(src, fmap, buildTestAllowlist(t), contactSchema().desc)
}

// TestTestLeadEmail_IsStableAndSourceScoped pins the identity: stable across clicks
// (so the second click updates one contact instead of littering a customer's CRM)
// and distinct per source (so two sources cannot collide into each other's row).
func TestTestLeadEmail_IsStableAndSourceScoped(t *testing.T) {
	a, b := testSource(t, "", ""), testSource(t, "", "")

	if testLeadEmail(a) != testLeadEmail(a) {
		t.Error("the test identity must be stable, or every click creates another contact")
	}
	if testLeadEmail(a) == testLeadEmail(b) {
		t.Error("two sources must not share a test identity")
	}
	if !strings.HasSuffix(testLeadEmail(a), "@lead-test.invalid") {
		t.Errorf("the test identity must sit in the RFC 2606 reserved TLD: %s", testLeadEmail(a))
	}
	if !IsTestContactEmail(strings.ToUpper(testLeadEmail(a))) {
		t.Error("IsTestContactEmail must normalize before matching, or a case variant reads as a real contact")
	}
}

// TestBuildTestPayload_NeverSendsAPhone is the load-bearing one.
//
// There is no safe synthetic phone: normalizePhone reduces to digits and
// contacts.phone is free text validated by nothing, so any value we invent CAN equal
// what some human typed into a real record. On a phone-matching source that is a
// direct write onto a stranger. The only guarantee is to never send one.
func TestBuildTestPayload_NeverSendsAPhone(t *testing.T) {
	maps := []string{
		`{}`,
		`{"Phone Number": {"target_key": "phone"}}`,
		`{"Work Email": {"target_key": "email"}, "Mobile": {"target_key": "phone"}}`,
		`{"Full Name": {"target_key": "first_name", "transform": "split_name"}, "Cell": {"target_key": "phone"}}`,
	}
	for _, m := range maps {
		src := testSource(t, m, `["email","phone"]`)
		fields, uncovered, err := buildPayload(t, src)
		if err != nil {
			t.Fatalf("buildTestPayload(%s): %v", m, err)
		}
		for k := range fields {
			if strings.EqualFold(k, "phone") {
				t.Errorf("field_map %s produced a phone key — a test lead must never carry one", m)
			}
		}
		if err := assertNoPhone(fields); err != nil {
			t.Errorf("assertNoPhone rejected its own payload for %s: %v", m, err)
		}
		_ = uncovered // phone's disclosure is the panel's standing one, not a per-source line
	}
}

// TestAssertNoPhone_Rejects proves the assertion can actually fail — a guard that
// only ever passes is indistinguishable from no guard.
func TestAssertNoPhone_Rejects(t *testing.T) {
	if err := assertNoPhone(map[string]any{"email": "x@y.invalid", "phone": "+1 555 0100"}); err == nil {
		t.Fatal("assertNoPhone must reject a payload carrying a phone")
	}
	if err := assertNoPhone(map[string]any{"PHONE": "+1 555 0100"}); err == nil {
		t.Fatal("assertNoPhone must be case-insensitive")
	}
}

// TestBuildTestPayload_ReverseMapsTheSourcesOwnKeys is the point of the feature: the
// payload is keyed the way the PROVIDER keys it, so the test exercises the admin's
// real mapping. A fixed {email, first_name} payload would take Apply's passthrough
// branch and report success while the mapping was never touched.
func TestBuildTestPayload_ReverseMapsTheSourcesOwnKeys(t *testing.T) {
	src := testSource(t, `{"Work Email": {"target_key": "email"}, "Company Size": {"target_key": "tier"}}`, "")
	fields, _, err := buildPayload(t, src)
	if err != nil {
		t.Fatalf("buildTestPayload: %v", err)
	}
	if fields["Work Email"] != testLeadEmail(src) {
		t.Errorf(`the identity must ride the source's own key: got %v`, fields)
	}
	if _, leaked := fields["email"]; leaked {
		t.Error("the literal target key must not be sent alongside the mapped source key — map order would pick the winner")
	}
}

// TestBuildTestPayload_IdentityMapDegenerates: an L1-era source (field_map={}) must
// still be testable, via the same passthrough real leads take.
func TestBuildTestPayload_IdentityMapDegenerates(t *testing.T) {
	src := testSource(t, `{}`, "")
	fields, _, err := buildPayload(t, src)
	if err != nil {
		t.Fatalf("buildTestPayload: %v", err)
	}
	if fields["email"] != testLeadEmail(src) {
		t.Errorf("an identity-map source must carry the identity under `email`: %v", fields)
	}
	if fields["first_name"] != "Test" || fields["last_name"] != "Lead" {
		t.Errorf("a test contact needs a human-readable name, or synthesizeFirstName names it crm-test-…: %v", fields)
	}
}

// TestBuildTestPayload_RefusesToRedirectItsOwnIdentity covers the mapping shapes that
// would leave the test with no identity — each one a route to matching on something
// real rather than on us.
func TestBuildTestPayload_RefusesToRedirectItsOwnIdentity(t *testing.T) {
	t.Run("email is itself a mapped source key", func(t *testing.T) {
		// Apply's passthrough fires only for keys ABSENT from the map, so falling back
		// to the literal `email` here would rewrite our address into `phone` and hand
		// normalizePhone the UUID's digits as a live match key.
		src := testSource(t, `{"email": {"target_key": "phone"}}`, `["email","phone"]`)
		if _, _, err := buildPayload(t, src); err == nil {
			t.Fatal("must refuse: the identity would be redirected into phone")
		}
	})

	t.Run("split_name targets email", func(t *testing.T) {
		// split_name ignores TargetKey and writes first_name/last_name, so this map
		// produces no email at all — and ValidateFieldMap does not reject it.
		src := testSource(t, `{"Full Name": {"target_key": "email", "transform": "split_name"}}`, "")
		if _, _, err := buildPayload(t, src); err == nil {
			t.Fatal("must refuse: split_name onto email yields no address")
		}
	})
}

// TestBuildTestPayload_SkipsWhatItCannotSynthesize: a guessed value in a select/number
// target 400s the whole write, and the admin reads that as "the test is broken"
// rather than "this field wasn't tested". Skip, and say so.
func TestBuildTestPayload_SkipsWhatItCannotSynthesize(t *testing.T) {
	src := testSource(t, `{"Plan": {"target_key": "tier"}}`, "") // tier is a select
	fields, uncovered, err := buildPayload(t, src)
	if err != nil {
		t.Fatalf("buildTestPayload: %v", err)
	}
	if _, sent := fields["Plan"]; sent {
		t.Error("a select target must not receive a guessed value — fieldvalidate would reject the whole lead")
	}
	if !strings.Contains(strings.Join(uncovered, " "), "tier") {
		t.Errorf("a skipped field must be reported as uncovered, never silently dropped: %v", uncovered)
	}
}

// TestBuildTestPayload_IsDeterministic: Go randomizes map iteration, and a payload
// that changes run to run makes a failing test unreproducible for the admin.
func TestBuildTestPayload_IsDeterministic(t *testing.T) {
	src := testSource(t, `{"a_email": {"target_key": "email"}, "z_email": {"target_key": "email"}}`, "")
	first, _, err := buildPayload(t, src)
	if err != nil {
		t.Fatalf("buildTestPayload: %v", err)
	}
	for i := 0; i < 25; i++ {
		got, _, err := buildPayload(t, src)
		if err != nil {
			t.Fatalf("buildTestPayload: %v", err)
		}
		if len(got) != len(first) {
			t.Fatalf("payload shape drifts between runs: %v vs %v", first, got)
		}
		for k, v := range first {
			if got[k] != v {
				t.Fatalf("payload drifts between runs at %s: %v vs %v", k, v, got[k])
			}
		}
	}
	if first["a_email"] != testLeadEmail(src) {
		t.Errorf("a tie between two email targets must resolve to the sorted-first key: %v", first)
	}
}

// TestAssertTestIdentity compares against the server-computed constant, never the
// payload's own email — the mapping sits in between and can delete the identity, and
// a guard comparing the payload to itself passes vacuously in exactly those cases.
func TestAssertTestIdentity(t *testing.T) {
	src := testSource(t, "", "")

	if err := assertTestIdentity(src, TestOriginAdmin, testLeadEmail(src)); err != nil {
		t.Errorf("the identity we built must pass: %v", err)
	}
	if err := assertTestIdentity(src, TestOriginAdmin, "real.person@customer.com"); err == nil {
		t.Fatal("a rewritten identity must abort the test")
	}
	if err := assertTestIdentity(src, TestOriginAdmin, ""); err == nil {
		t.Fatal("an identity dropped by the mapping must abort — on a phone-matching source it would match something real")
	}
	// An L3 provider test carries a real payload we did not build, so it has no
	// synthetic identity to assert.
	if err := assertTestIdentity(src, TestOriginProvider, "real.person@customer.com"); err != nil {
		t.Errorf("a provider-declared test must not be held to our identity: %v", err)
	}
	if err := assertTestIdentity(src, TestOriginNone, "real.person@customer.com"); err != nil {
		t.Errorf("a real lead must not be held to the test identity: %v", err)
	}
}

// TestAssertTestProvenance is the write-side guarantee, mutation-proofed: delete the
// call in upsert and this must fail.
func TestAssertTestProvenance(t *testing.T) {
	src := testSource(t, "", "")
	real := "someone@customer.com"
	mine := testLeadEmail(src)

	if err := assertTestProvenance(src, nil); err != nil {
		t.Errorf("no match is the ordinary create path: %v", err)
	}
	if err := assertTestProvenance(src, &domain.Contact{Email: &mine}); err != nil {
		t.Errorf("our own prior test contact must be updatable: %v", err)
	}
	if err := assertTestProvenance(src, &domain.Contact{Email: &real}); err == nil {
		t.Fatal("a test lead must refuse to touch a real contact")
	}
	if err := assertTestProvenance(src, &domain.Contact{Email: nil}); err == nil {
		t.Fatal("a contact with no email is not ours — a phone-matched real record reaches here with a nil email")
	}
}

// stubMatcher records which lookups the pipeline actually issued.
type stubMatcher struct {
	byEmail     *domain.Contact
	byPhone     []domain.Contact
	emailCalls  []string
	phoneCalled bool
}

func (s *stubMatcher) FindByNormalizedEmail(_ context.Context, _ uuid.UUID, email string) (*domain.Contact, error) {
	s.emailCalls = append(s.emailCalls, email)
	return s.byEmail, nil
}

func (s *stubMatcher) FindByNormalizedPhone(_ context.Context, _ uuid.UUID, _ string) ([]domain.Contact, error) {
	s.phoneCalled = true
	return s.byPhone, nil
}

// recordingWriter captures what the pipeline actually tried to write.
type recordingWriter struct {
	creates, updates int
}

func (w *recordingWriter) Create(_ context.Context, _, _ uuid.UUID, slug string, _ domain.RecordWriteInput) (*domain.UniformRecord, error) {
	w.creates++
	return &domain.UniformRecord{ID: uuid.New(), Object: slug}, nil
}

func (w *recordingWriter) Update(_ context.Context, _ uuid.UUID, slug string, id uuid.UUID, _ domain.RecordWriteInput) (*domain.UniformRecord, error) {
	w.updates++
	return &domain.UniformRecord{ID: id, Object: slug}, nil
}

// TestUpsert_TestLeadRefusesToTouchARealContact pins the provenance guard AT ITS CALL
// SITE, which the unit test above cannot: delete the assertTestProvenance call in
// upsert and this must fail.
//
// The scenario is the one that actually costs a customer something — a real contact
// somehow answers the test's match, and under the default fill_blank_only policy the
// pipeline would write our synthetic address onto their record. Zero writes is the
// only acceptable outcome.
func TestUpsert_TestLeadRefusesToTouchARealContact(t *testing.T) {
	realEmail := "cfo@bigcustomer.com"
	realContact := &domain.Contact{ID: uuid.New(), FirstName: "Real", Email: &realEmail}

	for _, policy := range []string{UpdatePolicyFillBlankOnly, UpdatePolicyOverwrite, UpdatePolicyCreateOnly} {
		t.Run(policy, func(t *testing.T) {
			w := &recordingWriter{}
			svc := &LeadIngestService{matcher: &stubMatcher{byEmail: realContact}, records: w}
			src := testSource(t, "", "")
			src.UpdatePolicy = policy
			lead := RawLead{TestOrigin: TestOriginAdmin}

			_, err := svc.upsert(context.Background(), src, lead, map[string]any{}, testLeadEmail(src), nil, nil)

			if err == nil {
				// create_only writes nothing, but still hands back the matched id as the
				// test's result — and the UI deep-links it. "Here is your test lead"
				// pointing at a real customer is the same failure minus the edit.
				t.Fatal("a test lead that matched a real contact must abort, not report success")
			}
			if w.creates != 0 || w.updates != 0 {
				t.Errorf("a test lead must never write when it matched a real contact: %d creates, %d updates", w.creates, w.updates)
			}
		})
	}
}

// TestUpsert_TestLeadUpdatesItsOwnPriorContact is the control: the guard must not be
// so strict that the feature stops working. Click two updates click one's contact.
func TestUpsert_TestLeadUpdatesItsOwnPriorContact(t *testing.T) {
	src := testSource(t, "", "")
	mine := testLeadEmail(src)
	prior := &domain.Contact{ID: uuid.New(), FirstName: "Test", Email: &mine}
	w := &recordingWriter{}
	svc := &LeadIngestService{matcher: &stubMatcher{byEmail: prior}, records: w}

	res, err := svc.upsert(context.Background(), src, RawLead{TestOrigin: TestOriginAdmin},
		map[string]any{"first_name": "Test"}, mine, nil, nil)

	if err != nil {
		t.Fatalf("a test lead must be able to update its own prior contact: %v", err)
	}
	if res.Outcome != OutcomeUpdated || res.RecordID != prior.ID {
		t.Errorf("expected an update of the prior test contact, got %+v", res)
	}
	if w.creates != 0 {
		t.Error("a second click must not create a second test contact")
	}
}

// TestFindTestMatch_IgnoresMatchFields is the construction-level safety property: the
// pipeline never issues a query that CAN return a real contact, so the guarantee
// holds even if someone deletes the provenance guard.
//
// The phone-only source is the case that matters. For it, requiresEmail is false and
// the ordinary findMatch never runs its email leg — so a single hit on a real
// customer's handset would return that customer straight to the update branch.
func TestFindTestMatch_IgnoresMatchFields(t *testing.T) {
	realContact := domain.Contact{ID: uuid.New(), FirstName: "Real"}
	m := &stubMatcher{byPhone: []domain.Contact{realContact}}
	svc := &LeadIngestService{matcher: m}
	src := testSource(t, "", `["phone"]`)

	res, err := svc.findTestMatch(context.Background(), src)
	if err != nil {
		t.Fatalf("findTestMatch: %v", err)
	}
	if m.phoneCalled {
		t.Error("a test lead must never issue a phone lookup — it can only return real contacts")
	}
	if len(m.emailCalls) != 1 || m.emailCalls[0] != testLeadEmail(src) {
		t.Errorf("expected exactly one lookup, for our own identity: %v", m.emailCalls)
	}
	if res.Contact != nil {
		t.Error("no prior test contact means no match, whatever match_fields says")
	}
}
