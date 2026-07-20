package repository

import (
	"encoding/json"
	"regexp"
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
