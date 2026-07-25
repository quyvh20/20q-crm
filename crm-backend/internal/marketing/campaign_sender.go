package marketing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"crm-backend/internal/automation"
	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/datatypes"
)

// M7.2 send-lane worker. It drains the recipient roster of every 'sending' campaign
// under a shared provider rate limit, sending one /emails per recipient with a
// deterministic idempotency key (exactly-once across lost-ack retries + worker
// restarts). Every send re-checks the LIVE M1 suppression verdict at claim (a
// mid-send unsubscribe is dropped, never mailed). The roster row is the sole durable
// send-state authority. LIVE sending is gated on the B3 DKIM test — until confirmed,
// run against a fake Resend / an unverified account.

const (
	campaignPollInterval = 3 * time.Second
	campaignClaimBatch   = 25
	campaignMaxDrain     = 40
	campaignMaxAttempts  = 5
	campaignReapInterval = 5 * time.Minute
	campaignReapGrace    = 10 * time.Minute
	providerBucketKey    = "provider" // one global bucket (Resend limit is per-team)
	providerWaitCap      = 5 * time.Second
	campaignPruneInterval  = 6 * time.Hour
	campaignRosterRetention = 30 * 24 * time.Hour // keep a finished campaign's roster 30d
)

// marketingSender is the automation transport seam (satisfied by *automation.Engine).
type marketingSender interface {
	HydrateMarketingContext(ctx context.Context, orgID, contactID uuid.UUID, campaignName, orgName string, includeCompany bool) automation.EvalContext
	SendMarketingEmail(ctx context.Context, to, subject, bodyHTML, fromName, fromAddress, replyTo string, cc []string, headers map[string]string, idempotencyKey string) (any, error)
}

// providerLimiter throttles all sends to the Resend per-team rps (B1). Fixed-window
// is sufficient; a false result carries the retry-after to wait.
type providerLimiter interface {
	Allow(ctx context.Context, key string) (bool, time.Duration)
}

// senderStore is the persistence the worker needs.
type senderStore interface {
	ClaimSendableRecipients(ctx context.Context, limit int) ([]domain.CampaignRecipient, error)
	MarkRecipientDispatched(ctx context.Context, id uuid.UUID) error
	MarkRecipientSent(ctx context.Context, id uuid.UUID, providerMessageID string) error
	MarkRecipientFailed(ctx context.Context, id uuid.UUID, errMsg string) error
	MarkRecipientSuppressed(ctx context.Context, id uuid.UUID, reason string) error
	RependRecipient(ctx context.Context, id uuid.UUID, backoff time.Duration, errMsg string) error
	ReapStrandedRecipients(ctx context.Context, grace time.Duration, maxAttempts int) (int64, error)
	MarkDrainedCampaignsSent(ctx context.Context) (int64, error)
	CountRosterByStatus(ctx context.Context, campaignID uuid.UUID) (map[string]int, error)
	GetCampaignByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Campaign, error)
	GetContentByID(ctx context.Context, orgID, id uuid.UUID) (*CampaignContent, error)
	GetProfile(ctx context.Context, orgID uuid.UUID) (*OrgMarketingProfile, error)
	GetOrgName(ctx context.Context, orgID uuid.UUID) (string, error)
}

type fromResolver interface {
	ResolveFromAddress(ctx context.Context, orgID uuid.UUID, domainID *uuid.UUID) (string, error)
}

type tokenMinter interface {
	Mint(orgID uuid.UUID, emailNorm string, opts ...MintOpt) (string, error)
}

// CampaignSender is the send-lane worker.
type CampaignSender struct {
	store    senderStore
	sender   marketingSender
	guard    *SuppressionGuard
	from     fromResolver
	tokens   tokenMinter
	limiter  providerLimiter
	redis    *redis.Client // nullable — SSE progress degrades to no-op (FE can poll /progress)
	apiBase  string
	frontend string
	logger   *slog.Logger
}

// NewCampaignSender builds the worker. guard is derived from the same repo; redis may
// be nil (SSE progress then no-ops and the FE falls back to polling GET /:id/progress).
func NewCampaignSender(store senderStore, guard *SuppressionGuard, sender marketingSender, from fromResolver, tokens tokenMinter, limiter providerLimiter, redisClient *redis.Client, apiBase, frontend string, logger *slog.Logger) *CampaignSender {
	if logger == nil {
		logger = slog.Default()
	}
	return &CampaignSender{store: store, sender: sender, guard: guard, from: from, tokens: tokens, limiter: limiter, redis: redisClient, apiBase: apiBase, frontend: frontend, logger: logger}
}

// campaignSendContext is the per-campaign data loaded once per drain (not per
// recipient), so a 5k send does not reload the campaign/content/profile 5k times.
type campaignSendContext struct {
	campaign    *domain.Campaign
	content     *CampaignContent
	profile     *OrgMarketingProfile
	fromAddr    string
	orgName     string
	hasCompany  bool
	fatalReason string // non-empty ⇒ this campaign's rows can't send right now
	transient   bool   // fatalReason came from a DB/transport error → repend, not fail
}

// StartCampaignSender runs the drain loop until ctx is done.
func StartCampaignSender(ctx context.Context, s *CampaignSender) {
	if s.sender == nil {
		s.logger.Warn("marketing: campaign sender not started (no email transport)")
		return
	}
	ticker := time.NewTicker(campaignPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.drain(ctx)
		}
	}
}

func (s *CampaignSender) drain(ctx context.Context) {
	touched := map[uuid.UUID]uuid.UUID{} // campaignID → orgID, for the final progress event
	for round := 0; round < campaignMaxDrain; round++ {
		claimed, err := s.store.ClaimSendableRecipients(ctx, campaignClaimBatch)
		if err != nil {
			s.logger.Error("marketing: claim recipients failed", "error", err)
			return
		}
		if len(claimed) == 0 {
			break
		}
		for campID, orgID := range s.processBatch(ctx, claimed) {
			touched[campID] = orgID
		}
		if len(claimed) < campaignClaimBatch {
			break
		}
		if ctx.Err() != nil {
			return
		}
	}
	if _, err := s.store.MarkDrainedCampaignsSent(ctx); err != nil {
		s.logger.Error("marketing: finalize drained campaigns failed", "error", err)
	}
	// Final progress event per campaign touched this drain — after the finalize flip,
	// so a just-completed campaign streams its 'sent' status to the UI.
	for campID, orgID := range touched {
		s.publishProgress(ctx, orgID, campID)
	}
}

// processBatch groups claimed rows by campaign, loads each campaign's send context
// once, sends each recipient, then publishes a live progress event per campaign.
// Returns the campaigns it touched (campaignID → orgID).
func (s *CampaignSender) processBatch(ctx context.Context, rows []domain.CampaignRecipient) map[uuid.UUID]uuid.UUID {
	ctxs := map[uuid.UUID]*campaignSendContext{}
	orgOf := map[uuid.UUID]uuid.UUID{}
	for i := range rows {
		r := rows[i]
		sc := ctxs[r.CampaignID]
		if sc == nil {
			sc = s.loadCampaignContext(ctx, r.OrgID, r.CampaignID)
			ctxs[r.CampaignID] = sc
			orgOf[r.CampaignID] = r.OrgID
		}
		s.sendOne(ctx, r, sc)
	}
	for campID, orgID := range orgOf {
		s.publishProgress(ctx, orgID, campID)
	}
	return orgOf
}

// publishProgress emits a campaign_progress event (status + roster counts) onto the
// org SSE channel the FE already subscribes to (/api/events). No-op without Redis;
// best-effort — a publish failure never affects the send.
func (s *CampaignSender) publishProgress(ctx context.Context, orgID, campID uuid.UUID) {
	if s.redis == nil {
		return
	}
	counts, err := s.store.CountRosterByStatus(ctx, campID)
	if err != nil {
		return
	}
	status := ""
	if camp, err := s.store.GetCampaignByID(ctx, orgID, campID); err == nil && camp != nil {
		status = camp.Status
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	payload, err := json.Marshal(map[string]any{
		"type":        "campaign_progress",
		"campaign_id": campID.String(),
		"org_id":      orgID.String(),
		"status":      status,
		"counts":      counts,
		"total":       total,
	})
	if err != nil {
		return
	}
	s.redis.Publish(ctx, domain.OrgNotificationChannel(orgID), payload)
}

func (s *CampaignSender) loadCampaignContext(ctx context.Context, orgID, campID uuid.UUID) *campaignSendContext {
	sc := &campaignSendContext{}
	// A DB/transport error on ANY of these reads is TRANSIENT (repend, don't drop the
	// recipient); only a genuine nil/not-found or a bad-state result is a permanent
	// fatalReason. Collapsing the two (the pre-review bug) turned a Postgres blip into
	// silent, irrecoverable recipient loss for a whole batch.
	camp, err := s.store.GetCampaignByID(ctx, orgID, campID)
	if err != nil {
		sc.fatalReason, sc.transient = "load campaign: "+err.Error(), true
		return sc
	}
	if camp == nil {
		sc.fatalReason = "campaign not found"
		return sc
	}
	sc.campaign = camp
	// Status may have flipped (pause/cancel) between claim and now — do not send.
	if camp.Status != domain.CampaignStatusSending {
		sc.fatalReason = "paused_or_canceled"
		return sc
	}
	if camp.ContentID == nil {
		sc.fatalReason = "no content"
		return sc
	}
	content, err := s.store.GetContentByID(ctx, orgID, *camp.ContentID)
	if err != nil {
		sc.fatalReason, sc.transient = "load content: "+err.Error(), true
		return sc
	}
	if content == nil {
		sc.fatalReason = "content missing"
		return sc
	}
	sc.content = content
	sc.hasCompany = mergeScopeHasCompany(content.MergeScope)
	profile, err := s.store.GetProfile(ctx, orgID)
	if err != nil {
		sc.fatalReason, sc.transient = "load sender profile: "+err.Error(), true
		return sc
	}
	if profile == nil {
		sc.fatalReason = "no sender profile"
		return sc
	}
	sc.profile = profile
	fromAddr, err := s.from.ResolveFromAddress(ctx, orgID, camp.SendingDomainID)
	if err != nil {
		sc.fatalReason, sc.transient = "resolve from address: "+err.Error(), true
		return sc
	}
	if fromAddr == "" {
		sc.fatalReason = "no verified sending domain"
		return sc
	}
	sc.fromAddr = fromAddr
	sc.orgName, _ = s.store.GetOrgName(ctx, orgID)
	return sc
}

func (s *CampaignSender) sendOne(ctx context.Context, r domain.CampaignRecipient, sc *campaignSendContext) {
	// A paused/canceled campaign (status flipped after claim): return the row to
	// pending with no backoff so a resume re-sends it; a truly canceled campaign will
	// simply never be claimed again.
	if sc.fatalReason == "paused_or_canceled" {
		_ = s.store.RependRecipient(ctx, r.ID, 0, "campaign paused/canceled mid-send")
		return
	}
	if sc.fatalReason != "" {
		// Transient (DB/transport) → repend and retry; permanent (bad state / missing
		// content/domain) → fail. Attempt cap prevents an infinite repend loop.
		if sc.transient && r.Attempts < campaignMaxAttempts {
			_ = s.store.RependRecipient(ctx, r.ID, sendBackoff(r.Attempts), sc.fatalReason)
		} else {
			_ = s.store.MarkRecipientFailed(ctx, r.ID, sc.fatalReason)
		}
		return
	}

	// LIVE suppression/consent re-check (M1) — the authoritative gate. A mid-send
	// unsubscribe is dropped here, never mailed.
	verdict := s.guard.IsSendable(ctx, r.OrgID, r.EmailNormalized, ChannelMarketing, sc.campaign.TopicID)
	if !verdict.Sendable {
		// A transient ledger-read failure returns Reason "error" (IsSendable fails
		// closed) — that must NOT permanently suppress a mailable recipient. Retry it
		// (attempt-capped); a real suppression / no-lawful-basis is terminal.
		if verdict.Reason == "error" && r.Attempts < campaignMaxAttempts {
			_ = s.store.RependRecipient(ctx, r.ID, sendBackoff(r.Attempts), "consent check transient error")
			return
		}
		_ = s.store.MarkRecipientSuppressed(ctx, r.ID, verdict.Reason)
		return
	}

	// Throttle to the shared provider rps before every send.
	if !s.waitForProvider(ctx) {
		_ = s.store.RependRecipient(ctx, r.ID, campaignPollInterval, "rate-limit wait canceled")
		return
	}

	token, err := s.mintToken(r, sc.campaign.ID)
	if err != nil {
		_ = s.store.MarkRecipientFailed(ctx, r.ID, "unsubscribe token mint failed: "+err.Error())
		return
	}
	headers := MarketingUnsubHeaders(OneClickUnsubURL(s.apiBase, token))
	footer := FooterContext{OrgName: sc.orgName, PostalAddress: sc.profile.PhysicalPostalAddress, UnsubURL: PreferenceCenterURL(s.frontend, token)}

	var contactID uuid.UUID
	if r.ContactID != nil {
		contactID = *r.ContactID
	}
	ec := s.sender.HydrateMarketingContext(ctx, r.OrgID, contactID, sc.campaign.Name, sc.orgName, sc.hasCompany)
	html := RenderForRecipient(sc.content.BodyHTMLCompiled, ec, footer)
	subject := automation.InterpolateTemplate(sc.content.Subject, ec)

	idemKey := fmt.Sprintf("campaign:%s:contact:%s", sc.campaign.ID, r.ID)
	// Stamp dispatch BEFORE handing to Resend, so a crash between here and MarkSent
	// leaves a durable "was dispatched" marker the reaper reads to avoid a >24h
	// duplicate re-send. Best-effort: a stamp failure must not block the send.
	_ = s.store.MarkRecipientDispatched(ctx, r.ID)
	resp, err := s.sender.SendMarketingEmail(ctx, r.EmailNormalized, subject, html, sc.profile.FromName, sc.fromAddr, sc.profile.ReplyTo, nil, headers, idemKey)
	if err != nil {
		s.handleSendError(ctx, r, err)
		return
	}
	if err := s.store.MarkRecipientSent(ctx, r.ID, extractMessageID(resp)); err != nil {
		s.logger.Error("marketing: mark sent failed", "recipient", r.ID.String(), "error", err)
	}
}

func (s *CampaignSender) handleSendError(ctx context.Context, r domain.CampaignRecipient, err error) {
	var re *automation.RetryableError
	retryable := errors.As(err, &re)
	if retryable && r.Attempts < campaignMaxAttempts {
		_ = s.store.RependRecipient(ctx, r.ID, sendBackoff(r.Attempts), err.Error())
		return
	}
	// Permanent 4xx, or attempts exhausted.
	_ = s.store.MarkRecipientFailed(ctx, r.ID, err.Error())
}

func (s *CampaignSender) mintToken(r domain.CampaignRecipient, campID uuid.UUID) (string, error) {
	opts := []MintOpt{WithCampaignID(campID)}
	if r.ContactID != nil {
		opts = append(opts, WithContactID(*r.ContactID))
	}
	return s.tokens.Mint(r.OrgID, r.EmailNormalized, opts...)
}

// waitForProvider blocks (bounded) until the shared bucket admits one send, backing
// off the whole pool. Returns false only if ctx is canceled.
func (s *CampaignSender) waitForProvider(ctx context.Context) bool {
	for {
		if s.limiter == nil {
			return true
		}
		ok, retryAfter := s.limiter.Allow(ctx, providerBucketKey)
		if ok {
			return true
		}
		if retryAfter <= 0 || retryAfter > providerWaitCap {
			retryAfter = providerWaitCap
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(retryAfter):
		}
	}
}

// StartCampaignReaper recovers recipients stranded in 'processing' by a crashed worker.
func StartCampaignReaper(ctx context.Context, store senderStore, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(campaignReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := store.ReapStrandedRecipients(ctx, campaignReapGrace, campaignMaxAttempts); err != nil {
				logger.Error("marketing: campaign reaper failed", "error", err)
			} else if n > 0 {
				logger.Info("marketing: reaped stranded recipients", "count", n)
			}
		}
	}
}

// prunerStore is the slice of persistence the roster pruner needs.
type prunerStore interface {
	PruneCompletedRoster(ctx context.Context, olderThan time.Duration) (int64, error)
}

// StartCampaignPruner periodically reclaims the roster rows of long-finished
// campaigns (the campaign row is kept for reporting). Runs until ctx is done.
func StartCampaignPruner(ctx context.Context, store prunerStore, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(campaignPruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := store.PruneCompletedRoster(ctx, campaignRosterRetention); err != nil {
				logger.Error("marketing: campaign roster prune failed", "error", err)
			} else if n > 0 {
				logger.Info("marketing: pruned completed campaign roster rows", "count", n)
			}
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func sendBackoff(attempts int) time.Duration {
	// attempts is already incremented at claim (1 on the first try). Exponential,
	// capped at 5 minutes.
	d := time.Duration(1<<uint(attempts)) * 15 * time.Second
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

func mergeScopeHasCompany(scope datatypes.JSON) bool {
	if len(scope) == 0 {
		return false
	}
	var roots []string
	if err := json.Unmarshal(scope, &roots); err != nil {
		return false
	}
	for _, s := range roots {
		if s == "company" {
			return true
		}
	}
	return false
}

// extractMessageID best-effort pulls the Resend message id out of the send response
// (map{"response": map{"id": ...}}).
func extractMessageID(resp any) string {
	m, ok := resp.(map[string]any)
	if !ok {
		return ""
	}
	body, ok := m["response"].(map[string]any)
	if !ok {
		return ""
	}
	id, _ := body["id"].(string)
	return id
}
