package usecase

import (
	"context"
	"encoding/json"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// objectRegistryUseCase assembles one uniform descriptor for every object.
//
// In P2 the registry merges two live sources at read time, with no data
// duplication:
//
//   - System objects (contact/deal/company): native fields from object_fields
//     plus their admin-defined custom fields from org_settings.custom_field_defs.
//   - Custom objects: read straight from custom_object_defs.
//
// This keeps the existing custom-object write path untouched (no dual-write,
// avoiding the lost-update risk R6) until the P7 backfill.
type objectRegistryUseCase struct {
	repo          domain.ObjectRegistryRepository
	customRepo    domain.CustomObjectRepository
	orgSettingsUC domain.OrgSettingsUseCase
}

func NewObjectRegistryUseCase(
	repo domain.ObjectRegistryRepository,
	customRepo domain.CustomObjectRepository,
	orgSettingsUC domain.OrgSettingsUseCase,
) domain.ObjectRegistryUseCase {
	return &objectRegistryUseCase{
		repo:          repo,
		customRepo:    customRepo,
		orgSettingsUC: orgSettingsUC,
	}
}

// ListObjects returns every object (system first, then custom) as summaries.
func (uc *objectRegistryUseCase) ListObjects(ctx context.Context, orgID uuid.UUID) ([]domain.ObjectSummary, error) {
	if err := uc.repo.EnsureSystemObjects(ctx, orgID); err != nil {
		return nil, err
	}

	defs, err := uc.repo.ListDefs(ctx, orgID)
	if err != nil {
		return nil, err
	}
	counts, err := uc.repo.FieldCounts(ctx, orgID)
	if err != nil {
		return nil, err
	}

	// One read of all admin-defined custom fields, counted per entity_type, so
	// system objects can surface their custom fields without an N+1 of reads.
	customFieldCounts := map[string]int{}
	if allCustom, err := uc.orgSettingsUC.GetFieldDefs(ctx, orgID, ""); err == nil {
		for _, cd := range allCustom {
			customFieldCounts[cd.EntityType]++
		}
	}

	summaries := make([]domain.ObjectSummary, 0, len(defs))
	for _, d := range defs {
		fieldCount := counts[d.ID]
		// System objects also surface their admin-defined custom fields.
		if d.IsSystem {
			fieldCount += customFieldCounts[d.Slug]
		}
		summaries = append(summaries, domain.ObjectSummary{
			Slug:        d.Slug,
			Label:       d.Label,
			LabelPlural: d.LabelPlural,
			Icon:        d.Icon,
			Color:       d.Color,
			IsSystem:    d.IsSystem,
			FieldCount:  fieldCount,
			Searchable:  d.Searchable,
		})
	}

	// Append custom objects, which still live in custom_object_defs.
	customObjs, err := uc.customRepo.ListDefs(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for _, co := range customObjs {
		summaries = append(summaries, domain.ObjectSummary{
			Slug:        co.Slug,
			Label:       co.Label,
			LabelPlural: co.LabelPlural,
			Icon:        co.Icon,
			Color:       "#6B7280",
			IsSystem:    false,
			FieldCount:  len(parseFieldDefs(co.Fields)),
			Searchable:  co.Searchable,
		})
	}

	return summaries, nil
}

// GetSchema returns the full descriptor for one object by slug. A system slug
// takes precedence over a custom object that happens to reuse it.
func (uc *objectRegistryUseCase) GetSchema(ctx context.Context, orgID uuid.UUID, slug string) (*domain.ObjectDescriptor, error) {
	if err := uc.repo.EnsureSystemObjects(ctx, orgID); err != nil {
		return nil, err
	}

	if def, err := uc.repo.GetDefBySlug(ctx, orgID, slug); err != nil {
		return nil, err
	} else if def != nil {
		return uc.systemSchema(ctx, orgID, def)
	}

	if co, err := uc.customRepo.GetDefBySlug(ctx, orgID, slug); err != nil {
		return nil, err
	} else if co != nil {
		return customSchema(co), nil
	}

	return nil, &domain.AppError{Code: http.StatusNotFound, Message: "object not found"}
}

// systemSchema assembles a system object's descriptor: native fields from the
// registry, then admin-defined custom fields from org_settings.
func (uc *objectRegistryUseCase) systemSchema(ctx context.Context, orgID uuid.UUID, def *domain.ObjectDef) (*domain.ObjectDescriptor, error) {
	fields, err := uc.repo.ListFields(ctx, def.ID)
	if err != nil {
		return nil, err
	}

	descriptor := &domain.ObjectDescriptor{
		Slug:        def.Slug,
		Label:       def.Label,
		LabelPlural: def.LabelPlural,
		Icon:        def.Icon,
		Color:       def.Color,
		IsSystem:    true,
		Searchable:  def.Searchable,
		Fields:      make([]domain.FieldDescriptor, 0, len(fields)),
	}

	for _, f := range fields {
		fd := domain.FieldDescriptor{
			Key:      f.Key,
			Label:    f.Label,
			Type:     f.Type,
			Options:  parseOptions(f.Options),
			IsSystem: f.IsSystem,
			Required: f.IsRequired,
			Unique:   f.IsUnique,
		}
		if f.TargetSlug != nil {
			fd.TargetSlug = *f.TargetSlug
		}
		descriptor.Fields = append(descriptor.Fields, fd)
		// Resolve the display field's key from its id.
		if def.DisplayFieldID != nil && f.ID == *def.DisplayFieldID {
			descriptor.DisplayField = f.Key
		}
	}

	// Append admin-defined custom fields for this system object.
	if customDefs, err := uc.orgSettingsUC.GetFieldDefs(ctx, orgID, def.Slug); err == nil {
		for _, cd := range customDefs {
			descriptor.Fields = append(descriptor.Fields, fieldDescriptorFromDef(cd))
		}
	}

	if descriptor.DisplayField == "" && len(descriptor.Fields) > 0 {
		descriptor.DisplayField = descriptor.Fields[0].Key
	}
	return descriptor, nil
}

// customSchema assembles a custom object's descriptor from custom_object_defs.
func customSchema(co *domain.CustomObjectDef) *domain.ObjectDescriptor {
	defs := parseFieldDefs(co.Fields)
	descriptor := &domain.ObjectDescriptor{
		Slug:        co.Slug,
		Label:       co.Label,
		LabelPlural: co.LabelPlural,
		Icon:        co.Icon,
		Color:       "#6B7280",
		IsSystem:    false,
		Searchable:  co.Searchable,
		Fields:      make([]domain.FieldDescriptor, 0, len(defs)),
	}
	for _, cd := range defs {
		descriptor.Fields = append(descriptor.Fields, fieldDescriptorFromDef(cd))
	}
	descriptor.DisplayField = customDisplayField(defs)
	return descriptor
}

// ============================================================
// Helpers
// ============================================================

func fieldDescriptorFromDef(cd domain.CustomFieldDef) domain.FieldDescriptor {
	return domain.FieldDescriptor{
		Key:      cd.Key,
		Label:    cd.Label,
		Type:     cd.Type,
		Options:  cd.Options,
		IsSystem: false,
		Required: cd.Required,
	}
}

// customDisplayField mirrors the record-level display heuristic: first text
// field, else first field, else empty.
func customDisplayField(defs []domain.CustomFieldDef) string {
	for _, d := range defs {
		if d.Type == "text" {
			return d.Key
		}
	}
	if len(defs) > 0 {
		return defs[0].Key
	}
	return ""
}

func parseFieldDefs(raw domain.JSON) []domain.CustomFieldDef {
	if len(raw) == 0 {
		return nil
	}
	var defs []domain.CustomFieldDef
	if err := json.Unmarshal(raw, &defs); err != nil {
		return nil
	}
	return defs
}

func parseOptions(raw domain.JSON) []string {
	if len(raw) == 0 {
		return nil
	}
	var opts []string
	if err := json.Unmarshal(raw, &opts); err != nil {
		return nil
	}
	return opts
}
