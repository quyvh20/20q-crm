package usecase

import (
	"context"
	"testing"

	"crm-backend/internal/domain"
)

// Silence is the stricter sibling of suppression: it skips workflow enrollment AND
// stops date_field timers from arming. These tests pin the emitter half — that all
// three emitters translate the context flag onto the payload, since the payload is
// the only channel to the engine (the emitters detach onto context.Background()).
//
// Each silence test is paired with an ABSENCE control, for the reason the suppression
// tests already document: a positive-only assertion cannot catch an emitter that
// stamps the flag unconditionally, and that emitter would silence every write in the
// org — no error, no failing test.

// TestEvent_SilencedWriteStampsBothFlags is the co-presence guarantee, and the one
// that catches the sharpest mistake available here.
//
// A silenced payload MUST carry the suppression key too. The engine's enrollment
// guard reads the suppression key; if markAutomationFlags ever became an if/else
// chain, a silenced write would emit only `_silenced`, sail past that guard, and
// enroll every test lead — while the call site looked like it had handled the
// stricter case.
func TestEvent_SilencedWriteStampsBothFlags(t *testing.T) {
	svc := newContactSvc(t)
	ctx := domain.WithAutomationSilenced(context.Background())

	_, payload := awaitEvent(t, svc, createContact(svc, ctx))

	if payload[domain.AutomationSilencedPayloadKey] != true {
		t.Errorf("a silenced write must stamp %s=true: %+v", domain.AutomationSilencedPayloadKey, payload)
	}
	if payload[domain.AutomationSuppressedPayloadKey] != true {
		t.Errorf("a silenced write must ALSO stamp %s=true, or the engine's enrollment guard never sees it: %+v",
			domain.AutomationSuppressedPayloadKey, payload)
	}
}

// TestEvent_UnsilencedControl is the load-bearing half. Without it an always-on
// silence flag would stop every workflow in the app and the suite would stay green.
func TestEvent_UnsilencedControl(t *testing.T) {
	svc := newContactSvc(t)

	_, payload := awaitEvent(t, svc, createContact(svc, context.Background()))

	if _, present := payload[domain.AutomationSilencedPayloadKey]; present {
		t.Errorf("an ordinary write must not carry the silence key at all: %+v", payload)
	}
}

// TestEvent_SuppressedWriteIsNotSilenced pins the semantic split at the emitter.
// Backfill depends on it: a backfilled lead's close-date reminder should still arm,
// so a merely-suppressed write must NOT acquire the silence key on the way out.
func TestEvent_SuppressedWriteIsNotSilenced(t *testing.T) {
	svc := newContactSvc(t)
	ctx := domain.WithAutomationSuppressed(context.Background())

	_, payload := awaitEvent(t, svc, createContact(svc, ctx))

	if payload[domain.AutomationSuppressedPayloadKey] != true {
		t.Errorf("suppressed write should stamp the suppression key: %+v", payload)
	}
	if _, present := payload[domain.AutomationSilencedPayloadKey]; present {
		t.Errorf("suppression must not silence — a backfilled record's own timer should still arm: %+v", payload)
	}
}

// TestEvent_CustomObject_Silenced covers the emitter in record_service.go, which
// lives in a different file from the other two.
func TestEvent_CustomObject_Silenced(t *testing.T) {
	svc := newProjectSvc(t)
	ctx := domain.WithAutomationSilenced(domain.WithWriteSource(context.Background(), "integration:api"))

	gotType, payload := awaitEvent(t, svc, createProject(svc, ctx))

	if gotType != "project_created" {
		t.Errorf("event type = %q, want project_created", gotType)
	}
	if payload[domain.AutomationSilencedPayloadKey] != true || payload[domain.AutomationSuppressedPayloadKey] != true {
		t.Errorf("a silenced custom-object write must stamp both flags: %+v", payload)
	}
}

func TestEvent_CustomObject_UnsilencedControl(t *testing.T) {
	svc := newProjectSvc(t)

	_, payload := awaitEvent(t, svc, createProject(svc, context.Background()))

	if _, present := payload[domain.AutomationSilencedPayloadKey]; present {
		t.Errorf("an ordinary custom-object write must not carry the silence key: %+v", payload)
	}
}

// TestEvent_DealStageChanged_Silenced pins fireStageChanged — the one emitter that
// builds its own payload rather than sharing a construction site, and therefore the
// one most likely to drift.
func TestEvent_DealStageChanged_Silenced(t *testing.T) {
	svc, dealID, newStage := stageMoveSvc(t)
	ctx := domain.WithAutomationSilenced(context.Background())

	gotType, payload := awaitEvent(t, svc, moveStage(svc, ctx, dealID, newStage))

	if gotType != "deal_stage_changed" {
		t.Errorf("event type = %q, want deal_stage_changed", gotType)
	}
	if payload[domain.AutomationSilencedPayloadKey] != true || payload[domain.AutomationSuppressedPayloadKey] != true {
		t.Errorf("a silenced stage move must stamp both flags: %+v", payload)
	}
}

func TestEvent_DealStageChanged_UnsilencedControl(t *testing.T) {
	svc, dealID, newStage := stageMoveSvc(t)

	_, payload := awaitEvent(t, svc, moveStage(svc, context.Background(), dealID, newStage))

	if _, present := payload[domain.AutomationSilencedPayloadKey]; present {
		t.Errorf("an ordinary stage move must not carry the silence key: %+v", payload)
	}
}
