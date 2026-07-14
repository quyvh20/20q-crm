package usecase

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// objectRegistryUseCase assembles one uniform descriptor for every object.
//
// As of the P7 convergence, object_defs/object_fields is the single store for EVERY
// object — system (contact/deal/company) and custom alike — so the registry reads
// everything from one place. There is no separate custom_object_defs merge anymore.
type objectRegistryUseCase struct {
	repo domain.ObjectRegistryRepository
}

func NewObjectRegistryUseCase(repo domain.ObjectRegistryRepository) domain.ObjectRegistryUseCase {
	return &objectRegistryUseCase{repo: repo}
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
		summaries = append(summaries, domain.ObjectSummary{
			Slug:        d.Slug,
			Label:       d.Label,
			LabelPlural: d.LabelPlural,
			Icon:        d.Icon,
			Color:       d.Color,
			IsSystem:    d.IsSystem,
			FieldCount:  counts[d.ID],
			Searchable:  d.Searchable,
		})
	}
	return summaries, nil
}

// GetSchema returns the full descriptor for one object by slug. ListDefs orders
// system objects first, but slug is unique per org so the lookup is unambiguous.
func (uc *objectRegistryUseCase) GetSchema(ctx context.Context, orgID uuid.UUID, slug string) (*domain.ObjectDescriptor, error) {
	if err := uc.repo.EnsureSystemObjects(ctx, orgID); err != nil {
		return nil, err
	}

	def, err := uc.repo.GetDefBySlug(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, &domain.AppError{Code: http.StatusNotFound, Message: "object not found"}
	}
	return uc.buildSchema(ctx, def)
}

// ListIncomingRelations returns the (child object, relation field) pairs whose
// relation targets targetSlug — one query. EnsureSystemObjects keeps a first-touch
// org consistent with the other read paths.
func (uc *objectRegistryUseCase) ListIncomingRelations(ctx context.Context, orgID uuid.UUID, targetSlug string) ([]domain.IncomingRelation, error) {
	if err := uc.repo.EnsureSystemObjects(ctx, orgID); err != nil {
		return nil, err
	}
	return uc.repo.ListIncomingRelations(ctx, orgID, targetSlug)
}

// SetNumberPrefix updates an object's record-number prefix. A blank prefix resets
// to the slug default (read path falls back to UPPER(slug)).
func (uc *objectRegistryUseCase) SetNumberPrefix(ctx context.Context, orgID uuid.UUID, slug, prefix string) error {
	if err := uc.repo.EnsureSystemObjects(ctx, orgID); err != nil {
		return err
	}
	if err := uc.repo.SetNumberPrefix(ctx, orgID, slug, strings.TrimSpace(prefix)); err != nil {
		return &domain.AppError{Code: http.StatusNotFound, Message: "object not found"}
	}
	return nil
}

// objectHasOwner reports whether an object's records carry an owner_user_id — the
// anchor for row scope, assignment and sharing (U6.3).
//
// Everything is owned EXCEPT company: a company is a shared entity that many
// people's contacts and deals hang off, so pinning it to one owner (and thereby
// hiding it from every row-scoped colleague) would break the model rather than
// tighten it. Contacts, deals and every custom object are owned.
func objectHasOwner(slug string) bool {
	return slug != "company"
}

// numberPrefix resolves an object's record-number prefix: the configured value, or
// the uppercased slug as the default (matching the read-path COALESCE in SQL).
func numberPrefix(def *domain.ObjectDef) string {
	if def.NumberPrefix != nil && strings.TrimSpace(*def.NumberPrefix) != "" {
		return *def.NumberPrefix
	}
	return strings.ToUpper(def.Slug)
}

// buildSchema assembles any object's descriptor from object_fields — system and
// custom alike, since both now live in the registry tables (P7 convergence).
func (uc *objectRegistryUseCase) buildSchema(ctx context.Context, def *domain.ObjectDef) (*domain.ObjectDescriptor, error) {
	fields, err := uc.repo.ListFields(ctx, def.ID)
	if err != nil {
		return nil, err
	}

	descriptor := &domain.ObjectDescriptor{
		Slug:         def.Slug,
		Label:        def.Label,
		LabelPlural:  def.LabelPlural,
		Icon:         def.Icon,
		Color:        def.Color,
		IsSystem:     def.IsSystem,
		Searchable:   def.Searchable,
		HasOwner:     objectHasOwner(def.Slug),
		NumberPrefix: numberPrefix(def),
		Fields:       make([]domain.FieldDescriptor, 0, len(fields)),
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
		if f.ViaField != nil {
			fd.ViaField = *f.ViaField
		}
		if f.SourceField != nil {
			fd.SourceField = *f.SourceField
		}
		descriptor.Fields = append(descriptor.Fields, fd)
		// Resolve the display field's key from its id (system objects set this).
		if def.DisplayFieldID != nil && f.ID == *def.DisplayFieldID {
			descriptor.DisplayField = f.Key
		}
	}

	// Fallback display field (custom objects have no display_field_id): first text
	// field, else first field — matching the record-level display heuristic.
	if descriptor.DisplayField == "" {
		descriptor.DisplayField = fallbackDisplayField(descriptor.Fields)
	}
	return descriptor, nil
}

// fallbackDisplayField mirrors customDisplayField for FieldDescriptors: first text
// field, else first field, else empty.
func fallbackDisplayField(fields []domain.FieldDescriptor) string {
	for _, f := range fields {
		if f.Type == "text" {
			return f.Key
		}
	}
	if len(fields) > 0 {
		return fields[0].Key
	}
	return ""
}

// ============================================================
// Helpers
// ============================================================

// customDisplayField mirrors the record-level display heuristic: first text
// field, else first field, else empty. Used by RecordService.applyCustomDisplay.
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

// parseFieldDefs decodes a custom-object fields blob. Used by RecordService's
// read-time display resolution (applyCustomDisplay).
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
