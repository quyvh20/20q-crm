package usecase

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
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
	linkRepo       domain.LinkRepository
	tagRepo        domain.TagRepository
	emitEvent      domain.RecordEventEmitter
	// indexer keeps searchable objects in sync with the generic record_embeddings
	// index (P6). nil disables indexing (unit tests, or before startup wiring).
	indexer        domain.RecordIndexer
	// authz enforces Object-Level Security and records the audit trail (P5a). It
	// is the security chokepoint the plan promises: every public entry authorizes
	// here so OLS can't be forgotten in a handler. nil disables OLS/audit, which
	// only happens in unit tests that aren't exercising security.
	authz domain.RecordAuthorizer
}

// NewRecordService wires the unified service over the existing per-object
// usecases. orgSettingsUC supplies the custom-field definitions used to validate
// admin-defined fields on system objects. linkRepo + tagRepo back the universal
// relationship and tag surface (P4).
func NewRecordService(
	customObjUC domain.CustomObjectUseCase,
	orgSettingsUC domain.OrgSettingsUseCase,
	contactUC domain.ContactUseCase,
	companyUC domain.CompanyUseCase,
	dealUC domain.DealUseCase,
	linkRepo domain.LinkRepository,
	tagRepo domain.TagRepository,
	authz domain.RecordAuthorizer,
) domain.RecordService {
	return &recordService{
		customObjUC:   customObjUC,
		orgSettingsUC: orgSettingsUC,
		systemAdapters: map[string]systemObjectAdapter{
			"contact": &contactAdapter{uc: contactUC},
			"company": &companyAdapter{uc: companyUC},
			"deal":    &dealAdapter{uc: dealUC},
		},
		linkRepo: linkRepo,
		tagRepo:  tagRepo,
		authz:    authz,
	}
}

// SetEventEmitter wires the automation trigger callback. Called from main.go
// after the automation engine is initialized (matching the per-handler emitters).
// Until set, writes simply skip event emission.
func (s *recordService) SetEventEmitter(fn domain.RecordEventEmitter) {
	s.emitEvent = fn
	// The deal adapter fires deal_stage_changed from the uniform write path (P7),
	// so it needs the same emitter.
	if da, ok := s.systemAdapters["deal"].(*dealAdapter); ok {
		da.emit = fn
	}
}

const defaultRecordLimit = 25

// List returns one uniform page of records for an object, system or custom.
func (s *recordService) List(ctx context.Context, orgID uuid.UUID, slug string, in domain.RecordListInput) (*domain.RecordList, error) {
	if err := s.authorize(ctx, orgID, slug, domain.ActionRead); err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 || limit > 100 {
		limit = defaultRecordLimit
	}

	in.Limit = limit
	var out *domain.RecordList
	if a, ok := s.systemAdapters[slug]; ok {
		recs, next, err := a.list(ctx, orgID, in)
		if err != nil {
			return nil, err
		}
		out = &domain.RecordList{Records: recs, NextCursor: next}
	} else {
		var err error
		out, err = s.listCustom(ctx, orgID, slug, limit, in.Q, in.Cursor)
		if err != nil {
			return nil, err
		}
	}

	// FLS: strip hidden fields once for the whole page (read-only fields stay).
	if mask := s.fieldMask(ctx, orgID, slug); !mask.Empty() {
		for i := range out.Records {
			applyFieldMask(mask, &out.Records[i])
		}
	}
	return out, nil
}

// Get returns a single uniform record. It is the OLS-enforced public entry; all
// internal callers (link/tag/audit helpers) use getInternal so they never
// re-trigger an OLS check on a record the caller is already operating on.
func (s *recordService) Get(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) (*domain.UniformRecord, error) {
	if err := s.authorize(ctx, orgID, slug, domain.ActionRead); err != nil {
		return nil, err
	}
	rec, err := s.getInternal(ctx, orgID, slug, id)
	if err != nil {
		return nil, err
	}
	applyFieldMask(s.fieldMask(ctx, orgID, slug), rec) // FLS: strip hidden fields
	return rec, nil
}

// getInternal resolves a record without an OLS check (trusted internal read).
func (s *recordService) getInternal(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) (*domain.UniformRecord, error) {
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
	uniform := customToUniform(slug, rec)
	applyCustomDisplay(def, uniform) // R8: resolve title from the live field defs
	return uniform, nil
}

// Create validates and creates a record, returning the uniform shape.
func (s *recordService) Create(ctx context.Context, orgID, userID uuid.UUID, slug string, in domain.RecordWriteInput) (*domain.UniformRecord, error) {
	if err := s.authorize(ctx, orgID, slug, domain.ActionCreate); err != nil {
		return nil, err
	}
	// FLS write-guard before any persistence. The mask is reused below to strip the
	// response, so a viewer who can create a record still can't see a field hidden
	// from them in the create echo.
	mask := s.fieldMask(ctx, orgID, slug)
	if err := guardFieldWrites(mask, in.Fields); err != nil {
		return nil, err
	}

	if a, ok := s.systemAdapters[slug]; ok {
		if err := s.validateSystemCustomFields(ctx, orgID, slug, in.Fields, a); err != nil {
			return nil, err
		}
		rec, err := a.create(ctx, orgID, in.Fields)
		if err != nil {
			return nil, err
		}
		s.auditCreate(ctx, orgID, slug, rec, in.Fields)
		applyFieldMask(mask, rec)
		return rec, nil
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
	s.auditCreate(ctx, orgID, slug, uniform, in.Fields)
	s.fireEvent(orgID, slug+"_created", uniform) // automation sees the full record
	s.indexRecord(ctx, orgID, slug, uniform)     // search index sees the full record
	applyFieldMask(mask, uniform)                // strip only the response
	return uniform, nil
}

// Update validates and applies a partial update, returning the uniform shape. It
// reads the prior record first (no OLS) so the audit can capture a field-level
// before/after diff for exactly the keys the caller changed.
func (s *recordService) Update(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID, in domain.RecordWriteInput) (*domain.UniformRecord, error) {
	if err := s.authorize(ctx, orgID, slug, domain.ActionEdit); err != nil {
		return nil, err
	}
	// FLS write-guard before the prior read and any persistence; reused to strip
	// the response below.
	mask := s.fieldMask(ctx, orgID, slug)
	if err := guardFieldWrites(mask, in.Fields); err != nil {
		return nil, err
	}
	// Best-effort prior snapshot for the diff; a load error here is ignored and
	// surfaced authoritatively by the write itself below.
	prior, _ := s.getInternal(ctx, orgID, slug, id)

	if a, ok := s.systemAdapters[slug]; ok {
		if err := s.validateSystemCustomFields(ctx, orgID, slug, in.Fields, a); err != nil {
			return nil, err
		}
		rec, err := a.update(ctx, orgID, id, in.Fields)
		if err != nil {
			return nil, err
		}
		s.auditUpdate(ctx, orgID, slug, rec, prior, in.Fields)
		applyFieldMask(mask, rec)
		return rec, nil
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
	s.auditUpdate(ctx, orgID, slug, uniform, prior, in.Fields)
	s.fireEvent(orgID, slug+"_updated", uniform) // automation sees the full record
	s.indexRecord(ctx, orgID, slug, uniform)     // search index sees the full record
	applyFieldMask(mask, uniform)                // strip only the response
	return uniform, nil
}

// Delete soft-deletes a record, then cascade-soft-deletes every relationship/tag
// edge touching it (R3 — object_links has no DB foreign key on its polymorphic
// endpoints, so integrity is enforced here). The cascade runs after a successful
// delete and is idempotent, so a retry after a mid-flight failure converges.
func (s *recordService) Delete(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) error {
	if err := s.authorize(ctx, orgID, slug, domain.ActionDelete); err != nil {
		return err
	}

	if a, ok := s.systemAdapters[slug]; ok {
		if err := a.delete(ctx, orgID, id); err != nil {
			return err
		}
		s.auditDelete(ctx, orgID, slug, id)
		return s.cascadeLinks(ctx, orgID, slug, id)
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
	if err := s.customObjUC.DeleteRecord(ctx, orgID, id); err != nil {
		return err
	}
	s.auditDelete(ctx, orgID, slug, id)
	s.unindexRecord(ctx, orgID, slug, id) // drop the record from the search index
	return s.cascadeLinks(ctx, orgID, slug, id)
}

// cascadeLinks soft-deletes the deleted record's edges. A nil linkRepo (some unit
// tests) simply skips the cascade.
func (s *recordService) cascadeLinks(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) error {
	if s.linkRepo == nil {
		return nil
	}
	return s.linkRepo.CascadeSoftDelete(ctx, orgID, slug, id)
}

// ============================================================
// Object-Level Security + audit (P5a)
// ============================================================

// authorize delegates the OLS decision to the injected authorizer. A nil
// authorizer (unit tests not exercising security) means "allow". The authorizer
// itself reads the caller from the context, bypasses for owner/trusted calls, and
// default-denies otherwise.
func (s *recordService) authorize(ctx context.Context, orgID uuid.UUID, slug string, action domain.RecordAction) error {
	if s.authz == nil {
		return nil
	}
	return s.authz.Authorize(ctx, orgID, slug, action)
}

// actor returns the user id behind the request, or uuid.Nil for a trusted
// in-process call. Used to stamp the audit row.
func (s *recordService) actor(ctx context.Context) uuid.UUID {
	if c, ok := domain.CallerFromContext(ctx); ok {
		return c.UserID
	}
	return uuid.Nil
}

func (s *recordService) auditCreate(ctx context.Context, orgID uuid.UUID, slug string, rec *domain.UniformRecord, input map[string]interface{}) {
	if s.authz == nil || rec == nil {
		return
	}
	changes := map[string]interface{}{}
	for k := range input {
		changes[k] = map[string]interface{}{"new": rec.Fields[k]}
	}
	s.authz.Audit(ctx, domain.AuditEntry{
		OrgID:      orgID,
		ActorID:    s.actor(ctx),
		ObjectSlug: slug,
		RecordID:   rec.ID,
		Action:     domain.ActionCreate,
		Changes:    changes,
	})
}

// auditUpdate records a field-level before/after diff for exactly the keys the
// caller submitted, dropping keys whose value didn't actually change.
func (s *recordService) auditUpdate(ctx context.Context, orgID uuid.UUID, slug string, rec, prior *domain.UniformRecord, input map[string]interface{}) {
	if s.authz == nil || rec == nil {
		return
	}
	changes := map[string]interface{}{}
	for k := range input {
		var oldVal, newVal interface{}
		if prior != nil {
			oldVal = prior.Fields[k]
		}
		newVal = rec.Fields[k]
		if reflect.DeepEqual(oldVal, newVal) {
			continue // no-op write to this field — don't log noise
		}
		changes[k] = map[string]interface{}{"old": oldVal, "new": newVal}
	}
	if len(changes) == 0 {
		return // nothing actually changed
	}
	s.authz.Audit(ctx, domain.AuditEntry{
		OrgID:      orgID,
		ActorID:    s.actor(ctx),
		ObjectSlug: slug,
		RecordID:   rec.ID,
		Action:     domain.ActionEdit,
		Changes:    changes,
	})
}

func (s *recordService) auditDelete(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) {
	if s.authz == nil {
		return
	}
	s.authz.Audit(ctx, domain.AuditEntry{
		OrgID:      orgID,
		ActorID:    s.actor(ctx),
		ObjectSlug: slug,
		RecordID:   id,
		Action:     domain.ActionDelete,
		Changes:    map[string]interface{}{},
	})
}

// ============================================================
// Field-Level Security (P5b)
// ============================================================
//
// FLS is opt-in and enforced here, in the same chokepoint as OLS: hidden fields
// are stripped from every read response (strip before serialize), and writes to a
// hidden/read-only field are rejected outright. Automation/audit still see the
// full record — masking applies only to the JSON returned to the human caller —
// because the trigger payload and the audit diff are trusted internal consumers,
// not the API surface FLS is protecting.

// fieldMask returns the caller's FLS restrictions for an object, or the empty mask
// when FLS is disabled (no authorizer wired, as in unit tests that pass nil).
func (s *recordService) fieldMask(ctx context.Context, orgID uuid.UUID, slug string) domain.FieldMask {
	if s.authz == nil {
		return domain.FieldMask{}
	}
	return s.authz.FieldMask(ctx, orgID, slug)
}

// applyFieldMask strips hidden fields from a record's Fields before it is
// serialized (plan §7.4). Read-only fields stay visible; only hidden ones are
// removed. A no-op for the empty mask, so unrestricted objects pay nothing.
//
// Scope boundary: the derived Display title is intentionally NOT masked. It is the
// object's public label (a composite for system objects, the display-field value
// for custom ones), so hiding the field that *produces* the title is a degenerate
// config that would leave records labelless. FLS targets sensitive payload fields
// (salary, SSN), which are never an object's title; protecting the title itself is
// out of scope by design rather than by oversight.
func applyFieldMask(mask domain.FieldMask, rec *domain.UniformRecord) {
	if rec == nil || len(mask.Hidden) == 0 {
		return
	}
	for key := range mask.Hidden {
		delete(rec.Fields, key)
	}
}

// guardFieldWrites rejects a write touching any field the caller may not edit
// (hidden or read-only), failing the whole write with a 403 rather than silently
// dropping the field — so the caller learns the field is protected (plan P5b
// "reject writes to them").
func guardFieldWrites(mask domain.FieldMask, fields map[string]interface{}) error {
	if mask.Empty() {
		return nil
	}
	for key := range fields {
		if !mask.CanWrite(key) {
			return domain.NewAppError(http.StatusForbidden, "you do not have permission to edit the '"+key+"' field")
		}
	}
	return nil
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

	// R8: resolve each record's title from the live field defs (read once for the
	// whole page, not per record), so a renamed/reordered display field can't leave
	// a stale title behind. Falls back to the stored display_name when the def or
	// field value is unavailable.
	var def *domain.CustomObjectDef
	if d, derr := s.customObjUC.GetDefBySlug(ctx, orgID, slug); derr == nil {
		def = d
	}

	out := make([]domain.UniformRecord, 0, len(recs))
	for i := range recs {
		u := customToUniform(slug, &recs[i])
		applyCustomDisplay(def, u)
		out = append(out, *u)
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

// applyCustomDisplay overrides a custom record's title with the current value of
// its definition's display field, computed at read time (R8). This replaces the
// fragile "display_name captured at write time" behaviour: if an admin renames or
// reorders fields, the title now follows the live schema instead of rotting. When
// the def is missing or the display field is empty, the stored display_name (set
// in customToUniform) stands. Reuses the same display-field heuristic the registry
// schema uses (customDisplayField), so list/detail/schema all agree.
func applyCustomDisplay(def *domain.CustomObjectDef, rec *domain.UniformRecord) {
	if def == nil {
		return
	}
	key := customDisplayField(parseFieldDefs(def.Fields))
	if key == "" {
		return
	}
	if v, ok := rec.Fields[key]; ok {
		if s := displayString(v); s != "" {
			rec.Display = s
		}
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
