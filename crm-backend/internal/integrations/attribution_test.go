package integrations

import (
	"context"
	"net/url"
	"testing"
	"unicode/utf8"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// fakeFieldDefs records what was seeded and can pretend the org already owns a key.
type fakeFieldDefs struct {
	existing []domain.CustomFieldDef
	created  []domain.CreateFieldDefInput
	err      error
}

func (f *fakeFieldDefs) GetFieldDefs(_ context.Context, _ uuid.UUID, _ string) ([]domain.CustomFieldDef, error) {
	return f.existing, f.err
}

func (f *fakeFieldDefs) CreateFieldDef(_ context.Context, _ uuid.UUID, in domain.CreateFieldDefInput) (*domain.CustomFieldDef, error) {
	f.created = append(f.created, in)
	return &domain.CustomFieldDef{Key: in.Key, Type: in.Type}, nil
}

func TestSeedAttributionFields(t *testing.T) {
	t.Run("seeds every field as non-required text", func(t *testing.T) {
		mgr := &fakeFieldDefs{}
		m, err := SeedAttributionFields(context.Background(), mgr, uuid.New(), "contact")
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		if m["lead_source"] != "lead_source" || m["utm_campaign"] != "utm_campaign" {
			t.Errorf("canonical keys should map to themselves in a clean org: %+v", m)
		}
		for _, in := range mgr.created {
			// text, not select: a select freezes its options, so the day L3 adds
			// google_ads every capture in every previously-seeded org would 400 on an
			// unknown option.
			if in.Type != "text" {
				t.Errorf("%s seeded as %q — attribution fields must be plain text", in.Key, in.Type)
			}
			// required would reject any lead that lacks it — the opposite of the point.
			if in.Required {
				t.Errorf("%s seeded as required; a lead that lacks it must still land", in.Key)
			}
		}
	})

	t.Run("adopts the org's own text-like field rather than duplicating it", func(t *testing.T) {
		// An org migrating from HubSpot very plausibly already has lead_source.
		// Seeding a second one splits their data across two identical-looking fields.
		mgr := &fakeFieldDefs{existing: []domain.CustomFieldDef{{Key: "lead_source", Type: "text"}}}
		m, err := SeedAttributionFields(context.Background(), mgr, uuid.New(), "contact")
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		if m["lead_source"] != "lead_source" {
			t.Errorf("an existing text field should be adopted; got %q", m["lead_source"])
		}
		for _, in := range mgr.created {
			if in.Key == "lead_source" {
				t.Error("must not re-create a field the org already owns")
			}
		}
	})

	t.Run("sidesteps an incompatible field instead of 400ing every lead", func(t *testing.T) {
		// Their lead_source is a select with its own options. Writing
		// "integration:api" into it would fail fieldvalidate and reject the WHOLE
		// contact write — on every single lead.
		mgr := &fakeFieldDefs{existing: []domain.CustomFieldDef{
			{Key: "lead_source", Type: "select", Options: []string{"Referral", "Cold call"}},
		}}
		m, err := SeedAttributionFields(context.Background(), mgr, uuid.New(), "contact")
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		if m["lead_source"] != "crm_lead_source" {
			t.Errorf("an incompatible field must be sidestepped under the reserved prefix; got %q", m["lead_source"])
		}
	})
}

func TestAttributionValues(t *testing.T) {
	src := &LeadSource{Kind: KindAPI, Name: "Website form"}

	t.Run("parses utm and click ids out of the page url", func(t *testing.T) {
		// Server-side parsing, HubSpot-style: a form embed knows its own
		// location.href, but making every customer's JS decompose it correctly is the
		// step that quietly does not happen.
		vals := attributionValues(src, LeadContext{
			PageURL: "https://acme.com/pricing?utm_source=google&utm_medium=cpc&utm_campaign=q3&gclid=abc123",
		})
		if vals["utm_source"] != "google" || vals["utm_medium"] != "cpc" || vals["utm_campaign"] != "q3" {
			t.Errorf("utm parsing wrong: %+v", vals)
		}
		if vals["gclid"] != "abc123" {
			t.Errorf("gclid should be captured: %+v", vals)
		}
	})

	t.Run("origin is system-stamped, never caller-supplied", func(t *testing.T) {
		vals := attributionValues(src, LeadContext{})
		if vals["lead_source"] != "integration:api" {
			t.Errorf("lead_source must be the source's own kind; got %q", vals["lead_source"])
		}
		if vals["lead_source_detail"] != "Website form" {
			t.Errorf("detail should name the source; got %q", vals["lead_source_detail"])
		}
	})

	t.Run("an unparseable url loses utms, never the lead", func(t *testing.T) {
		vals := attributionValues(src, LeadContext{PageURL: "://not a url"})
		if vals["lead_source"] != "integration:api" {
			t.Error("origin must survive a bad page_url")
		}
	})
}

func TestApplyAttribution_FirstVsLastTouch(t *testing.T) {
	m := AttributionMap{"lead_source": "lead_source", "utm_campaign": "utm_campaign"}
	vals := map[string]string{"lead_source": "integration:api", "utm_campaign": "q3"}

	t.Run("create stamps first touch", func(t *testing.T) {
		out := map[string]any{}
		applyAttribution(out, m, vals, true)
		if out["lead_source"] != "integration:api" || out["utm_campaign"] != "q3" {
			t.Errorf("first touch writes everything: %+v", out)
		}
	})

	t.Run("update never rewrites how the person FIRST found us", func(t *testing.T) {
		// Overwriting lead_source on every resubmission is how a CRM ends up
		// reporting that every customer came from "newsletter".
		out := map[string]any{}
		applyAttribution(out, m, vals, false)
		if _, ok := out["lead_source"]; ok {
			t.Error("lead_source is first-touch only; it must not move on an update")
		}
		if out["utm_campaign"] != "q3" {
			t.Error("last-touch campaign should still update")
		}
	})

	t.Run("a collision-resolved key is written under its resolved name", func(t *testing.T) {
		out := map[string]any{}
		applyAttribution(out, AttributionMap{"lead_source": "crm_lead_source"}, vals, true)
		if out["crm_lead_source"] != "integration:api" {
			t.Errorf("must write to the resolved key: %+v", out)
		}
		if _, ok := out["lead_source"]; ok {
			t.Error("must not write the canonical name when it was sidestepped")
		}
	})
}

func TestTruncate(t *testing.T) {
	t.Run("a multi-byte rune straddling the limit is dropped, not split", func(t *testing.T) {
		// "😀" is 4 bytes (U+1F600), so the cut at 4 lands two bytes into it.
		// Byte-slicing would yield an invalid sequence that renders as U+FFFD with
		// no error — silent corruption of the customer's attribution data.
		got := truncate("ab😀", 4)
		if got != "ab" {
			t.Errorf("expected the straddling rune dropped whole, got %q (% x)", got, got)
		}
		if !utf8.ValidString(got) {
			t.Errorf("truncate produced invalid UTF-8: % x", got)
		}
	})

	t.Run("a rune ending exactly on the limit is kept", func(t *testing.T) {
		if got := truncate("ab😀", 6); got != "ab😀" {
			t.Errorf("nothing to cut at the exact length, got %q", got)
		}
		if got := truncate("😀x", 4); got != "😀" {
			t.Errorf("a rune ending exactly on the boundary survives, got %q", got)
		}
	})

	t.Run("the ceiling stays a BYTE ceiling", func(t *testing.T) {
		// The columns behind these values count bytes, so the result must never
		// exceed n even though the cut is rune-aligned.
		for _, n := range []int{1, 2, 3, 4, 5, 6, 7} {
			if got := truncate("日本語テスト", n); len(got) > n {
				t.Errorf("truncate(_, %d) returned %d bytes: %q", n, len(got), got)
			} else if !utf8.ValidString(got) {
				t.Errorf("truncate(_, %d) produced invalid UTF-8: % x", n, got)
			}
		}
	})

	t.Run("a whole CJK campaign name survives the live 255-byte path", func(t *testing.T) {
		vals := attributionValues(
			&LeadSource{Name: "Site", Kind: "api"},
			LeadContext{PageURL: "https://x.test/?utm_campaign=" + url.QueryEscape("日本語キャンペーン")},
		)
		if vals["utm_campaign"] != "日本語キャンペーン" {
			t.Errorf("a short CJK campaign must pass through intact, got %q", vals["utm_campaign"])
		}
	})

	t.Run("shorter than the limit is returned unchanged", func(t *testing.T) {
		if got := truncate("plain", 255); got != "plain" {
			t.Errorf("got %q", got)
		}
		if got := truncate("", 255); got != "" {
			t.Errorf("got %q", got)
		}
	})
}
