package integrations

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// fakeMatcher serves canned lookups.
type fakeMatcher struct {
	byEmail *domain.Contact
	byPhone []domain.Contact
}

func (f *fakeMatcher) FindByNormalizedEmail(_ context.Context, _ uuid.UUID, _ string) (*domain.Contact, error) {
	return f.byEmail, nil
}
func (f *fakeMatcher) FindByNormalizedPhone(_ context.Context, _ uuid.UUID, _ string) ([]domain.Contact, error) {
	return f.byPhone, nil
}

func matchSvc(m *fakeMatcher) *LeadIngestService {
	return &LeadIngestService{matcher: m}
}

func phoneSource() *LeadSource {
	return &LeadSource{MatchFields: []byte(`["email","phone"]`)}
}

// TestNormalizePhone_AgreesWithTheIndex pins the digits-only contract. The Go
// normalizer and the SQL expression behind idx_contacts_org_phone_digits
// (regexp_replace(phone,'[^0-9]',”,'g')) must produce identical strings — drift
// makes matching silently stop using the index and start disagreeing with itself.
func TestNormalizePhone_AgreesWithTheIndex(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"+1 (555) 123-4567", "15551234567"},
		{"555.123.4567", "5551234567"},
		{"  555 123 4567  ", "5551234567"},
		{"", ""},
		{"not a phone", ""},
	} {
		if got := normalizePhone(tc.in); got != tc.want {
			t.Errorf("normalizePhone(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestNormalizePhone_DoesNotGuessACountryCode documents the deliberate miss. There
// is no per-org region to resolve "555 0100" against, and inventing +1 would invent
// a match between two different people. A miss costs a duplicate (recoverable); a
// wrong merge costs two records (not).
func TestNormalizePhone_DoesNotGuessACountryCode(t *testing.T) {
	intl := normalizePhone("+1 555 123 4567")
	local := normalizePhone("555 123 4567")
	if intl == local {
		t.Fatal("a national and an international form must NOT collide — that would be a guess")
	}
}

func TestFindMatch(t *testing.T) {
	ctx := context.Background()
	existing := domain.Contact{ID: uuid.New(), FirstName: "Ada"}

	t.Run("email wins before phone is consulted", func(t *testing.T) {
		// Email identifies a person; a phone identifies a handset. Order matters.
		m := &fakeMatcher{byEmail: &existing, byPhone: []domain.Contact{{ID: uuid.New()}, {ID: uuid.New()}}}
		res, err := matchSvc(m).findMatch(ctx, phoneSource(), map[string]any{"phone": "+15551234567"}, "ada@x.com")
		if err != nil {
			t.Fatal(err)
		}
		if res.MatchedOn != MatchEmail || res.Contact == nil || res.Contact.ID != existing.ID {
			t.Errorf("email must win: %+v", res)
		}
		if res.Ambiguous {
			t.Error("an email match must not be reported ambiguous just because the phone is shared")
		}
	})

	t.Run("a unique phone matches when there is no email", func(t *testing.T) {
		m := &fakeMatcher{byPhone: []domain.Contact{existing}}
		res, err := matchSvc(m).findMatch(ctx, phoneSource(), map[string]any{"phone": "+15551234567"}, "")
		if err != nil {
			t.Fatal(err)
		}
		if res.MatchedOn != MatchPhone || res.Contact == nil {
			t.Errorf("a single phone match should match: %+v", res)
		}
	})

	// THE rule. Several people share a phone all the time — spouses, a switchboard,
	// a recycled number. Picking the oldest would silently fuse two different people
	// into one contact and, under overwrite, let one person's data destroy another's.
	// A duplicate is visible and mergeable; a wrong merge is silent and permanent.
	t.Run("an ambiguous phone REFUSES to merge and says why", func(t *testing.T) {
		m := &fakeMatcher{byPhone: []domain.Contact{{ID: uuid.New()}, {ID: uuid.New()}}}
		res, err := matchSvc(m).findMatch(ctx, phoneSource(), map[string]any{"phone": "+15551234567"}, "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Contact != nil {
			t.Fatal("must NOT pick one of several people sharing a phone")
		}
		if !res.Ambiguous {
			t.Error("the refusal must be reported as ambiguity")
		}
		if res.AmbiguityNote == "" {
			t.Error("the resulting duplicate must be explained, or it looks like a bug")
		}
	})

	t.Run("a too-short phone is not matched on", func(t *testing.T) {
		// A 3-digit "phone" would match half the database.
		m := &fakeMatcher{byPhone: []domain.Contact{existing}}
		res, err := matchSvc(m).findMatch(ctx, phoneSource(), map[string]any{"phone": "123"}, "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Contact != nil {
			t.Error("a fragment must not be treated as a phone number")
		}
	})

	t.Run("a source that does not match on phone never consults it", func(t *testing.T) {
		m := &fakeMatcher{byPhone: []domain.Contact{existing}}
		src := &LeadSource{MatchFields: []byte(`["email"]`)}
		res, err := matchSvc(m).findMatch(ctx, src, map[string]any{"phone": "+15551234567"}, "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Contact != nil {
			t.Error("phone must not be used unless the source asked for it")
		}
	})

	t.Run("no match is not an error", func(t *testing.T) {
		res, err := matchSvc(&fakeMatcher{}).findMatch(ctx, phoneSource(), map[string]any{}, "new@x.com")
		if err != nil || res.Contact != nil || res.Ambiguous {
			t.Errorf("a genuinely new lead should just be new: %+v %v", res, err)
		}
	})
}

func TestRequiresEmail(t *testing.T) {
	// Email stops being mandatory only when the source can match on something else:
	// otherwise an email-less lead can neither match nor conflict (the unique index
	// is partial on email IS NOT NULL) and every resubmission inserts another row.
	if !requiresEmail(&LeadSource{MatchFields: []byte(`["email"]`)}) {
		t.Error("an email-only source must still require an email")
	}
	if requiresEmail(phoneSource()) {
		t.Error("a source that matches on phone must accept a phone-only lead")
	}
	if !requiresEmail(&LeadSource{}) {
		t.Error("the default (no match_fields) must require an email")
	}
}

func TestValidateMatchFields(t *testing.T) {
	if err := ValidateMatchFields([]string{"email", "phone"}); err != nil {
		t.Errorf("a valid list should pass: %v", err)
	}
	if err := ValidateMatchFields(nil); err == nil {
		t.Error("an empty list must be rejected — matching on nothing duplicates everything")
	}
	if err := ValidateMatchFields([]string{"ssn"}); err == nil {
		t.Error("an unsupported field must be rejected at save time")
	}
	if err := ValidateMatchFields([]string{"email", "email"}); err == nil {
		t.Error("a duplicate match field is a config mistake worth surfacing")
	}
}

func TestParseMatchFields_DefaultsToEmail(t *testing.T) {
	// Every source predating this feature has match_fields=["email"], but a NULL or
	// malformed column must not mean "match on nothing", which would duplicate every
	// lead forever.
	for _, raw := range []string{"", "null", "[]", "{bad json"} {
		got := ParseMatchFields([]byte(raw))
		if len(got) != 1 || got[0] != MatchEmail {
			t.Errorf("ParseMatchFields(%q) = %v, want [email]", raw, got)
		}
	}
}
