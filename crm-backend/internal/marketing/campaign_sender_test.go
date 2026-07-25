package marketing

import (
	"context"
	"errors"
	"testing"
	"time"

	"crm-backend/internal/automation"
	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// ── fakes ──────────────────────────────────────────────────────────────────────

type recStore struct {
	sent       map[uuid.UUID]string
	failed     map[uuid.UUID]string
	suppressed map[uuid.UUID]string
	repended   map[uuid.UUID]string
}

func newRecStore() *recStore {
	return &recStore{
		sent: map[uuid.UUID]string{}, failed: map[uuid.UUID]string{},
		suppressed: map[uuid.UUID]string{}, repended: map[uuid.UUID]string{},
	}
}

func (f *recStore) MarkRecipientSent(_ context.Context, id uuid.UUID, pmid string) error {
	f.sent[id] = pmid
	return nil
}
func (f *recStore) MarkRecipientFailed(_ context.Context, id uuid.UUID, msg string) error {
	f.failed[id] = msg
	return nil
}
func (f *recStore) MarkRecipientSuppressed(_ context.Context, id uuid.UUID, reason string) error {
	f.suppressed[id] = reason
	return nil
}
func (f *recStore) RependRecipient(_ context.Context, id uuid.UUID, _ time.Duration, msg string) error {
	f.repended[id] = msg
	return nil
}

// unused-by-sendOne stubs to satisfy senderStore
func (f *recStore) ClaimSendableRecipients(context.Context, int) ([]domain.CampaignRecipient, error) {
	return nil, nil
}
func (f *recStore) ReapStrandedRecipients(context.Context, time.Duration, int) (int64, error) {
	return 0, nil
}
func (f *recStore) MarkDrainedCampaignsSent(context.Context) (int64, error) { return 0, nil }
func (f *recStore) GetCampaignByID(context.Context, uuid.UUID, uuid.UUID) (*domain.Campaign, error) {
	return nil, nil
}
func (f *recStore) GetContentByID(context.Context, uuid.UUID, uuid.UUID) (*CampaignContent, error) {
	return nil, nil
}
func (f *recStore) GetProfile(context.Context, uuid.UUID) (*OrgMarketingProfile, error) {
	return nil, nil
}
func (f *recStore) GetOrgName(context.Context, uuid.UUID) (string, error) { return "Acme", nil }

type recSender struct {
	calls int
	resp  any
	err   error
}

func (f *recSender) HydrateMarketingContext(context.Context, uuid.UUID, uuid.UUID, string, string, bool) automation.EvalContext {
	return automation.EvalContext{Actions: map[string]any{}, Extra: map[string]any{}}
}
func (f *recSender) SendMarketingEmail(_ context.Context, _, _, _, _, _, _ string, _ []string, _ map[string]string, _ string) (any, error) {
	f.calls++
	return f.resp, f.err
}

type recFrom struct{}

func (recFrom) ResolveFromAddress(context.Context, uuid.UUID, *uuid.UUID) (string, error) {
	return "marketing@acme.com", nil
}

type recTokens struct{}

func (recTokens) Mint(uuid.UUID, string, ...MintOpt) (string, error) { return "tok", nil }

type fakeLedger struct {
	sups  []Suppression
	state *ContactMarketingState
}

func (f *fakeLedger) SuppressionsForEmail(context.Context, uuid.UUID, string) ([]Suppression, error) {
	return f.sups, nil
}
func (f *fakeLedger) MarketingStateForEmail(context.Context, uuid.UUID, string) (*ContactMarketingState, error) {
	return f.state, nil
}

// ── env ──────────────────────────────────────────────────────────────────────

func senderEnv(led *fakeLedger, resp any, sendErr error) (*CampaignSender, *recStore, *recSender) {
	store := newRecStore()
	snd := &recSender{resp: resp, err: sendErr}
	s := NewCampaignSender(store, NewSuppressionGuard(led), snd, recFrom{}, recTokens{}, nil, "https://api.example.com", "https://app.example.com", nil)
	return s, store, snd
}

func sendableLedger() *fakeLedger {
	return &fakeLedger{state: &ContactMarketingState{MarketingStatus: StatusSubscribed}}
}

func sendCtx() *campaignSendContext {
	return &campaignSendContext{
		campaign: &domain.Campaign{ID: uuid.New(), Name: "Blast"},
		content:  &CampaignContent{Subject: "Hi {{contact.first_name|there}}", BodyHTMLCompiled: "<p>x</p>", MergeScope: datatypes.JSON(`["contact"]`)},
		profile:  &OrgMarketingProfile{FromName: "Acme", PhysicalPostalAddress: "1 Main St", ReplyTo: "r@acme.com"},
		fromAddr: "marketing@acme.com",
		orgName:  "Acme",
	}
}

func recip(attempts int) domain.CampaignRecipient {
	return domain.CampaignRecipient{ID: uuid.New(), OrgID: uuid.New(), EmailNormalized: "a@x.com", Attempts: attempts}
}

// ── tests ──────────────────────────────────────────────────────────────────────

func TestSender_SuppressedDroppedAtClaim(t *testing.T) {
	led := &fakeLedger{sups: []Suppression{{Reason: "unsubscribe", Scope: ScopeAll}}}
	s, store, snd := senderEnv(led, nil, nil)
	r := recip(1)
	s.sendOne(context.Background(), r, sendCtx())
	if _, ok := store.suppressed[r.ID]; !ok {
		t.Fatal("a suppressed recipient must be marked suppressed")
	}
	if snd.calls != 0 {
		t.Fatal("a suppressed recipient must NOT be sent")
	}
}

func TestSender_Success(t *testing.T) {
	resp := map[string]any{"response": map[string]any{"id": "msg_123"}}
	s, store, snd := senderEnv(sendableLedger(), resp, nil)
	r := recip(1)
	s.sendOne(context.Background(), r, sendCtx())
	if snd.calls != 1 {
		t.Fatalf("expected one send, got %d", snd.calls)
	}
	if store.sent[r.ID] != "msg_123" {
		t.Fatalf("expected sent with provider id, got %q", store.sent[r.ID])
	}
}

func TestSender_RetryableRepends(t *testing.T) {
	s, store, _ := senderEnv(sendableLedger(), nil, automation.NewRetryableError(errors.New("429")))
	r := recip(1) // below max
	s.sendOne(context.Background(), r, sendCtx())
	if _, ok := store.repended[r.ID]; !ok {
		t.Fatal("a transient failure below max attempts must repend")
	}
	if _, ok := store.failed[r.ID]; ok {
		t.Fatal("must not be permanently failed")
	}
}

func TestSender_RetryableExhaustedFails(t *testing.T) {
	s, store, _ := senderEnv(sendableLedger(), nil, automation.NewRetryableError(errors.New("429")))
	r := recip(campaignMaxAttempts) // at cap
	s.sendOne(context.Background(), r, sendCtx())
	if _, ok := store.failed[r.ID]; !ok {
		t.Fatal("a transient failure at the attempt cap must fail terminally")
	}
	if _, ok := store.repended[r.ID]; ok {
		t.Fatal("must not repend past the cap")
	}
}

func TestSender_PermanentFails(t *testing.T) {
	s, store, _ := senderEnv(sendableLedger(), nil, errors.New("client error 422"))
	r := recip(1)
	s.sendOne(context.Background(), r, sendCtx())
	if _, ok := store.failed[r.ID]; !ok {
		t.Fatal("a permanent 4xx must fail terminally, not repend")
	}
}

func TestSender_PausedMidSendRepends(t *testing.T) {
	s, store, snd := senderEnv(sendableLedger(), nil, nil)
	sc := sendCtx()
	sc.fatalReason = "paused_or_canceled"
	r := recip(1)
	s.sendOne(context.Background(), r, sc)
	if _, ok := store.repended[r.ID]; !ok {
		t.Fatal("a paused/canceled campaign must repend its claimed rows, not send")
	}
	if snd.calls != 0 {
		t.Fatal("must not send while paused/canceled")
	}
}
