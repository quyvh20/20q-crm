package usecase

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"crm-backend/internal/domain"
	"crm-backend/internal/fieldvalidate"

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
	// numberRepo allocates and resolves human-readable record numbers. nil (unit
	// tests, or before startup wiring) simply means records carry no Number.
	numberRepo domain.RecordNumberRepository
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
	// Each system adapter fires its own create/update/delete (and the deal adapter
	// also deal_stage_changed) from the uniform write path (A2), so they all need
	// the emitter. A double-fire with a legacy handler emitter for the same write
	// is absorbed by the engine's per-minute idempotency key.
	if ca, ok := s.systemAdapters["contact"].(*contactAdapter); ok {
		ca.emit = fn
	}
	if co, ok := s.systemAdapters["company"].(*companyAdapter); ok {
		co.emit = fn
	}
	if da, ok := s.systemAdapters["deal"].(*dealAdapter); ok {
		da.emit = fn
	}
}

// SetNumberRepo wires the human-readable record-number allocator. Called once at
// startup from main.go (mirrors SetEventEmitter/SetSearchIndexer). Until set,
// records carry no Number.
func (s *recordService) SetNumberRepo(repo domain.RecordNumberRepository) {
	s.numberRepo = repo
}

// applyNumbers fills rec.Number for a page of records from the number side table,
// in one batched lookup. A nil numberRepo or a lookup error leaves Number empty —
// numbering is a display nicety and must never fail a read.
func (s *recordService) applyNumbers(ctx context.Context, orgID uuid.UUID, slug string, recs []domain.UniformRecord) {
	if s.numberRepo == nil || len(recs) == 0 {
		return
	}
	ids := make([]uuid.UUID, len(recs))
	for i := range recs {
		ids[i] = recs[i].ID
	}
	nums, err := s.numberRepo.NumbersFor(ctx, orgID, slug, ids)
	if err != nil {
		return
	}
	for i := range recs {
		if n, ok := nums[recs[i].ID]; ok {
			recs[i].Number = n
		}
	}
}

// setRecordNumber resolves and stamps a single record's human-readable number.
// Best-effort: a nil repo or a lookup error leaves Number empty so numbering can
// never fail a read/write.
func (s *recordService) setRecordNumber(ctx context.Context, orgID uuid.UUID, slug string, rec *domain.UniformRecord) {
	if s.numberRepo == nil || rec == nil {
		return
	}
	if nums, err := s.numberRepo.NumbersFor(ctx, orgID, slug, []uuid.UUID{rec.ID}); err == nil {
		rec.Number = nums[rec.ID]
	}
}

// allocateAndSetNumber assigns a fresh record number on create and stamps it onto
// the response. Best-effort: a nil repo or an allocation error leaves Number empty
// so a numbering hiccup can never fail the create itself.
func (s *recordService) allocateAndSetNumber(ctx context.Context, orgID uuid.UUID, slug string, rec *domain.UniformRecord) {
	if s.numberRepo == nil {
		return
	}
	if err := s.numberRepo.Allocate(ctx, orgID, slug, rec.ID); err != nil {
		return
	}
	s.setRecordNumber(ctx, orgID, slug, rec)
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
		out, err = s.listCustom(ctx, orgID, slug, limit, in.Q, in.Cursor, in.Filters)
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
	s.applyNumbers(ctx, orgID, slug, out.Records)
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
	s.setRecordNumber(ctx, orgID, slug, rec)           // resolve the human-readable number
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
	rec, err := s.customObjUC.GetRecord(ctx, orgID, slug, id)
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
		s.allocateAndSetNumber(ctx, orgID, slug, rec)
		applyFieldMask(mask, rec)
		return rec, nil
	}

	// owner_user_id is a column, not a blob key — split it out before marshalling.
	owner, _, rest, err := splitCustomOwner(in.Fields)
	if err != nil {
		return nil, err
	}
	data, err := marshalFields(rest)
	if err != nil {
		return nil, err
	}
	rec, err := s.customObjUC.CreateRecord(ctx, orgID, userID, slug, domain.CreateRecordInput{Data: data, OwnerUserID: owner})
	if err != nil {
		return nil, err
	}
	uniform := customToUniform(slug, rec)
	s.auditCreate(ctx, orgID, slug, uniform, in.Fields)
	s.allocateAndSetNumber(ctx, orgID, slug, uniform)
	s.fireEvent(ctx, orgID, slug+"_created", uniform) // automation sees the full record
	s.indexRecord(ctx, orgID, slug, uniform)          // search index sees the full record
	applyFieldMask(mask, uniform)                     // strip only the response
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
	// Best-effort prior snapshot for the audit diff; a load error here is ignored and
	// surfaced authoritatively by the write itself below.
	//
	// NOTE on partial edits: the frontend PATCHes only the fields the user actually
	// changed. Native system columns handle that natively (present-key = "change this
	// column"), so an FLS read-only field the user didn't touch is simply absent and
	// the guard above lets the write through. The two wholesale-replaced BLOBs — the
	// custom-object data blob and a system object's custom_fields — instead merge the
	// incoming keys over the stored blob at their write site (customObjectUseCase.
	// UpdateRecord / the deal & contact usecases), so a partial edit never blanks an
	// untouched field there. Merging here at the uniform layer would be wrong: it would
	// re-introduce every native key and make "stage present" (→ ChangeStage) fire on
	// every deal edit.
	prior, _ := s.getInternal(ctx, orgID, slug, id)

	if a, ok := s.systemAdapters[slug]; ok {
		if err := s.validateSystemCustomFieldsForUpdate(ctx, orgID, slug, in.Fields, prior, a); err != nil {
			return nil, err
		}
		rec, err := a.update(ctx, orgID, id, in.Fields)
		if err != nil {
			return nil, err
		}
		s.auditUpdate(ctx, orgID, slug, rec, prior, in.Fields)
		s.setRecordNumber(ctx, orgID, slug, rec) // keep the number on the edit echo
		applyFieldMask(mask, rec)
		return rec, nil
	}

	owner, clearOwner, rest, err := splitCustomOwner(in.Fields)
	if err != nil {
		return nil, err
	}
	// The custom update MERGES the data blob over the stored record (see
	// customObjectUseCase.UpdateRecord), so a partial edit rewrites only the keys it
	// carries. An owner-only change carries no data keys, so send nil (an empty "{}"
	// would still be a no-op merge, but sending nil skips the blob write entirely).
	var data domain.JSON
	if len(rest) > 0 {
		if data, err = marshalFields(rest); err != nil {
			return nil, err
		}
	}
	rec, err := s.customObjUC.UpdateRecord(ctx, orgID, slug, id, domain.UpdateRecordInput{
		Data:        data,
		OwnerUserID: owner,
		ClearOwner:  clearOwner,
	})
	if err != nil {
		return nil, err
	}
	uniform := customToUniform(slug, rec)
	s.auditUpdate(ctx, orgID, slug, uniform, prior, in.Fields)
	s.setRecordNumber(ctx, orgID, slug, uniform)      // keep the number on the edit echo
	s.fireEvent(ctx, orgID, slug+"_updated", uniform) // automation sees the full record
	s.indexRecord(ctx, orgID, slug, uniform)          // search index sees the full record
	applyFieldMask(mask, uniform)                     // strip only the response
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
	rec, err := s.customObjUC.GetRecord(ctx, orgID, slug, id)
	if err != nil {
		return err
	}
	if rec.ObjectDefID != def.ID {
		return domain.NewAppError(http.StatusNotFound, "record not found")
	}
	// Snapshot the record before deletion so a {slug}_deleted workflow can
	// condition on its fields.
	deletedSnapshot := customToUniform(slug, rec)
	if err := s.customObjUC.DeleteRecord(ctx, orgID, slug, id); err != nil {
		return err
	}
	s.auditDelete(ctx, orgID, slug, id)
	s.unindexRecord(ctx, orgID, slug, id)                     // drop the record from the search index
	s.fireEvent(ctx, orgID, slug+"_deleted", deletedSnapshot) // automation sees the deleted record (A2)
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
			return domain.NewAppError(http.StatusForbidden, "the \""+key+"\" field is read-only for your role")
		}
	}
	return nil
}

// ============================================================
// Custom-object path
// ============================================================

func (s *recordService) listCustom(ctx context.Context, orgID uuid.UUID, slug string, limit int, q, cursor string, filters map[string]string) (*domain.RecordList, error) {
	offset := decodeOffsetCursor(cursor)
	recs, total, err := s.customObjUC.ListRecords(ctx, orgID, slug, domain.RecordFilter{
		Limit:   limit,
		Offset:  offset,
		Q:       q,
		Filters: filters,
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

// customToUniform projects a JSONB-backed record into the uniform shape. The owner
// comes off its column and is surfaced BOTH as UniformRecord.OwnerUserID (the
// first-class field) and inside Fields, so the generic renderer, the report field
// catalog and the list filters can address it like any other value without the
// registry having to carry an owner field row.
func customToUniform(slug string, rec *domain.CustomObjectRecord) *domain.UniformRecord {
	fields := map[string]interface{}{}
	if len(rec.Data) > 0 {
		_ = json.Unmarshal(rec.Data, &fields)
	}
	if rec.OwnerUserID != nil {
		fields["owner_user_id"] = rec.OwnerUserID.String()
	}
	return &domain.UniformRecord{
		ID:          rec.ID,
		Object:      slug,
		Display:     rec.DisplayName,
		OwnerUserID: rec.OwnerUserID,
		Fields:      fields,
		CreatedAt:   rec.CreatedAt,
		UpdatedAt:   rec.UpdatedAt,
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
func (s *recordService) fireEvent(ctx context.Context, orgID uuid.UUID, eventType string, rec *domain.UniformRecord) {
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
		"trigger":   triggerMeta(ctx, eventType),
	}
	markAutomationFlags(ctx, payload)
	go s.emitEvent(context.Background(), orgID, eventType, payload)
}

// triggerMeta builds the "trigger" sub-map every automation payload carries. The
// source names the channel the write came from, read off the context (see
// domain.WithWriteSource) rather than hardcoded per emitter as it used to be.
// Shared by all three emitters (fireEvent above; fireLifecycleEvent and
// fireStageChanged in the _system sibling), which is why it takes ctx.
//
// Naming a source is opt-in per entry point, and no entry point does it yet: UI,
// AI and automation-action writes all still resolve to domain.DefaultWriteSource
// ("crm_ui"), exactly as before. Only the mechanism landed here — each caller
// becomes distinguishable when its own entry point starts setting the source.
func triggerMeta(ctx context.Context, eventType string) map[string]any {
	return map[string]any{
		"type":   eventType,
		"source": domain.WriteSourceFromContext(ctx),
	}
}

// markAutomationFlags stamps the enrollment-suppression and silence flags onto a
// trigger payload when the write asked for them. Suppression skips run creation but
// still arms date_field timers; silence also stops the arming. Each key is set ONLY
// when true, so an ordinary write's payload keeps the exact shape it had before the
// flags existed — no workflow, test, or stored trigger context sees a new key.
//
// The two checks are independent statements, never an if/else chain. Silence
// derives suppression (see domain.IsAutomationSuppressed), so a silenced write must
// carry BOTH keys: the engine's enrollment guard reads only the suppression key, so
// an `else if` here would emit a payload marked silenced-but-not-suppressed and
// enroll every test lead — while looking, at this call site, like it had handled the
// stricter case.
func markAutomationFlags(ctx context.Context, payload map[string]any) {
	if domain.IsAutomationSuppressed(ctx) {
		payload[domain.AutomationSuppressedPayloadKey] = true
	}
	if domain.IsAutomationSilenced(ctx) {
		payload[domain.AutomationSilencedPayloadKey] = true
	}
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
	if domain.IsPartialWrite(ctx) {
		return s.validateCustomValuesOnly(ctx, orgID, slug, custom)
	}
	raw, err := json.Marshal(custom)
	if err != nil {
		return domain.NewAppError(http.StatusBadRequest, "invalid custom field values")
	}
	return s.orgSettingsUC.ValidateCustomFields(ctx, orgID, slug, domain.JSON(raw))
}

// validateCustomValuesOnly type-checks the values a write carries WITHOUT
// enforcing that the org's required fields are present — the semantics a
// non-form write needs (see domain.WithPartialWrite).
//
// It uses fieldvalidate.ValidateValue per key rather than ValidateFields, because
// ValidateValue is presence-blind by contract ("A nil value always passes —
// presence/required is handled by ValidateFields, not here") while ValidateFields
// unconditionally runs a required-loop over the whole definition set. Every value
// present is still checked exactly as strictly as a form write; only absence stops
// being an error.
func (s *recordService) validateCustomValuesOnly(ctx context.Context, orgID uuid.UUID, slug string, custom map[string]interface{}) error {
	defs, err := s.orgSettingsUC.GetFieldDefs(ctx, orgID, slug)
	if err != nil {
		return err
	}
	if len(defs) == 0 {
		return nil
	}
	byKey := make(map[string]domain.CustomFieldDef, len(defs))
	for _, d := range defs {
		byKey[d.Key] = d
	}
	for key, val := range custom {
		def, ok := byKey[key]
		if !ok {
			// Unknown keys are the allowlist's business, not the validator's — and
			// ValidateFields ignores them too, so this stays consistent with the
			// strict path rather than inventing a new rejection here.
			continue
		}
		if err := fieldvalidate.ValidateValue(def, val); err != nil {
			return domain.NewAppError(http.StatusBadRequest, fmt.Sprintf("custom_fields.%s: %s", key, err.Error()))
		}
	}
	return nil
}

// validateSystemCustomFieldsForUpdate validates a partial update against the
// record's RESULTING state rather than the delta the request happens to carry.
//
// Validating the delta alone was a live bug. The uniform edit form PATCHes only
// what the user changed, but fieldvalidate's required-check ranges over the ORG'S
// DEFINITIONS, not over the payload — so a required custom field the user never
// touched is absent from the delta while sitting on the row, and the edit 400s
// with "custom_fields.<other> is required". Reproduced: an org with a required
// `industry` could not save a change to `notes`, even though industry was stored.
//
// The custom-object path has merged-then-validated since it shipped, for exactly
// this reason ("so a required-field check sees the whole record, not just the
// delta"); the system-object branch never got the same treatment. This closes that.
//
// prior comes from getInternal, which does NOT apply the FLS mask, so an
// FLS-hidden required field still counts as present — the merge base is the real
// row, not the caller's view of it. A nil prior (load failed / not found) falls
// back to validating the delta: the update itself is about to fail anyway, and
// inventing an empty merge base would report the wrong error.
//
// Full CREATES keep using validateSystemCustomFields: a create has no prior state,
// so enforcing required across the definition set is exactly right there.
func (s *recordService) validateSystemCustomFieldsForUpdate(ctx context.Context, orgID uuid.UUID, slug string, fields map[string]interface{}, prior *domain.UniformRecord, a systemObjectAdapter) error {
	custom := excludeKeys(fields, a.nativeKeys())
	if len(custom) == 0 {
		return nil // no custom keys — nothing this validator governs
	}
	// A non-form write relaxes presence entirely, so there is nothing to merge FOR:
	// type-check the values it carries and stop. Without this branch an ingested
	// lead updating an existing contact still 400s on the org's required fields —
	// the merged view cannot supply a field the record never had.
	if domain.IsPartialWrite(ctx) {
		return s.validateCustomValuesOnly(ctx, orgID, slug, custom)
	}
	if prior == nil {
		return s.validateSystemCustomFields(ctx, orgID, slug, fields, a)
	}
	// Overlay the delta on the stored custom fields: validate the record as it will
	// BE, not as the request describes it.
	merged := excludeKeys(prior.Fields, a.nativeKeys())
	for k, v := range custom {
		merged[k] = v
	}
	raw, err := json.Marshal(merged)
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

// customNativeKeys are the uniform field keys that map to real COLUMNS on
// custom_object_records rather than into its JSONB data blob (the custom-object
// twin of the system adapters' native-key maps). Owner is the only one today.
//
// Without this split, marshalFields would stuff owner_user_id into the blob while
// the column stayed NULL: the record would render an owner, the row predicate would
// not see one, and the two would drift silently. That is the single nastiest
// failure mode in U6.3.
var customNativeKeys = map[string]bool{"owner_user_id": true}

// splitCustomOwner pulls owner_user_id out of the uniform field map and parses it.
//
//	owner != nil            → assign to that user
//	clear == true           → unassign (an explicit null/"" on the wire)
//	owner == nil && !clear  → not supplied; leave the record's owner untouched
//
// The returned map is the remainder, destined for the JSONB blob.
func splitCustomOwner(fields map[string]interface{}) (owner *uuid.UUID, clear bool, rest map[string]interface{}, err error) {
	rest = excludeKeys(fields, customNativeKeys)
	raw, present := fields["owner_user_id"]
	if !present {
		return nil, false, rest, nil
	}
	switch v := raw.(type) {
	case nil:
		return nil, true, rest, nil
	case string:
		if v == "" {
			return nil, true, rest, nil
		}
		id, perr := uuid.Parse(v)
		if perr != nil {
			return nil, false, rest, domain.NewAppError(http.StatusBadRequest, "owner_user_id must be a user id")
		}
		return &id, false, rest, nil
	case uuid.UUID:
		if v == uuid.Nil {
			return nil, true, rest, nil
		}
		return &v, false, rest, nil
	default:
		return nil, false, rest, domain.NewAppError(http.StatusBadRequest, "owner_user_id must be a user id")
	}
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
