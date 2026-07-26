package marketing

import (
	"context"

	"crm-backend/internal/automation"
	"crm-backend/internal/emailutil"

	"github.com/google/uuid"
)

// SequenceSendPreparer implements automation.MarketingSendPreparer: it prepares one
// marketing-channel send for a drip-sequence send_email step (M8). It performs the SAME
// assembly the M7 campaign worker's sendOne does — the LIVE M1 suppression/lawful-basis
// gate, verified send-domain From resolution, the RFC-8058 List-Unsubscribe headers, and
// the M6 compiled footer/preheader render — so the two marketing send paths never drift
// on compliance. package marketing imports package automation (never the reverse), so
// this adapter lives here and is injected into the engine from cmd/server/main.go.
type SequenceSendPreparer struct {
	guard    *SuppressionGuard
	from     fromResolver
	tokens   tokenMinter
	store    contentProfileStore
	names    workflowNamer
	hydrator marketingHydrator
	apiBase  string
	frontend string
}

// contentProfileStore is the slice of the marketing repository the preparer reads
// (satisfied by *marketing.Repository).
type contentProfileStore interface {
	GetContentByID(ctx context.Context, orgID, id uuid.UUID) (*CampaignContent, error)
	GetProfile(ctx context.Context, orgID uuid.UUID) (*OrgMarketingProfile, error)
	GetOrgName(ctx context.Context, orgID uuid.UUID) (string, error)
}

// workflowNamer resolves the sequence workflow's display name for {{campaign.name}}
// (best-effort — a blank name just renders empty). Satisfied by *automation.Engine.
type workflowNamer interface {
	LoadWorkflow(ctx context.Context, orgID, wfID uuid.UUID) (*automation.Workflow, error)
}

// marketingHydrator builds the per-recipient merge context (contact + org + campaign +
// optional company — B4 scope). Satisfied by *automation.Engine.
type marketingHydrator interface {
	HydrateMarketingContext(ctx context.Context, orgID, contactID uuid.UUID, campaignName, orgName string, includeCompany bool) automation.EvalContext
}

// NewSequenceSendPreparer wires the preparer. store is the marketing repository;
// hydrator/names are the automation engine; from/tokens are the same instances the M7
// campaign sender uses.
func NewSequenceSendPreparer(store contentProfileStore, guard *SuppressionGuard, from fromResolver, tokens tokenMinter, names workflowNamer, hydrator marketingHydrator, apiBase, frontend string) *SequenceSendPreparer {
	return &SequenceSendPreparer{
		guard:    guard,
		from:     from,
		tokens:   tokens,
		store:    store,
		names:    names,
		hydrator: hydrator,
		apiBase:  apiBase,
		frontend: frontend,
	}
}

// PrepareMarketingSend runs the full pre-send assembly for one recipient. The ordering
// mirrors the M7 worker: gate → content → profile → from-address → token/headers/footer
// → hydrate + render. Every non-sendable outcome maps to a Skip (terminal or retryable)
// so a marketing drip never mails a suppressed/unconsented recipient and never sends
// unaligned or without a working unsubscribe link.
func (p *SequenceSendPreparer) PrepareMarketingSend(ctx context.Context, req automation.MarketingSendRequest) (automation.MarketingSendDecision, error) {
	emailNorm := emailutil.Normalize(req.ToEmail)
	if emailNorm == "" {
		return automation.MarketingSendDecision{Skip: true, SkipReason: "empty_email"}, nil
	}

	// LIVE suppression / lawful-basis gate (M1) — the authoritative do-not-mail check,
	// identical to the M7 worker (campaign_sender.go). A contact who unsubscribed after
	// enrollment is dropped HERE (opt-out cannot be enforced at trigger/timer time).
	// Fails closed: Reason "error" is a transient ledger read failure → retry, never a
	// permanent suppression of a mailable recipient.
	verdict := p.guard.IsSendable(ctx, req.OrgID, emailNorm, ChannelMarketing, req.TopicID)
	if !verdict.Sendable {
		if verdict.Reason == "error" {
			return automation.MarketingSendDecision{Skip: true, Retry: true, SkipReason: verdict.Reason}, nil
		}
		return automation.MarketingSendDecision{Skip: true, SkipReason: verdict.Reason}, nil
	}

	// M6 compiled content (the send body, still carrying {{merge}} + the baked footer/
	// preheader slot). A load error is transient; a deleted/foreign content is a broken
	// sequence step → terminal skip (visible in the action log), never an empty body.
	content, err := p.store.GetContentByID(ctx, req.OrgID, req.ContentID)
	if err != nil {
		return automation.MarketingSendDecision{Skip: true, Retry: true, SkipReason: "content_load_error"}, nil
	}
	if content == nil {
		return automation.MarketingSendDecision{Skip: true, SkipReason: "content_not_found"}, nil
	}

	// Sender profile — CAN-SPAM postal address + from-name + reply-to. A missing address
	// or from-name blocks the send by law; treat as a transient config gap (an admin can
	// fill it and the parked run resumes).
	profile, err := p.store.GetProfile(ctx, req.OrgID)
	if err != nil {
		return automation.MarketingSendDecision{Skip: true, Retry: true, SkipReason: "profile_load_error"}, nil
	}
	if profile == nil || profile.PhysicalPostalAddress == "" || profile.FromName == "" {
		return automation.MarketingSendDecision{Skip: true, Retry: true, SkipReason: "no_sender_profile"}, nil
	}

	// Verified send-domain From (DKIM/DMARC/Return-Path alignment — the global MAIL_FROM
	// is owned by no org and fails alignment). Empty ⇒ no verified domain yet (transient).
	fromAddr, err := p.from.ResolveFromAddress(ctx, req.OrgID, nil)
	if err != nil {
		return automation.MarketingSendDecision{Skip: true, Retry: true, SkipReason: "no_verified_domain"}, nil
	}
	if fromAddr == "" {
		return automation.MarketingSendDecision{Skip: true, Retry: true, SkipReason: "no_verified_domain"}, nil
	}

	// One-click unsubscribe token → RFC-8058 headers + the visible in-body footer link.
	// A mint failure (MARKETING_UNSUB_KEY unset) must never yield a marketing email with
	// no working unsubscribe — transient skip.
	opts := []MintOpt{WithCampaignID(req.WorkflowID)}
	if req.ContactID != uuid.Nil {
		opts = append(opts, WithContactID(req.ContactID))
	}
	if req.TopicID != nil {
		opts = append(opts, WithTopicID(*req.TopicID))
	}
	token, err := p.tokens.Mint(req.OrgID, emailNorm, opts...)
	if err != nil {
		return automation.MarketingSendDecision{Skip: true, Retry: true, SkipReason: "unsub_token_unavailable"}, nil
	}
	headers := MarketingUnsubHeaders(OneClickUnsubURL(p.apiBase, token))

	orgName, _ := p.store.GetOrgName(ctx, req.OrgID)
	seqName := ""
	if p.names != nil {
		if wf, err := p.names.LoadWorkflow(ctx, req.OrgID, req.WorkflowID); err == nil && wf != nil {
			seqName = wf.Name
		}
	}
	footer := FooterContext{
		OrgName:       orgName,
		PostalAddress: profile.PhysicalPostalAddress,
		UnsubURL:      PreferenceCenterURL(p.frontend, token),
	}

	ec := p.hydrator.HydrateMarketingContext(ctx, req.OrgID, req.ContactID, seqName, orgName, mergeScopeHasCompany(content.MergeScope))
	html := RenderForRecipient(content.BodyHTMLCompiled, ec, footer)
	subject := automation.InterpolateTemplate(content.Subject, ec)

	return automation.MarketingSendDecision{
		ToAddress:   emailNorm,
		Subject:     subject,
		BodyHTML:    html,
		FromName:    profile.FromName,
		FromAddress: fromAddr,
		ReplyTo:     profile.ReplyTo,
		Headers:     headers,
	}, nil
}
