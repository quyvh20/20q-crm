package usecase

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"crm-backend/internal/domain"
	"crm-backend/internal/fieldvalidate"

	"github.com/google/uuid"
)

type customObjectUseCase struct {
	repo         domain.CustomObjectRepository
	cacheBuster  domain.SchemaCacheBuster
}

func NewCustomObjectUseCase(repo domain.CustomObjectRepository, cacheBuster ...domain.SchemaCacheBuster) domain.CustomObjectUseCase {
	var cb domain.SchemaCacheBuster
	if len(cacheBuster) > 0 {
		cb = cacheBuster[0]
	}
	return &customObjectUseCase{repo: repo, cacheBuster: cb}
}

var slugRegex = regexp.MustCompile(`^[a-z][a-z0-9_]{0,49}$`)

// ============================================================
// Definitions
// ============================================================

func (uc *customObjectUseCase) ListDefs(ctx context.Context, orgID uuid.UUID) ([]domain.CustomObjectDef, error) {
	return uc.repo.ListDefs(ctx, orgID)
}

func (uc *customObjectUseCase) GetDefBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*domain.CustomObjectDef, error) {
	def, err := uc.repo.GetDefBySlug(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, &domain.AppError{Code: http.StatusNotFound, Message: "custom object not found"}
	}
	return def, nil
}

func (uc *customObjectUseCase) CreateDef(ctx context.Context, orgID uuid.UUID, input domain.CreateObjectDefInput) (*domain.CustomObjectDef, error) {
	// Validate slug
	slug := strings.TrimSpace(input.Slug)
	if !slugRegex.MatchString(slug) {
		return nil, &domain.AppError{Code: http.StatusBadRequest, Message: "slug must be lowercase alphanumeric with underscores, 1-50 chars, starting with a letter"}
	}

	// Check duplicate slug
	existing, err := uc.repo.GetDefBySlug(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, &domain.AppError{Code: http.StatusConflict, Message: "object with slug '" + slug + "' already exists"}
	}

	// Validate fields if provided
	if len(input.Fields) > 0 {
		if err := uc.validateFieldDefs(input.Fields); err != nil {
			return nil, err
		}
	}

	label := strings.TrimSpace(input.Label)
	labelPlural := strings.TrimSpace(input.LabelPlural)
	if label == "" {
		return nil, &domain.AppError{Code: http.StatusBadRequest, Message: "label is required"}
	}
	if labelPlural == "" {
		labelPlural = label + "s"
	}

	icon := input.Icon
	if icon == "" {
		icon = "📦"
	}

	fields := input.Fields
	if len(fields) == 0 {
		fields = domain.JSON("[]")
	}

	def := &domain.CustomObjectDef{
		ID:          uuid.New(),
		OrgID:       orgID,
		Slug:        slug,
		Label:       label,
		LabelPlural: labelPlural,
		Icon:        icon,
		Fields:      fields,
		Searchable:  input.Searchable,
	}

	if err := uc.repo.CreateDef(ctx, def); err != nil {
		return nil, err
	}
	uc.bustSchemaCache(ctx, orgID)
	return def, nil
}

func (uc *customObjectUseCase) UpdateDef(ctx context.Context, orgID uuid.UUID, slug string, input domain.UpdateObjectDefInput) (*domain.CustomObjectDef, error) {
	def, err := uc.repo.GetDefBySlug(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, &domain.AppError{Code: http.StatusNotFound, Message: "custom object not found"}
	}

	if input.Label != nil {
		l := strings.TrimSpace(*input.Label)
		if l == "" {
			return nil, &domain.AppError{Code: http.StatusBadRequest, Message: "label cannot be empty"}
		}
		def.Label = l
	}
	if input.LabelPlural != nil {
		def.LabelPlural = strings.TrimSpace(*input.LabelPlural)
	}
	if input.Icon != nil {
		def.Icon = *input.Icon
	}
	if len(input.Fields) > 0 {
		if err := uc.validateFieldDefs(input.Fields); err != nil {
			return nil, err
		}
		def.Fields = input.Fields
	}
	if input.Searchable != nil {
		def.Searchable = *input.Searchable
	}

	if err := uc.repo.UpdateDef(ctx, def); err != nil {
		return nil, err
	}
	uc.bustSchemaCache(ctx, orgID)
	return def, nil
}

func (uc *customObjectUseCase) DeleteDef(ctx context.Context, orgID uuid.UUID, slug string) error {
	def, err := uc.repo.GetDefBySlug(ctx, orgID, slug)
	if err != nil {
		return err
	}
	if def == nil {
		return &domain.AppError{Code: http.StatusNotFound, Message: "custom object not found"}
	}
	if err := uc.repo.SoftDeleteDef(ctx, orgID, def.ID); err != nil {
		return err
	}
	uc.bustSchemaCache(ctx, orgID)
	return nil
}

// ============================================================
// Records
// ============================================================

func (uc *customObjectUseCase) ListRecords(ctx context.Context, orgID uuid.UUID, slug string, f domain.RecordFilter) ([]domain.CustomObjectRecord, int64, error) {
	def, err := uc.repo.GetDefBySlug(ctx, orgID, slug)
	if err != nil {
		return nil, 0, err
	}
	if def == nil {
		return nil, 0, &domain.AppError{Code: http.StatusNotFound, Message: "custom object not found"}
	}
	return uc.repo.ListRecords(ctx, orgID, def.ID, slug, f)
}

func (uc *customObjectUseCase) GetRecord(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) (*domain.CustomObjectRecord, error) {
	rec, err := uc.repo.GetRecord(ctx, orgID, slug, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, &domain.AppError{Code: http.StatusNotFound, Message: "record not found"}
	}
	return rec, nil
}

func (uc *customObjectUseCase) CreateRecord(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, slug string, input domain.CreateRecordInput) (*domain.CustomObjectRecord, error) {
	def, err := uc.repo.GetDefBySlug(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, &domain.AppError{Code: http.StatusNotFound, Message: "custom object not found"}
	}

	// Parse data
	var dataMap map[string]interface{}
	if err := json.Unmarshal(input.Data, &dataMap); err != nil {
		return nil, &domain.AppError{Code: http.StatusBadRequest, Message: "invalid data JSON"}
	}

	// Validate against the object's field definitions (type, required, select
	// options) using the same validator system objects use.
	if err := uc.validateRecordData(def.Fields, dataMap); err != nil {
		return nil, err
	}

	// Compute display_name from first text field
	displayName := uc.computeDisplayName(def.Fields, dataMap)

	// Owner defaults to the creator (U6.3). Without this, a row-scoped user would
	// create a record and immediately lose sight of it — an unowned row matches no
	// row-scoped caller.
	owner := &userID
	if input.OwnerUserID != nil {
		owner = input.OwnerUserID
	}

	rec := &domain.CustomObjectRecord{
		ID:          uuid.New(),
		OrgID:       orgID,
		ObjectDefID: def.ID,
		DisplayName: displayName,
		Data:        input.Data,
		OwnerUserID: owner,
		CreatedBy:   &userID,
	}

	if err := uc.repo.CreateRecord(ctx, rec); err != nil {
		return nil, err
	}

	// Re-read to pick up DB defaults, but NEVER let the scoped read decide whether the
	// write happened: an owner-scoped caller who assigns a record to someone else can
	// no longer see it, so this read returns (nil, nil) and every caller downstream
	// would nil-deref on a write that actually succeeded. Fall back to what we wrote.
	out, err := uc.repo.GetRecord(ctx, orgID, slug, rec.ID)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return rec, nil
	}
	return out, nil
}

func (uc *customObjectUseCase) UpdateRecord(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID, input domain.UpdateRecordInput) (*domain.CustomObjectRecord, error) {
	def, err := uc.repo.GetDefBySlug(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, &domain.AppError{Code: http.StatusNotFound, Message: "custom object not found"}
	}

	rec, err := uc.repo.GetRecord(ctx, orgID, slug, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, &domain.AppError{Code: http.StatusNotFound, Message: "record not found"}
	}

	if len(input.Data) > 0 {
		var dataMap map[string]interface{}
		if err := json.Unmarshal(input.Data, &dataMap); err != nil {
			return nil, &domain.AppError{Code: http.StatusBadRequest, Message: "invalid data JSON"}
		}
		if err := uc.validateRecordData(def.Fields, dataMap); err != nil {
			return nil, err
		}
		rec.Data = input.Data
		rec.DisplayName = uc.computeDisplayName(def.Fields, dataMap)
	}
	if input.DisplayName != nil {
		rec.DisplayName = *input.DisplayName
	}
	// Reassign or unassign the owner. ClearOwner is what makes "unassign" expressible:
	// a nil OwnerUserID alone means "not supplied", so without the flag an owner could
	// be set but never removed. Save() writes the whole row, so a nil here really does
	// clear the column (no GORM zero-value omission on this path).
	if input.ClearOwner {
		rec.OwnerUserID = nil
	} else if input.OwnerUserID != nil {
		rec.OwnerUserID = input.OwnerUserID
	}

	if err := uc.repo.UpdateRecord(ctx, slug, rec); err != nil {
		return nil, err
	}
	// Re-read to pick up DB defaults, but NEVER let the scoped read decide whether the
	// write happened: an owner-scoped caller who assigns a record to someone else can
	// no longer see it, so this read returns (nil, nil) and every caller downstream
	// would nil-deref on a write that actually succeeded. Fall back to what we wrote.
	out, err := uc.repo.GetRecord(ctx, orgID, slug, rec.ID)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return rec, nil
	}
	return out, nil
}

func (uc *customObjectUseCase) DeleteRecord(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) error {
	return uc.repo.SoftDeleteRecord(ctx, orgID, slug, id)
}

// ============================================================
// Helpers
// ============================================================

// bustSchemaCache invalidates the AI knowledge builder cache so the AI
// immediately sees new/updated custom objects.
func (uc *customObjectUseCase) bustSchemaCache(ctx context.Context, orgID uuid.UUID) {
	if uc.cacheBuster != nil {
		uc.cacheBuster.BustCache(ctx, orgID)
	}
}
// validateFieldDefs checks all field definitions in the JSON array.
func (uc *customObjectUseCase) validateFieldDefs(fieldsJSON domain.JSON) error {
	var fields []domain.CustomFieldDef
	if err := json.Unmarshal(fieldsJSON, &fields); err != nil {
		return &domain.AppError{Code: http.StatusBadRequest, Message: "invalid fields JSON"}
	}
	keys := make(map[string]bool)
	for _, f := range fields {
		if f.Key == "" || f.Label == "" {
			return &domain.AppError{Code: http.StatusBadRequest, Message: "each field must have key and label"}
		}
		if !domain.ValidFieldTypes[f.Type] {
			return &domain.AppError{Code: http.StatusBadRequest, Message: "invalid field type: " + f.Type}
		}
		if f.Type == "select" && len(f.Options) == 0 {
			return &domain.AppError{Code: http.StatusBadRequest, Message: "select field '" + f.Key + "' must have at least one option"}
		}
		if keys[f.Key] {
			return &domain.AppError{Code: http.StatusBadRequest, Message: "duplicate field key: " + f.Key}
		}
		keys[f.Key] = true
	}
	return nil
}

// validateRecordData checks a record payload against its object's field
// definitions using the shared validator (type, required, select options).
// Unknown keys pass through. def.Fields is already validated on def write, so a
// parse failure here is a server-side data issue, not the caller's — we skip
// validation rather than reject a legitimate record.
func (uc *customObjectUseCase) validateRecordData(fieldsJSON domain.JSON, data map[string]interface{}) error {
	var fields []domain.CustomFieldDef
	if len(fieldsJSON) > 0 {
		if err := json.Unmarshal(fieldsJSON, &fields); err != nil {
			return nil
		}
	}
	return fieldvalidate.ValidateFields(fields, data, "data")
}

// computeDisplayName returns the value of the first text-type field, or "Untitled".
func (uc *customObjectUseCase) computeDisplayName(fieldsJSON domain.JSON, data map[string]interface{}) string {
	var fields []domain.CustomFieldDef
	if err := json.Unmarshal(fieldsJSON, &fields); err != nil {
		return "Untitled"
	}

	// Try to use the first text field's value
	for _, f := range fields {
		if f.Type == "text" {
			if val, ok := data[f.Key]; ok {
				if s, ok := val.(string); ok && s != "" {
					return s
				}
			}
		}
	}

	// Fallback: use any non-empty string value
	for _, f := range fields {
		if val, ok := data[f.Key]; ok {
			if s, ok := val.(string); ok && s != "" {
				return s
			}
		}
	}

	return "Untitled"
}
