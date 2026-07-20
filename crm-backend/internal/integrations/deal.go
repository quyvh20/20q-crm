package integrations

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// Lead → deal (plan L2 item 9).
//
// A source may declare that its leads are opportunities, not just contacts. When
// it does, a lead that produces a NEW contact also produces a deal, linked to that
// contact and stamped with the same attribution — which is the whole point: P9's
// source→revenue join needs the channel on the revenue row, not only on the person.
//
// Three boundaries decide the shape of everything below.
//
//  1. ONLY on the create branch. The daily cap counts CONTACT creates
//     (CountCreatedToday filters outcome='created'), so on the update branch there
//     is no backstop at all: a leaked key replaying one existing contact could mint
//     unbounded deals under a cap of 10. Gating on create makes the bound
//     deals ≤ created contacts ≤ daily_cap true by inheritance rather than by a
//     second mechanism nobody maintains. The cost is real and deliberate — a
//     returning customer's second submission produces no deal — so it is DISCLOSED
//     on the delivery rather than hidden (see noDealNote), and the plan's documented
//     alternative (a contact_created workflow with a trigger.source condition) is
//     the escape hatch for orgs that want one deal per submission.
//
//  2. NEVER for a test lead. A test contact is inert; a test deal is COUNTED — it
//     lands in the forecast total and in P9 revenue reports — and unlike the contact
//     it has no convergence mechanism (SendTestLead sends no ProviderEventID, so
//     each click is an honest separate delivery and the contact converges on its
//     stable synthetic identity while deals would simply accumulate). The plan
//     already refused an is_test marker on customer schema, so there is nowhere to
//     put "ignore this one". The test panel says so instead.
//
//  3. NEVER at the cost of the lead. A deal is downstream enrichment of a contact
//     that already landed; every degradation here is a note plus a warning, never a
//     failed delivery. Same polarity as attribution seeding and the consent write.

// DealConfig is a source's "also create a deal" setting.
//
// Stored inside the source's existing `config` JSONB under the "deal" key rather
// than in a column of its own. That is not laziness — it is the one storage option
// that adds NO new failure mode to the capture path. `config` has been in the
// CREATE TABLE since the table existed (cmd/server/main.go), so unlike every
// ALTER-added column it cannot be missing when the table is present, which is the
// exact hazard that forced owner_pool and consent to be unmapped. Nesting under a
// key leaves room for L3/L5 source-kind config beside it.
type DealConfig struct {
	Enabled bool `json:"enabled"`
	// StageID is the stage new deals land in. Validated at save time and RE-resolved
	// at ingest — stage deletion is a soft delete, so a stale id keeps satisfying the
	// FK forever and would otherwise write deals into a tombstone.
	StageID *uuid.UUID `json:"stage_id,omitempty"`
	// NameTemplate renders the deal's title. Never allowed to produce a blank —
	// dealAdapter.create 400s an empty title, which would turn a naming mistake into
	// a lost deal.
	NameTemplate string `json:"name_template,omitempty"`
}

// dealConfigKey namespaces this setting inside the shared config blob.
const dealConfigKey = "deal"

// dealSlug is the object a lead's opportunity is written to.
//
// NOT added to supportedTargets: that map governs what a lead BECOMES, and its
// restriction to system objects backed by an adapter still stands. This is a second,
// hardcoded write beside the contact — the slug is ours, never the caller's, so the
// custom-object CreatedBy=uuid.Nil hazard that closes supportedTargets cannot apply.
const dealSlug = "deal"

// DefaultDealNameTemplate is what a newly enabled source starts with.
const DefaultDealNameTemplate = "{{full_name}} — {{source_name}}"

// maxDealTitle mirrors deals.title VARCHAR(255). Postgres counts CHARACTERS for
// varchar, so the truncation below is rune-based on both counts.
const maxDealTitle = 255

// ParseDealConfig reads a source's deal setting. It NEVER returns an error:
// missing, {}, null, junk and a half-written migration all mean DISABLED.
//
// Same polarity as ParseFieldMap, ParseMatchFields and parsePoolUUIDs — config that
// cannot be read degrades this one feature, and must never be a reason to refuse a
// customer's lead. Fail-closed lives at save time, where an admin is watching.
func ParseDealConfig(raw datatypes.JSON) DealConfig {
	if len(raw) == 0 {
		return DealConfig{}
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return DealConfig{}
	}
	body, ok := envelope[dealConfigKey]
	if !ok || len(body) == 0 {
		return DealConfig{}
	}
	var cfg DealConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return DealConfig{}
	}
	// A stage that decoded to the zero UUID is no stage at all; treat it as unset so
	// ingest takes the fallback rather than writing an all-zero FK.
	if cfg.StageID != nil && *cfg.StageID == uuid.Nil {
		cfg.StageID = nil
	}
	return cfg
}

// MergeDealConfig folds a deal setting into an existing config blob, preserving
// every other key. Written this way rather than replacing the blob because L3 and
// L5 will put source-kind config beside it, and a wholesale replace here would
// delete theirs the first time an admin toggled this checkbox.
func MergeDealConfig(raw datatypes.JSON, cfg DealConfig) (datatypes.JSON, error) {
	envelope := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &envelope); err != nil {
			// An unreadable blob is replaced rather than propagated — but only here, at
			// save time, with an admin watching. Ingest never takes this path.
			envelope = map[string]any{}
		}
	}
	envelope[dealConfigKey] = cfg
	out, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(out), nil
}

// ── Naming ───────────────────────────────────────────────────────────────────

// dealNameTokens is the CLOSED vocabulary a title template may reference.
//
// Closed, deliberately, rather than "any mapped field". Two reasons, and the second
// is the one that matters: (a) the org's allowlist admits custom fields the
// configuring admin may not be permitted to READ, so an open vocabulary would make
// a deal title an FLS read primitive; (b) a deal title outlives erasure — the
// ledger redactor is keyed on the contact and does not reach deals.title — so every
// token here is a deliberate decision about what may survive a deletion request,
// and that decision must be reviewable as a list.
var dealNameTokens = map[string]bool{
	"first_name":  true,
	"last_name":   true,
	"full_name":   true,
	"email":       true,
	"company":     true,
	"source_name": true,
	"date":        true,
}

// DealNameTokens returns the vocabulary, sorted, for the UI and for error messages.
func DealNameTokens() []string {
	out := make([]string, 0, len(dealNameTokens))
	for k := range dealNameTokens {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ValidateDealNameTemplate checks a template at SAVE time, where failing closed is
// right because an admin is watching and can retry.
//
// What this can and cannot prove is the whole design: it proves a token is SPELLED
// correctly, and it cannot prove the token will be PRESENT on any given lead. That
// is why renderDealName still needs a fallback ladder. Catching {{frist_name}} in
// front of the admin — instead of shipping a year of identically-named deals — is
// the entire value, and it is worth having.
func ValidateDealNameTemplate(tpl string) error {
	trimmed := strings.TrimSpace(tpl)
	if trimmed == "" {
		return domain.NewAppError(400, "give new deals a name")
	}
	// A skeleton already over the limit can never fit once tokens resolve, so this is
	// the one length check that is knowable at save time.
	if utf8.RuneCountInString(trimmed) > maxDealTitle {
		return domain.NewAppError(400, "that deal name is too long (255 characters max)")
	}
	for _, tok := range templateTokens(trimmed) {
		if !dealNameTokens[tok] {
			return domain.NewAppError(400, "unknown field {{"+tok+"}} in the deal name — use one of: "+strings.Join(DealNameTokens(), ", "))
		}
	}
	return nil
}

// templateTokens extracts the {{name}} tokens from a template, in order.
func templateTokens(tpl string) []string {
	var out []string
	rest := tpl
	for {
		open := strings.Index(rest, "{{")
		if open < 0 {
			return out
		}
		rest = rest[open+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			return out // an unterminated {{ is text, not a token
		}
		out = append(out, strings.TrimSpace(rest[:end]))
		rest = rest[end+2:]
	}
}

// renderDealName resolves a template against one lead's written values.
//
// A local renderer rather than automation's InterpolateTemplate: that one is built
// around EvalContext (contact/deal/trigger/org/user/actions), a type this package
// has no business constructing to format one string.
//
// The unresolved-token rule is BLANK, never the literal. A kanban card reading
// "{{first_name}} — Website Form" is worse than one reading "Website Form": it
// looks like a broken product to the customer's own sales team. Save-time
// validation is what catches the typo; this is what contains a template that went
// stale after a field was removed.
func renderDealName(tpl string, vals map[string]string) string {
	var b strings.Builder
	rest := tpl
	for {
		open := strings.Index(rest, "{{")
		if open < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:open])
		rest = rest[open+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			b.WriteString("{{") // unterminated: it was never a token
			b.WriteString(rest)
			break
		}
		b.WriteString(vals[strings.TrimSpace(rest[:end])])
		rest = rest[end+2:]
	}
	return collapseSpaces(b.String())
}

// collapseSpaces tidies what an unresolved token leaves behind.
//
// Rendering "{{first_name}} {{last_name}} — {{source_name}}" for an email-only lead
// yields "  — Website Form". Collapsing runs of whitespace and stripping leading
// separators is what turns that into "Website Form" instead of shipping the gap.
func collapseSpaces(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	// Trim separators the template used between tokens that resolved to nothing.
	return strings.TrimSpace(strings.Trim(s, "-–—,|·:"))
}

// dealNameValues builds the render inputs from the contact that was just written.
//
// Read off the WRITTEN record rather than the inbound payload: the field map sits
// between the two and can rename, split or drop a key, and create() synthesizes a
// first_name for email-only leads. Naming the deal after what the payload SAID
// would contradict the contact the deal points at.
func dealNameValues(source *LeadSource, fields map[string]any, now time.Time) map[string]string {
	first := strings.TrimSpace(stringOf(fields["first_name"]))
	last := strings.TrimSpace(stringOf(fields["last_name"]))
	full := strings.TrimSpace(first + " " + last)
	return map[string]string{
		"first_name":  first,
		"last_name":   last,
		"full_name":   full,
		"email":       strings.TrimSpace(stringOf(fields["email"])),
		"company":     strings.TrimSpace(stringOf(fields["company"])),
		"source_name": source.Name,
		"date":        now.Format("2006-01-02"),
	}
}

// dealTitleFor renders the final, guaranteed-non-blank title.
//
// The ladder exists because dealAdapter.create rejects a blank title with a 400,
// which would convert "your template referenced a field this lead didn't have" into
// "your lead produced no deal". Every rung is guaranteed non-empty: source.Name is
// NOT NULL varchar(160) and required at save, and the final literal cannot fail.
func dealTitleFor(source *LeadSource, cfg DealConfig, fields map[string]any, now time.Time) string {
	tpl := cfg.NameTemplate
	if strings.TrimSpace(tpl) == "" {
		tpl = DefaultDealNameTemplate
	}
	title := renderDealName(tpl, dealNameValues(source, fields, now))
	if title == "" {
		title = strings.TrimSpace(source.Name)
	}
	if title == "" {
		title = "New lead"
	}
	return truncateChars(title, maxDealTitle)
}

// ── Stage resolution ─────────────────────────────────────────────────────────

// StageReader is the narrow slice of the pipeline the deal step needs.
//
// A port rather than the repository so the ingest service keeps one reason to know
// about stages: "is the configured one still real, and if not what is".
type StageReader interface {
	List(ctx context.Context, orgID uuid.UUID) ([]domain.PipelineStage, error)
}

// stageOutcome is what stage resolution decided, and what to tell the customer.
type stageOutcome struct {
	StageID *uuid.UUID
	Note    string
}

// resolveDealStage re-checks the configured stage against the live pipeline.
//
// This runs on EVERY deal-creating lead, and it is not paranoia. Stage deletion is
// a SOFT delete and pipelineStageUseCase.Delete asks nothing about who references
// the stage — so an admin restructuring their pipeline three months after
// configuring this source leaves the id pointing at a tombstone that still
// satisfies the FK. The resulting deal is worse than invisible: the board buckets
// an unknown stage into its FIRST column for display while the database disagrees,
// so the card looks filed correctly and reports grouped by stage orphan it. A
// forecast that is wrong and looks healthy is the failure this query buys off.
//
// Polarity is deliberately SPLIT, matching the routing rules on the same row:
//
//   - The lookup ERRORS → keep the configured stage. A liveness result we could not
//     obtain is not evidence the stage is dead, and re-filing every deal on a DB
//     blip would be a worse and much louder bug.
//   - The stage is confirmed GONE → fall back to the first live stage and SAY SO.
//     A deal in the wrong column that announces itself is triageable; a silent one
//     is not.
func resolveDealStage(ctx context.Context, stages StageReader, orgID uuid.UUID, cfg DealConfig) stageOutcome {
	if stages == nil {
		return stageOutcome{StageID: cfg.StageID}
	}
	live, err := stages.List(ctx, orgID)
	if err != nil {
		return stageOutcome{StageID: cfg.StageID} // unknown, never "it is gone"
	}
	if cfg.StageID != nil {
		for i := range live {
			if live[i].ID == *cfg.StageID {
				return stageOutcome{StageID: cfg.StageID}
			}
		}
	}
	// Configured stage is gone (or was never set). Fall back to the first stage by
	// position — List already orders by position ASC.
	if len(live) == 0 {
		return stageOutcome{Note: "this deal has no stage: the pipeline has no stages"}
	}
	first := live[0].ID
	if cfg.StageID == nil {
		return stageOutcome{StageID: &first}
	}
	return stageOutcome{
		StageID: &first,
		Note:    "the stage this source was configured with no longer exists; the deal went to " + live[0].Name,
	}
}

// ── The pipeline step ────────────────────────────────────────────────────────

// noDealNote is what a deal-enabled source says when a lead matched instead of
// creating.
//
// This disclosure is the price of gating on the create branch, and it is not
// optional. Without it the highest-intent lead a form ever receives — a known
// customer coming back for a second quote — produces no deal, and the delivery log
// says nothing at all, so the admin's only signal is a pipeline that quietly does
// not fill up. Said out loud, it is a decision the customer can see and route
// around (the documented contact_created workflow recipe).
const noDealNote = "matched an existing contact, so no deal was created"

// maybeCreateDeal runs the "also create a deal" step for one lead.
//
// Mutates result in place (DealID, Note, Warnings) and returns nothing: there is no
// error to propagate because there is no failure here that may cost the lead. The
// contact is already written and already attributed; every path below either adds a
// deal or explains why it did not.
func (s *LeadIngestService) maybeCreateDeal(ctx context.Context, source *LeadSource, lead RawLead, result *IngestResult, attr map[string]string) {
	cfg := ParseDealConfig(source.Config)
	if !cfg.Enabled {
		return
	}

	// A test lead never becomes a deal. Unlike the contact it has no convergence
	// mechanism, and it would be counted in the forecast and in revenue reports the
	// moment it landed. The test panel reports this as something the test did not
	// prove rather than pretending it did.
	if lead.IsTest() {
		return
	}

	if result.Outcome != OutcomeCreated {
		result.Note = joinNotes(result.Note, noDealNote)
		return
	}

	stage := resolveDealStage(ctx, s.stages, source.OrgID, cfg)

	fields := map[string]any{
		"title":   dealTitleFor(source, cfg, result.Fields, time.Now()),
		"contact": result.RecordID.String(),
	}
	if stage.StageID != nil {
		fields["stage"] = stage.StageID.String()
	}
	// The rep who got the contact gets the opportunity. An unowned deal is invisible
	// to own-scoped reps for exactly the reason an unowned contact is.
	if result.OwnerID != nil {
		fields["owner_user_id"] = result.OwnerID.String()
	}

	// Attribution is resolved against the DEAL's own field defs, never reused from
	// the contact's. Collision resolution is per-object: an org holding a text
	// lead_source on contact and a select lead_source on deal needs `lead_source` on
	// one and `crm_lead_source` on the other, and reusing the contact's map would
	// write into a key that either does not exist or rejects the value.
	//
	// Seeded lazily here, gated on Enabled, so a source that does not make deals
	// never pays for the extra round trip on the capture hot path.
	if amap, err := SeedAttributionFields(ctx, s.fields, source.OrgID, dealSlug); err == nil {
		applyAttribution(fields, amap, attr, true) // a new deal is always first touch
	}

	rec, err := s.records.Create(ctx, source.OrgID, uuid.Nil, dealSlug, domain.RecordWriteInput{Fields: fields})
	if err != nil {
		// The lead landed; the deal did not. Loud in both directions — the integrator
		// sees it on the response at integration time, and the admin sees it on the
		// delivery — but never fatal: refusing the whole delivery now would discard a
		// contact that is already written, attributed and owned.
		s.logf("integrations: could not create deal for lead",
			"source_id", source.ID.String(), "record_id", result.RecordID.String(), "error", err)
		result.Warnings = append(result.Warnings, "the contact was saved but its deal could not be created")
		result.Note = joinNotes(result.Note, "deal creation failed: "+err.Error())
		return
	}

	result.DealID = &rec.ID
	result.Note = joinNotes(result.Note, stage.Note, "also created deal "+rec.ID.String())
}

// truncateChars cuts to n CHARACTERS without splitting one.
//
// Deliberately NOT consent.go's truncateRunes, which bounds by BYTES and only backs
// off to a rune boundary. Both are rune-SAFE; they differ in what they count, and
// here the limit is deals.title VARCHAR(255), which Postgres counts in characters.
// Using the byte version would silently cut a Japanese or Greek deal name to a
// third of the length the column would happily have stored.
func truncateChars(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}
