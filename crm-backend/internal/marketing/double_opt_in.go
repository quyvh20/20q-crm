package marketing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"crm-backend/internal/domain"
	"crm-backend/internal/emailutil"

	"github.com/google/uuid"
)

// R9: double-opt-in confirmation. Unlocks cold-list mailing — an admin
// invites ONE contact (the granular unit; a bulk-audience variant is a
// straightforward extension of the same GrantLawfulBasis/mint/send loop, left
// out here to keep the first cut reviewable) to confirm before they become
// mailable, per email_marketing_plan.md's original design that was never
// built (see grantableBases's doc comment in models.go — an admin cannot
// declare double_opt_in on a subscriber's behalf; it has to come from them).

// transactionalSender is the seam this needs from *automation.Engine — its
// existing one-off transport (used today by the A5 test-send endpoint),
// reused here rather than duplicating it.
type transactionalSender interface {
	SendTestEmail(ctx context.Context, to, subject, bodyHTML string) error
}

// doubleOptInStore is the repository slice this usecase needs.
type doubleOptInStore interface {
	GrantLawfulBasis(ctx context.Context, orgID uuid.UUID, scope GrantScope, in GrantInput) (int64, error)
	PromoteDoubleOptIn(ctx context.Context, orgID uuid.UUID, emailNorm string) error
}

type DoubleOptInUseCase struct {
	store       doubleOptInStore
	tokens      *ConfirmTokenService
	guard       *SuppressionGuard
	sender      transactionalSender // nil-tolerant — see SetSender
	contactRepo domain.ContactRepository
	frontendURL string
	orgName     OrgNameResolver
	logger      *slog.Logger
}

// NewDoubleOptInUseCase builds the usecase with no send transport yet — it
// is constructed before *automation.Engine exists (Confirm/the public
// endpoint need no sender at all), and SetSender wires the transport once
// the engine is up. RequestForContact fails closed with a clear error until
// then, the same nil-tolerant shape as ContactHandler.emitEvent.
func NewDoubleOptInUseCase(
	store doubleOptInStore,
	tokens *ConfirmTokenService,
	guard *SuppressionGuard,
	contactRepo domain.ContactRepository,
	frontendURL string,
	orgName OrgNameResolver,
	logger *slog.Logger,
) *DoubleOptInUseCase {
	if logger == nil {
		logger = slog.Default()
	}
	return &DoubleOptInUseCase{
		store: store, tokens: tokens, guard: guard,
		contactRepo: contactRepo, frontendURL: frontendURL, orgName: orgName, logger: logger,
	}
}

// SetSender wires the email transport once *automation.Engine exists.
func (uc *DoubleOptInUseCase) SetSender(s transactionalSender) { uc.sender = s }

// RequestForContact records a pending double_opt_in basis for the contact
// (reusing the same GrantLawfulBasis write the admin bulk-grant uses, so an
// unsubscribed/cleaned address can never be re-invited into a live loop —
// its WHERE guard already refuses that), then mints a confirm token and
// sends the confirmation email. The address is NOT mailable yet — only the
// confirm-link click (PromoteDoubleOptIn) makes it so.
func (uc *DoubleOptInUseCase) RequestForContact(ctx context.Context, orgID, userID, contactID uuid.UUID) error {
	c, err := uc.contactRepo.GetByID(ctx, orgID, contactID)
	if err != nil {
		return domain.ErrInternal
	}
	if c == nil {
		return domain.ErrContactNotFound
	}
	if c.Email == nil || *c.Email == "" {
		return domain.NewAppError(400, "this contact has no email address")
	}
	if uc.sender == nil {
		return domain.NewAppError(503, "email sending is not configured")
	}
	emailNorm := emailutil.Normalize(*c.Email)

	if _, err := uc.store.GrantLawfulBasis(ctx, orgID, GrantScope{ContactIDs: []uuid.UUID{contactID}}, GrantInput{
		Basis: BasisDoubleOptIn, Source: "contact_double_opt_in_request", GrantedBy: userID, Now: time.Now(),
	}); err != nil {
		return fmt.Errorf("record pending consent: %w", err)
	}

	// The confirmation email is itself transactional (not the double-opt-in
	// mail this whole flow gates), so it is checked against the SAME hard
	// suppressions as any other transactional send — a hard-bounced or
	// complained address should not receive it either.
	verdict := uc.guard.IsSendable(ctx, orgID, emailNorm, ChannelTransactional, nil)
	if !verdict.Sendable {
		return domain.NewAppError(422, "this address cannot be emailed: "+verdict.Reason)
	}

	token, err := uc.tokens.Mint(orgID, emailNorm, &contactID)
	if err != nil {
		return fmt.Errorf("mint confirm token: %w", err)
	}

	orgName := ""
	if uc.orgName != nil {
		if name, err := uc.orgName(ctx, orgID); err == nil {
			orgName = name
		}
	}
	confirmURL := uc.frontendURL + "/marketing/confirm/" + token
	subject := "Please confirm your subscription"
	if orgName != "" {
		subject = "Please confirm your subscription to " + orgName
	}
	body := fmt.Sprintf(
		`<p>Hi,</p><p>%s would like to send you occasional emails. Please confirm you'd like to hear from them:</p><p><a href="%s">Confirm subscription</a></p><p>If you did not expect this, you can ignore this message — you will not be added without confirming.</p>`,
		orDefault(orgName, "We"), confirmURL,
	)

	if err := uc.sender.SendTestEmail(ctx, emailNorm, subject, body); err != nil {
		return fmt.Errorf("send confirmation email: %w", err)
	}
	return nil
}

// Confirm opens a token and promotes the address to subscribed. Called by
// the public (unauthenticated) confirm endpoint.
func (uc *DoubleOptInUseCase) Confirm(ctx context.Context, token string) (ConfirmToken, error) {
	tok, err := uc.tokens.Verify(token)
	if err != nil {
		return ConfirmToken{}, err
	}
	if err := uc.store.PromoteDoubleOptIn(ctx, tok.OrgID, tok.Email); err != nil {
		return ConfirmToken{}, err
	}
	return tok, nil
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
