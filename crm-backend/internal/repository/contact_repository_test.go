package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The bug this guards: a contact whose phone was 501-222-7363 was invisible to
// search, because the term only ever hit a tsvector built from name + email.
func TestPhoneSearchVariants(t *testing.T) {
	tests := []struct {
		name string
		q    string
		want []string
	}{
		// ── Recognized as a phone ──────────────────────────────────────────────
		{"the reported case", "501-222-7363", []string{"5012227363", "15012227363"}},
		{"bare digits", "5012227363", []string{"5012227363", "15012227363"}},
		{"parens and spaces", "(501) 222-7363", []string{"5012227363", "15012227363"}},
		{"dotted", "501.222.7363", []string{"5012227363", "15012227363"}},
		{"e164-ish drops the country code too", "+1 501-222-7363", []string{"15012227363", "5012227363"}},
		{"seven digits is the floor", "222-7363", []string{"2227363"}},
		{"long international gets no variant", "+44 20 7946 0958", []string{"442079460958"}},

		// ── Falls through to text search ───────────────────────────────────────
		{"a name", "ABC OK", nil},
		{"an email", "bob@example.com", nil},
		{"too few digits", "555-01", nil},
		{"digits with letters stay text", "Suite 5012227363", nil},
		{"an address is not a phone", "Suite 500", nil},
		{"empty", "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, phoneSearchVariants(tt.q))
		})
	}
}

// The SQL predicate and the variants must reduce a number the same way, or the
// query silently stops using idx_contacts_org_phone_digits. This asserts the Go
// half of that contract; integrations/repository_integration_test.go asserts the
// SQL half against a live Postgres.
func TestPhoneSearchVariantsMatchesNormalizerOutput(t *testing.T) {
	// Every accepted form of the same line must produce the same first variant,
	// which is what the digits index stores.
	for _, form := range []string{"5012227363", "501-222-7363", "(501) 222-7363", "501.222.7363", "501 222 7363"} {
		got := phoneSearchVariants(form)
		assert.NotEmpty(t, got, form)
		assert.Equal(t, "5012227363", got[0], form)
	}
}
