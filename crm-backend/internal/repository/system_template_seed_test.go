package repository

import (
	"encoding/json"
	"regexp"
	"sort"
	"testing"

	"crm-backend/internal/domain"
)

// These run without a database. Every shipped template is parsed and validated
// against the LIVE enums, so a template that would 400 at apply time — in front of
// a customer setting up their workspace — fails the build instead.

func TestEmbeddedTemplatesAreValid(t *testing.T) {
	files, err := loadTemplateFiles()
	if err != nil {
		t.Fatalf("embedded templates failed to load: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no templates embedded")
	}
	t.Logf("validated %d templates", len(files))
}

func TestEmbeddedTemplatesConvertToModel(t *testing.T) {
	files, err := loadTemplateFiles()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for i := range files {
		tf := files[i]
		m, err := tf.toModel()
		if err != nil {
			t.Fatalf("%s: toModel: %v", tf.Slug, err)
		}
		// Every JSONB column must be valid JSON, never nil — automation's `actions`
		// analogue is NOT NULL and a nil here would violate the column default.
		for name, raw := range map[string]domain.JSON{
			"pipeline_stages":   m.PipelineStages,
			"custom_field_defs": m.CustomFieldDefs,
			"object_defs":       m.ObjectDefs,
			"automation_rules":  m.AutomationRules,
			"kb_templates":      m.KBTemplates,
		} {
			if len(raw) == 0 {
				t.Errorf("%s: %s is empty", tf.Slug, name)
				continue
			}
			if !json.Valid(raw) {
				t.Errorf("%s: %s is not valid JSON", tf.Slug, name)
			}
		}
		if m.SpecVersion < 1 {
			t.Errorf("%s: spec_version must be >= 1", tf.Slug)
		}
	}
}

// The apply engine trusts these invariants; assert them on the real content rather
// than only inside the validator.
func TestEmbeddedTemplatesHaveUsablePipelines(t *testing.T) {
	files, err := loadTemplateFiles()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for i := range files {
		tf := files[i]
		if len(tf.PipelineStages) == 0 {
			continue
		}
		won := 0
		for _, s := range tf.PipelineStages {
			if s.IsWon {
				won++
			}
		}
		if won == 0 {
			// Without a won stage, ChangeStage can never close a deal and won/lost
			// reporting stays permanently empty.
			t.Errorf("%s: pipeline has no is_won stage", tf.Slug)
		}
	}
}

// The won stage marks the COMMERCIAL win, and it must be the last non-lost stage.
// ChangeStage clears is_won when a deal moves to an open stage, so a delivery stage
// sitting after the won one silently un-wins the deal and wipes closed_at — which
// reports revenue in the wrong period, in a direction nobody audits.
func TestEmbeddedTemplatesHaveTerminalWonStage(t *testing.T) {
	files, err := loadTemplateFiles()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for i := range files {
		tf := files[i]
		if len(tf.PipelineStages) == 0 {
			continue
		}
		byPos := make([]domain.TemplateStage, len(tf.PipelineStages))
		copy(byPos, tf.PipelineStages)
		sort.Slice(byPos, func(a, b int) bool { return byPos[a].Position < byPos[b].Position })

		seenWon := false
		for _, s := range byPos {
			if s.IsWon {
				seenWon = true
				continue
			}
			if seenWon && !s.IsLost {
				t.Errorf("%s: open stage %q sits after the won stage — a deal moved there would be un-won",
					tf.Slug, s.Name)
			}
		}
	}
}

func TestValidateTemplateFileRejectsOpenStageAfterWon(t *testing.T) {
	tf := templateFile{
		Slug: "ok", Name: "X", SpecVersion: 1,
		PipelineStages: []domain.TemplateStage{
			{Name: "Quote Sent", Position: 0},
			{Name: "Deposit Paid", Position: 1, IsWon: true},
			{Name: "Delivered", Position: 2}, // delivery state on the sales pipeline
			{Name: "Lost", Position: 3, IsLost: true},
		},
	}
	if err := ValidateTemplateFile(&tf); err == nil {
		t.Error("an open stage after the won stage must be rejected")
	}

	// The lost stage is allowed to sit after the won one — it always does.
	good := tf
	good.PipelineStages = []domain.TemplateStage{
		{Name: "Quote Sent", Position: 0},
		{Name: "Deposit Paid", Position: 1, IsWon: true},
		{Name: "Lost", Position: 2, IsLost: true},
	}
	if err := ValidateTemplateFile(&good); err != nil {
		t.Errorf("a lost stage after the won stage is normal, got %v", err)
	}
}

func TestEmbeddedTemplatesRejectReservedObjectSlugs(t *testing.T) {
	files, err := loadTemplateFiles()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for i := range files {
		for _, o := range files[i].ObjectDefs {
			if reservedObjectSlugs[o.Slug] {
				t.Errorf("%s: object slug %q shadows a system object", files[i].Slug, o.Slug)
			}
		}
	}
}

// Regression guard for the classification that decides which template workflows
// may switch themselves on. notify_user looks internal — its own doc comment says
// "in-app notification" — but the wired notifier sends real email when the
// recipient's preference has email on, so it must never be auto-activated.
func TestNotifyUserIsNotAutoActivatable(t *testing.T) {
	if domain.IsAutoActivatableAction("notify_user") {
		t.Error("notify_user must be treated as outbound: it can send real email")
	}
	for _, outbound := range []string{"send_email", "send_webhook", "ai_generate", "enroll_records"} {
		if domain.IsAutoActivatableAction(outbound) {
			t.Errorf("%s must not be auto-activatable", outbound)
		}
	}
	for _, internal := range []string{"create_task", "update_record", "log_activity", "create_record"} {
		if !domain.IsAutoActivatableAction(internal) {
			t.Errorf("%s should be auto-activatable", internal)
		}
	}
}

// The regexes here are copies of the canonical ones in the usecase package, which
// this package cannot import. If those ever change, this fails loudly rather than
// letting templates ship keys the real validator will reject.
func TestTemplateRegexesMatchUsecase(t *testing.T) {
	const (
		canonicalSlug = `^[a-z][a-z0-9_]{0,49}$`
		canonicalKey  = `^[a-z][a-z0-9_]{0,63}$`
	)
	if slugPattern.String() != canonicalSlug {
		t.Errorf("slugPattern drifted from usecase.slugRegex: %s != %s", slugPattern.String(), canonicalSlug)
	}
	if fieldKeyPattern.String() != canonicalKey {
		t.Errorf("fieldKeyPattern drifted from usecase.keyRegex: %s != %s", fieldKeyPattern.String(), canonicalKey)
	}
	// Sanity: the patterns actually behave as the length caps claim.
	if regexp.MustCompile(canonicalSlug).MatchString("Bad_Slug") {
		t.Error("slug pattern should reject uppercase")
	}
}

func TestValidateTemplateFileCatchesBadContent(t *testing.T) {
	cases := []struct {
		name string
		tf   templateFile
	}{
		{"no slug", templateFile{Name: "X", SpecVersion: 1}},
		{"bad slug", templateFile{Slug: "Bad-Slug", Name: "X", SpecVersion: 1}},
		{"zero spec version", templateFile{Slug: "ok", Name: "X"}},
		{"stage without won", templateFile{Slug: "ok", Name: "X", SpecVersion: 1,
			PipelineStages: []domain.TemplateStage{{Name: "A", Position: 0}}}},
		{"stage position out of range", templateFile{Slug: "ok", Name: "X", SpecVersion: 1,
			PipelineStages: []domain.TemplateStage{{Name: "A", Position: 7, IsWon: true}}}},
		{"stage won and lost", templateFile{Slug: "ok", Name: "X", SpecVersion: 1,
			PipelineStages: []domain.TemplateStage{{Name: "A", Position: 0, IsWon: true, IsLost: true}}}},
		{"invalid field type", templateFile{Slug: "ok", Name: "X", SpecVersion: 1,
			FieldDefs: []domain.TemplateFieldDef{{EntityType: "contact", Key: "k", Label: "L", Type: "currency"}}}},
		{"invalid entity type", templateFile{Slug: "ok", Name: "X", SpecVersion: 1,
			FieldDefs: []domain.TemplateFieldDef{{EntityType: "property", Key: "k", Label: "L", Type: "text"}}}},
		{"select without options", templateFile{Slug: "ok", Name: "X", SpecVersion: 1,
			FieldDefs: []domain.TemplateFieldDef{{EntityType: "contact", Key: "k", Label: "L", Type: "select"}}}},
		{"reserved object slug", templateFile{Slug: "ok", Name: "X", SpecVersion: 1,
			ObjectDefs: []domain.TemplateObjectDef{{Slug: "contact", Label: "C", LabelPlural: "Cs"}}}},
		{"dangling relation target", templateFile{Slug: "ok", Name: "X", SpecVersion: 1,
			ObjectDefs: []domain.TemplateObjectDef{{Slug: "thing", Label: "T", LabelPlural: "Ts",
				Fields: []domain.TemplateObjectField{{Key: "r", Label: "R", Type: "relation", TargetSlug: "nope"}}}}}},
		{"bad kb section", templateFile{Slug: "ok", Name: "X", SpecVersion: 1,
			KBTemplates: map[string]string{"nonsense": "x"}}},
		{"workflow bad activation", templateFile{Slug: "ok", Name: "X", SpecVersion: 1,
			Workflows: []domain.TemplateWorkflow{{Key: "k", Name: "N", Activation: "always",
				Trigger: domain.TemplateTrigger{Type: "contact_created"}, Steps: domain.JSON(`[{}]`)}}}},
		{"workflow without steps", templateFile{Slug: "ok", Name: "X", SpecVersion: 1,
			Workflows: []domain.TemplateWorkflow{{Key: "k", Name: "N", Activation: "auto",
				Trigger: domain.TemplateTrigger{Type: "contact_created"}}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tf := c.tf
			if err := ValidateTemplateFile(&tf); err == nil {
				t.Errorf("expected %s to be rejected", c.name)
			}
		})
	}
}

// Every template should act on its own won stage. Winning the work is the moment
// something has to happen next, and it is the step people most often forget — a
// template that installs a pipeline but nothing to fire at the end of it is doing
// half the job.
func TestEveryTemplateActsOnItsWonStage(t *testing.T) {
	files, err := loadTemplateFiles()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for i := range files {
		tf := files[i]
		if len(tf.PipelineStages) == 0 {
			continue
		}
		var won string
		for _, s := range tf.PipelineStages {
			if s.IsWon {
				won = s.Name
			}
		}
		found := false
		for _, w := range tf.Workflows {
			if w.Trigger.Type != "deal_stage_changed" {
				continue
			}
			var p struct {
				ToStage string `json:"to_stage"`
			}
			if len(w.Trigger.Params) > 0 {
				_ = json.Unmarshal(w.Trigger.Params, &p)
			}
			if declaresStage([]domain.TemplateStage{{Name: won}}, p.ToStage) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no automation fires on its won stage (%q)", tf.Slug, won)
		}
	}
}

// A named stage is resolved against the org's stages at APPLY time, so a typo
// would fail the workflow in front of the customer rather than at build time.
func TestValidateTemplateFileRejectsUnknownStageName(t *testing.T) {
	tf := templateFile{
		Slug: "ok", Name: "X", SpecVersion: 1,
		PipelineStages: []domain.TemplateStage{
			{Name: "Open", Position: 0},
			{Name: "Won", Position: 1, IsWon: true},
		},
		Workflows: []domain.TemplateWorkflow{{
			Key: "k", Name: "N", Activation: "auto",
			Trigger: domain.TemplateTrigger{
				Type:   "deal_stage_changed",
				Params: domain.JSON(`{"to_stage":"Clsoed Won"}`), // typo
			},
			Steps: domain.JSON(`[{"type":"action","id":"s1"}]`),
		}},
	}
	if err := ValidateTemplateFile(&tf); err == nil {
		t.Error("a to_stage naming a stage the template does not define must be rejected")
	}

	// The wildcard and a real stage name both stay valid.
	for _, ok := range []string{"*", "Won", "  won  "} {
		good := tf
		good.Workflows = []domain.TemplateWorkflow{{
			Key: "k", Name: "N", Activation: "auto",
			Trigger: domain.TemplateTrigger{
				Type:   "deal_stage_changed",
				Params: domain.JSON(`{"to_stage":"` + ok + `"}`),
			},
			Steps: domain.JSON(`[{"type":"action","id":"s1"}]`),
		}}
		if err := ValidateTemplateFile(&good); err != nil {
			t.Errorf("to_stage %q should be accepted, got %v", ok, err)
		}
	}
}

func TestValidateTemplateFileAcceptsRelationToSiblingObject(t *testing.T) {
	tf := templateFile{
		Slug: "ok", Name: "X", SpecVersion: 1,
		ObjectDefs: []domain.TemplateObjectDef{
			{Slug: "property", Label: "P", LabelPlural: "Ps"},
			{Slug: "viewing", Label: "V", LabelPlural: "Vs", Fields: []domain.TemplateObjectField{
				// Forward reference to a sibling declared in the same template.
				{Key: "prop", Label: "Prop", Type: "relation", TargetSlug: "property"},
				// And to a system object.
				{Key: "who", Label: "Who", Type: "relation", TargetSlug: "contact"},
			}},
		},
	}
	if err := ValidateTemplateFile(&tf); err != nil {
		t.Errorf("expected sibling + system relation targets to be accepted, got %v", err)
	}
}
