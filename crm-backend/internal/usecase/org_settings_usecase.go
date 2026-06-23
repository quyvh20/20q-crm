package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"crm-backend/internal/domain"
	"crm-backend/internal/fieldvalidate"

	"github.com/google/uuid"
)

// orgSettingsUseCase manages admin-defined ("custom") field definitions on the
// three system objects.
//
// As of the P7 cutover these defs live in object_fields (is_system=false),
// addressed by the system object's slug — the same store the registry, validation,
// FLS, and audit already key off. The legacy org_settings.custom_field_defs JSONB
// blob is no longer read or written here, which removes the lost-update race that
// rewrote the whole array on every edit (symptom #3 / R6). The public interface is
// unchanged, so every caller (settings handler, registry, RecordService validation,
// AI knowledge builder) keeps working transparently.
type orgSettingsUseCase struct {
	registry    domain.ObjectRegistryRepository
	cacheBuster domain.SchemaCacheBuster
}

func NewOrgSettingsUseCase(registry domain.ObjectRegistryRepository, cacheBuster ...domain.SchemaCacheBuster) domain.OrgSettingsUseCase {
	var cb domain.SchemaCacheBuster
	if len(cacheBuster) > 0 {
		cb = cacheBuster[0]
	}
	return &orgSettingsUseCase{registry: registry, cacheBuster: cb}
}

// keyRegex enforces snake_case keys: lowercase letters, digits, underscores.
var keyRegex = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// systemFieldSlugs are the system objects whose admin-defined fields this usecase
// manages. They mirror domain.ValidEntityTypes but in a deterministic order.
var systemFieldSlugs = []string{"contact", "company", "deal"}

// ============================================================
// GetFieldDefs
// ============================================================

func (uc *orgSettingsUseCase) GetFieldDefs(ctx context.Context, orgID uuid.UUID, entityType string) ([]domain.CustomFieldDef, error) {
	if err := uc.registry.EnsureSystemObjects(ctx, orgID); err != nil {
		return nil, domain.ErrInternal
	}

	slugs := systemFieldSlugs
	if entityType != "" {
		if !domain.ValidEntityTypes[entityType] {
			return nil, nil // unknown entity type — no defs, same as the legacy behaviour
		}
		slugs = []string{entityType}
	}

	var out []domain.CustomFieldDef
	for _, slug := range slugs {
		def, err := uc.registry.GetDefBySlug(ctx, orgID, slug)
		if err != nil {
			return nil, domain.ErrInternal
		}
		if def == nil {
			continue
		}
		fields, err := uc.registry.ListCustomFields(ctx, def.ID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		for _, f := range fields {
			out = append(out, customFieldDefFromField(f, slug))
		}
	}
	return out, nil
}

// ============================================================
// CreateFieldDef
// ============================================================

func (uc *orgSettingsUseCase) CreateFieldDef(ctx context.Context, orgID uuid.UUID, input domain.CreateFieldDefInput) (*domain.CustomFieldDef, error) {
	if !domain.ValidFieldTypes[input.Type] {
		return nil, domain.NewAppError(400, fmt.Sprintf("invalid field type: %s", input.Type))
	}
	if !domain.ValidEntityTypes[input.EntityType] {
		return nil, domain.NewAppError(400, fmt.Sprintf("invalid entity_type: %s", input.EntityType))
	}
	if !keyRegex.MatchString(input.Key) {
		return nil, domain.NewAppError(400, "key must be snake_case (lowercase letters, digits, underscores), 1-64 chars")
	}
	if input.Type == "select" && len(input.Options) == 0 {
		return nil, domain.NewAppError(400, "select type requires at least one option")
	}

	if err := uc.registry.EnsureSystemObjects(ctx, orgID); err != nil {
		return nil, domain.ErrInternal
	}
	def, err := uc.registry.GetDefBySlug(ctx, orgID, input.EntityType)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if def == nil {
		return nil, domain.ErrInternal
	}

	// Reject a key that collides with any existing field on the object — native or
	// custom. (The legacy store only checked custom keys per entity_type; including
	// native columns is strictly safer and matches the object_fields unique index.)
	if existing, err := uc.registry.GetFieldByDefKey(ctx, def.ID, input.Key); err != nil {
		return nil, domain.ErrInternal
	} else if existing != nil {
		return nil, domain.NewAppError(409, fmt.Sprintf("field '%s' already exists for %s", input.Key, input.EntityType))
	}

	pos := 0
	if input.Position != nil {
		pos = *input.Position
	} else {
		all, err := uc.registry.ListFields(ctx, def.ID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		for _, f := range all {
			if f.Position >= pos {
				pos = f.Position + 1
			}
		}
	}

	field := &domain.ObjectField{
		ID:          uuid.New(),
		OrgID:       orgID,
		ObjectDefID: def.ID,
		Key:         input.Key,
		Label:       input.Label,
		Type:        input.Type,
		Options:     marshalStringArray(input.Options),
		IsRequired:  input.Required,
		IsSystem:    false,
		StorageKind: "jsonb",
		Position:    pos,
	}
	if err := uc.registry.CreateField(ctx, field); err != nil {
		return nil, domain.ErrInternal
	}
	uc.bustSchemaCache(ctx, orgID)
	def2 := customFieldDefFromField(*field, input.EntityType)
	return &def2, nil
}

// ============================================================
// UpdateFieldDef
// ============================================================

func (uc *orgSettingsUseCase) UpdateFieldDef(ctx context.Context, orgID uuid.UUID, key string, input domain.UpdateFieldDefInput) (*domain.CustomFieldDef, error) {
	if err := uc.registry.EnsureSystemObjects(ctx, orgID); err != nil {
		return nil, domain.ErrInternal
	}
	field, slug, err := uc.registry.FindCustomFieldByKey(ctx, orgID, key)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if field == nil {
		return nil, domain.NewAppError(404, fmt.Sprintf("field '%s' not found", key))
	}

	if input.Label != nil {
		field.Label = *input.Label
	}
	if input.Type != nil {
		if !domain.ValidFieldTypes[*input.Type] {
			return nil, domain.NewAppError(400, fmt.Sprintf("invalid field type: %s", *input.Type))
		}
		field.Type = *input.Type
	}
	if input.Options != nil {
		field.Options = marshalStringArray(input.Options)
	}
	if input.Required != nil {
		field.IsRequired = *input.Required
	}
	if input.Position != nil {
		field.Position = *input.Position
	}

	if field.Type == "select" && len(parseOptions(field.Options)) == 0 {
		return nil, domain.NewAppError(400, "select type requires at least one option")
	}

	if err := uc.registry.SaveField(ctx, field); err != nil {
		return nil, domain.ErrInternal
	}
	uc.bustSchemaCache(ctx, orgID)
	def := customFieldDefFromField(*field, slug)
	return &def, nil
}

// ============================================================
// DeleteFieldDef
// ============================================================

func (uc *orgSettingsUseCase) DeleteFieldDef(ctx context.Context, orgID uuid.UUID, key string) error {
	if err := uc.registry.EnsureSystemObjects(ctx, orgID); err != nil {
		return domain.ErrInternal
	}
	field, _, err := uc.registry.FindCustomFieldByKey(ctx, orgID, key)
	if err != nil {
		return domain.ErrInternal
	}
	if field == nil {
		return domain.NewAppError(404, fmt.Sprintf("field '%s' not found", key))
	}
	if err := uc.registry.SoftDeleteFieldByID(ctx, orgID, field.ID); err != nil {
		return domain.ErrInternal
	}
	uc.bustSchemaCache(ctx, orgID)
	return nil
}

// ============================================================
// ValidateCustomFields — called by Contact/Company/Deal usecases
// ============================================================

func (uc *orgSettingsUseCase) ValidateCustomFields(ctx context.Context, orgID uuid.UUID, entityType string, fields domain.JSON) error {
	if len(fields) == 0 || string(fields) == "{}" || string(fields) == "null" {
		return nil // nothing to validate
	}

	defs, err := uc.GetFieldDefs(ctx, orgID, entityType)
	if err != nil {
		return err
	}
	if len(defs) == 0 {
		return nil // no definitions = no validation
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(fields), &data); err != nil {
		return domain.NewAppError(400, "custom_fields must be a valid JSON object")
	}

	// Delegate type/required checking to the shared validator so system and
	// custom objects behave identically.
	return fieldvalidate.ValidateFields(defs, data, "custom_fields")
}

// ============================================================
// Helpers
// ============================================================

// bustSchemaCache invalidates the AI knowledge builder cache so the AI
// immediately sees field definition changes.
func (uc *orgSettingsUseCase) bustSchemaCache(ctx context.Context, orgID uuid.UUID) {
	if uc.cacheBuster != nil {
		uc.cacheBuster.BustCache(ctx, orgID)
	}
}

// customFieldDefFromField projects an object_fields row into the legacy
// CustomFieldDef shape the API and validator speak.
func customFieldDefFromField(f domain.ObjectField, entityType string) domain.CustomFieldDef {
	return domain.CustomFieldDef{
		Key:        f.Key,
		Label:      f.Label,
		Type:       f.Type,
		EntityType: entityType,
		Options:    parseOptions(f.Options),
		Required:   f.IsRequired,
		Position:   f.Position,
	}
}

// marshalStringArray renders a string slice as a JSONB array, defaulting to "[]"
// so the options column is never NULL.
func marshalStringArray(opts []string) domain.JSON {
	if len(opts) == 0 {
		return domain.JSON("[]")
	}
	raw, err := json.Marshal(opts)
	if err != nil {
		return domain.JSON("[]")
	}
	return domain.JSON(raw)
}
