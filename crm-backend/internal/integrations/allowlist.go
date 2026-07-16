package integrations

import (
	"context"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// SchemaProvider is the narrow slice of the object registry this package needs:
// what fields an object legally has. Narrow on purpose — integrations must not
// depend on the whole registry usecase.
type SchemaProvider interface {
	GetSchema(ctx context.Context, orgID uuid.UUID, slug string) (*domain.ObjectDescriptor, error)
}

// blacklistedKeys are never writable from an inbound payload, whatever the schema
// says. This is not defence in depth — it is THE defence.
//
// Ingestion writes as a trusted callerless actor, so Authorize returns nil and the
// FLS field mask is empty: RecordService will write whatever it is handed. Nothing
// below this package rejects anything (fieldvalidate explicitly allows unknown
// keys; customFieldsJSON sweeps every non-native key into the blob). So:
//
//   - owner_user_id: the contact adapter reads a present-but-null owner as
//     UNASSIGN, so a payload carrying it could strip reps off records at will —
//     or, set to a chosen id, hand an attacker ownership. Ownership comes from the
//     source's config, never from the wire.
//   - company (and any relation-typed field): the adapter runs it through
//     uuidField, so a lead containing a company NAME — the single most likely key
//     a third-party payload carries — would 400 the whole lead. Quarantining it
//     accepts the lead and keeps the value visible in the ledger.
var blacklistedKeys = map[string]bool{
	"owner_user_id": true,
	"company":       true,
	"id":            true,
	"org_id":        true,
	"created_by":    true,
	"created_at":    true,
	"updated_at":    true,
	"deleted_at":    true,
}

// Allowlist is the set of field keys an inbound payload may write for one object.
type Allowlist struct {
	slug string
	keys map[string]bool
}

// BuildAllowlist derives the writable keys for a target object from the registry.
//
// It reads the REGISTRY rather than the usecase package's native-key maps, which
// are unexported anyway and already disagree with the registry (they carry
// owner_user_id; the registry does not). The registry is the side of that
// disagreement that cannot silently widen what a stranger may write.
//
// L1 SCOPE — system fields only; custom fields are quarantined. Not a
// simplification, a landmine: RecordService.validateSystemCustomFields
// early-returns only when a payload carries ZERO custom keys. Send one, and
// ValidateFields runs its required-check across the org's WHOLE definition set and
// 400s the lead for every required custom field the third party did not send. So
// one mapped custom key rejects the entire lead in any org with an unrelated
// required field. L2's mapping engine owns custom fields and must call
// ValidateValue per key rather than letting ValidateFields' required-loop near an
// ingest payload.
func BuildAllowlist(ctx context.Context, schema SchemaProvider, orgID uuid.UUID, slug string) (*Allowlist, error) {
	desc, err := schema.GetSchema(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}
	if desc == nil {
		return nil, domain.NewAppError(400, "unknown target object: "+slug)
	}
	keys := map[string]bool{}
	for _, f := range desc.Fields {
		if !f.IsSystem {
			continue // custom fields: L2
		}
		if f.Type == "relation" {
			continue // needs a resolved UUID; a lead carries a name
		}
		if blacklistedKeys[f.Key] {
			continue
		}
		keys[f.Key] = true
	}
	return &Allowlist{slug: slug, keys: keys}, nil
}

// Keys returns the permitted field keys (test/diagnostic helper).
func (a *Allowlist) Keys() []string {
	out := make([]string, 0, len(a.keys))
	for k := range a.keys {
		out = append(out, k)
	}
	return out
}

// Apply splits an inbound field map into what may be written and what may not.
//
// Quarantined keys are returned, never dropped: a customer who mapped a field we
// silently discarded finds out weeks later, from missing data. Empty-string values
// are quarantined too — `""` is not "absent" to the contact adapter, it is a value
// that OVERWRITES, so a provider sending {"last_name":""} would blank a real name
// even under fill_blank_only.
func (a *Allowlist) Apply(fields map[string]any) (allowed map[string]any, quarantined map[string]any) {
	allowed = map[string]any{}
	quarantined = map[string]any{}
	for k, v := range fields {
		if !a.keys[k] {
			quarantined[k] = v
			continue
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			quarantined[k] = v
			continue
		}
		if v == nil {
			quarantined[k] = v
			continue
		}
		allowed[k] = v
	}
	return allowed, quarantined
}
