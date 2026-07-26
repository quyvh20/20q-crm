package marketing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"crm-backend/internal/automation"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// ── fakes local to the preparer test ────────────────────────────────────────────

type seqStore struct {
	content *CampaignContent
	profile *OrgMarketingProfile
	cErr    error
	pErr    error
}

func (s *seqStore) GetContentByID(context.Context, uuid.UUID, uuid.UUID) (*CampaignContent, error) {
	return s.content, s.cErr
}
func (s *seqStore) GetProfile(context.Context, uuid.UUID) (*OrgMarketingProfile, error) {
	return s.profile, s.pErr
}
func (s *seqStore) GetOrgName(context.Context, uuid.UUID) (string, error) { return "Acme", nil }

// seqEngine satisfies both workflowNamer and marketingHydrator (as *automation.Engine
// does in production).
type seqEngine struct{}

func (seqEngine) HydrateMarketingContext(_ context.Context, _, _ uuid.UUID, campaignName, orgName string, _ bool) automation.EvalContext {
	return automation.EvalContext{
		Contact: map[string]any{"first_name": "Jane"},
		Org:     map[string]any{"name": orgName},
		Extra:   map[string]any{"campaign": map[string]any{"name": campaignName}},
		Actions: map[string]any{},
	}
}
func (seqEngine) LoadWorkflow(context.Context, uuid.UUID, uuid.UUID) (*automation.Workflow, error) {
	return &automation.Workflow{Name: "Welcome Drip"}, nil
}

type errLedger struct{}

func (errLedger) SuppressionsForEmail(context.Context, uuid.UUID, string) ([]Suppression, error) {
	return nil, errors.New("ledger down")
}
func (errLedger) MarketingStateForEmail(context.Context, uuid.UUID, string) (*ContactMarketingState, error) {
	return nil, nil
}

type emptyFrom struct{}

func (emptyFrom) ResolveFromAddress(context.Context, uuid.UUID, *uuid.UUID) (string, error) {
	return "", nil
}

func completeContent() *CampaignContent {
	return &CampaignContent{
		Subject:          "Hi {{contact.first_name|there}}",
		BodyHTMLCompiled: `<p>Body {{unsubscribe_url|#}}</p>`,
		MergeScope:       datatypes.JSON(`["contact","org","campaign"]`),
	}
}
func completeProfile() *OrgMarketingProfile {
	return &OrgMarketingProfile{FromName: "Acme", ReplyTo: "r@acme.com", PhysicalPostalAddress: "1 Main St"}
}

func newSeqPreparer(guard *SuppressionGuard, store *seqStore, from fromResolver) *SequenceSendPreparer {
	return NewSequenceSendPreparer(store, guard, from, recTokens{}, seqEngine{}, seqEngine{}, "https://api.example.com", "https://app.example.com")
}

func seqReq() automation.MarketingSendRequest {
	return automation.MarketingSendRequest{
		OrgID:      uuid.New(),
		WorkflowID: uuid.New(),
		ContactID:  uuid.New(),
		ToEmail:    "  Jane@Acme.com ",
		ContentID:  uuid.New(),
	}
}

// ── tests ───────────────────────────────────────────────────────────────────────

func TestPrepare_HappyPath(t *testing.T) {
	p := newSeqPreparer(NewSuppressionGuard(sendableLedger()), &seqStore{content: completeContent(), profile: completeProfile()}, recFrom{})
	dec, err := p.PrepareMarketingSend(context.Background(), seqReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Skip {
		t.Fatalf("expected send, got skip: %s", dec.SkipReason)
	}
	if dec.ToAddress != "jane@acme.com" {
		t.Errorf("recipient not normalized: %q", dec.ToAddress)
	}
	if dec.FromAddress != "marketing@acme.com" {
		t.Errorf("from address = %q, want verified send-domain address", dec.FromAddress)
	}
	if dec.FromName != "Acme" || dec.ReplyTo != "r@acme.com" {
		t.Errorf("from-name/reply-to not from profile: %q / %q", dec.FromName, dec.ReplyTo)
	}
	if dec.Subject != "Hi Jane" {
		t.Errorf("subject not merged: %q", dec.Subject)
	}
	if dec.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Errorf("missing byte-exact List-Unsubscribe-Post header: %v", dec.Headers)
	}
	if !strings.Contains(dec.BodyHTML, "/u/tok") {
		t.Errorf("footer unsubscribe URL not rendered into body: %q", dec.BodyHTML)
	}
}

func TestPrepare_SuppressedIsTerminalSkip(t *testing.T) {
	led := &fakeLedger{sups: []Suppression{{Reason: ReasonUnsubscribe, Scope: ScopeMarketing}}}
	p := newSeqPreparer(NewSuppressionGuard(led), &seqStore{content: completeContent(), profile: completeProfile()}, recFrom{})
	dec, err := p.PrepareMarketingSend(context.Background(), seqReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dec.Skip || dec.Retry {
		t.Fatalf("suppressed must be a terminal skip: skip=%v retry=%v", dec.Skip, dec.Retry)
	}
	if dec.SkipReason != "suppressed:unsubscribe" {
		t.Errorf("reason = %q", dec.SkipReason)
	}
}

func TestPrepare_LedgerErrorIsRetryableSkip(t *testing.T) {
	p := newSeqPreparer(NewSuppressionGuard(errLedger{}), &seqStore{content: completeContent(), profile: completeProfile()}, recFrom{})
	dec, _ := p.PrepareMarketingSend(context.Background(), seqReq())
	if !dec.Skip || !dec.Retry {
		t.Fatalf("a fail-closed ledger error must be a RETRYABLE skip: skip=%v retry=%v", dec.Skip, dec.Retry)
	}
	if dec.SkipReason != "error" {
		t.Errorf("reason = %q", dec.SkipReason)
	}
}

func TestPrepare_NoVerifiedDomainIsRetryableSkip(t *testing.T) {
	p := newSeqPreparer(NewSuppressionGuard(sendableLedger()), &seqStore{content: completeContent(), profile: completeProfile()}, emptyFrom{})
	dec, _ := p.PrepareMarketingSend(context.Background(), seqReq())
	if !dec.Skip || !dec.Retry || dec.SkipReason != "no_verified_domain" {
		t.Fatalf("missing verified domain must be a retryable skip: %+v", dec)
	}
}

func TestPrepare_DeletedContentIsTerminalSkip(t *testing.T) {
	p := newSeqPreparer(NewSuppressionGuard(sendableLedger()), &seqStore{content: nil, profile: completeProfile()}, recFrom{})
	dec, _ := p.PrepareMarketingSend(context.Background(), seqReq())
	if !dec.Skip || dec.Retry || dec.SkipReason != "content_not_found" {
		t.Fatalf("deleted content must be a terminal skip: %+v", dec)
	}
}

func TestPrepare_MissingPostalAddressIsRetryableSkip(t *testing.T) {
	noAddr := &OrgMarketingProfile{FromName: "Acme"} // no postal address
	p := newSeqPreparer(NewSuppressionGuard(sendableLedger()), &seqStore{content: completeContent(), profile: noAddr}, recFrom{})
	dec, _ := p.PrepareMarketingSend(context.Background(), seqReq())
	if !dec.Skip || !dec.Retry || dec.SkipReason != "no_sender_profile" {
		t.Fatalf("missing CAN-SPAM postal address must be a retryable skip: %+v", dec)
	}
}
