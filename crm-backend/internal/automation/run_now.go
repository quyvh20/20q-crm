package automation

import (
	"errors"
	"fmt"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// run_now.go holds the pure-logic core for the Run Now feature (P20): request
// classification, trigger/entity compatibility, Trigger_Context construction, and
// unique idempotency-key generation. These functions are deterministic and free of
// I/O so they can be exercised directly by property-based tests. The HTTP handler
// (handlers.go) and the engine entry point (engine.go) compose them.

// Run Now request-classification errors. They are distinct sentinel values so both
// the handler and tests can tell the rejection cases apart (and the handler can map
// each to the precise 400 error the spec requires).
var (
	// ErrRunNowBothIDs is returned when a request supplies both contact_id and deal_id.
	ErrRunNowBothIDs = errors.New("exactly one of contact_id or deal_id is required, but both were provided")
	// ErrRunNowNoIDs is returned when a request supplies neither contact_id nor deal_id.
	ErrRunNowNoIDs = errors.New("exactly one of contact_id or deal_id is required, but neither was provided")
	// ErrRunNowInvalidUUID is returned when the single provided identifier is not a
	// syntactically valid UUID. It is wrapped with the offending field name so the
	// handler can name the invalid identifier; use errors.Is to detect this case.
	ErrRunNowInvalidUUID = errors.New("invalid identifier: not a valid UUID")
)

// entityKindForTrigger maps a workflow trigger type to the Sample_Entity kind that is
// compatible with it: "contact" for contact_created, contact_updated, and
// webhook_inbound; "deal" for deal_stage_changed; "" for any unsupported trigger type.
//
// webhook_inbound deliberately maps to "contact" rather than being disabled or requiring a
// pasted JSON payload: the real inbound-webhook path (WebhookInbound in handlers.go)
// extracts the email, upserts a contact, and emits a contact-shaped trigger context. So a
// Run Now against a sample contact faithfully reproduces what a production webhook does,
// using the same contact picker — no special payload-synthesis path is needed.
func entityKindForTrigger(triggerType string) string {
	switch triggerType {
	case TriggerContactCreated, TriggerContactUpdated, TriggerWebhookInbound:
		return "contact"
	case TriggerDealStageChanged:
		return "deal"
	default:
		return ""
	}
}

// classifyRunNowRequest validates a Run Now request body and reports which kind of
// entity it targets.
//
// It returns kind "contact" with the parsed contact id when contact_id is the only
// non-empty field and is a valid UUID, kind "deal" with the parsed deal id when
// deal_id is the only non-empty field and is a valid UUID, and otherwise a distinct
// error: ErrRunNowBothIDs (both present), ErrRunNowNoIDs (neither present), or an
// error wrapping ErrRunNowInvalidUUID (the single provided id is not a valid UUID).
func classifyRunNowRequest(req RunNowRequest) (kind string, entityID uuid.UUID, err error) {
	hasContact := req.ContactID != ""
	hasDeal := req.DealID != ""

	switch {
	case hasContact && hasDeal:
		return "", uuid.Nil, ErrRunNowBothIDs
	case !hasContact && !hasDeal:
		return "", uuid.Nil, ErrRunNowNoIDs
	case hasContact:
		id, parseErr := uuid.Parse(req.ContactID)
		if parseErr != nil {
			return "", uuid.Nil, fmt.Errorf("contact_id %w", ErrRunNowInvalidUUID)
		}
		return "contact", id, nil
	default: // hasDeal
		id, parseErr := uuid.Parse(req.DealID)
		if parseErr != nil {
			return "", uuid.Nil, fmt.Errorf("deal_id %w", ErrRunNowInvalidUUID)
		}
		return "deal", id, nil
	}
}

// buildRunNowTriggerContext builds the Trigger_Context map for a Run Now execution,
// mirroring the payload shape that real CRM events produce (contact_handler.go /
// deal_handler.go) so conditions and templates resolve against real entity data.
//
// The returned map always contains:
//   - "entity_id": the entity's own id (read from entity["id"]);
//   - a key matching kind ("contact" or "deal") holding the entity map, which itself
//     carries its id under an "id" key so downstream executors can resolve it;
//   - "trigger": { "type": triggerType, "source": "run_now" }.
//
// It deliberately omits the "_internal_update" marker so the engine does not treat the
// run as a self-triggered update. For a deal_stage_changed trigger it additionally sets
// "old_stage_id" to "" (no real transition occurred) and "new_stage_id" to the deal's
// current stage_id.
func buildRunNowTriggerContext(kind, triggerType string, entity map[string]any) map[string]any {
	ctx := map[string]any{
		"entity_id": entity["id"],
		kind:        entity,
		"trigger": map[string]any{
			"type":   triggerType,
			"source": "run_now",
		},
	}

	if triggerType == TriggerDealStageChanged {
		// old_stage_id is empty: a manual run is not a real stage transition.
		ctx["old_stage_id"] = ""
		// new_stage_id mirrors the deal's current stage; preserve the raw value so it
		// equals deal["stage_id"] exactly, defaulting to "" when the deal has no stage.
		newStage := any("")
		if v, ok := entity["stage_id"]; ok {
			newStage = v
		}
		ctx["new_stage_id"] = newStage
	}

	return ctx
}

// newRunNowIdempotencyKey returns a unique-per-call idempotency key of the form
// "run_now:<uuid>". Generating a fresh UUID per request guarantees the key is distinct
// from any prior natural or manual trigger, so CreateRun always inserts a new run
// rather than de-duplicating it (the Run Now idempotency bypass). It is extracted as a
// function so its uniqueness can be property-tested.
func newRunNowIdempotencyKey() string {
	return "run_now:" + uuid.NewString()
}

// authorizeRunNow reports whether a caller with the given org role and id may Run Now a
// workflow created by createdBy (the Run Now permission model).
//
// A privileged role — "owner", "admin", or "manager" — may run ANY workflow in the org,
// matching the requireRole("admin","manager") guard the other workflow-mutating endpoints
// use, plus "owner", which delivery.RequireRole treats as a superuser that passes every
// role guard. Any other caller (e.g. a member/viewer, or a creator later demoted) may run
// ONLY a workflow they themselves created — the creator allowance. A nil caller id never
// satisfies the creator check, so an unauthenticated/identity-less request is denied.
//
// It is a pure function (no gin/DB) so the permission matrix can be exhaustively
// unit-tested; the handler reads role from the request context and createdBy from the
// loaded workflow and delegates the decision here.
func authorizeRunNow(role string, userID, createdBy uuid.UUID) bool {
	switch role {
	case "owner", "admin", "manager":
		return true
	}
	return userID != uuid.Nil && userID == createdBy
}

// authorizeRunNowCtx is the capability-aware Run Now / Retry gate (P3). The
// creator allowance always applies. When a capability checker is wired (prod),
// the workflows.run_any capability grants "run any" — so a custom role an admin
// grants it to can run any workflow, not just the system roles. Without a checker
// (unit tests), it falls back to the legacy owner/admin/manager role check so the
// pure-function permission matrix keeps holding.
func (h *Handler) authorizeRunNowCtx(c *gin.Context, role string, userID, createdBy uuid.UUID) bool {
	if userID != uuid.Nil && userID == createdBy {
		return true // creator may always run their own workflow
	}
	if h.capChecker != nil {
		orgIDVal, _ := c.Get("org_id")
		orgID, _ := orgIDVal.(uuid.UUID)
		return h.capChecker.HasCapability(c.Request.Context(), orgID, domain.CapWorkflowsRunAny) == nil
	}
	return authorizeRunNow(role, userID, createdBy)
}
