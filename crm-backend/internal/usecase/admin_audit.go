package usecase

import (
	"context"
	"encoding/json"
	"log"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// recordAdminEvent appends one 'admin'-category auth event for a workspace / role
// / permission mutation (P4). recordSecurityEvent is the same for the 'security'
// category (session revocation). Both are best-effort, mirroring
// authUseCase.recordAuthEvent: a write failure is logged and swallowed so it can
// never fail the action it records.
//
// The actor identity and transport meta (IP/UA) are resolved from the request
// context the auth middleware populates (WithCaller / WithRequestMeta), so callers
// pass only the org, event type, target, and event-specific detail — no gin, no
// RequestMeta threading through every usecase method.
func recordAdminEvent(ctx context.Context, w domain.AuthEventWriter, orgID uuid.UUID, eventType string, targetID *uuid.UUID, metadata map[string]interface{}) {
	recordContextEvent(ctx, w, "admin", eventType, orgID, targetID, metadata)
}

func recordSecurityEvent(ctx context.Context, w domain.AuthEventWriter, orgID uuid.UUID, eventType string, targetID *uuid.UUID, metadata map[string]interface{}) {
	recordContextEvent(ctx, w, "security", eventType, orgID, targetID, metadata)
}

func recordContextEvent(ctx context.Context, w domain.AuthEventWriter, category, eventType string, orgID uuid.UUID, targetID *uuid.UUID, metadata map[string]interface{}) {
	if w == nil {
		return // audit writer not wired (e.g. a unit test) — no-op
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		raw = []byte("{}")
	}
	e := &domain.AuthEvent{
		Category:  category,
		EventType: eventType,
		TargetID:  targetID,
		Metadata:  domain.JSON(raw),
	}
	if orgID != uuid.Nil {
		o := orgID
		e.OrgID = &o
	}
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
	if err := w.WriteAuthEvent(ctx, e); err != nil {
		log.Printf("auth_events: failed to record %s/%s: %v", category, eventType, err)
	}
}
