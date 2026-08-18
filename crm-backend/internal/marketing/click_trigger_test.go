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

// click_trigger_test.go covers email_clicked, which shares the open path's
// campaign gate, machine filter and contact resolution but adds one rule of its
// own: a click on OUR unsubscribe / preference-centre link must never start a
// workflow. Every marketing email carries that link, so getting this wrong
// would enrol people at the moment they ask to hear less from us.

const testFrontend = "https://app.example.com"

// clickEvent builds a matured (past-grace) campaign click carrying `link`.
// Passing an empty link omits the click object entirely, which is what an
// unexpected payload shape would look like to the parser.
func clickEvent(orgID uuid.UUID, campaignID *uuid.UUID, link string) MarketingEmailEvent {
	data := map[string]any{
		"email_id": "re_msg_click",
		"tags":     map[string]string{"contact_id": uuid.New().String()},
	}
	if link != "" {
		data["click"] = map[string]any{"link": link}
	}
	raw, _ := json.Marshal(map[string]any{"type": "email.clicked", "data": data})
	occurred := time.Now().Add(-2 * engagementGrace)
	return MarketingEmailEvent{
		ID:              uuid.New(),
		OrgID:           orgID,
		SvixID:          "sv_click_1",
		EventType:       ResendTypeClicked,
		EmailNormalized: "clicker@example.com",
		CampaignID:      campaignID,
		OccurredAt:      &occurred,
		RawPayload:      raw,
	}
}

func clickBridge(rec *bridgeRecorder) *EngagementBridge {
	b := rec.bridge()
	b.FrontendURL = testFrontend
	return b
}

func TestClickTrigger_EmitsForHumanCampaignClick(t *testing.T) {
	org, campaign := uuid.New(), uuid.New()
	store := &fakeConsumerStore{campaignExists: true, deliveredWithin: false}
	rec := &bridgeRecorder{contact: map[string]any{"id": "c1"}, contactID: uuid.New()}

	p := newProc(store)
	p.SetEngagementTrigger(clickBridge(rec))
	require.NoError(t, p.apply(context.Background(), clickEvent(org, &campaign, "https://example.com/pricing")))

	require.Len(t, rec.events, 1)
	assert.Equal(t, automation.TriggerEmailClicked, rec.types[0])
	payload := rec.events[0]
	assert.Equal(t, "re_msg_click", payload["email_id"])
	assert.Equal(t, "https://example.com/pricing", payload["link"], "the clicked URL rides in the payload for interpolation")
	trig, _ := payload["trigger"].(map[string]any)
	require.NotNil(t, trig)
	assert.Equal(t, automation.TriggerEmailClicked, trig["type"])
	assert.Equal(t, "https://example.com/pricing", trig["link"])
}

func TestClickTrigger_NeverFiresOnTheUnsubscribeLink(t *testing.T) {
	org, campaign := uuid.New(), uuid.New()
	// Both shapes of our own opt-out URL: the in-body preference centre a human
	// clicks, and the one-click endpoint mailbox providers hit.
	for _, link := range []string{
		testFrontend + "/u/TOKEN123",
		"https://api.example.com/api/marketing/u/TOKEN123",
	} {
		store := &fakeConsumerStore{campaignExists: true}
		rec := &bridgeRecorder{contact: map[string]any{"id": "c1"}, contactID: uuid.New()}
		p := newProc(store)
		p.SetEngagementTrigger(clickBridge(rec))

		require.NoError(t, p.apply(context.Background(), clickEvent(org, &campaign, link)))

		assert.Empty(t, rec.events, "an unsubscribe click must never start a workflow: %s", link)
		assert.Zero(t, store.campaignQueries, "the opt-out check short-circuits before any lookup")
	}
}

func TestClickTrigger_UnsubscribeExclusionSurvivesAnUnknownPayloadShape(t *testing.T) {
	org, campaign := uuid.New(), uuid.New()
	// The link is present in the body but under a key the parser does not know,
	// so extraction yields "". The exclusion must still catch it by scanning the
	// raw payload — failing open here is the one outcome we cannot accept.
	raw, _ := json.Marshal(map[string]any{
		"type": "email.clicked",
		"data": map[string]any{
			"email_id":    "re_msg_click",
			"clicked_url": testFrontend + "/u/TOKEN123",
		},
	})
	occurred := time.Now().Add(-2 * engagementGrace)
	evt := MarketingEmailEvent{
		ID: uuid.New(), OrgID: org, SvixID: "sv_click_2", EventType: ResendTypeClicked,
		EmailNormalized: "clicker@example.com", CampaignID: &campaign,
		OccurredAt: &occurred, RawPayload: raw,
	}

	store := &fakeConsumerStore{campaignExists: true}
	rec := &bridgeRecorder{contact: map[string]any{"id": "c1"}, contactID: uuid.New()}
	p := newProc(store)
	p.SetEngagementTrigger(clickBridge(rec))

	require.NoError(t, p.apply(context.Background(), evt))
	assert.Empty(t, rec.events, "an unrecognised payload shape must not defeat the opt-out exclusion")
}

func TestClickTrigger_SkipsMachineClicks(t *testing.T) {
	org, campaign := uuid.New(), uuid.New()
	// A click landing within the delivery window is a corporate link scanner
	// (Safe Links, Proofpoint) walking every URL, not a person.
	store := &fakeConsumerStore{campaignExists: true, deliveredWithin: true}
	rec := &bridgeRecorder{contact: map[string]any{"id": "c1"}, contactID: uuid.New()}

	p := newProc(store)
	p.SetEngagementTrigger(clickBridge(rec))
	require.NoError(t, p.apply(context.Background(), clickEvent(org, &campaign, "https://example.com/pricing")))

	assert.Empty(t, rec.events)
	assert.Equal(t, 1, store.deliveredQueries)
}

func TestClickTrigger_SkipsNonCampaignAttribution(t *testing.T) {
	org := uuid.New()
	workflowUUID := uuid.New() // an M8 sequence send echoes the workflow uuid here
	store := &fakeConsumerStore{campaignExists: false}
	rec := &bridgeRecorder{contact: map[string]any{"id": "c1"}, contactID: uuid.New()}

	p := newProc(store)
	p.SetEngagementTrigger(clickBridge(rec))
	require.NoError(t, p.apply(context.Background(), clickEvent(org, &workflowUUID, "https://example.com/x")))

	assert.Empty(t, rec.events, "sequence clicks stay out of v1 — that is the loop protection")
}

func TestClickTrigger_FreshClickDefersForTheDeliveredEvent(t *testing.T) {
	org, campaign := uuid.New(), uuid.New()
	store := &fakeConsumerStore{campaignExists: true}
	rec := &bridgeRecorder{contact: map[string]any{"id": "c1"}, contactID: uuid.New()}

	p := newProc(store)
	p.SetEngagementTrigger(clickBridge(rec))
	evt := clickEvent(org, &campaign, "https://example.com/pricing")
	now := time.Now()
	evt.OccurredAt = &now

	p.process(context.Background(), evt)

	assert.Equal(t, 1, store.deferred, "a fresh click waits for the delivered row like an open does")
	assert.Zero(t, store.repended, "waiting on the clock is not a retry")
	assert.Empty(t, rec.events)
}

func TestClickedLink_ToleratesEveryPlausibleShape(t *testing.T) {
	cases := map[string]map[string]any{
		"nested link": {"click": map[string]any{"link": "https://example.com/a"}},
		"nested url":  {"click": map[string]any{"url": "https://example.com/a"}},
		"flat link":   {"link": "https://example.com/a"},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			data["email_id"] = "re_1"
			raw, _ := json.Marshal(map[string]any{"type": "email.clicked", "data": data})
			assert.Equal(t, "https://example.com/a", clickedLink(MarketingEmailEvent{RawPayload: raw}))
		})
	}

	assert.Empty(t, clickedLink(MarketingEmailEvent{RawPayload: []byte("{not json")}))
	assert.Empty(t, clickedLink(MarketingEmailEvent{RawPayload: []byte(`{"data":{"email_id":"re_1"}}`)}))
}
