package integrations

import (
	"context"
	"net/url"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// Attribution answers "which channel produced this revenue" — the question the
// legacy webhook could never answer, because it threw away everything except the
// contact's own fields.
//
// The values land as ORDINARY org custom fields on the contact rather than a
// bespoke table. That is the whole point: an ordinary custom field is already
// reportable in the report builder and already usable in a workflow condition, so
// attribution needs no new surface anywhere else in the app.

// attributionKey is one seeded field.
type attributionKey struct {
	key   string
	label string
}

// attributionFields are seeded per org the first time a lead source is created.
//
// All plain TEXT, deliberately — including lead_source, which is conceptually an
// enum. A `select` freezes its options at seed time and fieldvalidate rejects any
// value outside them, so the day L3 adds `google_ads` (or L5 `facebook_form`) every
// capture in every org seeded before it would 400 on an unknown option. Text cannot
// rot that way. The cost is a free-text field in the reports UI; the alternative is
// a migration over every org's field defs each time a phase adds a channel.
var attributionFields = []attributionKey{
	{"lead_source", "Lead Source"},
	{"lead_source_detail", "Lead Source Detail"},
	{"utm_source", "UTM Source"},
	{"utm_medium", "UTM Medium"},
	{"utm_campaign", "UTM Campaign"},
	{"utm_term", "UTM Term"},
	{"utm_content", "UTM Content"},
	{"referrer_url", "Referrer URL"},
	{"gclid", "Google Click ID"},
	{"fbclid", "Facebook Click ID"},
}

// reservedPrefix namespaces a seeded field when the org already owns the plain key
// with an incompatible type.
const reservedPrefix = "crm_"

// textLikeTypes are the field types an existing definition may have for us to adopt
// it rather than seed alongside it. Anything else (select with its own options,
// number, date, relation) would reject our values or mean something different.
var textLikeTypes = map[string]bool{"text": true, "textarea": true, "string": true}

// FieldDefManager is the narrow slice of org settings attribution needs.
type FieldDefManager interface {
	GetFieldDefs(ctx context.Context, orgID uuid.UUID, entityType string) ([]domain.CustomFieldDef, error)
	CreateFieldDef(ctx context.Context, orgID uuid.UUID, input domain.CreateFieldDefInput) (*domain.CustomFieldDef, error)
}

// AttributionMap is the resolved key mapping for one org: canonical name → the key
// actually written on the record. They differ only where a collision forced the
// reserved prefix.
type AttributionMap map[string]string

// SeedAttributionFields ensures the org has somewhere to put attribution, and
// returns the resolved key for each canonical name.
//
// Collision rule (the interesting part). An org migrating from HubSpot or
// Salesforce very plausibly already has a `lead_source` field:
//   - Already ours / text-like → ADOPT it. Seeding a second one would split the
//     customer's data across two fields that look identical in the reports UI.
//   - Exists with an incompatible type (e.g. a select with its own options) → seed
//     `crm_lead_source` alongside it and write there. Writing our value into their
//     select would 400 the entire contact write on every single lead.
//
// Idempotent: safe to call on every source creation.
func SeedAttributionFields(ctx context.Context, mgr FieldDefManager, orgID uuid.UUID, slug string) (AttributionMap, error) {
	existing, err := mgr.GetFieldDefs(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]domain.CustomFieldDef, len(existing))
	for _, d := range existing {
		byKey[d.Key] = d
	}

	out := AttributionMap{}
	for _, f := range attributionFields {
		if def, ok := byKey[f.key]; ok {
			if textLikeTypes[def.Type] {
				out[f.key] = f.key // adopt the org's own field
				continue
			}
			// Incompatible: fall back to the reserved key.
			if _, taken := byKey[reservedPrefix+f.key]; taken {
				out[f.key] = reservedPrefix + f.key
				continue
			}
			if err := createTextField(ctx, mgr, orgID, slug, reservedPrefix+f.key, f.label+" (captured)"); err != nil {
				return nil, err
			}
			out[f.key] = reservedPrefix + f.key
			continue
		}
		if err := createTextField(ctx, mgr, orgID, slug, f.key, f.label); err != nil {
			return nil, err
		}
		out[f.key] = f.key
	}
	return out, nil
}

// createTextField adds one seeded field. A 409 (someone else created it in a
// concurrent request) is success, not failure — the field exists either way.
func createTextField(ctx context.Context, mgr FieldDefManager, orgID uuid.UUID, slug, key, label string) error {
	_, err := mgr.CreateFieldDef(ctx, orgID, domain.CreateFieldDefInput{
		Key:        key,
		Label:      label,
		Type:       "text",
		EntityType: slug,
		Required:   false, // never required: a lead that lacks it must still land
	})
	if err == nil {
		return nil
	}
	if appErr, ok := err.(*domain.AppError); ok && appErr.Code == 409 {
		return nil
	}
	return err
}

// LeadContext is the envelope a caller sends alongside the lead's fields.
type LeadContext struct {
	PageURL  string
	Referrer string
}

// parseLeadContext reads the context object the capture API accepts.
func parseLeadContext(raw map[string]any) LeadContext {
	return LeadContext{
		PageURL:  strings.TrimSpace(stringOf(raw["page_url"])),
		Referrer: strings.TrimSpace(stringOf(raw["referrer"])),
	}
}

// attributionValues derives what to stamp on the record.
//
// UTMs are parsed from the page URL server-side rather than accepted as discrete
// caller fields — the HubSpot model. A form embed already knows its own
// location.href; making it decompose that into utm_* correctly, in JavaScript, on
// every customer's site, is the step that quietly does not happen. Parsing one URL
// we were handed cannot drift.
func attributionValues(source *LeadSource, lctx LeadContext) map[string]string {
	out := map[string]string{
		// System-stamped, never caller-supplied: origin is ours to assert.
		"lead_source":        source.WriteSource(),
		"lead_source_detail": source.Name,
	}
	if lctx.Referrer != "" {
		out["referrer_url"] = truncate(lctx.Referrer, 500)
	}
	if lctx.PageURL == "" {
		return out
	}
	u, err := url.Parse(lctx.PageURL)
	if err != nil {
		return out // an unparseable URL loses UTMs, never the lead
	}
	q := u.Query()
	for _, k := range []string{"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "gclid", "fbclid"} {
		if v := strings.TrimSpace(q.Get(k)); v != "" {
			out[k] = truncate(v, 255)
		}
	}
	return out
}

// applyAttribution overlays attribution onto the fields being written, resolving
// each canonical name through the org's map.
//
// firstTouch distinguishes the two halves of the standard model: on CREATE every
// value is written (first touch — how this person first reached us, which must
// never be rewritten). On UPDATE only the last-touch keys move, so a contact who
// arrives via a Google ad and returns via a newsletter keeps both stories.
func applyAttribution(fields map[string]any, m AttributionMap, vals map[string]string, firstTouch bool) {
	for canonical, v := range vals {
		key, ok := m[canonical]
		if !ok || v == "" {
			continue
		}
		if !firstTouch && !lastTouchKeys[canonical] {
			continue
		}
		fields[key] = v
	}
}

// lastTouchKeys are the attribution values that legitimately change on a return
// visit. lead_source deliberately is NOT among them: the channel that first found
// this person is a fact about the past, and overwriting it on every resubmission is
// how a CRM ends up reporting that every customer came from "newsletter".
var lastTouchKeys = map[string]bool{
	"utm_source":   true,
	"utm_medium":   true,
	"utm_campaign": true,
	"utm_term":     true,
	"utm_content":  true,
	"gclid":        true,
	"fbclid":       true,
	"referrer_url": true,
}

// truncate bounds an attribution value.
//
// Delegates to the rune-safe cut rather than slicing bytes. It used to do
// `return s[:n]`, which severs a multi-byte rune mid-sequence; json.Marshal then
// REPLACES the invalid bytes with U+FFFD instead of erroring, so a utm_campaign
// carrying an accent or a CJK character silently became a different string that
// never grouped with its siblings in a report. Silent, and invisible until someone
// asked why one campaign had two rows.
func truncate(s string, n int) string {
	return truncateRunes(s, n)
}
