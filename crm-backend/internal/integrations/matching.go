package integrations

import (
	"context"
	"encoding/json"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// How a lead is matched to an existing contact.
//
// match_fields is an ORDERED list — first match wins — because the fields are not
// equally trustworthy. Email identifies a person; a phone identifies a handset,
// which several people may share. So email is tried first and phone only answers
// when email cannot.

const (
	MatchEmail = "email"
	MatchPhone = "phone"
)

var validMatchFields = map[string]bool{MatchEmail: true, MatchPhone: true}

// IsValidMatchField reports whether a match field is supported.
func IsValidMatchField(f string) bool { return validMatchFields[f] }

// ParseMatchFields decodes a source's match_fields, defaulting to email.
func ParseMatchFields(raw []byte) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return []string{MatchEmail}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil || len(out) == 0 {
		return []string{MatchEmail}
	}
	return out
}

// ValidateMatchFields checks a match list at save time.
func ValidateMatchFields(fields []string) error {
	if len(fields) == 0 {
		return domain.NewAppError(400, "choose at least one field to match leads on")
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if !IsValidMatchField(f) {
			return domain.NewAppError(400, "cannot match on: "+f)
		}
		if seen[f] {
			return domain.NewAppError(400, "duplicate match field: "+f)
		}
		seen[f] = true
	}
	return nil
}

// normalizePhone reduces a phone to comparable digits.
//
// It MUST agree exactly with the SQL the index and query use —
// regexp_replace(phone, '[^0-9]', ”, 'g') — or matching silently stops using the
// index and starts disagreeing with itself. Digits only: the '+' goes too, since
// keeping it in Go and dropping it in SQL is exactly the kind of drift nobody
// notices until dedupe quietly stops working.
//
// This is deliberately NOT E.164. Turning "555 0100" into +1... requires knowing
// the org's country, which this app does not record, and guessing wrong invents a
// match between two different people. So "+1 555 0100" and "555 0100" do not match
// each other: a miss, which costs a duplicate, instead of a wrong merge, which
// costs two records.
func normalizePhone(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// minPhoneDigits guards against matching on junk. A three-digit "phone" would
// match half the database; a real number is longer than any plausible fragment.
const minPhoneDigits = 7

// MatchResult is the outcome of looking for an existing contact.
type MatchResult struct {
	// Contact is the matched record, or nil when the lead is new.
	Contact *domain.Contact
	// MatchedOn names the field that matched (for the ledger).
	MatchedOn string
	// Ambiguous reports that a match field found SEVERAL contacts and we refused to
	// pick one. The caller creates a new record instead — see findMatch.
	Ambiguous bool
	// AmbiguityNote explains the refusal in the delivery log.
	AmbiguityNote string
}

// findMatch resolves a lead to an existing contact, trying each match field in
// order.
//
// The ambiguity rule is the important part. When a phone matches SEVERAL contacts
// we do not pick one — we report ambiguity and let the caller create a new record.
// The alternative is to merge a lead into whichever row happens to be oldest, which
// silently fuses two different people into one contact and, under an overwrite
// policy, lets one person's data overwrite another's. A duplicate is visible,
// explainable in the delivery log, and mergeable later; a wrong merge is silent and
// close to unrecoverable. When in doubt, make a new row and say why.
func (s *LeadIngestService) findMatch(ctx context.Context, source *LeadSource, fields map[string]any, email string) (*MatchResult, error) {
	for _, field := range ParseMatchFields(source.MatchFields) {
		switch field {
		case MatchEmail:
			if email == "" {
				continue
			}
			c, err := s.matcher.FindByNormalizedEmail(ctx, source.OrgID, email)
			if err != nil {
				return nil, err
			}
			if c != nil {
				return &MatchResult{Contact: c, MatchedOn: MatchEmail}, nil
			}
		case MatchPhone:
			digits := normalizePhone(stringOf(fields["phone"]))
			if len(digits) < minPhoneDigits {
				continue
			}
			matches, err := s.matcher.FindByNormalizedPhone(ctx, source.OrgID, digits)
			if err != nil {
				return nil, err
			}
			if len(matches) == 1 {
				return &MatchResult{Contact: &matches[0], MatchedOn: MatchPhone}, nil
			}
			if len(matches) > 1 {
				// Several people share this number. Refuse to guess.
				return &MatchResult{
					Ambiguous: true,
					AmbiguityNote: "this phone number is on " + itoa(len(matches)) +
						" contacts, so the lead was filed as a new contact rather than merged into one of them",
				}, nil
			}
		}
	}
	return &MatchResult{}, nil
}

// itoa avoids importing strconv for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// requiresEmail reports whether a lead must carry an email to be accepted.
//
// Email stops being mandatory only when the source can match on something else:
// otherwise a lead with no email can neither match nor conflict (the unique index
// is partial on email IS NOT NULL), so every resubmission inserts another row — an
// unbounded duplicate factory. Phone matching is what makes the phone-only lead
// (the common Facebook shape) safe to accept.
func requiresEmail(source *LeadSource) bool {
	for _, f := range ParseMatchFields(source.MatchFields) {
		if f == MatchPhone {
			return false
		}
	}
	return true
}

// compile-time guard: ContactMatcher must expose both lookups.
var _ = func(m ContactMatcher) {
	var _ func(context.Context, uuid.UUID, string) (*domain.Contact, error) = m.FindByNormalizedEmail
	var _ func(context.Context, uuid.UUID, string) ([]domain.Contact, error) = m.FindByNormalizedPhone
}
