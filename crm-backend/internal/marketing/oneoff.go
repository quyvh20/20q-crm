package marketing

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// oneToOneFromLocalPart is the local part used for a rep's 1:1 email — a
// generic shared mailbox (no per-rep inbox infrastructure exists yet), with
// ReplyTo set to the sending user's own account email so a reply lands in
// their real inbox. Two-way sync (reading those replies back into the CRM
// timeline) is the explicit large follow-on the plan defers separately.
const oneToOneFromLocalPart = "hello"

// oneToOneSender is the transport seam (satisfied by *automation.Engine's
// SendOneToOneEmail). Deliberately NOT marketingSender/SendMarketingEmail —
// that method's own doc comment says a transactional caller must never reach
// it (it carries the List-Unsubscribe machinery a 1:1 reply has no business
// attaching).
type oneToOneSender interface {
	SendOneToOneEmail(ctx context.Context, to, subject, bodyHTML, fromName, fromAddress, replyTo, idempotencyKey string) (any, error)
}

// OneToOneEmailUseCase is R9's "1:1 email from a record": send through the
// org's verified M2 domain, gated on the SAME suppression ledger as bulk mail
// (hard bounce/complaint/unsubscribe still block it) but on
// ChannelTransactional — no lawful-basis/consent check, because this is a rep
// emailing a known contact, not a marketing blast.
type OneToOneEmailUseCase struct {
	db          *gorm.DB
	guard       *SuppressionGuard
	from        fromResolver
	sender      oneToOneSender
	limiter     providerLimiter
	contactRepo domain.ContactRepository
	dealRepo    domain.DealRepository
	authRepo    domain.AuthRepository
	logger      *slog.Logger
}

func NewOneToOneEmailUseCase(
	db *gorm.DB,
	guard *SuppressionGuard,
	from fromResolver,
	sender oneToOneSender,
	limiter providerLimiter,
	contactRepo domain.ContactRepository,
	dealRepo domain.DealRepository,
	authRepo domain.AuthRepository,
	logger *slog.Logger,
) *OneToOneEmailUseCase {
	if logger == nil {
		logger = slog.Default()
	}
	return &OneToOneEmailUseCase{
		db: db, guard: guard, from: from, sender: sender, limiter: limiter,
		contactRepo: contactRepo, dealRepo: dealRepo, authRepo: authRepo, logger: logger,
	}
}

// Send delivers a 1:1 email to a contact, or to a deal's linked contact, then
// logs it to the timeline as an Activity — the same "what happened with this
// person" record a call/meeting/note already gets.
func (uc *OneToOneEmailUseCase) Send(ctx context.Context, orgID, userID uuid.UUID, slug string, recordID uuid.UUID, subject, bodyHTML string) (*domain.Activity, error) {
	var contactID uuid.UUID
	var dealID *uuid.UUID
	var toEmail string

	switch slug {
	case "contact":
		c, err := uc.contactRepo.GetByID(ctx, orgID, recordID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		if c == nil {
			return nil, domain.ErrContactNotFound
		}
		if c.Email == nil || *c.Email == "" {
			return nil, domain.NewAppError(400, "this contact has no email address")
		}
		contactID, toEmail = c.ID, *c.Email
	case "deal":
		d, err := uc.dealRepo.GetByID(ctx, orgID, recordID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		if d == nil {
			return nil, domain.NewAppError(404, "deal not found")
		}
		dealID = &d.ID
		if d.ContactID == nil {
			return nil, domain.NewAppError(400, "this deal has no linked contact to email")
		}
		c, err := uc.contactRepo.GetByID(ctx, orgID, *d.ContactID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		if c == nil || c.Email == nil || *c.Email == "" {
			return nil, domain.NewAppError(400, "this deal's contact has no email address")
		}
		contactID, toEmail = c.ID, *c.Email
	default:
		return nil, domain.NewAppError(400, "one-to-one email is only available on contacts and deals")
	}

	// LIVE suppression check, same principle as the campaign lane's live
	// re-check — but transactional: a bounce/complaint/unsubscribe still
	// blocks it, absence of marketing consent does not.
	verdict := uc.guard.IsSendable(ctx, orgID, toEmail, ChannelTransactional, nil)
	if !verdict.Sendable {
		return nil, domain.NewAppError(422, "this recipient cannot be emailed: "+verdict.Reason)
	}

	if !uc.waitForProvider(ctx) {
		return nil, ctx.Err()
	}

	fromAddr, err := uc.from.ResolveFromAddress(ctx, orgID, nil)
	if err != nil {
		return nil, domain.NewAppError(422, "no verified sending domain: "+err.Error())
	}
	// Swap the bulk local part ("marketing@…") for the 1:1 mailbox; the domain
	// half (DKIM/DMARC aligned) is what matters for deliverability, so reuse it.
	if at := strings.IndexByte(fromAddr, '@'); at >= 0 {
		fromAddr = oneToOneFromLocalPart + fromAddr[at:]
	}

	fromName, replyTo := "", ""
	if user, err := uc.authRepo.GetUserByID(ctx, userID); err == nil && user != nil {
		fromName = strings.TrimSpace(user.FirstName + " " + user.LastName)
		replyTo = user.Email
	}

	idemKey := fmt.Sprintf("oneoff:%s:%s:%d", slug, recordID, time.Now().UnixNano())
	if _, err := uc.sender.SendOneToOneEmail(ctx, toEmail, subject, bodyHTML, fromName, fromAddr, replyTo, idemKey); err != nil {
		return nil, fmt.Errorf("send failed: %w", err)
	}

	activity := domain.Activity{
		OrgID:      orgID,
		Type:       "email",
		UserID:     &userID,
		ContactID:  &contactID,
		DealID:     dealID,
		Title:      &subject,
		Body:       &bodyHTML,
		OccurredAt: time.Now(),
	}
	if err := uc.db.WithContext(ctx).Create(&activity).Error; err != nil {
		// The email is already sent — a logging failure must not report the send
		// itself as failed, only be noisy about the gap in the timeline.
		uc.logger.Error("one-to-one email: sent but activity log failed", "error", err)
	}
	return &activity, nil
}

// waitForProvider mirrors CampaignSender.waitForProvider exactly: block
// (bounded) until the shared Resend-team-wide bucket admits one send. Same
// bucket key as the bulk lane on purpose — Resend's rate limit is per-team,
// not per-feature.
func (uc *OneToOneEmailUseCase) waitForProvider(ctx context.Context) bool {
	for {
		if uc.limiter == nil {
			return true
		}
		ok, retryAfter := uc.limiter.Allow(ctx, providerBucketKey)
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
