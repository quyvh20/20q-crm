package automation

import (
	"context"

	"github.com/google/uuid"
)

// marketingChannelParam is the send_email step param value that routes a step through
// the M8 marketing lane (live suppression gate + org verified send-domain From +
// RFC-8058 List-Unsubscribe headers + M6 compiled footer/preheader). Any other value
// (or an absent param) keeps the byte-for-byte transactional path (Guardrail 9), so
// version-pinned in-flight runs — which carry no channel param — are unchanged.
const marketingChannelParam = "marketing"

// MarketingSendPreparer is the cross-package seam that lets a send_email step send a
// compliant MARKETING-channel email (M8 drip sequences) WITHOUT package automation
// importing package marketing (that import runs marketing→automation, so the reverse
// would close a cycle). It is declared here with automation-owned primitives only — no
// marketing.Channel / marketing.Sendability / marketing.FooterContext types may appear.
//
// The implementation lives in package marketing (marketing.SequenceSendPreparer) and is
// injected via Engine.SetMarketingSendPreparer from cmd/server/main.go. When it is nil,
// a step that sets channel=marketing fails permanently (fail-closed): the engine must
// never fall back to the global MAIL_FROM (owned by no org → DKIM/DMARC misalignment)
// nor send without the suppression gate + List-Unsubscribe + CAN-SPAM footer.
//
// PrepareMarketingSend runs the M1 suppression/lawful-basis gate LIVE at send time — a
// contact who unsubscribed mid-sequence is skipped here, never mailed, because opt-out
// provably cannot be enforced at trigger/timer time (fireTimerRun + the date_field
// materializer consult no suppression predicate). It also resolves the org's verified
// send-domain From (alignment), mints the one-click List-Unsubscribe headers, and
// renders the M6 compiled content (footer + preheader + merge) for the one recipient.
type MarketingSendPreparer interface {
	PrepareMarketingSend(ctx context.Context, req MarketingSendRequest) (MarketingSendDecision, error)
}

// MarketingSendRequest is the fully automation-typed input to PrepareMarketingSend.
type MarketingSendRequest struct {
	OrgID      uuid.UUID  // the sending org
	WorkflowID uuid.UUID  // the sequence workflow — the "campaign" the unsub token records
	ContactID  uuid.UUID  // uuid.Nil when the recipient has no contact row
	ToEmail    string     // the resolved recipient address (raw; the gate normalizes it)
	ContentID  uuid.UUID  // the M6 marketing content to render
	TopicID    *uuid.UUID // optional marketing topic (per-topic opt-down)
}

// MarketingSendDecision is what PrepareMarketingSend returns.
//
//   - Skip=false: every send field below is fully resolved; the executor sends.
//   - Skip=true, Retry=false: a TERMINAL, NON-error outcome (suppressed / no lawful
//     basis / deleted content). The executor records status="skipped" and the run
//     CONTINUES to its next step — this recipient is simply not mailed at this step.
//   - Skip=true, Retry=true: a TRANSIENT problem (ledger read error, no verified domain
//     yet, missing sender profile, unsubscribe key unset). The executor returns a
//     retryable error so the run parks + retries once the condition clears.
type MarketingSendDecision struct {
	Skip       bool
	Retry      bool   // Skip only: true = transient (retryable), false = terminal skip
	SkipReason string // machine-readable reason ("suppressed:...", "no_lawful_basis", "no_verified_domain", ...)

	ToAddress   string // the normalized recipient the email is actually sent to
	Subject     string
	BodyHTML    string
	FromName    string
	FromAddress string // the org's VERIFIED send-domain address (alignment) — never the global MAIL_FROM
	ReplyTo     string
	Headers     map[string]string // the RFC-8058 List-Unsubscribe pair
}

// SetMarketingSendPreparer injects the package-marketing implementation that lets a
// channel=marketing send_email step send a compliant marketing email (M8). It is set
// AFTER the engine and the marketing services are both constructed (the preparer depends
// on the engine for merge-context hydration), which breaks the construction cycle. A
// no-op when no email executor is registered.
func (e *Engine) SetMarketingSendPreparer(p MarketingSendPreparer) {
	if ex, ok := e.executors[ActionSendEmail].(*EmailExecutor); ok && ex != nil {
		ex.mktPreparer = p
	}
}

// contactIDFromEval best-effort reads the enrolled contact id out of a run's eval
// context (contact.id), returning uuid.Nil when absent/unparseable.
func contactIDFromEval(evalCtx EvalContext) uuid.UUID {
	if evalCtx.Contact == nil {
		return uuid.Nil
	}
	id, _ := evalCtx.Contact["id"].(string)
	if id == "" {
		return uuid.Nil
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil
	}
	return u
}

// uuidParam reads a raw uuid-valued step param (content_id / topic_id). These are fixed
// ids set by the builder, never merge templates, so they are read raw (not interpolated).
func uuidParam(params map[string]any, key string) (uuid.UUID, bool) {
	s, ok := params[key].(string)
	if !ok || s == "" {
		return uuid.Nil, false
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, false
	}
	return u, true
}
