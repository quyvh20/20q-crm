package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"crm-backend/internal/automation"
	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TemplatePermissionSeeder is the slice of the permission usecase the apply engine
// needs. Declared locally (rather than widening domain.PermissionUseCase) so this
// file states exactly what it depends on.
//
// Both calls are mandatory after installing objects: CreateDef seeds NO permission
// rows, OLS seeding is lazy, and an absent row means DENY — so without these the
// admin who just applied a template gets a 403 on their own new objects until the
// cache TTL expires.
type TemplatePermissionSeeder interface {
	EnsureSeeded(ctx context.Context, orgID uuid.UUID) error
	Invalidate(orgID uuid.UUID)
}

// TemplateRepoFactories builds tx-scoped repositories for Phase A.
//
// Injected as factories rather than constructed here because this package does not
// import internal/repository, and because EVERY repository in the phase must be
// built from the SAME tx handle: these repos capture whatever *gorm.DB they are
// given, and one built from the root handle would read a snapshot outside the
// transaction — unable to see Phase A's own uncommitted writes — making the
// "already exists?" probes silently mis-decide.
type TemplateRepoFactories struct {
	ObjectRegistry func(db *gorm.DB) domain.ObjectRegistryRepository
	CustomObject   func(db *gorm.DB) domain.CustomObjectRepository
}

type systemTemplateUseCase struct {
	db          *gorm.DB
	repo        domain.SystemTemplateRepository
	kb          domain.KnowledgeBaseUseCase
	orgSettings domain.OrgSettingsRepository
	permissions TemplatePermissionSeeder
	factories   TemplateRepoFactories
	// schemaInvalidator drops the workflow-builder's cached object schema. Optional.
	schemaInvalidator func(orgID uuid.UUID)
	// kbCacheBuster drops the assistant's cached system prompt. Optional, but
	// without it a template's AI persona sits behind a 30-minute cache and the
	// customer's assistant keeps its old identity long after they applied.
	kbCacheBuster domain.SchemaCacheBuster
}

// SetKBCacheBuster wires the AI prompt cache invalidator (the KnowledgeBuilder).
func (uc *systemTemplateUseCase) SetKBCacheBuster(b domain.SchemaCacheBuster) {
	uc.kbCacheBuster = b
}

func NewSystemTemplateUseCase(
	db *gorm.DB,
	repo domain.SystemTemplateRepository,
	kb domain.KnowledgeBaseUseCase,
	orgSettings domain.OrgSettingsRepository,
	permissions TemplatePermissionSeeder,
	factories TemplateRepoFactories,
) domain.SystemTemplateUseCase {
	return &systemTemplateUseCase{
		db: db, repo: repo, kb: kb, orgSettings: orgSettings,
		permissions: permissions, factories: factories,
	}
}

func (uc *systemTemplateUseCase) SetSchemaInvalidator(fn func(orgID uuid.UUID)) {
	uc.schemaInvalidator = fn
}

// ============================================================
// Reads
// ============================================================

func (uc *systemTemplateUseCase) List(ctx context.Context, orgID uuid.UUID) ([]domain.SystemTemplateView, error) {
	rows, err := uc.repo.List(ctx, true)
	if err != nil {
		return nil, domain.ErrInternal
	}
	applied, err := uc.repo.ListApplications(ctx, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	appliedSlugs := make(map[string]bool, len(applied))
	for _, a := range applied {
		appliedSlugs[a.TemplateSlug] = true
	}

	out := make([]domain.SystemTemplateView, 0, len(rows))
	for i := range rows {
		v := toView(&rows[i])
		v.Applied = appliedSlugs[rows[i].Slug]
		out = append(out, v)
	}
	return out, nil
}

func (uc *systemTemplateUseCase) Get(ctx context.Context, orgID uuid.UUID, slug string) (*domain.SystemTemplateDetail, error) {
	row, err := uc.repo.GetBySlug(ctx, slug)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if row == nil {
		return nil, domain.ErrTemplateNotFound
	}
	spec, err := decodeSpec(row)
	if err != nil {
		return nil, domain.ErrInternal
	}

	view := toView(row)
	if app, err := uc.repo.GetApplication(ctx, orgID, slug); err == nil && app != nil {
		view.Applied = true
	}
	detail := &domain.SystemTemplateDetail{
		SystemTemplateView: view,
		Stages:             spec.stages,
		Objects:            spec.objects,
		Fields:             spec.fields,
		Workflows:          spec.workflows,
		KBSections:         spec.kb,
	}
	if row.AIContext != nil {
		detail.AIContext = *row.AIContext
	}
	return detail, nil
}

func (uc *systemTemplateUseCase) ListApplied(ctx context.Context, orgID uuid.UUID) ([]domain.OrgTemplateApplication, error) {
	out, err := uc.repo.ListApplications(ctx, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	return out, nil
}

// ============================================================
// Spec decoding
// ============================================================

type templateSpec struct {
	stages    []domain.TemplateStage
	fields    []domain.TemplateFieldDef
	objects   []domain.TemplateObjectDef
	workflows []domain.TemplateWorkflow
	kb        map[string]string
}

// decodeSpec is lenient by design: a column that predates the current shape (or a
// row hand-edited in the DB) decodes to an empty slice rather than failing the
// whole template. The seed-time validator is where malformed content is caught.
func decodeSpec(row *domain.SystemTemplate) (*templateSpec, error) {
	s := &templateSpec{kb: map[string]string{}}

	// Each field is decoded independently and RESET on failure. Discarding the
	// error is not enough: encoding/json populates a slice element-by-element and
	// leaves everything decoded so far in place when it hits a bad one. A 2022 row
	// storing pipeline_stages as bare strings (`["New Lead","Qualified"]`) therefore
	// yields two zero-valued stages — which would reach the create loop as stages
	// with empty names — rather than the "no stages" it looks like it yields.
	decode := func(raw domain.JSON, fallback string, dst interface{}, reset func()) {
		if err := json.Unmarshal(nonEmptyJSON(raw, fallback), dst); err != nil {
			reset()
		}
	}
	decode(row.PipelineStages, "[]", &s.stages, func() { s.stages = nil })
	decode(row.CustomFieldDefs, "[]", &s.fields, func() { s.fields = nil })
	decode(row.ObjectDefs, "[]", &s.objects, func() { s.objects = nil })
	decode(row.AutomationRules, "[]", &s.workflows, func() { s.workflows = nil })
	decode(row.KBTemplates, "{}", &s.kb, func() { s.kb = nil })

	if s.kb == nil {
		s.kb = map[string]string{}
	}
	return s, nil
}

// nonEmptyJSON guards the two ways a JSONB column reaches us unusable: absent
// (nil, e.g. a column added after the row) and SQL NULL (literal "null").
func nonEmptyJSON(raw domain.JSON, fallback string) []byte {
	if len(raw) == 0 || string(raw) == "null" {
		return []byte(fallback)
	}
	return []byte(raw)
}

func toView(row *domain.SystemTemplate) domain.SystemTemplateView {
	spec, _ := decodeSpec(row)
	fieldCount := len(spec.fields)
	for _, o := range spec.objects {
		fieldCount += len(o.Fields)
	}
	return domain.SystemTemplateView{
		Slug:          row.Slug,
		Name:          row.Name,
		Category:      row.Category,
		Description:   row.Description,
		Icon:          row.Icon,
		SortOrder:     row.SortOrder,
		StageCount:    len(spec.stages),
		ObjectCount:   len(spec.objects),
		FieldCount:    fieldCount,
		WorkflowCount: len(spec.workflows),
		HasKB:         len(spec.kb) > 0,
	}
}

// ============================================================
// Apply
// ============================================================

// Apply installs a template into an org.
//
// Two phases, deliberately not one transaction:
//
//	Phase A (atomic) — system-object seeding, pipeline stages, custom objects and
//	their fields. These are structurally interdependent: a half-built object whose
//	relation field dangles is worse than nothing, so any error rolls all of it back.
//
//	Phase B (best-effort, post-commit) — KB sections, org settings, workflows. Each
//	is independently meaningful and some open their own transactions or bust caches,
//	so a failure here degrades the result to "partial" rather than throwing away a
//	perfectly good schema install.
//
// Everything is ADDITIVE. Nothing this function does deletes or overwrites a
// customer's existing data; every collision is a reported skip.
func (uc *systemTemplateUseCase) Apply(ctx context.Context, orgID, userID uuid.UUID, slug string, force bool) (*domain.TemplateApplyResult, error) {
	row, err := uc.repo.GetBySlug(ctx, slug)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if row == nil {
		return nil, domain.ErrTemplateNotFound
	}
	if !row.IsActive && !force {
		return nil, domain.ErrTemplateNotFound
	}

	// Ledger check. A repeat apply is a no-op that replays the stored report, so
	// double-clicking the button cannot install anything twice.
	if !force {
		if prev, err := uc.repo.GetApplication(ctx, orgID, slug); err == nil && prev != nil {
			var stored domain.TemplateApplyResult
			if len(prev.Result) > 0 {
				_ = json.Unmarshal(prev.Result, &stored)
			}
			stored.TemplateSlug = slug
			stored.Status = domain.TemplateApplyAlreadyApplied
			stored.SpecVersion = prev.SpecVersion
			return &stored, nil
		}
	}

	spec, err := decodeSpec(row)
	if err != nil {
		return nil, domain.ErrInternal
	}

	result := &domain.TemplateApplyResult{
		TemplateSlug: slug,
		SpecVersion:  row.SpecVersion,
		Items:        []domain.TemplateApplyItem{},
	}

	// ---------- Phase A ----------
	createdObjects := map[string]bool{}
	phaseAErr := uc.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		registryRepo := uc.factories.ObjectRegistry(tx)
		// Must run first: CreateDef's duplicate probe filters is_system = false, so
		// without the system objects present a template could insert a shadow object
		// on a reserved slug that RecordService silently ignores.
		if err := registryRepo.EnsureSystemObjects(ctx, orgID); err != nil {
			return err
		}

		if err := uc.applyStages(ctx, tx, orgID, spec.stages, result); err != nil {
			return err
		}
		if err := uc.applyObjects(ctx, tx, orgID, spec.objects, result, createdObjects); err != nil {
			return err
		}
		return uc.applySystemFields(ctx, tx, orgID, spec.fields, result)
	})
	if phaseAErr != nil {
		result.Status = domain.TemplateApplyFailed
		result.Items = append(result.Items, domain.TemplateApplyItem{
			Kind: "schema", Key: slug, Status: domain.TemplateItemFailed, Error: phaseAErr.Error(),
		})
		uc.recordApplication(ctx, orgID, userID, row, result)
		return result, domain.ErrInternal
	}

	// New objects are invisible until OLS rows exist and the cached deny is dropped.
	if uc.permissions != nil {
		if err := uc.permissions.EnsureSeeded(ctx, orgID); err != nil {
			result.Warnings = append(result.Warnings,
				"Object permissions could not be seeded automatically; an admin may need to grant access to the new objects.")
		}
		uc.permissions.Invalidate(orgID)
	}
	if uc.schemaInvalidator != nil {
		uc.schemaInvalidator(orgID)
	}

	// ---------- Phase B ----------
	uc.applyKB(ctx, orgID, userID, spec.kb, result)
	uc.applyOrgSettings(ctx, orgID, slug, row, result)
	uc.applyWorkflows(ctx, orgID, userID, spec.workflows, result)

	result.Status = domain.TemplateApplyApplied
	for _, it := range result.Items {
		if it.Status == domain.TemplateItemFailed {
			result.Status = domain.TemplateApplyPartial
			break
		}
	}
	uc.recordApplication(ctx, orgID, userID, row, result)
	return result, nil
}

// applyStages appends the template's stages, or replaces the untouched seed when
// it is safe to do so.
//
// Replace is only safe when the pipeline is still the factory default AND the org
// has no deals. Stage deletion is a SOFT delete, so the ON DELETE SET NULL foreign
// key on deals.stage_id never fires — a delete under live deals leaves every one of
// them pointing at a stage that no longer appears in any list.
func (uc *systemTemplateUseCase) applyStages(
	ctx context.Context, tx *gorm.DB, orgID uuid.UUID,
	stages []domain.TemplateStage, result *domain.TemplateApplyResult,
) error {
	if len(stages) == 0 {
		return nil
	}

	var existing []domain.PipelineStage
	if err := tx.WithContext(ctx).Where("org_id = ?", orgID).Order("position ASC").Find(&existing).Error; err != nil {
		return err
	}

	// Deliberately NOT DealUseCase.Count: that applies the caller's row-access
	// scope, so an own-scoped user would see 0 for an org with thousands of deals
	// and we would wipe the pipeline out from under them.
	var dealCount int64
	if err := tx.WithContext(ctx).Model(&domain.Deal{}).Where("org_id = ?", orgID).Count(&dealCount).Error; err != nil {
		return err
	}

	replace := dealCount == 0 && isUntouchedSeedPipeline(existing)
	if replace {
		for i := range existing {
			if err := tx.WithContext(ctx).
				Where("id = ? AND org_id = ?", existing[i].ID, orgID).
				Delete(&domain.PipelineStage{}).Error; err != nil {
				return err
			}
		}
		existing = nil
	}

	taken := make(map[string]bool, len(existing))
	maxPos := -1
	for _, s := range existing {
		taken[strings.ToLower(strings.TrimSpace(s.Name))] = true
		if s.Position > maxPos {
			maxPos = s.Position
		}
	}

	for _, st := range stages {
		key := strings.ToLower(strings.TrimSpace(st.Name))
		if taken[key] {
			result.Items = append(result.Items, domain.TemplateApplyItem{
				Kind: "pipeline_stage", Key: st.Name, Status: domain.TemplateItemSkipped, Reason: "name_exists",
			})
			continue
		}
		color := st.Color
		if color == "" {
			color = "#3B82F6"
		}
		// Positions must stay dense and must not collide with what is already there.
		pos := st.Position
		if !replace {
			pos = maxPos + 1
			maxPos++
		}
		row := &domain.PipelineStage{
			OrgID: orgID, Name: st.Name, Position: pos,
			Color: color, IsWon: st.IsWon, IsLost: st.IsLost,
		}
		if err := tx.WithContext(ctx).Create(row).Error; err != nil {
			return err
		}
		taken[key] = true
		result.Items = append(result.Items, domain.TemplateApplyItem{
			Kind: "pipeline_stage", Key: st.Name, Status: domain.TemplateItemCreated, ID: row.ID.String(),
		})
	}
	return nil
}

// seededStageNames mirrors defaultPipelineStages in auth_usecase.go and
// SEEDED_STAGE_NAMES in the frontend's useSetupChecklist.ts. Three copies of one
// list is not ideal, but the alternative here is importing across layers.
var seededStageNames = []string{"Lead In", "Qualified", "Proposal", "Negotiation", "Closed Won"}

// isUntouchedSeedPipeline mirrors the frontend's isPipelineCustomized, inverted.
// An EMPTY pipeline is not "untouched seed" — there is nothing to replace, so the
// append path handles it correctly either way.
func isUntouchedSeedPipeline(stages []domain.PipelineStage) bool {
	if len(stages) != len(seededStageNames) {
		return false
	}
	for i, s := range stages {
		if strings.TrimSpace(s.Name) != seededStageNames[i] {
			return false
		}
	}
	return true
}

// applyObjects creates the template's custom objects in two passes.
//
// Pass 1 creates each object with only its non-relation fields; pass 2 adds the
// relation fields once every sibling exists. This is required because the
// custom-object path does not validate target_slug at all — a forward reference
// would persist silently as a dangling relation instead of erroring.
func (uc *systemTemplateUseCase) applyObjects(
	ctx context.Context, tx *gorm.DB, orgID uuid.UUID,
	objects []domain.TemplateObjectDef, result *domain.TemplateApplyResult, created map[string]bool,
) error {
	if len(objects) == 0 {
		return nil
	}
	objUC := NewCustomObjectUseCase(uc.factories.CustomObject(tx))

	// Pass 1.
	for _, o := range objects {
		existing, err := objUC.GetDefBySlug(ctx, orgID, o.Slug)
		if err == nil && existing != nil {
			result.Items = append(result.Items, domain.TemplateApplyItem{
				Kind: "object_def", Key: o.Slug, Status: domain.TemplateItemSkipped, Reason: "slug_exists",
			})
			continue
		}
		nonRelation := make([]domain.CustomFieldDef, 0, len(o.Fields))
		for _, f := range o.Fields {
			if f.Type == "relation" {
				continue
			}
			nonRelation = append(nonRelation, toCustomFieldDef(f))
		}
		fieldsJSON, err := json.Marshal(nonRelation)
		if err != nil {
			return err
		}
		def, err := objUC.CreateDef(ctx, orgID, domain.CreateObjectDefInput{
			Slug: o.Slug, Label: o.Label, LabelPlural: o.LabelPlural,
			Icon: o.Icon, Searchable: o.Searchable, Fields: domain.JSON(fieldsJSON),
		})
		if err != nil {
			return fmt.Errorf("object %s: %w", o.Slug, err)
		}
		created[o.Slug] = true
		result.Items = append(result.Items, domain.TemplateApplyItem{
			Kind: "object_def", Key: o.Slug, Status: domain.TemplateItemCreated, ID: def.ID.String(),
		})
	}

	// Pass 2 — only for objects THIS apply created. UpdateDef reconciles fields by
	// key and deletes any not in the payload, so touching a pre-existing object here
	// would destroy the customer's own fields.
	for _, o := range objects {
		if !created[o.Slug] {
			continue
		}
		hasRelation := false
		for _, f := range o.Fields {
			if f.Type == "relation" {
				hasRelation = true
				break
			}
		}
		if !hasRelation {
			continue
		}
		all := make([]domain.CustomFieldDef, 0, len(o.Fields))
		for _, f := range o.Fields {
			all = append(all, toCustomFieldDef(f))
		}
		fieldsJSON, err := json.Marshal(all)
		if err != nil {
			return err
		}
		raw := domain.JSON(fieldsJSON)
		if _, err := objUC.UpdateDef(ctx, orgID, o.Slug, domain.UpdateObjectDefInput{Fields: raw}); err != nil {
			return fmt.Errorf("object %s relations: %w", o.Slug, err)
		}
	}
	return nil
}

func toCustomFieldDef(f domain.TemplateObjectField) domain.CustomFieldDef {
	return domain.CustomFieldDef{
		Key: f.Key, Label: f.Label, Type: f.Type, Options: f.Options,
		TargetSlug: f.TargetSlug, ViaField: f.ViaField, SourceField: f.SourceField,
		Required: f.Required, Position: f.Position,
	}
}

// applySystemFields adds the template's fields to contact/company/deal. A key that
// already exists is a skip, never an overwrite.
func (uc *systemTemplateUseCase) applySystemFields(
	ctx context.Context, tx *gorm.DB, orgID uuid.UUID,
	fields []domain.TemplateFieldDef, result *domain.TemplateApplyResult,
) error {
	if len(fields) == 0 {
		return nil
	}
	settingsUC := NewOrgSettingsUseCase(uc.factories.ObjectRegistry(tx))

	for _, f := range fields {
		existing, err := settingsUC.GetFieldDefs(ctx, orgID, f.EntityType)
		if err == nil {
			clash := false
			for _, e := range existing {
				if e.Key == f.Key {
					clash = true
					break
				}
			}
			if clash {
				result.Items = append(result.Items, domain.TemplateApplyItem{
					Kind: "field_def", Key: f.EntityType + "." + f.Key,
					Status: domain.TemplateItemSkipped, Reason: "key_exists",
				})
				continue
			}
		}
		pos := f.Position
		if _, err := settingsUC.CreateFieldDef(ctx, orgID, domain.CreateFieldDefInput{
			Key: f.Key, Label: f.Label, Type: f.Type, EntityType: f.EntityType,
			Options: f.Options, TargetSlug: f.TargetSlug, ViaField: f.ViaField,
			SourceField: f.SourceField, Required: f.Required, Position: &pos,
		}); err != nil {
			return fmt.Errorf("field %s.%s: %w", f.EntityType, f.Key, err)
		}
		result.Items = append(result.Items, domain.TemplateApplyItem{
			Kind: "field_def", Key: f.EntityType + "." + f.Key, Status: domain.TemplateItemCreated,
		})
	}
	return nil
}

// applyKB writes only sections that are absent or blank. Overwriting a customer's
// own knowledge-base prose with placeholder text is the single most destructive
// thing this feature could do, so it never happens.
func (uc *systemTemplateUseCase) applyKB(
	ctx context.Context, orgID, userID uuid.UUID,
	sections map[string]string, result *domain.TemplateApplyResult,
) {
	if uc.kb == nil || len(sections) == 0 {
		return
	}
	for section, content := range sections {
		title, ok := domain.ValidKBSections[section]
		if !ok || strings.TrimSpace(content) == "" {
			continue
		}
		if existing, err := uc.kb.GetSection(ctx, orgID, section); err == nil && existing != nil {
			if strings.TrimSpace(existing.Content) != "" {
				result.Items = append(result.Items, domain.TemplateApplyItem{
					Kind: "kb_section", Key: section, Status: domain.TemplateItemSkipped, Reason: "already_written",
				})
				continue
			}
		}
		if _, err := uc.kb.UpsertSection(ctx, orgID, userID, section, domain.UpsertKBInput{
			Title: title, Content: content,
		}); err != nil {
			result.Items = append(result.Items, domain.TemplateApplyItem{
				Kind: "kb_section", Key: section, Status: domain.TemplateItemFailed, Error: err.Error(),
			})
			continue
		}
		result.Items = append(result.Items, domain.TemplateApplyItem{
			Kind: "kb_section", Key: section, Status: domain.TemplateItemCreated,
		})
	}
}

// applyOrgSettings records which template shaped this workspace and installs the
// AI persona. industry_template_slug is written only when unset — it is
// single-valued, and the ledger is the real source of truth for multiple applies.
func (uc *systemTemplateUseCase) applyOrgSettings(
	ctx context.Context, orgID uuid.UUID, slug string,
	row *domain.SystemTemplate, result *domain.TemplateApplyResult,
) {
	if uc.orgSettings == nil {
		return
	}
	current, err := uc.orgSettings.GetByOrgID(ctx, orgID)
	if err != nil {
		return
	}
	settings := current
	if settings == nil {
		settings = &domain.OrgSettings{OrgID: orgID}
	}
	changed := false
	if settings.IndustryTemplateSlug == nil || *settings.IndustryTemplateSlug == "" {
		s := slug
		settings.IndustryTemplateSlug = &s
		changed = true
	}
	// Never clobber a persona the customer has written for themselves.
	if row.AIContext != nil && strings.TrimSpace(*row.AIContext) != "" &&
		(settings.AIContextOverride == nil || strings.TrimSpace(*settings.AIContextOverride) == "") {
		c := *row.AIContext
		settings.AIContextOverride = &c
		changed = true
	}
	if !changed {
		return
	}
	if err := uc.orgSettings.Upsert(ctx, settings); err != nil {
		result.Items = append(result.Items, domain.TemplateApplyItem{
			Kind: "org_settings", Key: slug, Status: domain.TemplateItemFailed, Error: err.Error(),
		})
		return
	}
	// The assistant's system prompt is cached for 30 minutes and the persona is not
	// part of its cache key, so without this the customer would apply a template and
	// keep talking to the old assistant for half an hour.
	if uc.kbCacheBuster != nil {
		uc.kbCacheBuster.BustCache(ctx, orgID)
	}
	result.Items = append(result.Items, domain.TemplateApplyItem{
		Kind: "org_settings", Key: slug, Status: domain.TemplateItemCreated,
	})
}

// applyWorkflows creates the template's automations.
//
// Activation policy: a workflow is switched ON only when it asked for it AND every
// action in its tree is on the auto-activatable allow-list. Anything containing an
// outbound action — including notify_user, which can send real email depending on
// the recipient's preferences — is created switched OFF and reported as
// needs_review. A template that starts emailing a customer's contacts seconds after
// they create a workspace is the worst failure mode this feature has.
func (uc *systemTemplateUseCase) applyWorkflows(
	ctx context.Context, orgID, userID uuid.UUID,
	workflows []domain.TemplateWorkflow, result *domain.TemplateApplyResult,
) {
	if len(workflows) == 0 {
		return
	}
	repo := automation.NewRepository(uc.db)

	// Stage-name → id map for trigger resolution below. Loaded once, after Phase A
	// has committed, so it includes the stages this apply just created.
	stageIDs := map[string]string{}
	var stages []domain.PipelineStage
	if err := uc.db.WithContext(ctx).Where("org_id = ?", orgID).Find(&stages).Error; err == nil {
		for _, s := range stages {
			stageIDs[strings.ToLower(strings.TrimSpace(s.Name))] = s.ID.String()
		}
	}

	for _, w := range workflows {
		// Name collision = skip. There is no unique index, so this is a probe.
		var count int64
		if err := uc.db.WithContext(ctx).Model(&automation.Workflow{}).
			Where("org_id = ? AND name = ? AND deleted_at IS NULL", orgID, w.Name).
			Count(&count).Error; err == nil && count > 0 {
			result.Items = append(result.Items, domain.TemplateApplyItem{
				Kind: "workflow", Key: w.Key, Status: domain.TemplateItemSkipped, Reason: "name_exists",
			})
			continue
		}

		var steps []automation.StepSpec
		if err := json.Unmarshal(nonEmptyJSON(w.Steps, "[]"), &steps); err != nil {
			result.Items = append(result.Items, domain.TemplateApplyItem{
				Kind: "workflow", Key: w.Key, Status: domain.TemplateItemFailed, Error: "steps are not valid JSON",
			})
			continue
		}

		// Actions is NOT NULL and must be derived, never hand-written. A nil flatten
		// result (condition-only tree) has to become [] or the insert violates the column.
		actions := automation.FlattenStepsToActions(steps)
		if actions == nil {
			actions = []automation.ActionSpec{}
		}
		params, resolveErr := resolveTriggerParams(w.Trigger, stageIDs)
		if resolveErr != nil {
			result.Items = append(result.Items, domain.TemplateApplyItem{
				Kind: "workflow", Key: w.Key, Status: domain.TemplateItemFailed, Error: resolveErr.Error(),
			})
			continue
		}
		triggerJSON, err := json.Marshal(map[string]interface{}{
			"type":   w.Trigger.Type,
			"params": params,
		})
		if err != nil {
			result.Items = append(result.Items, domain.TemplateApplyItem{
				Kind: "workflow", Key: w.Key, Status: domain.TemplateItemFailed, Error: err.Error(),
			})
			continue
		}
		actionsJSON, _ := json.Marshal(actions)
		stepsJSON, _ := json.Marshal(steps)

		if vr := automation.ValidateWorkflowPayload(triggerJSON, nil, actionsJSON, stepsJSON); vr != nil && !vr.Valid {
			msgs := make([]string, 0, len(vr.Errors))
			for _, e := range vr.Errors {
				msgs = append(msgs, e.Field+": "+e.Message)
			}
			result.Items = append(result.Items, domain.TemplateApplyItem{
				Kind: "workflow", Key: w.Key, Status: domain.TemplateItemFailed,
				Error: "invalid workflow: " + strings.Join(msgs, "; "),
			})
			continue
		}

		active, reason := shouldActivate(w, actions)
		wf := &automation.Workflow{
			OrgID:       orgID,
			Name:        w.Name,
			Description: w.Description,
			IsActive:    active,
			Trigger:     triggerJSON,
			Actions:     actionsJSON,
			Steps:       stepsJSON,
			// CreatedBy is the workflow's SECURITY PRINCIPAL at run time — an
			// unresolvable author degrades to a restricted caller under which record
			// writes are denied while outbound actions still fire. It must be a real user.
			CreatedBy: userID,
		}
		if err := repo.CreateWorkflow(ctx, wf); err != nil {
			result.Items = append(result.Items, domain.TemplateApplyItem{
				Kind: "workflow", Key: w.Key, Status: domain.TemplateItemFailed, Error: err.Error(),
			})
			continue
		}

		item := domain.TemplateApplyItem{
			Kind: "workflow", Key: w.Key, Status: domain.TemplateItemCreated, ID: wf.ID.String(),
		}
		if !active {
			item.Status = domain.TemplateItemNeedsReview
			item.Reason = reason
		}
		result.Items = append(result.Items, item)
	}
}

// shouldActivate implements the activation policy. Returns the decision and, when
// negative, the reason to surface to the user.
func shouldActivate(w domain.TemplateWorkflow, actions []automation.ActionSpec) (bool, string) {
	if w.Activation != domain.TemplateActivationAuto {
		return false, "template asks for manual activation"
	}
	// Timer-backed triggers are armed by the HTTP handler, not by anything below it.
	// Activating one here would produce a workflow that looks live and never fires.
	if w.Trigger.Type == "schedule" || w.Trigger.Type == "date_field" {
		return false, "time-based triggers must be switched on from the builder so their timers are armed"
	}
	for _, a := range actions {
		if !domain.IsAutoActivatableAction(a.Type) {
			return false, "contains an action that can reach outside the CRM (" + a.Type + ")"
		}
	}
	return true, ""
}

// resolveTriggerParams turns author-friendly trigger params into what the engine
// actually matches on.
//
// The engine compares to_stage/from_stage against a stage UUID, which a template
// cannot possibly know: stages are created per-org at apply time. So a template
// may write either the wildcard "*" (any stage) or a stage NAME, and this resolves
// the name against the stages the org now has — including the ones this apply just
// created. An unresolvable name fails the workflow loudly rather than silently
// installing an automation that can never match anything.
func resolveTriggerParams(trigger domain.TemplateTrigger, stageIDs map[string]string) (map[string]interface{}, error) {
	params := map[string]interface{}{}
	if len(trigger.Params) > 0 && string(trigger.Params) != "null" {
		if err := json.Unmarshal(trigger.Params, &params); err != nil {
			return nil, fmt.Errorf("trigger params are not valid JSON")
		}
	}
	for _, key := range []string{"to_stage", "from_stage"} {
		raw, ok := params[key].(string)
		if !ok || raw == "" || raw == "*" {
			continue
		}
		if _, err := uuid.Parse(raw); err == nil {
			continue // already an id
		}
		id, found := stageIDs[strings.ToLower(strings.TrimSpace(raw))]
		if !found {
			return nil, fmt.Errorf("%s references stage %q, which this workspace does not have", key, raw)
		}
		params[key] = id
	}
	return params, nil
}

func (uc *systemTemplateUseCase) recordApplication(
	ctx context.Context, orgID, userID uuid.UUID,
	row *domain.SystemTemplate, result *domain.TemplateApplyResult,
) {
	payload, err := json.Marshal(result)
	if err != nil {
		payload = []byte("{}")
	}
	_ = uc.repo.RecordApplication(ctx, &domain.OrgTemplateApplication{
		OrgID:        orgID,
		TemplateSlug: row.Slug,
		SpecVersion:  row.SpecVersion,
		Status:       result.Status,
		Result:       domain.JSON(payload),
		AppliedBy:    userID,
	})
}
