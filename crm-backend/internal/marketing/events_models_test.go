package marketing

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantReason string
		wantSoft   bool
	}{
		{"complaint", `{"type":"email.complained","data":{}}`, ReasonComplaint, false},
		{"hard bounce (Permanent)", `{"type":"email.bounced","data":{"bounce":{"type":"Permanent"}}}`, ReasonHardBounce, false},
		{"soft bounce (Transient)", `{"type":"email.bounced","data":{"bounce":{"type":"Transient"}}}`, ReasonSoftBounce, true},
		{"bounce undetermined => soft (conservative)", `{"type":"email.bounced","data":{"bounce":{"type":"Undetermined"}}}`, ReasonSoftBounce, true},
		{"bounce no classification => soft", `{"type":"email.bounced","data":{}}`, ReasonSoftBounce, true},
		{"failed => hard", `{"type":"email.failed","data":{}}`, ReasonHardBounce, false},
		{"delivered => none", `{"type":"email.delivered","data":{}}`, "", false},
		{"sent => none", `{"type":"email.sent","data":{}}`, "", false},
		{"opened => none", `{"type":"email.opened","data":{}}`, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, err := parseResendEnvelope([]byte(c.body))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got := classify(env)
			if got.Reason != c.wantReason || got.SoftBounce != c.wantSoft {
				t.Fatalf("classify = {%q, soft=%v}, want {%q, soft=%v}", got.Reason, got.SoftBounce, c.wantReason, c.wantSoft)
			}
		})
	}
}

func TestFromDomain(t *testing.T) {
	cases := map[string]string{
		`{"data":{"from":"Acme Marketing <noreply@send.acme.com>"}}`: "send.acme.com",
		`{"data":{"from":"noreply@acme.com"}}`:                       "acme.com",
		`{"data":{"from":"UPPER@Example.COM"}}`:                      "example.com",
		`{"data":{"from":""}}`:                                       "",
		`{"data":{"from":"garbage-no-at"}}`:                          "",
	}
	for body, want := range cases {
		env, _ := parseResendEnvelope([]byte(body))
		if got := env.Data.fromDomain(); got != want {
			t.Errorf("fromDomain(%s) = %q, want %q", body, got, want)
		}
	}
}

func TestRecipient(t *testing.T) {
	cases := map[string]string{
		`{"data":{"to":["User@Example.com"]}}`:            "user@example.com",
		`{"data":{"to":["a@x.com","b@y.com"]}}`:           "a@x.com",
		`{"data":{"to":"Bare <bare@z.com>"}}`:             "bare@z.com",
		`{"data":{"to":[]}}`:                              "",
		`{"data":{}}`:                                     "",
	}
	for body, want := range cases {
		env, _ := parseResendEnvelope([]byte(body))
		if got := env.Data.recipient(); got != want {
			t.Errorf("recipient(%s) = %q, want %q", body, got, want)
		}
	}
}

func TestParseResendEnvelope_BadJSON(t *testing.T) {
	if _, err := parseResendEnvelope([]byte(`not json`)); err == nil {
		t.Fatal("malformed body must return an error (caller acks+drops)")
	}
}
