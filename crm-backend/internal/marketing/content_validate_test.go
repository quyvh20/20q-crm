package marketing

import (
	"strings"
	"testing"
)

func docWithText(text string) BlockDocument {
	return BlockDocument{Blocks: []Block{{ID: "b1", Type: BlockText, Text: text}}}
}

func TestValidateContent(t *testing.T) {
	def := DefaultMergeScope() // contact, org, campaign

	t.Run("in-scope tag with fallback passes", func(t *testing.T) {
		errs := ValidateContent("Hi {{contact.first_name|there}}", "", BlockDocument{}, def)
		if len(errs) != 0 {
			t.Fatalf("expected no errors, got %+v", errs)
		}
	})
	t.Run("out-of-scope root rejected", func(t *testing.T) {
		errs := ValidateContent("", "", docWithText("{{deal.title|x}}"), def)
		if len(errs) != 1 || !strings.Contains(errs[0].Reason, "merge scope") {
			t.Fatalf("expected scope rejection, got %+v", errs)
		}
	})
	t.Run("unknown leaf rejected", func(t *testing.T) {
		errs := ValidateContent("", "", docWithText("{{contact.bogus|x}}"), def)
		if len(errs) != 1 || !strings.Contains(errs[0].Reason, "not a known") {
			t.Fatalf("expected unknown-leaf rejection, got %+v", errs)
		}
	})
	t.Run("missing fallback on non-guaranteed rejected", func(t *testing.T) {
		errs := ValidateContent("{{contact.first_name}}", "", BlockDocument{}, def)
		if len(errs) != 1 || !strings.Contains(errs[0].Reason, "fallback") {
			t.Fatalf("expected fallback requirement, got %+v", errs)
		}
	})
	t.Run("guaranteed tags are fallback-exempt", func(t *testing.T) {
		errs := ValidateContent("{{contact.email}} from {{org.name}}", "", BlockDocument{}, def)
		if len(errs) != 0 {
			t.Fatalf("contact.email/org.name should be exempt, got %+v", errs)
		}
	})
	t.Run("custom_fields allowed with fallback", func(t *testing.T) {
		errs := ValidateContent("", "", docWithText("{{contact.custom_fields.industry|tech}}"), def)
		if len(errs) != 0 {
			t.Fatalf("custom_fields should be allowed, got %+v", errs)
		}
	})
	t.Run("company rejected unless declared", func(t *testing.T) {
		errs := ValidateContent("{{company.name|Acme}}", "", BlockDocument{}, def)
		if len(errs) != 1 {
			t.Fatalf("company not in default scope, expected rejection: %+v", errs)
		}
		ok := ValidateContent("{{company.name|Acme}}", "", BlockDocument{}, []string{ScopeContact, ScopeOrg, ScopeCampaign, ScopeCompany})
		if len(ok) != 0 {
			t.Fatalf("company should pass when declared, got %+v", ok)
		}
	})
	t.Run("button label is validated too", func(t *testing.T) {
		doc := BlockDocument{Blocks: []Block{{ID: "btn", Type: BlockButton, Label: "Hi {{deal.title}}", Href: "https://x"}}}
		errs := ValidateContent("", "", doc, def)
		if len(errs) == 0 {
			t.Fatalf("expected button-label tag to be validated")
		}
	})
}

func TestNormalizeMergeScope(t *testing.T) {
	// Junk filtered, defaults always present, company preserved, de-duped.
	got := NormalizeMergeScope([]string{"company", "deal", "company", "contact"})
	want := map[string]bool{ScopeContact: true, ScopeOrg: true, ScopeCampaign: true, ScopeCompany: true}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, r := range got {
		if !want[r] {
			t.Fatalf("unexpected root %q in %v", r, got)
		}
	}
	// Empty input → defaults only.
	if base := NormalizeMergeScope(nil); len(base) != 3 {
		t.Fatalf("empty → defaults, got %v", base)
	}
}

func TestValidateContent_URLAndAltFieldsGated(t *testing.T) {
	def := DefaultMergeScope()
	// Out-of-scope token in a button HREF must now be rejected (was a bypass).
	hrefBad := BlockDocument{Blocks: []Block{{ID: "b", Type: BlockButton, Label: "Go", Href: "{{deal.id}}"}}}
	if errs := ValidateContent("", "", hrefBad, def); len(errs) == 0 {
		t.Fatalf("out-of-scope token in Href must be rejected")
	}
	// Fallback-less token in an image SRC must be rejected.
	srcBad := BlockDocument{Blocks: []Block{{ID: "i", Type: BlockImage, Src: "https://cdn/{{contact.custom_fields.hero}}", Alt: "x"}}}
	if errs := ValidateContent("", "", srcBad, def); len(errs) == 0 {
		t.Fatalf("fallback-less token in Src must be rejected")
	}
	// A well-formed in-scope token in Href passes: contact.email is guaranteed-exempt,
	// and a token with an explicit fallback is fine too.
	ok := BlockDocument{Blocks: []Block{{ID: "b", Type: BlockButton, Label: "Go", Href: "https://x/{{contact.email}}?u={{contact.id|0}}"}}}
	if errs := ValidateContent("", "", ok, def); len(errs) != 0 {
		t.Fatalf("guaranteed/fallback tokens in Href should pass, got %+v", errs)
	}
}
