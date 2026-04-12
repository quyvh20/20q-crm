package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

type orgSettingsUseCase struct {
	repo domain.OrgSettingsRepository
}

func NewOrgSettingsUseCase(repo domain.OrgSettingsRepository) domain.OrgSettingsUseCase {
	return &orgSettingsUseCase{repo: repo}
}

// keyRegex enforces snake_case keys: lowercase letters, digits, underscores.
var keyRegex = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ============================================================
// GetFieldDefs
// ============================================================

func (uc *orgSettingsUseCase) GetFieldDefs(ctx context.Context, orgID uuid.UUID, entityType string) ([]domain.CustomFieldDef, error) {
	all, err := uc.loadAllDefs(ctx, orgID)
	if err != nil {
		return nil, err
	}

	if entityType == "" {
		sort.Slice(all, func(i, j int) bool { return all[i].Position < all[j].Position })
		return all, nil
	}

	var filtered []domain.CustomFieldDef
	for _, d := range all {
		if d.EntityType == entityType {
			filtered = append(filtered, d)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Position < filtered[j].Position })
	return filtered, nil
}

// ============================================================
// CreateFieldDef
// ============================================================

func (uc *orgSettingsUseCase) CreateFieldDef(ctx context.Context, orgID uuid.UUID, input domain.CreateFieldDefInput) (*domain.CustomFieldDef, error) {
	// Validate type
	if !domain.ValidFieldTypes[input.Type] {
		return nil, domain.NewAppError(400, fmt.Sprintf("invalid field type: %s", input.Type))
	}
	if !domain.ValidEntityTypes[input.EntityType] {
		return nil, domain.NewAppError(400, fmt.Sprintf("invalid entity_type: %s", input.EntityType))
	}
	// Validate key format
	if !keyRegex.MatchString(input.Key) {
		return nil, domain.NewAppError(400, "key must be snake_case (lowercase letters, digits, underscores), 1-64 chars")
	}
	// Select type requires options
	if input.Type == "select" && len(input.Options) == 0 {
		return nil, domain.NewAppError(400, "select type requires at least one option")
	}

	all, err := uc.loadAllDefs(ctx, orgID)
	if err != nil {
		return nil, err
	}

	// Check for duplicate key within the same entity type
	for _, d := range all {
		if d.Key == input.Key && d.EntityType == input.EntityType {
			return nil, domain.NewAppError(409, fmt.Sprintf("field '%s' already exists for %s", input.Key, input.EntityType))
		}
	}

	// Auto-assign position if not provided
	pos := 0
	if input.Position != nil {
		pos = *input.Position
	} else {
		for _, d := range all {
			if d.EntityType == input.EntityType && d.Position >= pos {
				pos = d.Position + 1
			}
		}
	}

	def := domain.CustomFieldDef{
		Key:        input.Key,
		Label:      input.Label,
		Type:       input.Type,
		EntityType: input.EntityType,
		Options:    input.Options,
		Required:   input.Required,
		Position:   pos,
	}

	all = append(all, def)
	if err := uc.saveDefs(ctx, orgID, all); err != nil {
		return nil, err
	}
	return &def, nil
}

// ============================================================
// UpdateFieldDef
// ============================================================

func (uc *orgSettingsUseCase) UpdateFieldDef(ctx context.Context, orgID uuid.UUID, key string, input domain.UpdateFieldDefInput) (*domain.CustomFieldDef, error) {
	all, err := uc.loadAllDefs(ctx, orgID)
	if err != nil {
		return nil, err
	}

	idx := -1
	for i, d := range all {
		if d.Key == key {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil, domain.NewAppError(404, fmt.Sprintf("field '%s' not found", key))
	}

	if input.Label != nil {
		all[idx].Label = *input.Label
	}
	if input.Type != nil {
		if !domain.ValidFieldTypes[*input.Type] {
			return nil, domain.NewAppError(400, fmt.Sprintf("invalid field type: %s", *input.Type))
		}
		all[idx].Type = *input.Type
	}
	if input.Options != nil {
		all[idx].Options = input.Options
	}
	if input.Required != nil {
		all[idx].Required = *input.Required
	}
	if input.Position != nil {
		all[idx].Position = *input.Position
	}

	// Re-validate select
	if all[idx].Type == "select" && len(all[idx].Options) == 0 {
		return nil, domain.NewAppError(400, "select type requires at least one option")
	}

	if err := uc.saveDefs(ctx, orgID, all); err != nil {
		return nil, err
	}
	return &all[idx], nil
}

// ============================================================
// DeleteFieldDef
// ============================================================

func (uc *orgSettingsUseCase) DeleteFieldDef(ctx context.Context, orgID uuid.UUID, key string) error {
	all, err := uc.loadAllDefs(ctx, orgID)
	if err != nil {
		return err
	}

	var filtered []domain.CustomFieldDef
	found := false
	for _, d := range all {
		if d.Key == key {
			found = true
			continue
		}
		filtered = append(filtered, d)
	}
	if !found {
		return domain.NewAppError(404, fmt.Sprintf("field '%s' not found", key))
	}

	return uc.saveDefs(ctx, orgID, filtered)
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

	// Parse the incoming custom fields
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(fields), &data); err != nil {
		return domain.NewAppError(400, "custom_fields must be a valid JSON object")
	}

	// Build a lookup of defined keys
	defMap := make(map[string]domain.CustomFieldDef)
	for _, d := range defs {
		defMap[d.Key] = d
	}

	// Validate each provided field
	for key, val := range data {
		def, ok := defMap[key]
		if !ok {
			continue // allow unknown keys (for flexibility)
		}
		if err := validateFieldValue(def, val); err != nil {
			return domain.NewAppError(400, fmt.Sprintf("custom_fields.%s: %s", key, err.Error()))
		}
	}

	// Check required fields
	for _, def := range defs {
		if def.Required {
			v, exists := data[def.Key]
			if !exists || v == nil || v == "" {
				return domain.NewAppError(400, fmt.Sprintf("custom_fields.%s is required", def.Key))
			}
		}
	}

	return nil
}

// ============================================================
// Helpers
// ============================================================

func validateFieldValue(def domain.CustomFieldDef, val interface{}) error {
	if val == nil {
		return nil
	}

	switch def.Type {
	case "text", "url":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("expected string, got %T", val)
		}
	case "number":
		switch v := val.(type) {
		case float64: // JSON numbers
			_ = v
		case string:
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				return fmt.Errorf("expected number, got %q", v)
			}
		default:
			return fmt.Errorf("expected number, got %T", val)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("expected boolean, got %T", val)
		}
	case "date":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("expected date string, got %T", val)
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			if _, err := time.Parse(time.RFC3339, s); err != nil {
				return fmt.Errorf("expected date in YYYY-MM-DD or RFC3339 format")
			}
		}
	case "select":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("expected string for select, got %T", val)
		}
		valid := false
		for _, opt := range def.Options {
			if strings.EqualFold(opt, s) {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("value %q is not a valid option (valid: %v)", s, def.Options)
		}
	}
	return nil
}

func (uc *orgSettingsUseCase) loadAllDefs(ctx context.Context, orgID uuid.UUID) ([]domain.CustomFieldDef, error) {
	settings, err := uc.repo.GetByOrgID(ctx, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if settings == nil {
		return nil, nil
	}

	var defs []domain.CustomFieldDef
	if len(settings.CustomFieldDefs) > 0 && string(settings.CustomFieldDefs) != "null" && string(settings.CustomFieldDefs) != "[]" {
		if err := json.Unmarshal([]byte(settings.CustomFieldDefs), &defs); err != nil {
			return nil, domain.NewAppError(500, "failed to parse custom field definitions")
		}
	}
	return defs, nil
}

func (uc *orgSettingsUseCase) saveDefs(ctx context.Context, orgID uuid.UUID, defs []domain.CustomFieldDef) error {
	raw, err := json.Marshal(defs)
	if err != nil {
		return domain.ErrInternal
	}

	settings, err := uc.repo.GetByOrgID(ctx, orgID)
	if err != nil {
		return domain.ErrInternal
	}
	if settings == nil {
		settings = &domain.OrgSettings{OrgID: orgID}
	}
	settings.CustomFieldDefs = domain.JSON(raw)

	if err := uc.repo.Upsert(ctx, settings); err != nil {
		return domain.ErrInternal
	}
	return nil
}
