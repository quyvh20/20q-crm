package marketing

import "testing"

func TestParseDMARC(t *testing.T) {
	cases := []struct {
		name    string
		txts    []string
		wantP   string
		wantSP  string
		wantOK  bool
	}{
		{"none", []string{"v=DMARC1; p=none; rua=mailto:x@y.com"}, "none", "", true},
		{"reject", []string{"v=DMARC1; p=reject"}, "reject", "", true},
		{"p + sp", []string{"V=DMARC1;  P = Quarantine ; sp=reject; pct=100"}, "quarantine", "reject", true},
		{"sp only, no p → not a valid org policy", []string{"v=DMARC1; sp=quarantine"}, "", "quarantine", false},
		{"not a dmarc record", []string{"v=spf1 include:_spf.resend.com ~all"}, "", "", false},
		{"empty", nil, "", "", false},
		{"found among noise", []string{"random", "v=DMARC1; p=none"}, "none", "", true},
		{"invalid p value ignored", []string{"v=DMARC1; p=bogus"}, "", "", false},
		{"multiple DMARC records → treated as absent", []string{"v=DMARC1; p=reject", "v=DMARC1; p=none"}, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, sp, ok := parseDMARC(tc.txts)
			if ok != tc.wantOK || p != tc.wantP || (tc.wantOK && sp != tc.wantSP) {
				t.Fatalf("got (p=%q,sp=%q,ok=%v), want (p=%q,sp=%q,ok=%v)", p, sp, ok, tc.wantP, tc.wantSP, tc.wantOK)
			}
		})
	}
}

func TestParentDomain(t *testing.T) {
	cases := map[string]string{
		"acme.com":          "",          // apex → no parent chase
		"mail.acme.com":     "acme.com",  // subdomain → org domain
		"a.b.c.example.com": "b.c.example.com",
		"localhost":         "",
	}
	for in, want := range cases {
		if got := parentDomain(in); got != want {
			t.Errorf("parentDomain(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDeriveRecordVerification(t *testing.T) {
	rec := func(kind, status string) ResendDNSRecord { return ResendDNSRecord{Record: kind, Status: status} }

	t.Run("all verified", func(t *testing.T) {
		spf, dkim := deriveRecordVerification([]ResendDNSRecord{
			rec("SPF", "verified"), rec("SPF", "verified"), rec("DKIM", "verified"),
		})
		if !spf || !dkim {
			t.Fatalf("spf=%v dkim=%v, want true true", spf, dkim)
		}
	})
	t.Run("one SPF record pending fails SPF", func(t *testing.T) {
		spf, dkim := deriveRecordVerification([]ResendDNSRecord{
			rec("SPF", "verified"), rec("SPF", "pending"), rec("DKIM", "verified"),
		})
		if spf || !dkim {
			t.Fatalf("spf=%v dkim=%v, want false true", spf, dkim)
		}
	})
	t.Run("no DKIM records → dkim false", func(t *testing.T) {
		spf, dkim := deriveRecordVerification([]ResendDNSRecord{rec("SPF", "verified")})
		if !spf || dkim {
			t.Fatalf("spf=%v dkim=%v, want true false", spf, dkim)
		}
	})
	t.Run("empty → both false (nothing seen)", func(t *testing.T) {
		spf, dkim := deriveRecordVerification(nil)
		if spf || dkim {
			t.Fatalf("spf=%v dkim=%v, want false false", spf, dkim)
		}
	})
}

func TestEmailDomain_CanBulkSend(t *testing.T) {
	p := func(s string) *string { return &s }
	cases := []struct {
		name       string
		d          EmailDomain
		wantSend   bool
		wantReason string
	}{
		{"all good", EmailDomain{SPFVerified: true, DKIMVerified: true, DMARCPolicy: p("none")}, true, ""},
		{"spf missing", EmailDomain{SPFVerified: false, DKIMVerified: true, DMARCPolicy: p("none")}, false, "spf_unverified"},
		{"dkim missing", EmailDomain{SPFVerified: true, DKIMVerified: false, DMARCPolicy: p("none")}, false, "dkim_unverified"},
		{"dmarc missing", EmailDomain{SPFVerified: true, DKIMVerified: true, DMARCPolicy: nil}, false, "dmarc_missing"},
		{"dmarc empty string", EmailDomain{SPFVerified: true, DKIMVerified: true, DMARCPolicy: p("")}, false, "dmarc_missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.d.CanBulkSend(); got != tc.wantSend {
				t.Fatalf("CanBulkSend()=%v want %v", got, tc.wantSend)
			}
			if got := tc.d.NotSendableReason(); got != tc.wantReason {
				t.Fatalf("NotSendableReason()=%q want %q", got, tc.wantReason)
			}
		})
	}
}

func TestNormalizeDomain(t *testing.T) {
	cases := map[string]string{
		"Example.com":               "example.com",
		"  https://www.Example.com/ ": "example.com",
		"http://foo.example.com/x/y": "foo.example.com",
		"send.example.com.":          "send.example.com",
		"WWW.TEST.IO":                "test.io",
	}
	for in, want := range cases {
		if got := normalizeDomain(in); got != want {
			t.Errorf("normalizeDomain(%q)=%q want %q", in, got, want)
		}
	}
}
