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
// As of the P7 cutover, object_fields is the single field-def store, so the
// registry reads everything from one place — no blob merge:
//
//   - System objects (contact/deal/company): all fields (native + admin-defined
//     custom) from object_fields.
//   - Custom objects: read straight from custom_object_defs.
type objectRegistryUseCase struct {
	repo       domain.ObjectRegistryRepository
	customRepo domain.CustomObjectRepository
}

func NewObjectRegistryUseCase(
	repo domain.ObjectRegistryRepository,
	customRepo domain.CustomObjectRepository,
) domain.ObjectRegistryUseCase {
	return &objectRegistryUseCase{
		repo:       repo,
		customRepo: customRepo,
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

	summaries := make([]domain.ObjectSummary, 0, len(defs))
	for _, d := range defs {
		// object_fields now holds every field (native + custom), so the count is
		// complete with no separate blob read.
		fieldCount := counts[d.ID]
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

// systemSchema assembles a system object's descriptor from object_fields, which
// (post-P7) holds both native and admin-defined custom fields in one ordered list.
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
