package usecase

import "testing"

// TestValidatePassword pins the self-contained policy: length + composition +
// common-password blocklist. Weak/breached-adjacent inputs must be rejected with
// a non-nil (400) error; acceptable passwords must pass.
func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name    string
		pw      string
		wantErr bool
	}{
		{"too short", "ab1!", true},
		{"letters only, no number or symbol", "abcdefgh", true},
		{"digits only, no letter", "12345678", true},
		{"exact common password", "password", true},
		{"exact common password123", "password123", true},
		{"valid letters+digits", "abcd1234", false},
		{"valid letters+symbol", "abcdefg!", false},
		{"valid strong", "Str0ng-Pass!", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePassword(c.pw)
			if (err != nil) != c.wantErr {
				t.Errorf("validatePassword(%q) err=%v, wantErr=%v", c.pw, err, c.wantErr)
			}
		})
	}
}
