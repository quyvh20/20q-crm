package marketing

import (
	"context"
	"time"

	"crm-backend/internal/emailutil"

	"github.com/google/uuid"
)

// ledgerReader is the slice of the repository the guard needs. Declaring it here
// (consumer-side) keeps IsSendable unit-testable with a fake — no DB required.
type ledgerReader interface {
	SuppressionsForEmail(ctx context.Context, orgID uuid.UUID, emailNorm string) ([]Suppression, error)
	MarketingStateForEmail(ctx context.Context, orgID uuid.UUID, emailNorm string) (*ContactMarketingState, error)
}

// SuppressionGuard is the SOLE pre-send chokepoint. It is built in M1 but not yet
// wired into any send path (there is no send path until M7/M8) — a deliberate M1
// exit criterion. When it is wired, the caller supplies the channel so the gate
// never touches transactional or version-pinned in-flight sends (Guardrail 9).
type SuppressionGuard struct {
	r   ledgerReader
	now func() time.Time
}

// NewSuppressionGuard builds the guard over a ledger reader (the *Repository).
func NewSuppressionGuard(r ledgerReader) *SuppressionGuard {
	return &SuppressionGuard{r: r, now: time.Now}
}

// Sendability is the verdict IsSendable returns — sendable plus, when not, a stable
// machine-readable reason for logging/telemetry.
type Sendability struct {
	Sendable bool
	// Reason is one of: "" (sendable), "suppressed:<reason>", "no_lawful_basis",
	// "empty_email", "error".
	Reason string
}

// IsSendable is the authoritative do-not-mail verdict for one email on one channel.
//
//   - transactional: blocked only by a scope=all suppression (hard bounce /
//     complaint / soft-bounce-over-threshold). An unsubscribe never blocks it.
//   - marketing: blocked by any scope=all OR scope=marketing suppression
//     (topic-aware), AND additionally requires a positive lawful basis — absence of
//     a consent row is NOT consent.
//
// topicID may be nil (no topic). It only affects the marketing channel.
func (g *SuppressionGuard) IsSendable(ctx context.Context, orgID uuid.UUID, email string, channel Channel, topicID *uuid.UUID) Sendability {
	emailNorm := emailutil.Normalize(email)
	if emailNorm == "" {
		return Sendability{Sendable: false, Reason: "empty_email"}
	}

	sups, err := g.r.SuppressionsForEmail(ctx, orgID, emailNorm)
	if err != nil {
		// Fail CLOSED: a ledger read failure must never let a send slip past the gate.
		return Sendability{Sendable: false, Reason: "error"}
	}
	for _, s := range sups {
		if s.Suppresses(channel, topicID) {
			return Sendability{Sendable: false, Reason: "suppressed:" + s.Reason}
		}
	}

	// Transactional mail needs no positive consent — only the absence of a global
	// suppression, checked above.
	if channel != ChannelMarketing {
		return Sendability{Sendable: true}
	}

	state, err := g.r.MarketingStateForEmail(ctx, orgID, emailNorm)
	if err != nil {
		return Sendability{Sendable: false, Reason: "error"}
	}
	if !state.HasLawfulBasis(g.now()) {
		return Sendability{Sendable: false, Reason: "no_lawful_basis"}
	}
	return Sendability{Sendable: true}
}
