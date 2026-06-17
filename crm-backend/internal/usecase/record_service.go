package usecase

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// recordService is the unified read/write engine over every object (plan §3.4).
// It dispatches on storage kind:
//
//   - System objects (contact/deal/company) route to their existing typed
//     usecases via per-slug adapters (record_service_system.go), preserving
//     embeddings, preloads, and stage side-effects.
//   - Custom objects route to the generic custom-object usecase (JSONB storage),
//     keeping its P1 validation and display recompute.
//
// Callers (HTTP handlers, and later AI/automation) only ever see one uniform
// record shape. Validation runs here for system objects (the typed usecases
// don't validate their custom_fields blob today) and inside the custom-object
// usecase for JSONB objects — so every write is validated the same way.
//
// System objects take precedence over a custom object that happens to reuse a
// reserved slug, matching objectRegistryUseCase.GetSchema.
type recordService struct {
	customObjUC    domain.CustomObjectUseCase
	orgSettingsUC  domain.OrgSettingsUseCase
	systemAdapters map[string]systemObjectAdapter
	emitEvent      domain.RecordEventEmitter
}

// NewRecordService wires the unified service over the existing per-object
// usecases. orgSettingsUC supplies the custom-field definitions used to validate
// admin-defined fields on system objects.
func NewRecordService(
	customObjUC domain.CustomObjectUseCase,
	orgSettingsUC domain.OrgSettingsUseCase,
	contactUC domain.ContactUseCase,
	companyUC domain.CompanyUseCase,
	dealUC domain.DealUseCase,
) domain.RecordService {
	return &recordService{
		customObjUC:   customObjUC,
		orgSettingsUC: orgSettingsUC,
		systemAdapters: map[string]systemObjectAdapter{
			"contact": &contactAdapter{uc: contactUC},
			"company": &companyAdapter{uc: companyUC},
			"deal":    &dealAdapter{uc: dealUC},
		},
	}
}

// SetEventEmitter wires the automation trigger callback. Called from main.go
// after the automation engine is initialized (matching the per-handler emitters).
// Until set, writes simply skip event emission.
func (s *recordService) SetEventEmitter(fn domain.RecordEventEmitter) {
	s.emitEvent = fn
}

const defaultRecordLimit = 25

// List returns one uniform page of records for an object, system or custom.
func (s *recordService) List(ctx context.Context, orgID uuid.UUID, slug string, in domain.RecordListInput) (*domain.RecordList, error) {
	limit := in.Limit
	if limit <= 0 || limit > 100 {
		limit = defaultRecordLimit
	}

	if a, ok := s.systemAdapters[slug]; ok {
		recs, next, err := a.list(ctx, orgID, limit, in.Q, in.Cursor)
		if err != nil {
			return nil, err
		}
		return &domain.RecordList{Records: recs, NextCursor: next}, nil
	}

	return s.listCustom(ctx, orgID, slug, limit, in.Q, in.Cursor)
}

// Get returns a single uniform record.
func (s *recordService) Get(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) (*domain.UniformRecord, error) {
	if a, ok := s.systemAdapters[slug]; ok {
		return a.get(ctx, orgID, id)
	}

	def, err := s.customObjUC.GetDefBySlug(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}
	rec, err := s.customObjUC.GetRecord(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if rec.ObjectDefID != def.ID {
		return nil, domain.NewAppError(http.StatusNotFound, "record not found")
	}
	return customToUniform(slug, rec), nil
}

// Create validates and creates a record, returning the uniform shape.
func (s *recordService) Create(ctx context.Context, orgID, userID uuid.UUID, slug string, in domain.RecordWriteInput) (*domain.UniformRecord, error) {
	if a, ok := s.systemAdapters[slug]; ok {
		if err := s.validateSystemCustomFields(ctx, orgID, slug, in.Fields, a); err != nil {
			return nil, err
		}
		return a.create(ctx, orgID, in.Fields)
	}

	data, err := marshalFields(in.Fields)
	if err != nil {
		return nil, err
	}
	rec, err := s.customObjUC.CreateRecord(ctx, orgID, userID, slug, domain.CreateRecordInput{Data: data})
	if err != nil {
		return nil, err
	}
	uniform := customToUniform(slug, rec)
	s.fireEvent(orgID, slug+"_created", uniform)
	return uniform, nil
}

// Update validates and applies a partial update, returning the uniform shape.
func (s *recordService) Update(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID, in domain.RecordWriteInput) (*domain.UniformRecord, error) {
	if a, ok := s.systemAdapters[slug]; ok {
		if err := s.validateSystemCustomFields(ctx, orgID, slug, in.Fields, a); err != nil {
			return nil, err
		}
		return a.update(ctx, orgID, id, in.Fields)
	}

	data, err := marshalFields(in.Fields)
	if err != nil {
		return nil, err
	}
	rec, err := s.customObjUC.UpdateRecord(ctx, orgID, slug, id, domain.UpdateRecordInput{Data: data})
	if err != nil {
		return nil, err
	}
	uniform := customToUniform(slug, rec)
	s.fireEvent(orgID, slug+"_updated", uniform)
	return uniform, nil
}

// Delete soft-deletes a record.
func (s *recordService) Delete(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) error {
	if a, ok := s.systemAdapters[slug]; ok {
		return a.delete(ctx, orgID, id)
	}

	// Confirm the record belongs to this custom object before deleting, so a
	// record id from a sibling object can't be deleted via the wrong slug.
	def, err := s.customObjUC.GetDefBySlug(ctx, orgID, slug)
	if err != nil {
		return err
	}
	rec, err := s.customObjUC.GetRecord(ctx, orgID, id)
	if err != nil {
		return err
	}
	if rec.ObjectDefID != def.ID {
		return domain.NewAppError(http.StatusNotFound, "record not found")
	}
	return s.customObjUC.DeleteRecord(ctx, orgID, id)
}

// ============================================================
// Custom-object path
// ============================================================

func (s *recordService) listCustom(ctx context.Context, orgID uuid.UUID, slug string, limit int, q, cursor string) (*domain.RecordList, error) {
	offset := decodeOffsetCursor(cursor)
	recs, total, err := s.customObjUC.ListRecords(ctx, orgID, slug, domain.RecordFilter{
		Limit:  limit,
		Offset: offset,
		Q:      q,
	})
	if err != nil {
		return nil, err
	}

	out := make([]domain.UniformRecord, 0, len(recs))
	for i := range recs {
		out = append(out, *customToUniform(slug, &recs[i]))
	}

	next := ""
	if nextOffset := offset + len(recs); int64(nextOffset) < total {
		next = encodeOffsetCursor(nextOffset)
	}
	return &domain.RecordList{Records: out, NextCursor: next}, nil
}

// customToUniform projects a JSONB-backed record into the uniform shape.
func customToUniform(slug string, rec *domain.CustomObjectRecord) *domain.UniformRecord {
	fields := map[string]interface{}{}
	if len(rec.Data) > 0 {
		_ = json.Unmarshal(rec.Data, &fields)
	}
	return &domain.UniformRecord{
		ID:        rec.ID,
		Object:    slug,
		Display:   rec.DisplayName,
		Fields:    fields,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	}
}

// fireEvent emits a custom-object automation trigger, mirroring the payload the
// legacy custom-object handler builds (entity_id, the record flattened under its
// slug key, trigger metadata) so existing custom-object workflows keep firing
// after the UI moves to the uniform endpoint. Fire-and-forget with
// context.Background() so a cancelled request can't kill the async run (see the
// inbound-webhook lesson). Only the custom-object write path calls this; system
// objects keep their automation on the legacy pages until the workflow engine
// cuts over (plan P7).
func (s *recordService) fireEvent(orgID uuid.UUID, eventType string, rec *domain.UniformRecord) {
	if s.emitEvent == nil {
		return
	}
	recordData := map[string]any{
		"id":           rec.ID.String(),
		"display_name": rec.Display,
	}
	for k, v := range rec.Fields {
		recordData[k] = v
	}
	payload := map[string]any{
		"entity_id": rec.ID.String(),
		rec.Object:  recordData,
		"trigger": map[string]any{
			"type":   eventType,
			"source": "crm_ui",
		},
	}
	go s.emitEvent(context.Background(), orgID, eventType, payload)
}

// ============================================================
// Validation
// ============================================================

// validateSystemCustomFields type-checks the admin-defined (non-native) fields
// of a system-object write against the org's field definitions, using the same
// shared validator custom objects use. Native columns are coerced/validated by
// the adapter. This closes the gap where the typed usecases never validated
// their custom_fields blob (plan P1 → "wire validation into the write path").
func (s *recordService) validateSystemCustomFields(ctx context.Context, orgID uuid.UUID, slug string, fields map[string]interface{}, a systemObjectAdapter) error {
	custom := excludeKeys(fields, a.nativeKeys())
	if len(custom) == 0 {
		return nil
	}
	raw, err := json.Marshal(custom)
	if err != nil {
		return domain.NewAppError(http.StatusBadRequest, "invalid custom field values")
	}
	return s.orgSettingsUC.ValidateCustomFields(ctx, orgID, slug, domain.JSON(raw))
}

// ============================================================
// Shared helpers
// ============================================================

// marshalFields turns the uniform field map into a JSONB payload for the
// custom-object usecase. A nil map becomes an empty object.
func marshalFields(fields map[string]interface{}) (domain.JSON, error) {
	if fields == nil {
		fields = map[string]interface{}{}
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, domain.NewAppError(http.StatusBadRequest, "invalid field values")
	}
	return domain.JSON(raw), nil
}

// excludeKeys returns the subset of fields whose keys are not in exclude.
func excludeKeys(fields map[string]interface{}, exclude map[string]bool) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range fields {
		if !exclude[k] {
			out[k] = v
		}
	}
	return out
}

// Offset cursors keep the uniform list API cursor-based for every object: system
// objects pass through their typed repo's keyset cursor; custom objects encode
// the next offset here. Callers treat the cursor as opaque.

func encodeOffsetCursor(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte("off:" + strconv.Itoa(offset)))
}

func decodeOffsetCursor(cursor string) int {
	if cursor == "" {
		return 0
	}
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0
	}
	s := string(raw)
	if !strings.HasPrefix(s, "off:") {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(s, "off:"))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
