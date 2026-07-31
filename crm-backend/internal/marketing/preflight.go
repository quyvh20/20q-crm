package marketing

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// preflight.go answers, for one org, "is everything in place for a marketing send
// to actually reach someone?" — read-only, and without ever revealing a secret.
//
// It exists because every failure mode in this stack is silent. An unset signing
// key, a domain that never had tracking switched on, a missing sender-profile row,
// or an org whose contacts carry no lawful basis all present identically: a green
// launch checklist followed by nothing arriving. The per-campaign readiness check
// covers four of those; this covers the deployment-level rest.

// PreflightEnv carries deployment facts the marketing module cannot read itself.
// internal/marketing deliberately does not import pkg/config, so these are passed
// in as ALREADY-DERIVED booleans, ints and non-secret strings. No key material ever
// crosses this boundary.
type PreflightEnv struct {
	AppEnv               string
	MailDisabled         bool
	MailFrom             string
	ResendAPIKeySet      bool
	ResendWebhookSecretSet bool
	ResendMaxRPS         int
	// UnsubKeyVersions lists the keyring versions that PARSED. Reporting the version
	// integers is safe and is the only useful thing that can be said about a signing
	// key without leaking it.
	UnsubKeyVersions []int
	UnsubKeyPrimary  int
	FrontendURL      string
	PublicAPIBaseURL string
	SendWorkerStarted bool
}

// preflightStore is the read side the report needs.
type preflightStore interface {
	GetProfile(ctx context.Context, orgID uuid.UUID) (*OrgMarketingProfile, error)
	CountMailableContacts(ctx context.Context, orgID uuid.UUID) (int64, error)
	CountKnownMarketingContacts(ctx context.Context, orgID uuid.UUID) (int64, error)
	HasConsentUniqueConstraint(ctx context.Context) (bool, error)
}

// PreflightService evaluates the report.
type PreflightService struct {
	store   preflightStore
	domains domainGate
	tokens  tokenGate
	env     PreflightEnv
	logger  *slog.Logger
}

func NewPreflightService(store preflightStore, domains domainGate, tokens tokenGate, env PreflightEnv, logger *slog.Logger) *PreflightService {
	return &PreflightService{store: store, domains: domains, tokens: tokens, env: env, logger: logger}
}

type checkAccumulator struct {
	checks []domain.LaunchCheck
}

func (a *checkAccumulator) add(key, label string, ok bool, detail string) {
	a.checks = append(a.checks, domain.LaunchCheck{Key: key, Label: label, OK: ok, Detail: detail})
}

func (a *checkAccumulator) warn(key, label string, ok bool, detail string) {
	a.checks = append(a.checks, domain.LaunchCheck{
		Key: key, Label: label, OK: ok, Detail: detail, Severity: domain.SeverityWarn,
	})
}

// Evaluate builds the report. It never returns an error for a failing CHECK — a red
// row is the payload, not a fault. It errors only when it cannot read at all.
func (s *PreflightService) Evaluate(ctx context.Context, orgID uuid.UUID) (*domain.MarketingPreflight, error) {
	var a checkAccumulator

	// ── Deployment configuration ──────────────────────────────────────────────

	// The worst silent failure in the stack: the marketing lane does NOT
	// short-circuit on a missing API key. It 401s per recipient and burns the whole
	// roster to `failed`, one row at a time, with no single obvious error.
	a.add("resend_api_key", "Resend API key configured", s.env.ResendAPIKeySet,
		"RESEND_API_KEY is not set — a launch would fail every recipient individually rather than refusing up front")

	a.add("unsubscribe_key", "One-click unsubscribe signing key", s.tokens != nil && s.tokens.Configured(),
		unsubKeyDetail(s.env))

	// MAIL_DISABLED gates the platform mailer, NOT the marketing sender. Saying so
	// explicitly matters: an operator who sets it expecting a global kill switch
	// would still send marketing.
	a.warn("mail_disabled", "Platform mailer enabled", !s.env.MailDisabled,
		"MAIL_DISABLED is set. Note this stops transactional platform mail only — it does NOT stop marketing campaigns")

	a.warn("delivery_webhook", "Delivery webhook configured", s.env.ResendWebhookSecretSet,
		"RESEND_WEBHOOK_SECRET is not set, so every delivery event 401s. Sending still works, but bounce/complaint auto-suppression and all campaign analytics stay dark")

	a.add("public_api_base_url", "Public API base URL set", strings.TrimSpace(s.env.PublicAPIBaseURL) != "" && !isLocalURL(s.env.PublicAPIBaseURL),
		fmt.Sprintf("PUBLIC_API_BASE_URL is %q — mailbox providers POST one-click unsubscribes here, and it is baked permanently into mail already sent", s.env.PublicAPIBaseURL))

	a.add("frontend_url", "Frontend URL set", strings.TrimSpace(s.env.FrontendURL) != "" && !isLocalURL(s.env.FrontendURL),
		fmt.Sprintf("FRONTEND_URL is %q — this is the unsubscribe landing page baked into every footer, and it cannot be corrected in mail already delivered", s.env.FrontendURL))

	a.warn("send_worker", "Campaign send worker running", s.env.SendWorkerStarted,
		"the campaign sender did not start, so launched campaigns will sit in 'sending' with nothing draining the roster")

	// ── Org configuration ─────────────────────────────────────────────────────

	if s.domains != nil {
		domOK, reason, err := s.domains.CanBulkSend(ctx, orgID)
		if err != nil {
			a.add("sending_domain", "Verified sending domain", false, "could not check the sending domain")
		} else {
			a.add("sending_domain", "Verified sending domain", domOK, domainPreflightDetail(reason))
		}
	}

	profile, perr := s.store.GetProfile(ctx, orgID)
	switch {
	case perr != nil:
		a.add("sender_profile", "Sender profile complete", false, "could not load the sender profile")
	default:
		ok, reason := SenderProfileSendable(profile)
		a.add("sender_profile", "Sender profile complete", ok, senderPreflightDetail(reason))
	}

	// The recipient-claim SQL INNER JOINs org_marketing_profile, so a missing row
	// means the worker claims zero recipients forever — with no error anywhere.
	a.add("marketing_profile_row", "Marketing profile row exists", profile != nil,
		"no org_marketing_profile row: the send worker's claim query INNER JOINs this table, so it will silently claim zero recipients")

	// ── Lawful basis ──────────────────────────────────────────────────────────

	mailable, mErr := s.store.CountMailableContacts(ctx, orgID)
	known, kErr := s.store.CountKnownMarketingContacts(ctx, orgID)
	if mErr != nil || kErr != nil {
		a.add("lawful_basis", "Contacts with a lawful basis", false, "could not count lawful-basis coverage")
	} else {
		a.add("lawful_basis", "Contacts with a lawful basis", mailable > 0,
			lawfulBasisDetail(mailable, known))
	}

	// ON CONFLICT (org_id, email_normalized) needs this constraint as its arbiter.
	// Unlike its siblings it has no probe-and-refuse boot guard, so nothing repairs
	// its absence, and every consent grant would fail with SQLSTATE 42P10.
	//
	// A failed probe reports as a failed check rather than dropping the row: a check
	// that silently disappears when its own query breaks is worse than no check,
	// because the report still looks complete.
	hasUniq, uniqErr := s.store.HasConsentUniqueConstraint(ctx)
	switch {
	case uniqErr != nil:
		a.warn("consent_constraint", "Consent uniqueness constraint present", false,
			"could not verify UNIQUE (org_id, email_normalized) on contact_marketing_state")
	default:
		a.warn("consent_constraint", "Consent uniqueness constraint present", hasUniq,
			"contact_marketing_state is missing UNIQUE (org_id, email_normalized); granting a lawful basis will fail until it is restored")
	}

	out := &domain.MarketingPreflight{
		Checks:           a.checks,
		MailableContacts: mailable,
		KnownContacts:    known,
	}
	out.Ready = true
	for _, ck := range a.checks {
		if !ck.OK && ck.Severity != domain.SeverityWarn {
			out.Ready = false
			break
		}
	}
	return out, nil
}

func unsubKeyDetail(env PreflightEnv) string {
	if len(env.UnsubKeyVersions) == 0 {
		return "MARKETING_UNSUB_KEY is not set. Sends are refused rather than delivered without an unsubscribe link, so nothing goes out until it is configured"
	}
	return fmt.Sprintf("set; keyring versions %v; signing under v%d", env.UnsubKeyVersions, env.UnsubKeyPrimary)
}

// isLocalURL flags a value that is almost certainly a leftover development default.
// FRONTEND_URL defaults to a localhost origin and is written permanently into every
// unsubscribe link, so shipping it is unrecoverable for mail already delivered.
func isLocalURL(u string) bool {
	l := strings.ToLower(u)
	return strings.Contains(l, "localhost") || strings.Contains(l, "127.0.0.1") || strings.Contains(l, "[::1]")
}

func domainPreflightDetail(reason string) string {
	switch reason {
	case "", "ok":
		return ""
	case "no_domains":
		return "no sending domain has been added for this org"
	case "not_verified":
		return "the domain's SPF/DKIM records have not verified yet"
	case "no_dmarc":
		return "no DMARC policy is published for the domain, so one-click unsubscribe is withheld by mailbox providers"
	default:
		return reason
	}
}

func senderPreflightDetail(reason string) string {
	switch reason {
	case "":
		return ""
	case "no_profile":
		return "no sender profile has been saved"
	case "marketing_paused":
		return "marketing is paused for this org (auto-paused by the complaint-rate breaker, or paused manually)"
	case "no_postal_address":
		return "no physical postal address — CAN-SPAM requires one in every marketing footer"
	case "no_from_name":
		return "no from-name is set"
	default:
		return reason
	}
}

func lawfulBasisDetail(mailable, known int64) string {
	if mailable > 0 {
		return fmt.Sprintf("%d of %d known addresses carry a lawful basis", mailable, known)
	}
	if known == 0 {
		return "no marketing consent has been recorded for any address. Declare a lawful basis for contacts you may lawfully mail, or every recipient will be suppressed with \"no_lawful_basis\""
	}
	return fmt.Sprintf("none of the %d known addresses carry a lawful basis — every recipient would be suppressed with \"no_lawful_basis\"", known)
}
