package integrations

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"crm-backend/internal/domain"
)

// The test lead: a made-up lead an admin pushes through the REAL pipeline to find
// out whether their integration works before a customer's lead is the one that
// finds out.
//
// Everything about the synthetic identity lives in this file, because the identity
// is the safety property. A test lead runs the same upsert a real one does, so if
// its identity can match a real contact, the button silently edits a stranger's
// record — and under update_policy=overwrite, destroys it. That has to be
// impossible, not unlikely.

// Test origins — WHO called this a test. The distinction matters because it decides
// how much the pipeline may trust the claim.
const (
	// TestOriginNone is an ordinary lead.
	TestOriginNone = ""
	// TestOriginAdmin is our own "Send test lead" button: server-built payload,
	// server-built identity, never reachable from a request body. Because the
	// identity is ours, it can be ASSERTED (see assertTestIdentity).
	TestOriginAdmin = "admin"
	// TestOriginProvider is a platform telling us a lead is a test (L3's Google Ads
	// is_test flag). Reserved deliberately: such a lead carries a REAL payload we did
	// not build, so the admin-identity guards below must not run on it, and its
	// volume is a sabotage tell L6 alerts on. Nothing sets it yet.
	TestOriginProvider = "provider"
)

// testLeadDomain is RFC 2606's reserved TLD: it can never be registered, resolved,
// or delivered to. Chosen over a plausible-looking address so that if a test lead
// ever does escape into a mail path, it bounces at DNS rather than reaching a real
// inbox.
const testLeadDomain = "@lead-test.invalid"

// testLeadEmail is the test lead's identity, derived purely from the source id.
//
// Derived rather than stored: a column would need a migration and a boot guard, and
// would then be a second thing that can disagree with reality. A pure function of
// source.ID is stable across restarts, unique per source, and needs no schema.
func testLeadEmail(source *LeadSource) string {
	return "crm-test-" + source.ID.String() + testLeadDomain
}

// IsTestContactEmail reports whether an address is one of ours. Exported for the
// delivery-log/UI side to explain a contact that looks odd.
func IsTestContactEmail(email string) bool {
	return strings.HasSuffix(normalizeEmail(email), testLeadDomain)
}

// buildTestPayload builds the synthetic lead, keyed the way THIS source's provider
// would key it.
//
// Reverse-mapping the source's own field_map is the entire point. A fixed
// {email, first_name} payload would take Apply's passthrough branch (which fires for
// any key ABSENT from the map), write a contact, and report success — while the
// admin's mapping was never touched. The test would pass most confidently on the
// source that is most broken, which is the exact false-confidence artifact this
// button exists to prevent.
//
// uncovered names what the test could NOT exercise, so the UI can say so. A field we
// cannot synthesize a plausible value for is skipped and named, never guessed at: a
// guessed value in a `number` or `select` target 400s the whole write, and an admin
// reads that as "the test is broken", not "this field wasn't tested". A false
// failure costs as much trust as a false pass.
func buildTestPayload(source *LeadSource, fmap FieldMap, allow *Allowlist, desc *domain.ObjectDescriptor) (fields map[string]any, uncovered []string, err error) {
	fields = map[string]any{}
	types := fieldTypes(desc)

	identityKey, err := testIdentitySourceKey(source, fmap)
	if err != nil {
		return nil, nil, err
	}
	fields[identityKey] = testLeadEmail(source)

	// Walk the map in sorted order: Go randomizes map iteration, and a payload that
	// differs run to run makes a failing test unreproducible for the admin looking at
	// it.
	srcKeys := make([]string, 0, len(fmap))
	for k := range fmap {
		srcKeys = append(srcKeys, k)
	}
	sort.Strings(srcKeys)

	seen := map[string]bool{}
	for _, srcKey := range srcKeys {
		if srcKey == identityKey {
			continue // already carries the identity
		}
		entry := fmap[srcKey]
		if entry.Transform == TransformSplitName {
			fields[srcKey] = "Test Lead"
			seen["first_name"], seen["last_name"] = true, true
			continue
		}
		target := entry.TargetKey
		if target == "" || !allow.Permits(target) {
			continue // a broken mapping: save-time validation is where that surfaces
		}
		v, ok := testValueFor(target, types[target])
		if !ok {
			uncovered = append(uncovered, describeField(target, types[target], desc))
			continue
		}
		fields[srcKey] = v
		seen[target] = true
	}

	// Give the contact a human-readable name. Without this the create branch
	// synthesizes one from the email's local part and the admin gets a contact called
	// "crm-test-8f3a…", which looks like a bug rather than a test.
	for _, k := range []string{"first_name", "last_name"} {
		if seen[k] || !allow.Permits(k) {
			continue
		}
		// Only where the key is free: a source key mapped ONTO first_name is handled
		// above, and writing the literal key too would let Go's map order pick the
		// winner.
		if _, mapped := fmap[k]; mapped {
			continue
		}
		fields[k] = map[string]string{"first_name": "Test", "last_name": "Lead"}[k]
	}

	// A phone is never sent (see assertNoPhone), but it is deliberately NOT added to
	// uncovered: that would duplicate the standing "phone matching is not tested"
	// disclosure the result panel always shows, and a list that repeats itself is a
	// list people stop reading. uncovered is for what THIS source's mapping left
	// untested.
	sort.Strings(uncovered)
	return fields, uncovered, nil
}

// testIdentitySourceKey decides which payload key carries the synthetic address.
//
// It refuses rather than guesses, because every ambiguity here is a route by which
// the identity lands somewhere other than `email` — and an identity that isn't an
// email is an identity that can match a real contact.
func testIdentitySourceKey(source *LeadSource, fmap FieldMap) (string, error) {
	mapped := make([]string, 0, 2)
	for srcKey, entry := range fmap {
		if entry.TargetKey == "email" && entry.Transform != TransformSplitName {
			mapped = append(mapped, srcKey)
		}
		// split_name IGNORES TargetKey and writes first_name/last_name itself
		// (mapping.go), and ValidateFieldMap does not reject the combination. So a map
		// that "targets email" via split_name produces NO email key at all: the test
		// would run with no identity, and on a phone-matching source (where email is
		// not required) it would proceed to match on something else entirely.
		if entry.TargetKey == "email" && entry.Transform == TransformSplitName {
			return "", domain.NewAppError(http.StatusUnprocessableEntity,
				"this source maps \""+srcKey+"\" onto email with a split-name transform, which never produces an email. Fix the mapping, then test.")
		}
	}
	if len(mapped) > 0 {
		sort.Strings(mapped) // deterministic pick when several keys target email
		return mapped[0], nil
	}

	// Nothing targets email, so fall back to the literal key — which is what Apply's
	// passthrough would carry. Only safe while `email` is not ITSELF a mapped source
	// key: the passthrough fires only for keys ABSENT from the map, so on a map like
	// {"email": {target_key: "phone"}} our identity would be rewritten into `phone`
	// and hand normalizePhone the UUID's digits as a live match key against every
	// real contact in the org.
	if _, taken := fmap["email"]; taken {
		return "", domain.NewAppError(http.StatusUnprocessableEntity,
			"this source maps its own \"email\" key onto a different field, so a test lead has nowhere to put its address. Map some field onto email, then test.")
	}
	return "email", nil
}

// testValueFor returns a synthetic value for a target field, and whether one could
// be produced at all.
//
// phone is absent by omission AND by the assertion below. Everything non-textual is
// skipped: fieldvalidate would reject a made-up select option or a string in a
// number field and fail the whole lead.
func testValueFor(target, fieldType string) (any, bool) {
	if target == "phone" {
		return nil, false
	}
	switch target {
	case "first_name":
		return "Test", true
	case "last_name":
		return "Lead", true
	case "email":
		return nil, false // the identity is placed by the caller, never here
	}
	switch fieldType {
	case "text", "textarea", "string", "":
		return "Test lead", true
	default:
		return nil, false
	}
}

// testLeadContext is the synthetic envelope. The URL's UTMs are free coverage of the
// attribution path: ingest parses them server-side, so a click proves UTM capture
// end-to-end. example.com is IANA-reserved for exactly this.
func testLeadContext() map[string]any {
	return map[string]any{
		"page_url": "https://example.com/lead-test?utm_source=crm-test&utm_medium=test&utm_campaign=send-test-lead",
		"referrer": "https://example.com/",
	}
}

// assertNoPhone is a hard guarantee, not a formality: a synthetic phone is the one
// way this button can silently edit a real customer.
//
// There is no safe synthetic phone. The tempting arguments — "no valid E.164 number
// has 16 digits", "this country code is unassigned" — are true and irrelevant.
// normalizePhone keeps only [0-9] and the SQL index does the same, while
// contacts.phone is free text validated by nothing on the write path: a real contact
// holding "+1 555 123 4567 ext 8901234" normalizes to 18 digits. The comparison is
// against whatever a human typed, not against a valid number.
//
// So the test simply never sends one, and says so in the UI rather than pretending
// phone matching was covered.
func assertNoPhone(fields map[string]any) error {
	for k := range fields {
		if strings.EqualFold(strings.TrimSpace(k), "phone") {
			return domain.NewAppError(http.StatusInternalServerError,
				"refusing to send a test lead carrying a phone number")
		}
	}
	return nil
}

// assertTestIdentity is the last gate before the pipeline commits: the lead we are
// about to run must still carry the identity we built.
//
// It compares against the server-computed constant, NEVER against the payload's own
// email. The field_map sits between buildTestPayload and this point and can delete
// the identity on the way through — a transform can redirect it, an empty TargetKey
// drops it into failures, the allowlist quarantines a blanked value. A guard that
// compared the payload to itself would pass vacuously in exactly those cases, and on
// a phone-matching source (requiresEmail == false) the lead would then run with no
// identity at all and match on something real.
//
// Only admin-origin tests are asserted: an L3 provider test carries a real payload we
// did not build, so it has no synthetic identity to check.
func assertTestIdentity(source *LeadSource, origin, email string) error {
	if origin != TestOriginAdmin {
		return nil
	}
	if email != testLeadEmail(source) {
		return domain.NewAppError(http.StatusUnprocessableEntity,
			"this source's field mapping rewrites the test lead's own address, so the test cannot run safely. Fix the mapping, then test.")
	}
	return nil
}

// findTestMatch resolves a test lead to its own prior test contact — and to nothing
// else.
//
// It deliberately IGNORES source.match_fields. That is the construction-level safety
// property: the pipeline never issues a query that CAN return a real contact, so the
// guarantee survives someone later deleting a guard. Routing a test through the
// ordinary findMatch would be actively dangerous on a phone-matching source, where
// requiresEmail is false, the email leg never runs, and a single phone hit on a real
// customer's handset returns that customer's record to the update branch.
//
// The cost is honest and disclosed: phone matching is the one pipeline behaviour this
// button cannot exercise, and the UI says so.
func (s *LeadIngestService) findTestMatch(ctx context.Context, source *LeadSource) (*MatchResult, error) {
	c, err := s.matcher.FindByNormalizedEmail(ctx, source.OrgID, testLeadEmail(source))
	if err != nil {
		return nil, err
	}
	if c == nil {
		return &MatchResult{}, nil
	}
	return &MatchResult{Contact: c, MatchedOn: MatchEmail}, nil
}

// assertTestProvenance is the write-side half of the guarantee: refuse to touch a
// pre-existing record that is not our own test contact.
//
// The query side (findTestMatch) already makes this unreachable. It exists anyway
// because the two fail independently, and because the thing it prevents — editing a
// real customer's record and then handing the admin a deep-link to it — is not
// recoverable by an apology.
func assertTestProvenance(source *LeadSource, existing *domain.Contact) error {
	if existing == nil {
		return nil
	}
	if !IsTestContactEmail(derefString(existing.Email)) {
		return domain.NewAppError(http.StatusConflict,
			"the test lead matched a real contact, so nothing was written. This usually means a contact already exists with the test address.")
	}
	return nil
}

// fieldTypes indexes a descriptor's field types by key.
func fieldTypes(desc *domain.ObjectDescriptor) map[string]string {
	out := map[string]string{}
	if desc == nil {
		return out
	}
	for _, f := range desc.Fields {
		out[f.Key] = f.Type
	}
	return out
}

// describeField renders a field for the "not covered by this test" list, preferring
// the admin-facing label over the key.
func describeField(key, fieldType string, desc *domain.ObjectDescriptor) string {
	label := key
	if desc != nil {
		for _, f := range desc.Fields {
			if f.Key == key && strings.TrimSpace(f.Label) != "" {
				label = f.Label
				break
			}
		}
	}
	if fieldType == "" {
		return label
	}
	return label + " (" + fieldType + ")"
}
