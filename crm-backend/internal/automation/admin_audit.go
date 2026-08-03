package automation

import (
	"context"
	"encoding/json"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// admin_audit.go records the automation admin actions that touch credentials or
// destroy configuration.
//
// The module had no audit trail at all, which was most conspicuous on
// RevealWebhookSecret: its own doc comment describes the endpoint as "an explicit,
// auditable retrieval", and nothing was recorded. Reveal hands back the org's
// inbound signing secret — the credential that authenticates every inbound
// automation call — so "who fetched it, and when" is exactly the question an
// incident asks first.
//
// usecase.recordAdminEvent is unexported and lives in another package, so the shape
// is replicated here, as it already was for marketing consent.

const (
	// EventWebhookSecretRevealed is a read of the org's inbound signing secret.
	EventWebhookSecretRevealed = "automation.webhook_secret.revealed"
	// EventWebhookSecretRotated is a rotation, which invalidates every sender
	// currently signing with the old value.
	EventWebhookSecretRotated = "automation.webhook_secret.rotated"
	// EventWorkflowDeleted records the destruction of a workflow definition.
	EventWorkflowDeleted = "automation.workflow.deleted"
)

// recordAdminEvent writes one admin audit row, best-effort.
//
// Best-effort is a deliberate trade, not an oversight: failing the reveal because
// its audit row could not be written would take a working feature down over a
// logging fault. The durable record of the SECRET itself is the row in
// automation_workflow_org_tokens; this is the operational trail beside it.
func (h *Handler) recordAdminEvent(ctx context.Context, orgID uuid.UUID, eventType string, metadata map[string]any) {
	if h.audit == nil {
		return
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		raw = []byte("{}")
	}
	e := &domain.AuthEvent{
		Category:  "admin",
		EventType: eventType,
		Metadata:  domain.JSON(raw),
	}
	if orgID != uuid.Nil {
		o := orgID
		e.OrgID = &o
	}
	// Caller identity and request metadata ride on the context, stamped by the auth
	// middleware — the same source the rest of the audit trail reads.
	if caller, ok := domain.CallerFromContext(ctx); ok && caller.UserID != uuid.Nil {
		a := caller.UserID
		e.ActorID = &a
	}
	if meta, ok := domain.RequestMetaFromContext(ctx); ok {
		if meta.IP != "" {
			ip := meta.IP
			e.IP = &ip
		}
		if meta.UserAgent != "" {
			ua := meta.UserAgent
			e.UserAgent = &ua
		}
	}
	if err := h.audit.WriteAuthEvent(ctx, e); err != nil && h.logger != nil {
		h.logger.Error("automation: failed to record admin audit event",
			"error", err, "event_type", eventType, "org_id", orgID.String())
	}
}
