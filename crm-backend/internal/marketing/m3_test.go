package marketing

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"crm-backend/internal/automation"
	"crm-backend/internal/integrations/envelope"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testTokenService(t *testing.T) *TokenService {
	t.Helper()
	ring, err := envelope.ParseKeyring(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	require.NoError(t, err)
	return NewTokenService(ring)
}

// ── token service ────────────────────────────────────────────────────────────

func TestUnsubToken_RoundTrip(t *testing.T) {
	ts := testTokenService(t)
	org := uuid.New()
	cid := uuid.New()
	tok, err := ts.Mint(org, "jane@acme.com", WithContactID(cid))
	require.NoError(t, err)
	assert.NotContains(t, tok, "jane@acme.com", "the email must never appear in the token/URL")

	got, err := ts.Verify(tok)
	require.NoError(t, err)
	assert.Equal(t, org, got.OrgID)
	assert.Equal(t, "jane@acme.com", got.Email)
	require.NotNil(t, got.ContactID)
	assert.Equal(t, cid, *got.ContactID)
	assert.Nil(t, got.CampaignID, "campaign id absent at M3")
}

func TestUnsubToken_TamperRejected(t *testing.T) {
	ts := testTokenService(t)
	tok, _ := ts.Mint(uuid.New(), "jane@acme.com")
	parts := strings.Split(tok, ".")
	b := []byte(parts[2])
	b[0] ^= 0xFF
	parts[2] = string(b)
	_, err := ts.Verify(strings.Join(parts, "."))
	assert.Error(t, err, "a tampered token must fail verification (fail-closed)")
}

func TestUnsubToken_CrossKeyRejected(t *testing.T) {
	a := testTokenService(t)
	ringB, _ := envelope.ParseKeyring(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	b := NewTokenService(ringB)
	tok, _ := a.Mint(uuid.New(), "jane@acme.com")
	_, err := b.Verify(tok)
	assert.Error(t, err, "a token minted under a different key must not verify")
}

func TestUnsubToken_NotConfigured(t *testing.T) {
	ts := NewTokenService(nil)
	assert.False(t, ts.Configured())
	_, err := ts.Mint(uuid.New(), "jane@acme.com")
	assert.ErrorIs(t, err, ErrTokensNotConfigured)
	_, err = ts.Verify("senv1.1.AAAA")
	assert.ErrorIs(t, err, ErrTokensNotConfigured)
}

// ── render footer/preheader resolver ───────────────────────────────────────────

// footerLike mimics the compiled footer the M6.1 compiler bakes in (bare
// unsubscribe_url slot + org name/address slots) plus a body merge tag and the
// author-placeable dotted twin.
const footerLike = `<p>Hi {{contact.first_name|there}}</p>` +
	`<div>{{org.name|}}<br />{{org.postal_address|}}<br />` +
	`<a href="{{unsubscribe_url|#}}">Unsubscribe</a></div>` +
	`<a href="{{campaign.unsubscribe_url|#}}">also unsub</a>`

func TestRenderForRecipient_ResolvesFooterAndDoesNotMutate(t *testing.T) {
	ctx := automation.EvalContext{Contact: map[string]any{"first_name": "Jane"}}
	fc := FooterContext{
		OrgName:       "Acme Inc",
		PostalAddress: "1 Main St, Springfield",
		UnsubURL:      "https://app.example.com/u/TOKEN123",
	}
	out := RenderForRecipient(footerLike, ctx, fc)

	assert.Contains(t, out, "Hi Jane")
	assert.Contains(t, out, "Acme Inc")
	assert.Contains(t, out, "1 Main St, Springfield")
	// Bare footer slot substituted directly.
	assert.Contains(t, out, `href="https://app.example.com/u/TOKEN123">Unsubscribe`)
	// Dotted twin resolved via campaign scope to the same URL.
	assert.Contains(t, out, `href="https://app.example.com/u/TOKEN123">also unsub`)
	// No unresolved tokens survive.
	assert.NotContains(t, out, "{{unsubscribe_url")
	assert.NotContains(t, out, "{{org.")
	assert.NotContains(t, out, "{{campaign.")
	assert.NotContains(t, out, "{{contact.")

	// The stored input must be untouched (resolver returns a new string).
	assert.Contains(t, footerLike, "{{unsubscribe_url|#}}", "stored compiled HTML must not be mutated")
}

func TestRenderForRecipient_DoesNotMutateCallerContext(t *testing.T) {
	base := automation.EvalContext{
		Org:   map[string]any{"name": "Original"},
		Extra: map[string]any{"campaign": map[string]any{"foo": "bar"}},
	}
	_ = RenderForRecipient(footerLike, base, FooterContext{OrgName: "Override", PostalAddress: "addr", UnsubURL: "https://x/u/T"})
	// The caller's maps must be unchanged (M7 reuses a base context across recipients).
	assert.Equal(t, "Original", base.Org["name"], "caller Org map must not be mutated")
	camp, _ := base.Extra["campaign"].(map[string]any)
	require.NotNil(t, camp)
	assert.Equal(t, "bar", camp["foo"])
	_, hasUnsub := camp["unsubscribe_url"]
	assert.False(t, hasUnsub, "caller campaign map must not gain unsubscribe_url")
}

// ── headers + URL builders ─────────────────────────────────────────────────────

func TestMarketingUnsubHeaders_ByteExact(t *testing.T) {
	h := MarketingUnsubHeaders("https://api.example.com/api/marketing/u/T")
	assert.Equal(t, "<https://api.example.com/api/marketing/u/T>", h["List-Unsubscribe"])
	assert.Equal(t, "List-Unsubscribe=One-Click", h["List-Unsubscribe-Post"],
		"the one-click token is a fixed literal Gmail/Yahoo match exactly")
	assert.Len(t, h, 2)
}

func TestUnsubURLBuilders(t *testing.T) {
	assert.Equal(t, "https://api.example.com/api/marketing/u/TOK",
		OneClickUnsubURL("https://api.example.com/", "TOK"), "trailing slash on the base must be trimmed")
	assert.Equal(t, "https://app.example.com/u/TOK",
		PreferenceCenterURL("https://app.example.com", "TOK"))
}

// ── sender-profile gate ─────────────────────────────────────────────────────────

func TestSenderProfileSendable(t *testing.T) {
	assert.Equalf(t, false, mustBool(SenderProfileSendable(nil)), "nil profile is never sendable")

	complete := &OrgMarketingProfile{FromName: "Acme", PhysicalPostalAddress: "1 Main St"}
	ok, reason := SenderProfileSendable(complete)
	assert.True(t, ok)
	assert.Equal(t, "", reason)

	paused := &OrgMarketingProfile{FromName: "Acme", PhysicalPostalAddress: "1 Main St", MarketingPaused: true}
	ok, reason = SenderProfileSendable(paused)
	assert.False(t, ok)
	assert.Equal(t, "marketing_paused", reason)

	noAddr := &OrgMarketingProfile{FromName: "Acme"}
	ok, reason = SenderProfileSendable(noAddr)
	assert.False(t, ok)
	assert.Equal(t, "no_postal_address", reason)

	noName := &OrgMarketingProfile{PhysicalPostalAddress: "1 Main St"}
	ok, reason = SenderProfileSendable(noName)
	assert.False(t, ok)
	assert.Equal(t, "no_from_name", reason)
}

func mustBool(b bool, _ string) bool { return b }
