package marketing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"crm-backend/internal/automation"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for the email_opened engagement bridge (arc G): the ResendProcessor
// must emit ONLY for human opens of real campaign emails, with a resolvable
// contact, and must never let bridge failures affect event processing.

type bridgeRecorder struct {
	events   []map[string]any
	orgs     []uuid.UUID
	types    []string
	contact  map[string]any
	contactID uuid.UUID
	loadErr  error
	loadedBy []struct {
		ContactID uuid.UUID
		Email     string
	}
}

func (b *bridgeRecorder) bridge() *OpenTriggerBridge {
	return &OpenTriggerBridge{
		TriggerEvent: func(_ context.Context, orgID uuid.UUID, eventType string, payload map[string]any) {
			b.orgs = append(b.orgs, orgID)
			b.types = append(b.types, eventType)
			b.events = append(b.events, payload)
		},
		LoadContact: func(_ context.Context, _ uuid.UUID, contactID uuid.UUID, email string) (map[string]any, uuid.UUID, error) {
			b.loadedBy = append(b.loadedBy, struct {
				ContactID uuid.UUID
				Email     string
			}{contactID, email})
			return b.contact, b.contactID, b.loadErr
		},
	}
}

func openEvent(orgID uuid.UUID, campaignID *uuid.UUID) MarketingEmailEvent {
	contactTag := uuid.New()
	raw, _ := json.Marshal(map[string]any{
		"type": "email.opened",
		"data": map[string]any{
			"email_id": "re_msg_123",
			"tags":     map[string]string{"contact_id": contactTag.String()},
		},
	})
	// Past the delivered-event grace window — these tests exercise the emit
	// path itself; the grace deferral has its own tests below.
	occurred := time.Now().Add(-2 * openTriggerGrace)
	return MarketingEmailEvent{
		ID:              uuid.New(),
		OrgID:           orgID,
		SvixID:          "sv_1",
		EventType:       ResendTypeOpened,
		EmailNormalized: "opener@example.com",
		CampaignID:      campaignID,
		OccurredAt:      &occurred,
		RawPayload:      raw,
	}
}

func TestOpenTrigger_FreshOpenDefersForDeliveredEvent(t *testing.T) {
	org := uuid.New()
	campaign := uuid.New()
	store := &fakeConsumerStore{campaignExists: true}
	rec := &bridgeRecorder{contact: map[string]any{"id": "c1"}, contactID: uuid.New()}

	p := newProc(store)
	p.SetOpenTrigger(rec.bridge())
	evt := openEvent(org, &campaign)
	now := time.Now()
	evt.OccurredAt = &now // inside the grace window

	p.process(context.Background(), evt)

	assert.Equal(t, 1, store.repended, "a fresh campaign open must repend, not process")
	assert.Empty(t, store.finished)
	assert.Empty(t, rec.events)
	assert.Zero(t, store.deliveredQueries, "the machine-open check must not run early")
}

func TestOpenTrigger_GraceDoesNotDeferOtherEvents(t *testing.T) {
	org := uuid.New()
	campaign := uuid.New()
	store := &fakeConsumerStore{campaignExists: true, deliveredWithin: false}
	rec := &bridgeRecorder{contact: map[string]any{"id": "c1"}, contactID: uuid.New()}

	p := newProc(store)
	p.SetOpenTrigger(rec.bridge())

	// An uncampaigned fresh open finishes immediately (it can never emit).
	evt := openEvent(org, nil)
	now := time.Now()
	evt.OccurredAt = &now
	p.process(context.Background(), evt)
	assert.Zero(t, store.repended)
	assert.Equal(t, []string{EventStatusDone}, store.finished)

	// A matured campaign open processes straight through and emits.
	p.process(context.Background(), openEvent(org, &campaign))
	assert.Zero(t, store.repended)
	require.Len(t, rec.events, 1)
}

func TestOpenTrigger_EmitsForHumanCampaignOpen(t *testing.T) {
	org := uuid.New()
	campaign := uuid.New()
	store := &fakeConsumerStore{campaignExists: true, deliveredWithin: false}
	rec := &bridgeRecorder{contact: map[string]any{"id": "c1", "email": "opener@example.com"}, contactID: uuid.New()}

	p := newProc(store)
	p.SetOpenTrigger(rec.bridge())
	require.NoError(t, p.apply(context.Background(), openEvent(org, &campaign)))

	require.Len(t, rec.events, 1)
	assert.Equal(t, automation.TriggerEmailOpened, rec.types[0])
	assert.Equal(t, org, rec.orgs[0])
	payload := rec.events[0]
	assert.Equal(t, rec.contactID.String(), payload["entity_id"])
	assert.Equal(t, "re_msg_123", payload["email_id"])
	assert.Equal(t, campaign.String(), payload["campaign_id"])
	trig, _ := payload["trigger"].(map[string]any)
	require.NotNil(t, trig)
	assert.Equal(t, automation.TriggerEmailOpened, trig["type"])
	// The contact_id send tag is preferred for resolution.
	require.Len(t, rec.loadedBy, 1)
	assert.NotEqual(t, uuid.Nil, rec.loadedBy[0].ContactID)
	assert.Equal(t, "opener@example.com", rec.loadedBy[0].Email)
}

func TestOpenTrigger_SkipsNonCampaignAttribution(t *testing.T) {
	org := uuid.New()
	workflowUUID := uuid.New() // M8 sequence sends echo the workflow uuid here
	store := &fakeConsumerStore{campaignExists: false}
	rec := &bridgeRecorder{contact: map[string]any{"id": "c1"}, contactID: uuid.New()}

	p := newProc(store)
	p.SetOpenTrigger(rec.bridge())
	require.NoError(t, p.apply(context.Background(), openEvent(org, &workflowUUID)))

	assert.Empty(t, rec.events, "sequence/unknown attribution must not fire (loop protection)")
	assert.Equal(t, 1, store.campaignQueries)
}

func TestOpenTrigger_SkipsUncampaignedOpen(t *testing.T) {
	store := &fakeConsumerStore{campaignExists: true}
	rec := &bridgeRecorder{contact: map[string]any{"id": "c1"}, contactID: uuid.New()}

	p := newProc(store)
	p.SetOpenTrigger(rec.bridge())
	require.NoError(t, p.apply(context.Background(), openEvent(uuid.New(), nil)))

	assert.Empty(t, rec.events)
	assert.Zero(t, store.campaignQueries, "no campaign_id → no lookup at all")
}

func TestOpenTrigger_SkipsMachineOpen(t *testing.T) {
	org := uuid.New()
	campaign := uuid.New()
	store := &fakeConsumerStore{campaignExists: true, deliveredWithin: true} // ≤10s after delivery
	rec := &bridgeRecorder{contact: map[string]any{"id": "c1"}, contactID: uuid.New()}

	p := newProc(store)
	p.SetOpenTrigger(rec.bridge())
	require.NoError(t, p.apply(context.Background(), openEvent(org, &campaign)))

	assert.Empty(t, rec.events, "Apple-MPP prefetch opens must not fire")
	assert.Equal(t, 1, store.deliveredQueries)
}

func TestOpenTrigger_SkipsWhenNoContactMatches(t *testing.T) {
	org := uuid.New()
	campaign := uuid.New()
	store := &fakeConsumerStore{campaignExists: true}
	rec := &bridgeRecorder{contact: nil} // no match

	p := newProc(store)
	p.SetOpenTrigger(rec.bridge())
	require.NoError(t, p.apply(context.Background(), openEvent(org, &campaign)))

	assert.Empty(t, rec.events)
}

func TestOpenTrigger_BridgeAbsentIsInert(t *testing.T) {
	org := uuid.New()
	campaign := uuid.New()
	store := &fakeConsumerStore{campaignExists: true}

	p := newProc(store) // no SetOpenTrigger
	require.NoError(t, p.apply(context.Background(), openEvent(org, &campaign)))
	assert.Zero(t, store.campaignQueries)
}

func TestOpenTrigger_StoreErrorsNeverFailTheEvent(t *testing.T) {
	org := uuid.New()
	campaign := uuid.New()
	store := &fakeConsumerStore{campaignErr: assert.AnError}
	rec := &bridgeRecorder{contact: map[string]any{"id": "c1"}, contactID: uuid.New()}

	p := newProc(store)
	p.SetOpenTrigger(rec.bridge())
	// apply must return nil — a ledger-only event may never repend on bridge failures.
	require.NoError(t, p.apply(context.Background(), openEvent(org, &campaign)))
	assert.Empty(t, rec.events)
}

func TestOpenEventIdentity_ParsesTagAndMessageID(t *testing.T) {
	campaign := uuid.New()
	evt := openEvent(uuid.New(), &campaign)
	contactID, emailID := openEventIdentity(evt)
	assert.NotEqual(t, uuid.Nil, contactID)
	assert.Equal(t, "re_msg_123", emailID)

	evt.RawPayload = []byte("{not json")
	contactID, emailID = openEventIdentity(evt)
	assert.Equal(t, uuid.Nil, contactID)
	assert.Equal(t, "", emailID)
}
