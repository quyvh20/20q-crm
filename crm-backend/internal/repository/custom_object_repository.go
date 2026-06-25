package repository

import (
	"context"
	"encoding/json"
	"errors"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type customObjectRepository struct {
	db *gorm.DB
}

func NewCustomObjectRepository(db *gorm.DB) domain.CustomObjectRepository {
	return &customObjectRepository{db: db}
}

// ============================================================
// Definitions
// ============================================================
//
// As of the P7 convergence, custom object defs + fields live in the registry tables
// (object_defs with is_system=false, object_fields with storage_kind='jsonb'), not
// custom_object_defs. These methods map between that storage and the CustomObjectDef
// shape (with its Fields blob) the port exposes, so RecordService, the AI builder,
// and the custom-object handler are unchanged. Records still live in
// custom_object_records (see the Records section) and reference object_defs(id).

func (r *customObjectRepository) ListDefs(ctx context.Context, orgID uuid.UUID) ([]domain.CustomObjectDef, error) {
	var defs []domain.ObjectDef
	if err := r.db.WithContext(ctx).
		Where("org_id = ? AND is_system = ?", orgID, false).
		Order("created_at ASC").
		Find(&defs).Error; err != nil {
		return nil, err
	}
	out := make([]domain.CustomObjectDef, 0, len(defs))
	for i := range defs {
		cd, err := r.toCustomDef(ctx, &defs[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *cd)
	}
	return out, nil
}

func (r *customObjectRepository) GetDefBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*domain.CustomObjectDef, error) {
	var def domain.ObjectDef
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND slug = ? AND is_system = ?", orgID, slug, false).
		First(&def).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return r.toCustomDef(ctx, &def)
}

func (r *customObjectRepository) GetDefByID(ctx context.Context, orgID uuid.UUID, id uuid.UUID) (*domain.CustomObjectDef, error) {
	var def domain.ObjectDef
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ? AND is_system = ?", orgID, id, false).
		First(&def).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return r.toCustomDef(ctx, &def)
}

func (r *customObjectRepository) CreateDef(ctx context.Context, def *domain.CustomObjectDef) error {
	if def.ID == uuid.Nil {
		def.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		od := domain.ObjectDef{
			ID:          def.ID,
			OrgID:       def.OrgID,
			Slug:        def.Slug,
			Label:       def.Label,
			LabelPlural: def.LabelPlural,
			Icon:        def.Icon,
			Color:       "#6B7280",
			IsSystem:    false,
			Storage:     "jsonb",
			Searchable:  def.Searchable,
		}
		if err := tx.Create(&od).Error; err != nil {
			return err
		}
		return insertCustomFields(tx, def.OrgID, def.ID, parseCustomFieldDefs(def.Fields))
	})
}

func (r *customObjectRepository) UpdateDef(ctx context.Context, def *domain.CustomObjectDef) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.ObjectDef{}).
			Where("id = ? AND org_id = ?", def.ID, def.OrgID).
			Updates(map[string]interface{}{
				"label":        def.Label,
				"label_plural": def.LabelPlural,
				"icon":         def.Icon,
				"searchable":   def.Searchable,
			}).Error; err != nil {
			return err
		}
		// Reconcile the def's fields against object_fields by key: update existing,
		// insert new, delete removed. (The unified field editor writes object_fields
		// directly; this keeps the legacy "save the whole fields blob" path working.)
		return reconcileCustomFields(tx, def.OrgID, def.ID, parseCustomFieldDefs(def.Fields))
	})
}

func (r *customObjectRepository) SoftDeleteDef(ctx context.Context, orgID uuid.UUID, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ? AND is_system = ?", orgID, id, false).
		Delete(&domain.ObjectDef{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	// Soft-delete the object's fields too, so they don't linger as active fields.
	return r.db.WithContext(ctx).Where("object_def_id = ?", id).Delete(&domain.ObjectField{}).Error
}

// ============================================================
// Registry ↔ CustomObjectDef mapping helpers (P7)
// ============================================================

// toCustomDef projects a registry ObjectDef (+ its object_fields) into the
// CustomObjectDef shape, rebuilding the Fields blob from object_fields rows.
func (r *customObjectRepository) toCustomDef(ctx context.Context, def *domain.ObjectDef) (*domain.CustomObjectDef, error) {
	var fields []domain.ObjectField
	if err := r.db.WithContext(ctx).
		Where("object_def_id = ? AND is_system = ?", def.ID, false).
		Order("position ASC, created_at ASC").
		Find(&fields).Error; err != nil {
		return nil, err
	}
	cfds := make([]domain.CustomFieldDef, 0, len(fields))
	for i := range fields {
		cfds = append(cfds, domain.CustomFieldDef{
			Key:      fields[i].Key,
			Label:    fields[i].Label,
			Type:     fields[i].Type,
			Options:  parseStringArray(fields[i].Options),
			Required: fields[i].IsRequired,
			Position: fields[i].Position,
		})
	}
	raw, err := json.Marshal(cfds)
	if err != nil {
		return nil, err
	}
	return &domain.CustomObjectDef{
		ID:          def.ID,
		OrgID:       def.OrgID,
		Slug:        def.Slug,
		Label:       def.Label,
		LabelPlural: def.LabelPlural,
		Icon:        def.Icon,
		Fields:      domain.JSON(raw),
		Searchable:  def.Searchable,
		CreatedAt:   def.CreatedAt,
		UpdatedAt:   def.UpdatedAt,
	}, nil
}

// insertCustomFields writes a def's fields as object_fields rows (position = array order).
func insertCustomFields(tx *gorm.DB, orgID, defID uuid.UUID, fields []domain.CustomFieldDef) error {
	for i, f := range fields {
		of := domain.ObjectField{
			ID:          uuid.New(),
			OrgID:       orgID,
			ObjectDefID: defID,
			Key:         f.Key,
			Label:       f.Label,
			Type:        f.Type,
			Options:     marshalStringArrayJSON(f.Options),
			IsRequired:  f.Required,
			IsSystem:    false,
			StorageKind: "jsonb",
			Position:    i,
		}
		if err := tx.Create(&of).Error; err != nil {
			return err
		}
	}
	return nil
}

// reconcileCustomFields makes object_fields match the desired field set by key.
func reconcileCustomFields(tx *gorm.DB, orgID, defID uuid.UUID, desired []domain.CustomFieldDef) error {
	var existing []domain.ObjectField
	if err := tx.Where("object_def_id = ? AND is_system = ?", defID, false).Find(&existing).Error; err != nil {
		return err
	}
	byKey := make(map[string]domain.ObjectField, len(existing))
	for i := range existing {
		byKey[existing[i].Key] = existing[i]
	}
	keep := make(map[string]bool, len(desired))
	for i, f := range desired {
		keep[f.Key] = true
		if ex, ok := byKey[f.Key]; ok {
			if err := tx.Model(&domain.ObjectField{}).Where("id = ?", ex.ID).Updates(map[string]interface{}{
				"label":       f.Label,
				"type":        f.Type,
				"options":     marshalStringArrayJSON(f.Options),
				"is_required": f.Required,
				"position":    i,
			}).Error; err != nil {
				return err
			}
		} else {
			of := domain.ObjectField{
				ID: uuid.New(), OrgID: orgID, ObjectDefID: defID, Key: f.Key, Label: f.Label,
				Type: f.Type, Options: marshalStringArrayJSON(f.Options), IsRequired: f.Required,
				IsSystem: false, StorageKind: "jsonb", Position: i,
			}
			if err := tx.Create(&of).Error; err != nil {
				return err
			}
		}
	}
	for i := range existing {
		if !keep[existing[i].Key] {
			if err := tx.Where("id = ?", existing[i].ID).Delete(&domain.ObjectField{}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func parseCustomFieldDefs(raw domain.JSON) []domain.CustomFieldDef {
	if len(raw) == 0 {
		return nil
	}
	var defs []domain.CustomFieldDef
	if err := json.Unmarshal(raw, &defs); err != nil {
		return nil
	}
	return defs
}

func parseStringArray(raw domain.JSON) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func marshalStringArrayJSON(opts []string) domain.JSON {
	if len(opts) == 0 {
		return domain.JSON("[]")
	}
	raw, err := json.Marshal(opts)
	if err != nil {
		return domain.JSON("[]")
	}
	return domain.JSON(raw)
}

// ============================================================
// Records
// ============================================================

func (r *customObjectRepository) ListRecords(ctx context.Context, orgID uuid.UUID, defID uuid.UUID, f domain.RecordFilter) ([]domain.CustomObjectRecord, int64, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}

	query := r.db.WithContext(ctx).
		Where("custom_object_records.org_id = ? AND custom_object_records.object_def_id = ?", orgID, defID)

	// Search by display_name
	if f.Q != "" {
		query = query.Where("custom_object_records.display_name ILIKE ?", "%"+f.Q+"%")
	}

	// Count total
	var total int64
	countQ := r.db.WithContext(ctx).Model(&domain.CustomObjectRecord{}).
		Where("org_id = ? AND object_def_id = ?", orgID, defID)
	if f.Q != "" {
		countQ = countQ.Where("display_name ILIKE ?", "%"+f.Q+"%")
	}
	countQ.Count(&total)

	var records []domain.CustomObjectRecord
	err := query.
		Order("custom_object_records.created_at DESC").
		Offset(f.Offset).
		Limit(limit).
		Find(&records).Error

	return records, total, err
}

func (r *customObjectRepository) GetRecord(ctx context.Context, orgID uuid.UUID, id uuid.UUID) (*domain.CustomObjectRecord, error) {
	var rec domain.CustomObjectRecord
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		First(&rec).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

func (r *customObjectRepository) CreateRecord(ctx context.Context, rec *domain.CustomObjectRecord) error {
	return r.db.WithContext(ctx).Create(rec).Error
}

func (r *customObjectRepository) UpdateRecord(ctx context.Context, rec *domain.CustomObjectRecord) error {
	return r.db.WithContext(ctx).Save(rec).Error
}

func (r *customObjectRepository) SoftDeleteRecord(ctx context.Context, orgID uuid.UUID, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		Delete(&domain.CustomObjectRecord{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
