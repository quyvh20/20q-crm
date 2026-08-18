package marketing

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// click_ingest_test.go guards the ingest boundary against the click payload's
// unverified shape.
//
// resendEventData is what parseResendEnvelope decodes, and webhook_handlers
// treats a decode error as "authentic but unparseable — ack and drop": no
// ledger row, HTTP 200, never redelivered. So a strongly-typed click field
// would mean one unexpected shape silently bins EVERY click event — the
// automation trigger, the M9 analytics counts and the A/B decider alike,
// permanently. The click keys are therefore json.RawMessage with all tolerance
// pushed into clickedLink(), exactly as To and Tags already are.

func TestParseResendEnvelope_SurvivesEveryClickShape(t *testing.T) {
	shapes := map[string]string{
		"object with link":  `{"link":"https://x.com/a"}`,
		"object with url":   `{"url":"https://x.com/a"}`,
		"object with extra": `{"link":"https://x.com/a","ipAddress":"1.2.3.4","timestamp":"2026-08-18T00:00:00Z"}`,
		"bare string":       `"https://x.com/a"`,
		"array of objects":  `[{"link":"https://x.com/a"}]`,
		"nested object":     `{"link":{"href":"https://x.com/a"}}`,
		"null":              `null`,
		"number":            `42`,
	}
	for name, click := range shapes {
		t.Run(name, func(t *testing.T) {
			raw := []byte(`{"type":"email.clicked","created_at":"2026-08-18T00:00:00.000Z","data":{` +
				`"email_id":"re_msg_1","from":"news@send.acme.com","to":["clicker@example.com"],` +
				`"click":` + click + `}}`)

			env, err := parseResendEnvelope(raw)

			require.NoError(t, err, "an unexpected click shape must never fail the whole decode — that would drop the event")
			assert.Equal(t, "email.clicked", env.Type)
			assert.Equal(t, "re_msg_1", env.Data.EmailID, "the rest of the payload must still be readable")
			assert.Equal(t, "clicker@example.com", env.Data.recipient())
		})
	}
}

func TestParseResendEnvelope_SurvivesEveryFlatLinkShape(t *testing.T) {
	for name, link := range map[string]string{
		"string": `"https://x.com/a"`,
		"object": `{"url":"https://x.com/a"}`,
		"array":  `["https://x.com/a"]`,
	} {
		t.Run(name, func(t *testing.T) {
			raw := []byte(`{"type":"email.clicked","data":{"email_id":"re_msg_1","link":` + link + `}}`)
			env, err := parseResendEnvelope(raw)
			require.NoError(t, err)
			assert.Equal(t, "re_msg_1", env.Data.EmailID)
		})
	}
}

func TestClickedLink_ExtractsFromTheShapesWeKnow(t *testing.T) {
	for name, tc := range map[string]struct{ click, want string }{
		"object link":    {`{"link":"https://x.com/a"}`, "https://x.com/a"},
		"object url":     {`{"url":"https://x.com/a"}`, "https://x.com/a"},
		"bare string":    {`"https://x.com/a"`, "https://x.com/a"},
		"array of links": {`[{"link":"https://x.com/a"}]`, "https://x.com/a"},
		"unknown shape":  {`42`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			raw := []byte(`{"data":{"email_id":"re_1","click":` + tc.click + `}}`)
			assert.Equal(t, tc.want, clickedLink(MarketingEmailEvent{RawPayload: raw}))
		})
	}
}

// The opt-out exclusion must key on the URL PATH, not on the origin configured
// right now: the link was baked into the email when it was sent, and mail sits
// in inboxes for weeks — an origin change would otherwise silently un-recognise
// every unsubscribe link still in flight.
func TestIsOptOutClick_MatchesRegardlessOfOrigin(t *testing.T) {
	p := newProc(&fakeConsumerStore{})
	p.SetEngagementTrigger(&EngagementBridge{FrontendURL: "https://new-domain.example"})

	optOut := []string{
		"https://old-domain.example/u/TOKEN",       // the origin we no longer use
		"https://new-domain.example/u/TOKEN",       // the current one
		"http://localhost:5173/u/TOKEN",            // dev
		"https://api.acme.com/api/marketing/u/TOK", // one-click endpoint, any host
	}
	for _, link := range optOut {
		assert.True(t, p.isOptOutClick(link, MarketingEmailEvent{}), "must be recognised as opt-out: %s", link)
	}

	normal := []string{
		"https://acme.com/pricing",
		"https://acme.com/blog/2026/upgrade",
		"https://acme.com/updates",
	}
	for _, link := range normal {
		assert.False(t, p.isOptOutClick(link, MarketingEmailEvent{}), "must NOT be treated as opt-out: %s", link)
	}
}

func TestIsOptOutClick_WorksWithNoConfiguredOrigin(t *testing.T) {
	p := newProc(&fakeConsumerStore{})
	p.SetEngagementTrigger(&EngagementBridge{}) // FrontendURL unset

	assert.True(t, p.isOptOutClick("https://anything.example/u/TOKEN", MarketingEmailEvent{}))
	assert.False(t, p.isOptOutClick("https://anything.example/pricing", MarketingEmailEvent{}))
}
