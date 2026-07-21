package integrations

import (
	"github.com/google/uuid"
)

// Per-delivery retry.
//
// The scope here is narrower than it first looks, and the narrowing is the design.
// An adversarial pass over three candidate designs produced 22 high-severity
// hazards, and SEVEN of them were one hazard wearing different hats — every one in
// the path where an admin button re-runs a SYNC delivery from its stored payload:
//
//   - the stranded-event reaper fails a running replay as "the server restarted",
//     within its 10-minute grace, and by flipping the row back to `failed` re-opens
//     the retry guard so a second run can start on top of the first;
//   - the guard cannot see a concurrent Idempotency-Key re-run at all, because that
//     path re-runs in place and never writes `processing` to the database;
//   - `result_record_id` — the anti-double-write guard everything depends on — is
//     written by the very call that can fail AFTER the record exists, so it is NULL
//     exactly when a lead HAS been written;
//   - `FinishEvent` is a wholesale Save, so a losing run writes NULL back over a
//     result another run just committed, re-arming the button on a delivery that
//     already produced a contact.
//
// All four are unreachable if we do not replay stored payloads on demand. And sync
// channels already have a recovery path that is safe by construction: the integrator
// re-sends with the same Idempotency-Key and re-enters Ingest's replay switch, which
// is where those invariants are already encoded. A button would duplicate that path
// with none of its protection, and could not work at all for the two pre-auth
// payload shapes (a Google key-mismatch row stores a REDACTED envelope, not lead
// fields; a form rejection stores nested unauthenticated body).
//
// So retry means one thing: hand a provider delivery back to the async worker, which
// re-FETCHES it from the provider. It is a true retry rather than a replay, it needs
// no stored-payload interpretation, and it covers the case worth the most — a
// Facebook lead quarantined because its form was not enabled yet, recovered the
// moment the admin enables it.

// RetryMode is what, if anything, an admin may do with a delivery.
type RetryMode string

const (
	// RetryModeRefetch: hand it back to the worker to fetch again from the provider.
	RetryModeRefetch RetryMode = "refetch"
	// RetryModeNone: nothing to do here. Reason says why, in the admin's language.
	RetryModeNone RetryMode = "none"
)

// Retry reasons. Rendered by the frontend through a copy table keyed on these, never
// by echoing a server string — the same posture as the connect-error banner.
const (
	RetryReasonAlreadyWritten = "already_written"
	RetryReasonIncomplete     = "incomplete"
	RetryReasonAlreadyDone    = "already_done"
	RetryReasonInFlight       = "in_flight"
	RetryReasonSyncChannel    = "sync_channel"
	RetryReasonWriteFailure   = "write_failure"
	// RetryReasonFormClosed is decided by the HANDLER, not by classifyRetry: it needs
	// a live lookup, not just the row.
	RetryReasonFormClosed = "form_closed"
)

// RetryPlan is the server's verdict on one delivery, shipped on the wire so the
// client renders a decision rather than making one.
type RetryPlan struct {
	Mode   RetryMode `json:"mode"`
	Reason string    `json:"reason,omitempty"`
}

// classifyRetry decides what may be done with a delivery. Ordered arms, first match
// wins, and the order is the safety argument.
func classifyRetry(ev *IntegrationEvent) RetryPlan {
	switch {
	// FIRST, before anything about status: a delivery that already produced a record
	// must never run again, whatever its row says. This is the guard the reaper also
	// leans on, and it is stated first so no later arm can be reached around it.
	case ev.ResultRecordID != nil:
		return RetryPlan{Mode: RetryModeNone, Reason: RetryReasonAlreadyWritten}

	// A terminal-success status with NO record is not a success — the codebase
	// already calls this a bookkeeping bug rather than something to confirm. It is
	// surfaced as its own reason so it reads as the lost lead it is, instead of
	// "nothing to retry".
	case ev.Status == EventStatusProcessed || ev.Status == EventStatusTest || ev.Status == EventStatusDuplicate:
		return RetryPlan{Mode: RetryModeNone, Reason: RetryReasonIncomplete}

	// In flight. Re-queueing races the worker that holds it.
	case ev.Status == EventStatusPending || ev.Status == EventStatusProcessing:
		return RetryPlan{Mode: RetryModeNone, Reason: RetryReasonInFlight}

	// Not a failure at all — nothing was attempted.
	case ev.Status != EventStatusFailed && ev.Status != EventStatusQuarantined:
		return RetryPlan{Mode: RetryModeNone, Reason: RetryReasonAlreadyDone}

	// No provider to re-fetch from. Sync deliveries recover by the sender re-sending
	// with the same Idempotency-Key, which re-enters Ingest's replay switch; flipping
	// one to `pending` here would hand it to a worker that immediately fails it with
	// "delivery has no connection".
	case ev.ConnectionID == nil:
		return RetryPlan{Mode: RetryModeNone, Reason: RetryReasonSyncChannel}

	// A source was already resolved for this delivery, so the failure happened INSIDE
	// the write — and this arm is doing two jobs at once, both load-bearing:
	//
	//  1. It excludes every BACKFILL row. An import stamps source_id at insert and
	//     carries a connection_id like any provider delivery, so it would otherwise
	//     qualify — and re-running one through the webhook door drops the bulk
	//     delivery mode that is the ONLY thing suppressing automation on a historical
	//     import. One click could enrol months of old leads into every
	//     contact_created workflow and mail all of them. (Structural rather than a
	//     marker on the row: rows written before any marker existed would be
	//     indistinguishable, and "we cannot tell" must not resolve to "send the
	//     emails".)
	//  2. What it excludes otherwise is a webhook delivery that failed during the
	//     write — and the worker has ALREADY retried those up to maxWebhookAttempts
	//     automatically, so by the time one reads `failed` the automatic budget is
	//     spent and a manual repeat of the same thing is not a remedy.
	//
	// What remains is exactly the class worth a button: deliveries that failed BEFORE
	// a source was resolved — fetch failures, unopenable credentials, and above all
	// the form-not-enabled quarantine, where the leads are recoverable the moment the
	// admin enables the form.
	case ev.SourceID != nil:
		return RetryPlan{Mode: RetryModeNone, Reason: RetryReasonWriteFailure}

	default:
		return RetryPlan{Mode: RetryModeRefetch}
	}
}

// eventView is the delivery as the API renders it: the model plus the server's retry
// verdict.
//
// Embedding rather than enumerating is deliberate. An enumerated projection would
// have to be kept in step with the model by hand, and the field most likely to be
// forgotten is `Consent` — which is `gorm:"-"` and filled by a SECOND query after
// the list read, so a projection built at the natural place (right after the repo
// call) would ship `consent: null` on every row and silently delete both the
// "recorded only, nothing enforces it" disclosure and the erasure tombstone. The
// model-as-DTO exposure it inherits is real and is handled the way this package
// already handles it: a new column stays off the struct.
type eventView struct {
	IntegrationEvent
	Retry RetryPlan `json:"retry"`
}

func viewOfEvent(ev IntegrationEvent) eventView {
	return eventView{IntegrationEvent: ev, Retry: classifyRetry(&ev)}
}

// eventPage is the org-wide ledger's envelope.
//
// NOTE the shape: `{"data": {"events": [...], "next_cursor": "..."}}`. The SHIPPED
// per-source route keeps its bare `{"data": [...]}` and is deliberately untouched —
// the frontend coerces any unexpected shape to an empty array rather than erroring,
// so repointing the existing log at this envelope would render a full ledger as
// "No deliveries yet" for anyone whose bundle and backend disagree by one deploy.
// An admin debugging a live source would conclude nothing had ever been sent.
type eventPage struct {
	Events     []eventView `json:"events"`
	NextCursor string      `json:"next_cursor,omitempty"`
	// Sources names the sources the page references, INCLUDING soft-deleted ones,
	// so a row is never labelled by a bare uuid.
	Sources map[string]eventSourceLabel `json:"sources"`
}

type eventSourceLabel struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Deleted bool   `json:"deleted"`
}

// eventCursor encodes a keyset position. Opaque to the client on purpose: it is a
// position, not a page number, and a client that could construct one would be able
// to make the two halves of the comparison disagree.
type eventCursor struct {
	At string    `json:"at"`
	ID uuid.UUID `json:"id"`
}
