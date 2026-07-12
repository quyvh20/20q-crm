package http

import (
	"context"
	"encoding/json"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// Field-Level Security for the LEGACY per-object routes (/contacts, /companies,
// /deals). The registry path strips hidden fields and guards writes inside
// RecordService; these routes return typed rows and bind typed inputs, so the
// same mask is applied here at the delivery boundary instead — the admin Field
// Security grid governs both surfaces (U0.1). The OLS-read gate is the route
// middleware added alongside in router.go.

// fieldMasker is the narrow slice of the permission engine the legacy handlers
// need. Satisfied by domain.PermissionUseCase; nil (as in handler unit tests)
// means the empty mask, so behavior there is unchanged.
type fieldMasker interface {
	FieldMask(ctx context.Context, orgID uuid.UUID, slug string) domain.FieldMask
}

// legacyFieldAliases maps a registry field key to the legacy JSON keys it
// covers. Relation fields surface on the legacy shape as both the FK column and
// the preloaded object, so hiding the field hides both.
var legacyFieldAliases = map[string]map[string][]string{
	"contact": {
		"company":       {"company_id", "company"},
		"owner_user_id": {"owner_user_id", "owner"},
	},
	"deal": {
		"contact":       {"contact_id", "contact"},
		"company":       {"company_id", "company"},
		"stage":         {"stage_id", "stage"},
		"owner_user_id": {"owner_user_id", "owner"},
	},
	"company": {},
}

// legacyMask resolves the caller's FLS mask for an object, tolerating a nil
// masker (tests) with the empty mask.
func legacyMask(masker fieldMasker, ctx context.Context, orgID uuid.UUID, slug string) domain.FieldMask {
	if masker == nil {
		return domain.FieldMask{}
	}
	return masker.FieldMask(ctx, orgID, slug)
}

// maskLegacy strips FLS-hidden fields from a legacy response payload (one row
// or a slice) by round-tripping through JSON. The empty mask returns the
// payload untouched, so unrestricted orgs pay nothing. On any marshal error the
// payload is returned as-is rather than failing the request — the same
// best-effort stance RecordService takes on its audit writes — because by this
// point OLS has already authorized the read.
func maskLegacy(mask domain.FieldMask, slug string, payload any) any {
	if len(mask.Hidden) == 0 {
		return payload
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return payload
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return payload
	}
	switch v := generic.(type) {
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				stripHiddenLegacy(mask, slug, m)
			}
		}
	case map[string]any:
		stripHiddenLegacy(mask, slug, v)
	}
	return generic
}

func stripHiddenLegacy(mask domain.FieldMask, slug string, m map[string]any) {
	for key := range mask.Hidden {
		names := legacyFieldAliases[slug][key]
		if len(names) == 0 {
			names = []string{key}
		}
		for _, n := range names {
			delete(m, n)
		}
		if cf, ok := m["custom_fields"].(map[string]any); ok {
			delete(cf, key)
		}
	}
}

// guardLegacyWrite rejects a legacy write touching any field the caller may not
// edit, failing the whole write rather than silently dropping the field — the
// same contract as RecordService's guardFieldWrites, same message.
func guardLegacyWrite(mask domain.FieldMask, keys []string) error {
	if mask.Empty() {
		return nil
	}
	for _, key := range keys {
		if !mask.CanWrite(key) {
			return domain.NewAppError(http.StatusForbidden, "you do not have permission to edit the '"+key+"' field")
		}
	}
	return nil
}

// customFieldKeys lists the keys inside a custom_fields blob (nil-safe), since
// the FLS grid keys custom fields flat by their own key.
func customFieldKeys(raw domain.JSON) []string {
	if len(raw) == 0 {
		return nil
	}
	var cf map[string]any
	if err := json.Unmarshal([]byte(raw), &cf); err != nil {
		return nil
	}
	keys := make([]string, 0, len(cf))
	for k := range cf {
		keys = append(keys, k)
	}
	return keys
}

// ── Touched-field extraction per legacy input type ──────────────────────────
// Keys are REGISTRY field keys (the mask's vocabulary): relation fields are
// "company"/"contact"/"stage", not the *_id column names. Create inputs count a
// field as touched only when a value was actually supplied (non-nil pointer /
// non-zero), mirroring how the registry path only sees provided keys.

func contactCreateKeys(in domain.CreateContactInput) []string {
	keys := []string{"first_name"}
	if in.LastName != "" {
		keys = append(keys, "last_name")
	}
	if in.Email != nil {
		keys = append(keys, "email")
	}
	if in.Phone != nil {
		keys = append(keys, "phone")
	}
	if in.CompanyID != nil {
		keys = append(keys, "company")
	}
	if in.OwnerUserID != nil {
		keys = append(keys, "owner_user_id")
	}
	return append(keys, customFieldKeys(in.CustomFields)...)
}

func contactUpdateKeys(in domain.UpdateContactInput) []string {
	var keys []string
	if in.FirstName != nil {
		keys = append(keys, "first_name")
	}
	if in.LastName != nil {
		keys = append(keys, "last_name")
	}
	if in.Email != nil {
		keys = append(keys, "email")
	}
	if in.Phone != nil {
		keys = append(keys, "phone")
	}
	if in.CompanyID != nil {
		keys = append(keys, "company")
	}
	if in.OwnerUserID != nil {
		keys = append(keys, "owner_user_id")
	}
	if in.CustomFields != nil {
		keys = append(keys, customFieldKeys(*in.CustomFields)...)
	}
	return keys
}

func companyCreateKeys(in domain.CreateCompanyInput) []string {
	keys := []string{"name"}
	if in.Industry != nil {
		keys = append(keys, "industry")
	}
	if in.Website != nil {
		keys = append(keys, "website")
	}
	return append(keys, customFieldKeys(in.CustomFields)...)
}

func companyUpdateKeys(in domain.UpdateCompanyInput) []string {
	var keys []string
	if in.Name != nil {
		keys = append(keys, "name")
	}
	if in.Industry != nil {
		keys = append(keys, "industry")
	}
	if in.Website != nil {
		keys = append(keys, "website")
	}
	if in.CustomFields != nil {
		keys = append(keys, customFieldKeys(*in.CustomFields)...)
	}
	return keys
}

func dealCreateKeys(in domain.CreateDealInput) []string {
	keys := []string{"title"}
	// Value/Probability are non-pointer on the legacy create input, so a zero is
	// indistinguishable from "not sent" — only a non-zero value counts as touched
	// (a read-only 'value' must not block creating a valueless deal).
	if in.Value != 0 {
		keys = append(keys, "value")
	}
	if in.Probability != 0 {
		keys = append(keys, "probability")
	}
	if in.ContactID != nil {
		keys = append(keys, "contact")
	}
	if in.CompanyID != nil {
		keys = append(keys, "company")
	}
	if in.StageID != nil {
		keys = append(keys, "stage")
	}
	if in.OwnerUserID != nil {
		keys = append(keys, "owner_user_id")
	}
	if in.ExpectedCloseAt != nil {
		keys = append(keys, "expected_close_at")
	}
	return append(keys, customFieldKeys(in.CustomFields)...)
}

func dealUpdateKeys(in domain.UpdateDealInput) []string {
	var keys []string
	if in.Title != nil {
		keys = append(keys, "title")
	}
	if in.Value != nil {
		keys = append(keys, "value")
	}
	if in.Probability != nil {
		keys = append(keys, "probability")
	}
	if in.ContactID != nil {
		keys = append(keys, "contact")
	}
	if in.CompanyID != nil {
		keys = append(keys, "company")
	}
	if in.StageID != nil {
		keys = append(keys, "stage")
	}
	if in.OwnerUserID != nil {
		keys = append(keys, "owner_user_id")
	}
	if in.ExpectedCloseAt != nil {
		keys = append(keys, "expected_close_at")
	}
	if in.CustomFields != nil {
		keys = append(keys, customFieldKeys(*in.CustomFields)...)
	}
	return keys
}
