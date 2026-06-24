package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// systemObjectAdapter bridges one system object's typed usecase to the uniform
// record shape. Each adapter knows how to project its typed struct into a flat
// field map and how to build that struct's create/update input from one — so the
// rest of the engine never special-cases contact vs deal vs company.
type systemObjectAdapter interface {
	list(ctx context.Context, orgID uuid.UUID, in domain.RecordListInput) ([]domain.UniformRecord, string, error)
	get(ctx context.Context, orgID, id uuid.UUID) (*domain.UniformRecord, error)
	create(ctx context.Context, orgID uuid.UUID, fields map[string]interface{}) (*domain.UniformRecord, error)
	update(ctx context.Context, orgID, id uuid.UUID, fields map[string]interface{}) (*domain.UniformRecord, error)
	delete(ctx context.Context, orgID, id uuid.UUID) error
	// nativeKeys are the field keys backed by typed columns (plus reserved
	// columns). Anything else in a write payload is an admin-defined custom field
	// stored in the row's custom_fields blob.
	nativeKeys() map[string]bool
}

// Native/reserved key sets. Relation keys (e.g. "company") and ownership columns
// are listed so they never leak into the custom_fields JSONB blob.
var (
	contactNativeKeys = map[string]bool{
		"first_name": true, "last_name": true, "email": true,
		"phone": true, "company": true, "owner_user_id": true,
	}
	companyNativeKeys = map[string]bool{
		"name": true, "industry": true, "website": true,
	}
	dealNativeKeys = map[string]bool{
		"title": true, "value": true, "probability": true, "stage": true,
		"contact": true, "company": true, "expected_close_at": true, "owner_user_id": true,
	}
)

// ============================================================
// Contact
// ============================================================

type contactAdapter struct{ uc domain.ContactUseCase }

func (a *contactAdapter) nativeKeys() map[string]bool { return contactNativeKeys }

func (a *contactAdapter) list(ctx context.Context, orgID uuid.UUID, in domain.RecordListInput) ([]domain.UniformRecord, string, error) {
	contacts, next, err := a.uc.List(ctx, orgID, domain.ContactFilter{
		Q:           in.Q,
		Limit:       in.Limit,
		Cursor:      in.Cursor,
		Semantic:    in.Semantic,
		CompanyID:   filterUUID(in.Filters, "company"),
		OwnerUserID: filterUUID(in.Filters, "owner_user_id"),
		TagIDs:      in.TagIDs,
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]domain.UniformRecord, 0, len(contacts))
	for i := range contacts {
		out = append(out, *contactToUniform(&contacts[i]))
	}
	return out, next, nil
}

func (a *contactAdapter) get(ctx context.Context, orgID, id uuid.UUID) (*domain.UniformRecord, error) {
	c, err := a.uc.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	return contactToUniform(c), nil
}

func (a *contactAdapter) create(ctx context.Context, orgID uuid.UUID, fields map[string]interface{}) (*domain.UniformRecord, error) {
	firstName, _ := stringField(fields, "first_name")
	if strings.TrimSpace(firstName) == "" {
		return nil, domain.NewAppError(400, "first_name is required")
	}
	companyID, err := uuidField(fields, "company")
	if err != nil {
		return nil, err
	}
	ownerID, err := uuidField(fields, "owner_user_id")
	if err != nil {
		return nil, err
	}
	input := domain.CreateContactInput{
		FirstName:    firstName,
		LastName:     derefStr(strPtr(fields, "last_name")),
		Email:        strPtr(fields, "email"),
		Phone:        strPtr(fields, "phone"),
		CompanyID:    companyID,
		OwnerUserID:  ownerID,
		CustomFields: customFieldsJSON(fields, contactNativeKeys),
	}
	c, err := a.uc.Create(ctx, orgID, input)
	if err != nil {
		return nil, err
	}
	return contactToUniform(c), nil
}

func (a *contactAdapter) update(ctx context.Context, orgID, id uuid.UUID, fields map[string]interface{}) (*domain.UniformRecord, error) {
	var input domain.UpdateContactInput
	if v := strPtr(fields, "first_name"); v != nil {
		input.FirstName = v
	}
	if v := strPtr(fields, "last_name"); v != nil {
		input.LastName = v
	}
	if _, ok := fields["email"]; ok {
		input.Email = strPtr(fields, "email")
	}
	if _, ok := fields["phone"]; ok {
		input.Phone = strPtr(fields, "phone")
	}
	if _, ok := fields["company"]; ok {
		cid, err := uuidField(fields, "company")
		if err != nil {
			return nil, err
		}
		if cid != nil { // partial-update pattern: a relation can be set but not cleared here
			input.CompanyID = cid
		}
	}
	if _, ok := fields["owner_user_id"]; ok {
		oid, err := uuidField(fields, "owner_user_id")
		if err != nil {
			return nil, err
		}
		if oid != nil {
			input.OwnerUserID = oid
		}
	}
	if cf, ok := customFieldsJSONIfAny(fields, contactNativeKeys); ok {
		input.CustomFields = &cf
	}
	c, err := a.uc.Update(ctx, orgID, id, input)
	if err != nil {
		return nil, err
	}
	return contactToUniform(c), nil
}

func (a *contactAdapter) delete(ctx context.Context, orgID, id uuid.UUID) error {
	return a.uc.Delete(ctx, orgID, id)
}

func contactToUniform(c *domain.Contact) *domain.UniformRecord {
	fields := mergeCustomFields(c.CustomFields)
	fields["first_name"] = c.FirstName
	fields["last_name"] = c.LastName
	fields["email"] = derefStr(c.Email)
	fields["phone"] = derefStr(c.Phone)
	fields["company"] = uuidStr(c.CompanyID)

	display := strings.TrimSpace(c.FirstName + " " + c.LastName)
	if display == "" {
		display = derefStr(c.Email)
	}
	if display == "" {
		display = "Untitled"
	}
	return &domain.UniformRecord{
		ID: c.ID, Object: "contact", Display: display, Fields: fields,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// ============================================================
// Company
// ============================================================

type companyAdapter struct{ uc domain.CompanyUseCase }

func (a *companyAdapter) nativeKeys() map[string]bool { return companyNativeKeys }

func (a *companyAdapter) list(ctx context.Context, orgID uuid.UUID, in domain.RecordListInput) ([]domain.UniformRecord, string, error) {
	companies, next, err := a.uc.List(ctx, orgID, domain.CompanyFilter{Q: in.Q, Limit: in.Limit, Cursor: in.Cursor})
	if err != nil {
		return nil, "", err
	}
	out := make([]domain.UniformRecord, 0, len(companies))
	for i := range companies {
		out = append(out, *companyToUniform(&companies[i]))
	}
	return out, next, nil
}

func (a *companyAdapter) get(ctx context.Context, orgID, id uuid.UUID) (*domain.UniformRecord, error) {
	c, err := a.uc.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	return companyToUniform(c), nil
}

func (a *companyAdapter) create(ctx context.Context, orgID uuid.UUID, fields map[string]interface{}) (*domain.UniformRecord, error) {
	name, _ := stringField(fields, "name")
	if strings.TrimSpace(name) == "" {
		return nil, domain.NewAppError(400, "name is required")
	}
	input := domain.CreateCompanyInput{
		Name:         name,
		Industry:     strPtr(fields, "industry"),
		Website:      strPtr(fields, "website"),
		CustomFields: customFieldsJSON(fields, companyNativeKeys),
	}
	c, err := a.uc.Create(ctx, orgID, input)
	if err != nil {
		return nil, err
	}
	return companyToUniform(c), nil
}

func (a *companyAdapter) update(ctx context.Context, orgID, id uuid.UUID, fields map[string]interface{}) (*domain.UniformRecord, error) {
	var input domain.UpdateCompanyInput
	if v := strPtr(fields, "name"); v != nil {
		input.Name = v
	}
	if _, ok := fields["industry"]; ok {
		input.Industry = strPtr(fields, "industry")
	}
	if _, ok := fields["website"]; ok {
		input.Website = strPtr(fields, "website")
	}
	if cf, ok := customFieldsJSONIfAny(fields, companyNativeKeys); ok {
		input.CustomFields = &cf
	}
	c, err := a.uc.Update(ctx, orgID, id, input)
	if err != nil {
		return nil, err
	}
	return companyToUniform(c), nil
}

func (a *companyAdapter) delete(ctx context.Context, orgID, id uuid.UUID) error {
	return a.uc.Delete(ctx, orgID, id)
}

func companyToUniform(c *domain.Company) *domain.UniformRecord {
	fields := mergeCustomFields(c.CustomFields)
	fields["name"] = c.Name
	fields["industry"] = derefStr(c.Industry)
	fields["website"] = derefStr(c.Website)

	display := strings.TrimSpace(c.Name)
	if display == "" {
		display = "Untitled"
	}
	return &domain.UniformRecord{
		ID: c.ID, Object: "company", Display: display, Fields: fields,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// ============================================================
// Deal
// ============================================================

type dealAdapter struct {
	uc domain.DealUseCase
	// emit fires deal automation triggers from the uniform write path (P7). Wired
	// by RecordService.SetEventEmitter; nil before startup wiring (and in unit tests).
	emit domain.RecordEventEmitter
}

func (a *dealAdapter) nativeKeys() map[string]bool { return dealNativeKeys }

func (a *dealAdapter) list(ctx context.Context, orgID uuid.UUID, in domain.RecordListInput) ([]domain.UniformRecord, string, error) {
	deals, next, err := a.uc.List(ctx, orgID, domain.DealFilter{
		Q:           in.Q,
		Limit:       in.Limit,
		Cursor:      in.Cursor,
		StageID:     filterUUID(in.Filters, "stage"),
		ContactID:   filterUUID(in.Filters, "contact"),
		OwnerUserID: filterUUID(in.Filters, "owner_user_id"),
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]domain.UniformRecord, 0, len(deals))
	for i := range deals {
		out = append(out, *dealToUniform(&deals[i]))
	}
	return out, next, nil
}

func (a *dealAdapter) get(ctx context.Context, orgID, id uuid.UUID) (*domain.UniformRecord, error) {
	d, err := a.uc.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	return dealToUniform(d), nil
}

func (a *dealAdapter) create(ctx context.Context, orgID uuid.UUID, fields map[string]interface{}) (*domain.UniformRecord, error) {
	title, _ := stringField(fields, "title")
	if strings.TrimSpace(title) == "" {
		return nil, domain.NewAppError(400, "title is required")
	}
	contactID, err := uuidField(fields, "contact")
	if err != nil {
		return nil, err
	}
	companyID, err := uuidField(fields, "company")
	if err != nil {
		return nil, err
	}
	stageID, err := uuidField(fields, "stage")
	if err != nil {
		return nil, err
	}
	value, _, err := floatField(fields, "value")
	if err != nil {
		return nil, err
	}
	probability, _, err := intField(fields, "probability")
	if err != nil {
		return nil, err
	}
	closeAt, err := dateField(fields, "expected_close_at")
	if err != nil {
		return nil, err
	}
	ownerID, err := uuidField(fields, "owner_user_id")
	if err != nil {
		return nil, err
	}
	input := domain.CreateDealInput{
		Title:           title,
		ContactID:       contactID,
		CompanyID:       companyID,
		StageID:         stageID,
		Value:           value,
		Probability:     probability,
		ExpectedCloseAt: closeAt,
		OwnerUserID:     ownerID,
		CustomFields:    customFieldsJSON(fields, dealNativeKeys),
	}
	d, err := a.uc.Create(ctx, orgID, input)
	if err != nil {
		return nil, err
	}
	return dealToUniform(d), nil
}

func (a *dealAdapter) update(ctx context.Context, orgID, id uuid.UUID, fields map[string]interface{}) (*domain.UniformRecord, error) {
	// A stage change is applied through ChangeStage (won/lost close logic +
	// auto-activity) and fires deal_stage_changed — the same side-effects the
	// kanban always had. Routing every uniform stage change this way unifies the
	// legacy split where the kanban triggered side-effects but the edit form didn't.
	var stageID *uuid.UUID
	stagePresent := false
	if _, ok := fields["stage"]; ok {
		sid, err := uuidField(fields, "stage")
		if err != nil {
			return nil, err
		}
		stageID = sid
		stagePresent = stageID != nil
	}

	var input domain.UpdateDealInput
	nonStage := false
	if v := strPtr(fields, "title"); v != nil {
		input.Title = v
		nonStage = true
	}
	if _, ok := fields["contact"]; ok {
		cid, err := uuidField(fields, "contact")
		if err != nil {
			return nil, err
		}
		if cid != nil {
			input.ContactID = cid
			nonStage = true
		}
	}
	if _, ok := fields["company"]; ok {
		cid, err := uuidField(fields, "company")
		if err != nil {
			return nil, err
		}
		if cid != nil {
			input.CompanyID = cid
			nonStage = true
		}
	}
	if f, ok, err := floatField(fields, "value"); err != nil {
		return nil, err
	} else if ok {
		input.Value = &f
		nonStage = true
	}
	if n, ok, err := intField(fields, "probability"); err != nil {
		return nil, err
	} else if ok {
		input.Probability = &n
		nonStage = true
	}
	if _, ok := fields["expected_close_at"]; ok {
		closeAt, err := dateField(fields, "expected_close_at")
		if err != nil {
			return nil, err
		}
		input.ExpectedCloseAt = closeAt
		nonStage = true
	}
	if _, ok := fields["owner_user_id"]; ok {
		oid, err := uuidField(fields, "owner_user_id")
		if err != nil {
			return nil, err
		}
		if oid != nil { // partial-update: owner can be reassigned but not cleared here
			input.OwnerUserID = oid
			nonStage = true
		}
	}
	if cf, ok := customFieldsJSONIfAny(fields, dealNativeKeys); ok {
		input.CustomFields = &cf
		nonStage = true
	}

	// Capture the prior stage before any write, for the stage-changed event.
	var oldStageID *uuid.UUID
	if stagePresent {
		if old, err := a.uc.GetByID(ctx, orgID, id); err == nil && old != nil {
			oldStageID = old.StageID
		}
	}

	var d *domain.Deal
	var err error
	if nonStage {
		d, err = a.uc.Update(ctx, orgID, id, input)
		if err != nil {
			return nil, err
		}
	}
	if stagePresent {
		d, err = a.uc.ChangeStage(ctx, orgID, id, domain.UpdateDealStageInput{StageID: *stageID})
		if err != nil {
			return nil, err
		}
		a.fireStageChanged(orgID, oldStageID, d)
	}
	if d == nil { // empty payload — return current state unchanged
		d, err = a.uc.GetByID(ctx, orgID, id)
		if err != nil {
			return nil, err
		}
	}
	return dealToUniform(d), nil
}

// fireStageChanged emits the deal_stage_changed automation trigger (P7 workflow
// cutover) when a deal's stage actually moved, in the same payload shape the legacy
// deal handler produced (deal map keyed by stage_id/contact_id/…). Fire-and-forget
// with context.Background() so a cancelled request can't kill the async run.
func (a *dealAdapter) fireStageChanged(orgID uuid.UUID, oldStageID *uuid.UUID, d *domain.Deal) {
	if a.emit == nil || d == nil {
		return
	}
	newStageID := d.StageID
	changed := (oldStageID == nil) != (newStageID == nil) ||
		(oldStageID != nil && newStageID != nil && *oldStageID != *newStageID)
	if !changed {
		return
	}
	payload := map[string]any{
		"entity_id":    d.ID.String(),
		"deal":         dealAutomationMap(d),
		"old_stage_id": uuidStr(oldStageID),
		"new_stage_id": uuidStr(newStageID),
		"trigger": map[string]any{
			"type":   "deal_stage_changed",
			"source": "crm_ui",
		},
	}
	go a.emit(context.Background(), orgID, "deal_stage_changed", payload)
}

// dealAutomationMap mirrors the delivery layer's dealToMap so the uniform write
// path produces the exact deal shape the workflow engine's conditions/templates
// expect (stage_id/contact_id/company_id, is_won/is_lost, …).
func dealAutomationMap(d *domain.Deal) map[string]any {
	m := map[string]any{
		"id":          d.ID.String(),
		"title":       d.Title,
		"value":       d.Value,
		"probability": d.Probability,
		"is_won":      d.IsWon,
		"is_lost":     d.IsLost,
	}
	if d.ContactID != nil {
		m["contact_id"] = d.ContactID.String()
	}
	if d.CompanyID != nil {
		m["company_id"] = d.CompanyID.String()
	}
	if d.StageID != nil {
		m["stage_id"] = d.StageID.String()
	}
	if d.OwnerUserID != nil {
		m["owner_user_id"] = d.OwnerUserID.String()
	}
	if d.ExpectedCloseAt != nil {
		m["expected_close_at"] = d.ExpectedCloseAt.Format(time.RFC3339)
	}
	if d.ClosedAt != nil {
		m["closed_at"] = d.ClosedAt.Format(time.RFC3339)
	}
	return m
}

func (a *dealAdapter) delete(ctx context.Context, orgID, id uuid.UUID) error {
	return a.uc.Delete(ctx, orgID, id)
}

func dealToUniform(d *domain.Deal) *domain.UniformRecord {
	fields := mergeCustomFields(d.CustomFields)
	fields["title"] = d.Title
	fields["value"] = d.Value
	fields["probability"] = d.Probability
	fields["stage"] = uuidStr(d.StageID)
	fields["contact"] = uuidStr(d.ContactID)
	fields["company"] = uuidStr(d.CompanyID)
	if d.ExpectedCloseAt != nil {
		fields["expected_close_at"] = d.ExpectedCloseAt.Format("2006-01-02")
	} else {
		fields["expected_close_at"] = ""
	}

	display := strings.TrimSpace(d.Title)
	if display == "" {
		display = "Untitled"
	}
	return &domain.UniformRecord{
		ID: d.ID, Object: "deal", Display: display, Fields: fields,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

// ============================================================
// Custom-fields blob helpers
// ============================================================

// mergeCustomFields decodes a custom_fields JSONB blob into a field map. The
// native columns are overlaid on top of this by each projector.
func mergeCustomFields(raw domain.JSON) map[string]interface{} {
	out := map[string]interface{}{}
	if len(raw) == 0 {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]interface{}{}
	}
	return out
}

// customFieldsJSON marshals the non-native subset of a write payload for a
// create. nil means "no custom fields provided" (leave the column at its default).
func customFieldsJSON(fields map[string]interface{}, native map[string]bool) domain.JSON {
	sub := excludeKeys(fields, native)
	if len(sub) == 0 {
		return nil
	}
	raw, err := json.Marshal(sub)
	if err != nil {
		return nil
	}
	return domain.JSON(raw)
}

// customFieldsJSONIfAny is the update variant: ok is false when the payload
// carries no custom-field keys, so the existing blob is left untouched rather
// than wiped.
func customFieldsJSONIfAny(fields map[string]interface{}, native map[string]bool) (domain.JSON, bool) {
	sub := excludeKeys(fields, native)
	if len(sub) == 0 {
		return nil, false
	}
	raw, err := json.Marshal(sub)
	if err != nil {
		return nil, false
	}
	return domain.JSON(raw), true
}

// ============================================================
// Field-extraction helpers (JSON-decoded map → typed values)
// ============================================================

func stringField(fields map[string]interface{}, key string) (string, bool) {
	v, ok := fields[key]
	if !ok || v == nil {
		return "", false
	}
	switch t := v.(type) {
	case string:
		return t, true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(t), true
	default:
		return fmt.Sprintf("%v", t), true
	}
}

// strPtr returns a pointer to the string value when the key is present (so an
// empty string can clear a text column), or nil when the key is absent.
func strPtr(fields map[string]interface{}, key string) *string {
	v, ok := fields[key]
	if !ok || v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		s = fmt.Sprintf("%v", v)
	}
	return &s
}

// uuidField parses a relation value. Absent, null, or empty → (nil, nil).
func uuidField(fields map[string]interface{}, key string) (*uuid.UUID, error) {
	v, ok := fields[key]
	if !ok || v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, domain.NewAppError(400, key+" must be a UUID string")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil, domain.NewAppError(400, key+" is not a valid UUID")
	}
	return &id, nil
}

// floatField returns (value, present, error). A present-but-empty string counts
// as not present so it doesn't force a zero.
func floatField(fields map[string]interface{}, key string) (float64, bool, error) {
	v, ok := fields[key]
	if !ok || v == nil {
		return 0, false, nil
	}
	switch t := v.(type) {
	case float64:
		return t, true, nil
	case float32:
		return float64(t), true, nil
	case int:
		return float64(t), true, nil
	case int64:
		return float64(t), true, nil
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return 0, false, domain.NewAppError(400, key+" must be a number")
		}
		return f, true, nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false, nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false, domain.NewAppError(400, key+" must be a number")
		}
		return f, true, nil
	default:
		return 0, false, domain.NewAppError(400, key+" must be a number")
	}
}

func intField(fields map[string]interface{}, key string) (int, bool, error) {
	f, ok, err := floatField(fields, key)
	if err != nil || !ok {
		return 0, ok, err
	}
	return int(f), true, nil
}

// dateField returns an RFC3339 string pointer (what the deal usecase expects),
// accepting either RFC3339 or YYYY-MM-DD input. Absent/empty → (nil, nil).
func dateField(fields map[string]interface{}, key string) (*string, error) {
	v, ok := fields[key]
	if !ok || v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, domain.NewAppError(400, key+" must be a date string")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if _, err := time.Parse(time.RFC3339, s); err == nil {
		return &s, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		out := t.UTC().Format(time.RFC3339)
		return &out, nil
	}
	return nil, domain.NewAppError(400, key+" must be YYYY-MM-DD or RFC3339")
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// filterUUID parses a relation/owner filter value from the uniform list query into
// a *uuid.UUID, returning nil when the key is absent, blank, or not a valid UUID
// (an unparseable filter is ignored rather than erroring the whole list).
func filterUUID(filters map[string]string, key string) *uuid.UUID {
	if filters == nil {
		return nil
	}
	v := strings.TrimSpace(filters[key])
	if v == "" {
		return nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil
	}
	return &id
}

// displayString renders a JSON-decoded field value as a record title. Strings
// pass through; numbers/bools are stringified; nil becomes "". Used by the R8
// read-time display resolution.
func displayString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func uuidStr(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
