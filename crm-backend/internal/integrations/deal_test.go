package integrations

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// ── Config parsing: tolerant at read, because ingest must never fail a lead ───

func TestParseDealConfig_EverythingUnreadableMeansDisabled(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"nil", ""},
		{"empty object", `{}`},
		{"null", `null`},
		{"deal is null", `{"deal":null}`},
		{"deal is a string", `{"deal":"yes"}`},
		{"not json at all", `<html>502</html>`},
		{"deal key absent but siblings present", `{"google_ads":{"customer_id":"123"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseDealConfig(datatypes.JSON(c.raw)); got.Enabled {
				t.Fatalf("unreadable config must mean disabled, got %+v", got)
			}
		})
	}
}

// A zero-UUID stage is no stage. Writing it would put an all-zero FK on the deal.
func TestParseDealConfig_ZeroStageIsTreatedAsUnset(t *testing.T) {
	cfg := ParseDealConfig(datatypes.JSON(`{"deal":{"enabled":true,"stage_id":"00000000-0000-0000-0000-000000000000"}}`))
	if cfg.StageID != nil {
		t.Fatalf("the zero UUID is not a stage, got %v", cfg.StageID)
	}
}

func TestParseDealConfig_RoundTrips(t *testing.T) {
	stage := uuid.New()
	raw, err := MergeDealConfig(nil, DealConfig{Enabled: true, StageID: &stage, NameTemplate: "{{email}}"})
	if err != nil {
		t.Fatal(err)
	}
	got := ParseDealConfig(raw)
	if !got.Enabled || got.StageID == nil || *got.StageID != stage || got.NameTemplate != "{{email}}" {
		t.Fatalf("round trip lost something: %+v", got)
	}
}

// The whole reason the setting is nested under a key rather than owning the blob:
// L3/L5 will store source-kind config beside it, and toggling this checkbox must
// not delete theirs.
func TestMergeDealConfig_PreservesSiblingKeys(t *testing.T) {
	existing := datatypes.JSON(`{"google_ads":{"customer_id":"123-456"},"deal":{"enabled":false}}`)
	merged, err := MergeDealConfig(existing, DealConfig{Enabled: true, NameTemplate: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(merged), "123-456") {
		t.Fatalf("a sibling key was destroyed: %s", merged)
	}
	if !ParseDealConfig(merged).Enabled {
		t.Fatalf("the deal key did not take: %s", merged)
	}
}

// ── Template validation: fails CLOSED, because an admin is watching ──────────

func TestValidateDealNameTemplate(t *testing.T) {
	cases := []struct {
		name    string
		tpl     string
		wantErr bool
	}{
		{"a real template", "{{full_name}} — {{source_name}}", false},
		{"plain text with no tokens", "Website enquiry", false},
		{"every token in the vocabulary", "{{first_name}}{{last_name}}{{full_name}}{{email}}{{company}}{{source_name}}{{date}}", false},
		{"tolerates padding inside the braces", "{{ full_name }}", false},
		{"blank", "   ", true},
		{"a typo", "{{frist_name}}", true},
		{"a field outside the vocabulary", "{{phone}}", true},
		{"a custom field is not in the vocabulary either", "{{lead_source}}", true},
		{"a skeleton no rendering could ever fit", strings.Repeat("x", 256), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateDealNameTemplate(c.tpl)
			if (err != nil) != c.wantErr {
				t.Fatalf("ValidateDealNameTemplate(%q) error = %v, wantErr %v", c.tpl, err, c.wantErr)
			}
		})
	}
}

// The vocabulary is closed on purpose: an open one would make a deal title both an
// FLS read primitive and an unbounded decision about what survives erasure.
func TestDealNameTokens_IsAClosedReviewableList(t *testing.T) {
	got := strings.Join(DealNameTokens(), ",")
	want := "company,date,email,first_name,full_name,last_name,source_name"
	if got != want {
		t.Fatalf("the token vocabulary changed to %q.\nThat is a deliberate decision about what may appear in a deal title and outlive an erasure request — update this test only alongside that decision.", got)
	}
}

// ── Rendering: never a literal token, never a blank title ────────────────────

func TestRenderDealName(t *testing.T) {
	vals := map[string]string{"first_name": "Ada", "last_name": "Lovelace", "source_name": "Website Form"}
	cases := []struct{ name, tpl, want string }{
		{"resolves", "{{first_name}} {{last_name}}", "Ada Lovelace"},
		{"an unresolved token leaves no literal", "{{first_name}} {{email}}", "Ada"},
		{"collapses the gap a missing token leaves", "{{email}} {{first_name}}", "Ada"},
		{"strips a dangling separator", "{{email}} — {{source_name}}", "Website Form"},
		{"everything missing renders empty, not a skeleton", "{{email}} — {{company}}", ""},
		{"an unterminated brace is text, not a token", "50% off {{ now", "50% off {{ now"},
		{"no tokens at all", "Website enquiry", "Website enquiry"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := renderDealName(c.tpl, vals); got != c.want {
				t.Errorf("renderDealName(%q) = %q, want %q", c.tpl, got, c.want)
			}
		})
	}
}

// dealAdapter.create rejects a blank title with a 400, so a template that resolves
// to nothing would turn a naming mistake into a LOST deal. Every rung of the ladder
// has to be non-empty.
func TestDealTitleFor_IsNeverBlank(t *testing.T) {
	now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	src := &LeadSource{Name: "Website Form"}

	if got := dealTitleFor(src, DealConfig{NameTemplate: "{{full_name}} — {{source_name}}"},
		map[string]any{"first_name": "Ada", "last_name": "Lovelace"}, now); got != "Ada Lovelace — Website Form" {
		t.Errorf("normal render = %q", got)
	}
	// An email-only lead: every contact token is blank, so the ladder falls to the
	// source name rather than shipping "  — Website Form".
	if got := dealTitleFor(src, DealConfig{NameTemplate: "{{first_name}} {{last_name}}"},
		map[string]any{}, now); got != "Website Form" {
		t.Errorf("empty render must fall back to the source name, got %q", got)
	}
	// Even a nameless source cannot produce a blank title.
	if got := dealTitleFor(&LeadSource{Name: "  "}, DealConfig{NameTemplate: "{{email}}"},
		map[string]any{}, now); got != "New lead" {
		t.Errorf("the last rung must be a literal, got %q", got)
	}
	// An empty template is the default template, not an empty title.
	if got := dealTitleFor(src, DealConfig{}, map[string]any{"first_name": "Ada"}, now); got != "Ada — Website Form" {
		t.Errorf("a blank template must use the default, got %q", got)
	}
	if got := dealTitleFor(src, DealConfig{NameTemplate: "{{date}}"}, map[string]any{}, now); got != "2026-07-20" {
		t.Errorf("date token = %q", got)
	}
}

// deals.title is VARCHAR(255) and Postgres counts CHARACTERS, so the cut is
// rune-based on both counts — byte-slicing would both over-truncate and corrupt.
func TestDealTitleFor_TruncatesByCharacterWithoutCorrupting(t *testing.T) {
	long := strings.Repeat("日", 400)
	title := dealTitleFor(&LeadSource{Name: "S"}, DealConfig{NameTemplate: "{{first_name}}"},
		map[string]any{"first_name": long}, time.Now())

	if n := len([]rune(title)); n != maxDealTitle {
		t.Fatalf("title kept %d characters, want %d — a byte-based cut would keep ~85 and waste two thirds of the column", n, maxDealTitle)
	}
	if strings.ContainsRune(title, '�') {
		t.Fatal("truncation severed a rune: json.Marshal replaces the broken bytes silently, so this corrupts rather than errors")
	}
}

// ── Stage resolution: split polarity ─────────────────────────────────────────

type stubStages struct {
	stages []domain.PipelineStage
	err    error
	calls  int
}

func (s *stubStages) List(_ context.Context, _ uuid.UUID) ([]domain.PipelineStage, error) {
	s.calls++
	return s.stages, s.err
}

func TestResolveDealStage(t *testing.T) {
	live := uuid.New()
	first := uuid.New()
	gone := uuid.New()
	pipeline := []domain.PipelineStage{
		{ID: first, Name: "Lead In", Position: 0},
		{ID: live, Name: "Qualified", Position: 1},
	}

	t.Run("configured stage still exists: used as-is, nothing said", func(t *testing.T) {
		out := resolveDealStage(context.Background(), &stubStages{stages: pipeline}, uuid.New(), DealConfig{StageID: &live})
		if out.StageID == nil || *out.StageID != live {
			t.Fatalf("stage = %v, want %v", out.StageID, live)
		}
		if out.Note != "" {
			t.Errorf("a healthy stage needs no disclosure, got %q", out.Note)
		}
	})

	t.Run("configured stage was deleted: falls back to the first AND says so", func(t *testing.T) {
		out := resolveDealStage(context.Background(), &stubStages{stages: pipeline}, uuid.New(), DealConfig{StageID: &gone})
		if out.StageID == nil || *out.StageID != first {
			t.Fatalf("stage = %v, want the first live stage %v", out.StageID, first)
		}
		if !strings.Contains(out.Note, "Lead In") {
			t.Errorf("a silent re-file is the bug this check exists to prevent; note = %q", out.Note)
		}
	})

	// The routing rule, one setting over: a liveness result we could not obtain is
	// NOT evidence the stage is dead. Re-filing every deal on a DB blip would be a
	// worse and much louder bug than trusting the configured value.
	t.Run("lookup error: keeps the configured stage, fails OPEN", func(t *testing.T) {
		out := resolveDealStage(context.Background(), &stubStages{err: errors.New("db is down")}, uuid.New(), DealConfig{StageID: &gone})
		if out.StageID == nil || *out.StageID != gone {
			t.Fatalf("an error must not be read as 'the stage is gone', got %v", out.StageID)
		}
		if out.Note != "" {
			t.Errorf("nothing was decided, so nothing should be claimed; note = %q", out.Note)
		}
	})

	t.Run("no stages at all: no stage, and it is disclosed", func(t *testing.T) {
		out := resolveDealStage(context.Background(), &stubStages{stages: nil}, uuid.New(), DealConfig{StageID: &gone})
		if out.StageID != nil {
			t.Fatalf("stage = %v, want nil", out.StageID)
		}
		if out.Note == "" {
			t.Error("a stageless deal must say why")
		}
	})

	t.Run("no stage reader wired: degrades, never panics", func(t *testing.T) {
		out := resolveDealStage(context.Background(), nil, uuid.New(), DealConfig{StageID: &live})
		if out.StageID == nil || *out.StageID != live {
			t.Fatalf("stage = %v", out.StageID)
		}
	})
}

// ── The pipeline step ────────────────────────────────────────────────────────

// dealWriter records the deal write specifically, so the assertions can be about
// what landed on the opportunity rather than about call counts.
type dealWriter struct {
	dealFields map[string]any
	deals      int
	err        error
}

func (w *dealWriter) Create(_ context.Context, _, _ uuid.UUID, slug string, in domain.RecordWriteInput) (*domain.UniformRecord, error) {
	if slug == dealSlug {
		w.deals++
		w.dealFields = in.Fields
		if w.err != nil {
			return nil, w.err
		}
	}
	return &domain.UniformRecord{ID: uuid.New(), Object: slug}, nil
}

func (w *dealWriter) Update(_ context.Context, _ uuid.UUID, slug string, id uuid.UUID, _ domain.RecordWriteInput) (*domain.UniformRecord, error) {
	return &domain.UniformRecord{ID: id, Object: slug}, nil
}

// noFieldDefs satisfies FieldDefManager without a DB: attribution seeding is
// exercised elsewhere, and here it must simply not be a reason the deal fails.
type noFieldDefs struct{ err error }

func (n noFieldDefs) GetFieldDefs(_ context.Context, _ uuid.UUID, _ string) ([]domain.CustomFieldDef, error) {
	return nil, n.err
}

func (n noFieldDefs) CreateFieldDef(_ context.Context, _ uuid.UUID, _ domain.CreateFieldDefInput) (*domain.CustomFieldDef, error) {
	return nil, n.err
}

func dealEnabledSource(t *testing.T, stage uuid.UUID) *LeadSource {
	t.Helper()
	cfg, err := MergeDealConfig(nil, DealConfig{Enabled: true, StageID: &stage, NameTemplate: "{{full_name}} — {{source_name}}"})
	if err != nil {
		t.Fatal(err)
	}
	return &LeadSource{ID: uuid.New(), OrgID: uuid.New(), Kind: "api", Name: "Website Form", Config: cfg}
}

func TestMaybeCreateDeal_CreatesALinkedAttributedDeal(t *testing.T) {
	stage := uuid.New()
	src := dealEnabledSource(t, stage)
	w := &dealWriter{}
	owner := uuid.New()
	contactID := uuid.New()
	svc := &LeadIngestService{
		records: w,
		fields:  noFieldDefs{},
		stages:  &stubStages{stages: []domain.PipelineStage{{ID: stage, Name: "Lead In"}}},
	}
	res := &IngestResult{
		RecordID: contactID,
		Outcome:  OutcomeCreated,
		OwnerID:  &owner,
		Fields:   map[string]any{"first_name": "Ada", "last_name": "Lovelace", "email": "ada@x.com"},
	}

	svc.maybeCreateDeal(context.Background(), src, RawLead{}, res, map[string]string{"lead_source": "integration:api"})

	if w.deals != 1 {
		t.Fatalf("expected exactly one deal, got %d", w.deals)
	}
	if res.DealID == nil {
		t.Fatal("the deal id must reach the caller")
	}
	if got := w.dealFields["title"]; got != "Ada Lovelace — Website Form" {
		t.Errorf("title = %v", got)
	}
	// The linkage the plan asks for: deals.contact_id, via the registry's `contact`
	// relation key.
	if got := w.dealFields["contact"]; got != contactID.String() {
		t.Errorf("the deal must link the contact it came from: contact = %v, want %v", got, contactID)
	}
	if got := w.dealFields["stage"]; got != stage.String() {
		t.Errorf("stage = %v, want %v", got, stage)
	}
	// The rep who got the contact gets the opportunity — an unowned deal is invisible
	// to own-scoped reps for the same reason an unowned contact is.
	if got := w.dealFields["owner_user_id"]; got != owner.String() {
		t.Errorf("owner = %v, want %v", got, owner)
	}
	if !strings.Contains(res.Note, "also created deal") {
		t.Errorf("the delivery must record what it made; note = %q", res.Note)
	}
}

func TestMaybeCreateDeal_SkipsWhenNotAsked(t *testing.T) {
	cases := []struct {
		name     string
		source   func(t *testing.T, stage uuid.UUID) *LeadSource
		lead     RawLead
		outcome  string
		wantNote string
	}{
		{
			name:    "the source does not make deals",
			source:  func(t *testing.T, _ uuid.UUID) *LeadSource { return &LeadSource{Name: "S"} },
			outcome: OutcomeCreated,
		},
		{
			// A test deal would be counted in the forecast and in revenue reports, and
			// unlike the test CONTACT it has no convergence mechanism — five clicks
			// would leave five real deals with nothing marking them synthetic.
			name:    "a test lead never opens a deal",
			source:  dealEnabledSource,
			lead:    RawLead{TestOrigin: TestOriginAdmin},
			outcome: OutcomeCreated,
		},
		{
			// The disclosure that makes the create-branch gate honest rather than a
			// silent under-creation.
			name:     "a matched contact is disclosed, not silently skipped",
			source:   dealEnabledSource,
			outcome:  OutcomeUpdated,
			wantNote: noDealNote,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stage := uuid.New()
			w := &dealWriter{}
			svc := &LeadIngestService{records: w, fields: noFieldDefs{}, stages: &stubStages{}}
			res := &IngestResult{RecordID: uuid.New(), Outcome: c.outcome, Fields: map[string]any{}}

			svc.maybeCreateDeal(context.Background(), c.source(t, stage), c.lead, res, nil)

			if w.deals != 0 {
				t.Fatalf("expected no deal, got %d", w.deals)
			}
			if res.DealID != nil {
				t.Error("no deal was made, so no deal id may be reported")
			}
			if c.wantNote != "" && !strings.Contains(res.Note, c.wantNote) {
				t.Errorf("note = %q, want it to contain %q", res.Note, c.wantNote)
			}
		})
	}
}

// The lead is already written, attributed and owned by the time the deal is
// attempted. Refusing the delivery now would discard it — so a failed deal is loud
// in both directions and fatal in neither.
func TestMaybeCreateDeal_AFailedDealNeverCostsTheLead(t *testing.T) {
	stage := uuid.New()
	src := dealEnabledSource(t, stage)
	w := &dealWriter{err: errors.New("stage_id violates foreign key constraint")}
	svc := &LeadIngestService{
		records: w,
		fields:  noFieldDefs{},
		stages:  &stubStages{stages: []domain.PipelineStage{{ID: stage, Name: "Lead In"}}},
	}
	contactID := uuid.New()
	res := &IngestResult{RecordID: contactID, Outcome: OutcomeCreated, Fields: map[string]any{"first_name": "Ada"}}

	svc.maybeCreateDeal(context.Background(), src, RawLead{}, res, nil)

	if res.RecordID != contactID {
		t.Fatal("the contact must survive its deal failing")
	}
	if res.DealID != nil {
		t.Error("no deal exists, so none may be reported")
	}
	if len(res.Warnings) == 0 {
		t.Error("the integrator must hear about this at integration time")
	}
	if !strings.Contains(res.Note, "deal creation failed") {
		t.Errorf("the delivery must record the failure; note = %q", res.Note)
	}
}

// Attribution seeding failing is not a reason to lose the deal — same polarity the
// contact write already takes.
func TestMaybeCreateDeal_SurvivesAttributionSeedFailure(t *testing.T) {
	stage := uuid.New()
	src := dealEnabledSource(t, stage)
	w := &dealWriter{}
	svc := &LeadIngestService{
		records: w,
		fields:  noFieldDefs{err: errors.New("field defs unavailable")},
		stages:  &stubStages{stages: []domain.PipelineStage{{ID: stage}}},
	}
	res := &IngestResult{RecordID: uuid.New(), Outcome: OutcomeCreated, Fields: map[string]any{"first_name": "Ada"}}

	svc.maybeCreateDeal(context.Background(), src, RawLead{}, res, map[string]string{"lead_source": "integration:api"})

	if w.deals != 1 {
		t.Fatalf("the deal must still be created, got %d", w.deals)
	}
}

// The deal write must inherit the contact write's context marks. Verified through
// the constructor rather than by inspecting maybeCreateDeal, because the mark is
// carried by ctx and the bug would be passing ledgerCtx (or the request ctx) in.
func TestMaybeCreateDeal_RunsOnTheSilencedContext(t *testing.T) {
	src := dealEnabledSource(t, uuid.New())
	ictx, cancel := newIngestContext(src, RawLead{TestOrigin: TestOriginAdmin}, time.Minute)
	defer cancel()
	if !domain.IsAutomationSilenced(ictx) {
		t.Fatal("a test lead's write context must be silenced")
	}
	bctx, bcancel := newIngestContext(src, RawLead{DeliveryMode: DeliveryBatch}, time.Minute)
	defer bcancel()
	if !domain.IsAutomationSuppressed(bctx) {
		t.Fatal("a batch delivery's write context must be suppressed, or 100 recovered leads fire 100 deal_created workflows")
	}
}
