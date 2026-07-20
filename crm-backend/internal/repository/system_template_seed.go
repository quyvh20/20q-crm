package repository

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"crm-backend/internal/domain"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Mirrors of the canonical validators, which live in the usecase package
// (slugRegex in custom_object_usecase.go, keyRegex in org_settings_usecase.go) and
// cannot be imported from here. Note the deliberate length difference: object slugs
// cap at 50 chars, field keys at 64. Kept in sync by TestTemplateRegexesMatchUsecase.
var (
	slugPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,49}$`)
	fieldKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// reservedObjectSlugs is derived from systemObjectSpecs — the same list that seeds
// the registry — rather than hardcoded, so a future system object is reserved
// automatically. A template must never define an object with one of these slugs:
// CreateDef's duplicate probe filters on is_system = false, so the insert would
// SUCCEED and create a shadow object that RecordService silently ignores.
var reservedObjectSlugs = func() map[string]bool {
	m := make(map[string]bool, len(systemObjectSpecs))
	for _, s := range systemObjectSpecs {
		m[s.slug] = true
	}
	return m
}()

// Template content lives as one JSON file per industry rather than as Go string
// literals: at 20+ templates the literals are unreviewable, whereas a JSON file is
// a clean diff and can be validated by a test with no database at all.
//
//go:embed templates/*.json
var templateFS embed.FS

// templateFile is the on-disk shape. It is deliberately NOT domain.SystemTemplate:
// the spec sub-objects are typed here so a malformed template fails at seed/test
// time instead of at apply time, in front of a customer.
type templateFile struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	SortOrder   int    `json:"sort_order"`
	SpecVersion int    `json:"spec_version"`
	IsActive    *bool  `json:"is_active"`

	AIContext      string                     `json:"ai_context"`
	PipelineStages []domain.TemplateStage     `json:"pipeline_stages"`
	FieldDefs      []domain.TemplateFieldDef  `json:"custom_field_defs"`
	ObjectDefs     []domain.TemplateObjectDef `json:"object_defs"`
	Workflows      []domain.TemplateWorkflow  `json:"automation_rules"`
	KBTemplates    map[string]string          `json:"kb_templates"`
}

// loadTemplateFiles parses and validates every embedded template, sorted by
// filename so seeding order is deterministic.
func loadTemplateFiles() ([]templateFile, error) {
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && path.Ext(e.Name()) == ".json" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	out := make([]templateFile, 0, len(names))
	seen := map[string]string{}
	for _, name := range names {
		raw, err := templateFS.ReadFile(path.Join("templates", name))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		var tf templateFile
		// Strict decoding: a typo'd key in a hand-written template must fail loudly
		// at boot rather than silently shipping a template missing half its content.
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&tf); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if err := ValidateTemplateFile(&tf); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if prev, dup := seen[tf.Slug]; dup {
			return nil, fmt.Errorf("%s: duplicate slug %q (also in %s)", name, tf.Slug, prev)
		}
		seen[tf.Slug] = name
		out = append(out, tf)
	}
	return out, nil
}

// ValidateTemplateFile enforces the invariants the apply engine relies on. It is
// exported so the seed test can assert every shipped template passes without a DB.
func ValidateTemplateFile(tf *templateFile) error {
	if tf.Slug == "" || tf.Name == "" {
		return fmt.Errorf("slug and name are required")
	}
	if !slugPattern.MatchString(tf.Slug) {
		return fmt.Errorf("slug %q must match %s", tf.Slug, slugPattern.String())
	}
	if tf.SpecVersion < 1 {
		return fmt.Errorf("spec_version must be >= 1")
	}

	// Stages: dense positions and at least one terminal won stage. Without a won
	// stage a deal can never be closed and won/lost reporting is permanently empty.
	if n := len(tf.PipelineStages); n > 0 {
		positions := map[int]bool{}
		won := false
		for _, s := range tf.PipelineStages {
			if s.Name == "" {
				return fmt.Errorf("pipeline stage name is required")
			}
			if s.IsWon && s.IsLost {
				return fmt.Errorf("stage %q cannot be both won and lost", s.Name)
			}
			if s.IsWon {
				won = true
			}
			if s.Position < 0 || s.Position >= n {
				return fmt.Errorf("stage %q position %d out of range 0..%d", s.Name, s.Position, n-1)
			}
			if positions[s.Position] {
				return fmt.Errorf("duplicate stage position %d", s.Position)
			}
			positions[s.Position] = true
		}
		if !won {
			return fmt.Errorf("pipeline needs at least one is_won stage")
		}
	}

	// System-object fields.
	for _, f := range tf.FieldDefs {
		if !domain.ValidEntityTypes[f.EntityType] {
			return fmt.Errorf("field %q: entity_type %q is not a system object", f.Key, f.EntityType)
		}
		if err := validateTemplateFieldShape(f.Key, f.Label, f.Type, f.Options, f.TargetSlug); err != nil {
			return err
		}
	}

	// Custom objects. Relation targets may point at a system object or at another
	// object declared in this same template.
	localSlugs := map[string]bool{}
	for _, o := range tf.ObjectDefs {
		localSlugs[o.Slug] = true
	}
	for _, o := range tf.ObjectDefs {
		if o.Slug == "" || o.Label == "" || o.LabelPlural == "" {
			return fmt.Errorf("object %q: slug, label and label_plural are required", o.Slug)
		}
		if !slugPattern.MatchString(o.Slug) {
			return fmt.Errorf("object slug %q must match %s", o.Slug, slugPattern.String())
		}
		if reservedObjectSlugs[o.Slug] {
			return fmt.Errorf("object slug %q is reserved for a system object", o.Slug)
		}
		for _, f := range o.Fields {
			if err := validateTemplateFieldShape(f.Key, f.Label, f.Type, f.Options, f.TargetSlug); err != nil {
				return fmt.Errorf("object %q: %w", o.Slug, err)
			}
			if f.Type == "relation" && !localSlugs[f.TargetSlug] && !reservedObjectSlugs[f.TargetSlug] {
				return fmt.Errorf("object %q field %q: relation target %q is neither a system object nor declared in this template",
					o.Slug, f.Key, f.TargetSlug)
			}
		}
	}

	// KB sections must be real sections or they are silently unreachable in the UI.
	for section := range tf.KBTemplates {
		if _, ok := domain.ValidKBSections[section]; !ok {
			return fmt.Errorf("kb section %q is not a valid section", section)
		}
	}

	// Workflows. Step-shape validation proper belongs to the automation package
	// (which this one must not import); what is checked here is the envelope.
	wfKeys := map[string]bool{}
	for _, w := range tf.Workflows {
		if w.Key == "" || w.Name == "" {
			return fmt.Errorf("workflow key and name are required")
		}
		if wfKeys[w.Key] {
			return fmt.Errorf("duplicate workflow key %q", w.Key)
		}
		wfKeys[w.Key] = true
		if w.Trigger.Type == "" {
			return fmt.Errorf("workflow %q: trigger.type is required", w.Key)
		}
		if w.Activation != domain.TemplateActivationAuto && w.Activation != domain.TemplateActivationManual {
			return fmt.Errorf("workflow %q: activation must be %q or %q",
				w.Key, domain.TemplateActivationAuto, domain.TemplateActivationManual)
		}
		if len(w.Steps) == 0 {
			return fmt.Errorf("workflow %q: steps are required", w.Key)
		}
		// The engine rejects a deal_stage_changed trigger with no to_stage, and it
		// does so at APPLY time — i.e. in front of the customer. Catch it here.
		// Value is either "*" (any stage) or a stage name the apply engine resolves
		// to that org's stage id; a raw id is meaningless in a shared template.
		if w.Trigger.Type == "deal_stage_changed" {
			var p struct {
				ToStage   string `json:"to_stage"`
				FromStage string `json:"from_stage"`
			}
			if len(w.Trigger.Params) > 0 {
				_ = json.Unmarshal(w.Trigger.Params, &p)
			}
			if strings.TrimSpace(p.ToStage) == "" {
				return fmt.Errorf("workflow %q: deal_stage_changed needs trigger.params.to_stage (\"*\" for any stage, or a stage name)", w.Key)
			}
			// A named stage is resolved to that org's stage id at apply time, and an
			// unresolvable name fails the workflow THERE — in front of the customer.
			// Since a template installs its own stages, the name has to be one of them.
			for _, ref := range []struct{ field, value string }{
				{"to_stage", p.ToStage}, {"from_stage", p.FromStage},
			} {
				if ref.value == "" || ref.value == "*" {
					continue
				}
				if !declaresStage(tf.PipelineStages, ref.value) {
					return fmt.Errorf("workflow %q: %s names stage %q, which this template does not define",
						w.Key, ref.field, ref.value)
				}
			}
		}
	}
	return nil
}

// declaresStage reports whether the template defines a stage of this name, using
// the same case-insensitive, space-trimmed match the apply engine resolves with.
func declaresStage(stages []domain.TemplateStage, name string) bool {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, s := range stages {
		if strings.ToLower(strings.TrimSpace(s.Name)) == want {
			return true
		}
	}
	return false
}

func validateTemplateFieldShape(key, label, fieldType string, options []string, targetSlug string) error {
	if key == "" || label == "" {
		return fmt.Errorf("field %q: key and label are required", key)
	}
	if !fieldKeyPattern.MatchString(key) {
		return fmt.Errorf("field key %q must match %s", key, fieldKeyPattern.String())
	}
	if !domain.ValidFieldTypes[fieldType] {
		return fmt.Errorf("field %q: type %q is not a valid field type", key, fieldType)
	}
	if fieldType == "select" && len(options) == 0 {
		return fmt.Errorf("field %q: select needs options", key)
	}
	if fieldType == "relation" && targetSlug == "" {
		return fmt.Errorf("field %q: relation needs target_slug", key)
	}
	return nil
}

// toModel converts a parsed file into the persisted row, re-marshalling the spec
// sub-objects into their JSONB columns.
func (tf *templateFile) toModel() (*domain.SystemTemplate, error) {
	marshal := func(v interface{}, empty string) (domain.JSON, error) {
		if v == nil {
			return domain.JSON(empty), nil
		}
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		return domain.JSON(b), nil
	}

	stages, err := marshal(orEmptySlice(tf.PipelineStages), "[]")
	if err != nil {
		return nil, err
	}
	fields, err := marshal(orEmptySlice(tf.FieldDefs), "[]")
	if err != nil {
		return nil, err
	}
	objects, err := marshal(orEmptySlice(tf.ObjectDefs), "[]")
	if err != nil {
		return nil, err
	}
	workflows, err := marshal(orEmptySlice(tf.Workflows), "[]")
	if err != nil {
		return nil, err
	}
	kb := tf.KBTemplates
	if kb == nil {
		kb = map[string]string{}
	}
	kbJSON, err := marshal(kb, "{}")
	if err != nil {
		return nil, err
	}

	active := true
	if tf.IsActive != nil {
		active = *tf.IsActive
	}
	category := tf.Category
	if category == "" {
		category = "general"
	}
	sortOrder := tf.SortOrder
	if sortOrder == 0 {
		sortOrder = 100
	}

	m := &domain.SystemTemplate{
		Slug:            tf.Slug,
		Name:            tf.Name,
		Category:        category,
		Description:     tf.Description,
		Icon:            tf.Icon,
		SortOrder:       sortOrder,
		IsActive:        active,
		SpecVersion:     tf.SpecVersion,
		PipelineStages:  stages,
		CustomFieldDefs: fields,
		ObjectDefs:      objects,
		AutomationRules: workflows,
		KBTemplates:     kbJSON,
	}
	if tf.AIContext != "" {
		ctx := tf.AIContext
		m.AIContext = &ctx
	}
	return m, nil
}

// SeedSystemTemplates idempotently upserts every embedded template and retires
// anything else. Returns the number of rows written.
//
// Upsert, never delete-then-insert: org_settings.industry_template_slug carries a
// foreign key onto system_templates.slug, so removing a row would either fail or
// orphan an org's setting. Retiring a template means is_active = false.
//
// The DO UPDATE is deliberately UNCONDITIONAL. An earlier version gated it on
// `spec_version < excluded.spec_version` to protect hand-edited rows, which was a
// bug with teeth: the boot guard back-fills pre-existing rows to spec_version 1
// and new templates also ship at 1, so `1 < 1` was false and the 2022 rows kept
// their unapplyable payloads (bare stage-name strings, `currency` field types)
// while looking perfectly healthy in the catalog. The embedded files are the
// source of truth for shipped content, and there is no admin UI for hand-editing
// a system template, so there is nothing legitimate for a gate to protect.
func SeedSystemTemplates(db *gorm.DB) (int, error) {
	files, err := loadTemplateFiles()
	if err != nil {
		return 0, err
	}
	if len(files) == 0 {
		return 0, nil
	}

	rows := make([]domain.SystemTemplate, 0, len(files))
	slugs := make([]string, 0, len(files))
	for i := range files {
		m, err := files[i].toModel()
		if err != nil {
			return 0, fmt.Errorf("%s: %w", files[i].Slug, err)
		}
		rows = append(rows, *m)
		slugs = append(slugs, files[i].Slug)
	}

	err = db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "slug"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"name", "category", "description", "icon", "sort_order", "is_active",
			"spec_version", "pipeline_stages", "custom_field_defs", "object_defs",
			"ai_context", "automation_rules", "kb_templates", "updated_at",
		}),
	}).CreateInBatches(rows, 20).Error
	if err != nil {
		return 0, err
	}

	// Retire any row we no longer ship. This is what removes the pre-existing
	// rows whose slugs have no modern replacement (e.g. the 2022 "agency" row):
	// left active they would sit in the picker looking real and fail on apply,
	// because their field defs use types that no longer exist.
	//
	// is_active = false rather than DELETE, so the org_settings foreign key holds
	// and an org that already applied one keeps a resolvable reference.
	if err := db.Model(&domain.SystemTemplate{}).
		Where("slug NOT IN ?", slugs).
		Where("is_active = ?", true).
		Updates(map[string]interface{}{"is_active": false}).Error; err != nil {
		return len(rows), err
	}
	return len(rows), nil
}

func orEmptySlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
