package integrations

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// The batch capture endpoint: up to 100 leads in one request, each with its own
// result.
//
// It exists because a provider's retry window can lapse while an integration is
// broken, and the leads that were dropped in the meantime have nowhere else to go.
// A CSV import would take them, but it bypasses mapping, attribution, owner routing
// and the delivery ledger — so what lands is not the lead that was sent. This path
// preserves all of it.
//
// The governing rule is that a batch of N costs exactly what N single requests cost
// on every existing bound. If it did not, shipping this endpoint would silently
// redefine every limit that protects the single one.

// Delivery modes. Set from the ROUTE (or the server-side backfill executor),
// never from a payload.
const (
	DeliveryDirect = ""
	DeliveryBatch  = "batch"
	// DeliveryBackfill is a provider historical import (L5.4). It shares batch's
	// BULK semantics — automation suppressed unless the admin opts in — because
	// importing 500 historical leads must not enrol 500 contacts into every
	// contact_created workflow. Kept distinct from DeliveryBatch so the ledger and
	// any future per-mode tuning can tell a public batch POST from a backfill.
	DeliveryBackfill = "backfill"
)

// isBulkDelivery reports whether a delivery mode carries bulk semantics — the ones
// that suppress automation by default and take the shorter per-item write budget.
func isBulkDelivery(mode string) bool {
	return mode == DeliveryBatch || mode == DeliveryBackfill
}

const (
	// maxBatchItems is the spec's ceiling, further clamped by the limiter's window:
	// admitting a batch larger than any window could ever charge would guarantee a
	// 429 after the caller had already paid to serialize it.
	maxBatchItems = 100
	// batchBodyLimit is larger than the single-lead cap because 100 leads legitimately
	// are — but still bounded, since this is a public write endpoint.
	batchBodyLimit = 1 << 20 // 1MB
	// maxItemKeys bounds one item's field count. A payload of ten thousand junk keys
	// is stored twice (raw_payload and quarantined_fields) and scanned by the mapping
	// UI's observed-keys query.
	maxItemKeys = 200

	// batchBudget is the wall clock for the whole request, checked only at item
	// boundaries so an in-flight write is never abandoned halfway.
	batchBudget = 40 * time.Second
	// batchLedgerTimeout is the REAL per-item ceiling: the ledger context is armed
	// first and every bookkeeping call hangs against it.
	batchLedgerTimeout = 15 * time.Second
	// batchWriteTimeout is deliberately well under the ledger's, so even a maximally
	// slow record write leaves reserve for the FinishEvent that records it. Inverting
	// these two makes post-write bookkeeping fail systematically and strands rows at
	// `processing`.
	batchWriteTimeout = 7 * time.Second

	// capRecheckInterval re-reads the daily cap mid-loop so two concurrent batches
	// converge on the true count instead of each spending the same headroom.
	capRecheckInterval = 25
)

// Item statuses.
const (
	ItemOK           = "ok"
	ItemDuplicate    = "duplicate"
	ItemError        = "error"
	ItemNotAttempted = "not_attempted"
	// ItemIndeterminate is the ONE status where a record may exist despite an error:
	// the write succeeded and the bookkeeping that records it did not. Never
	// auto-retryable — a blind retry would create the lead twice.
	ItemIndeterminate = "indeterminate"
)

// Machine-readable failure codes. An integrator branches on `retryable`; these say
// why.
const (
	CodeMissingKey       = "missing_idempotency_key"
	CodeDuplicateKey     = "duplicate_key_in_batch"
	CodeItemTooLarge     = "item_too_large"
	CodeTooManyKeys      = "too_many_keys"
	CodeNoFields         = "no_fields"
	CodeDailyCapReached  = "daily_cap_reached"
	CodeCapUnverified    = "cap_unverified"
	CodeBatchDeadline    = "batch_deadline"
	CodeClientDisconnect = "client_disconnected"
	CodeRejected         = "rejected"
	CodeInternal         = "internal_error"
)

type batchItem struct {
	Fields map[string]any `json:"fields"`
	// IdempotencyKey is REQUIRED, and enforced per item rather than per batch.
	//
	// Required because without it a retry is a duplicate factory on precisely the
	// shape L2 already ships: a phone-matching source whose number sits on several
	// contacts reports ambiguity, refuses to merge, and creates a new contact — which
	// raises the match count, so the next retry is ambiguous again. It never
	// converges, and the phone index cannot be UNIQUE, so nothing downstream catches
	// it.
	IdempotencyKey string         `json:"idempotency_key"`
	Context        map[string]any `json:"context"`
	Consent        map[string]any `json:"consent"`
}

type batchRequest struct {
	Items []batchItem `json:"items"`
}

// batchItemResult is one row's outcome. Every field exists so a retry loop can
// resend exactly the rows that did not land — the whole point of the endpoint.
type batchItemResult struct {
	Index          int    `json:"index"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	Status         string `json:"status"`
	RecordID       string `json:"record_id,omitempty"`
	EventID        string `json:"event_id,omitempty"`
	Outcome        string `json:"outcome,omitempty"`
	Code           string `json:"code,omitempty"`
	Message        string `json:"message,omitempty"`
	// Retryable is the ONLY field a retry loop should branch on.
	Retryable   bool     `json:"retryable,omitempty"`
	Quarantined []string `json:"quarantined,omitempty"`
	Note        string   `json:"note,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
	// DealID is the opportunity this row also produced, when the source asks for one.
	DealID string `json:"deal_id,omitempty"`
}

type batchResponse struct {
	// BatchID is server-generated and stamped on every delivery's context, so the
	// ledger can answer "show me that recovery run".
	BatchID  string            `json:"batch_id"`
	Received int               `json:"received"`
	Succeeded int              `json:"succeeded"`
	Failed   int               `json:"failed"`
	Results  []batchItemResult `json:"results"`
}

// prescan validates shape before anything is written, and resolves intra-batch key
// collisions.
//
// Everything here fails a single ITEM, never the batch: the spec is verbatim "one
// bad row never poisons the batch", and a whole-batch 4xx is terminal for Make and
// Zapier — one blank id in a provider export would mean the recovery run simply does
// not happen and the leads stay lost.
func prescan(items []batchItem) []batchItemResult {
	out := make([]batchItemResult, len(items))
	seen := map[string]int{}

	for i, it := range items {
		key := strings.TrimSpace(it.IdempotencyKey)
		out[i] = batchItemResult{Index: i, IdempotencyKey: key}

		switch {
		case key == "":
			out[i].Status, out[i].Code, out[i].Retryable = ItemNotAttempted, CodeMissingKey, false
			out[i].Message = "each item needs its own idempotency_key so a retry can tell this lead apart from the others"
		case len(it.Fields) == 0:
			out[i].Status, out[i].Code, out[i].Retryable = ItemNotAttempted, CodeNoFields, false
			out[i].Message = "fields is required"
		case len(it.Fields) > maxItemKeys:
			out[i].Status, out[i].Code, out[i].Retryable = ItemNotAttempted, CodeTooManyKeys, false
			out[i].Message = "this item carries more fields than a lead plausibly has"
		default:
			if first, dup := seen[key]; dup {
				// First wins. Retryable TRUE: this row provably had no side effect, and
				// the defect is in the caller's KEY SCHEME rather than in the lead —
				// telling a conforming retry loop to give up would discard a real person.
				out[i].Status, out[i].Code, out[i].Retryable = ItemNotAttempted, CodeDuplicateKey, true
				out[i].Message = "another item in this batch (index " + itoa(first) + ") already used this idempotency_key"
				continue
			}
			seen[key] = i
		}
	}
	return out
}

// pending reports whether prescan left this row to be ingested.
func pending(r batchItemResult) bool { return r.Status == "" }

// refuse marks a row the loop declined to attempt. It guarantees zero side effects,
// so the caller may resend it verbatim.
func refuse(r *batchItemResult, code, msg string) {
	r.Status, r.Code, r.Retryable, r.Message = ItemNotAttempted, code, true, msg
}

// applyResult folds a successful ingest into the row's result.
func applyResult(r *batchItemResult, res *IngestResult) {
	r.Status = ItemOK
	if res.Duplicate {
		r.Status = ItemDuplicate
	}
	r.RecordID = res.RecordID.String()
	r.EventID = res.EventID.String()
	r.Outcome = res.Outcome
	r.Quarantined = res.Quarantined
	r.Note = res.Note
	r.Warnings = res.Warnings
	if res.DealID != nil {
		r.DealID = res.DealID.String()
	}
}

// consumedHeadroom reports whether this delivery actually created a record, and so
// spent one of the source's daily allowance.
//
// The Duplicate check is the subtle half and the reason a naive version breaks
// exactly the recovery case. A replayed delivery returns the PRIOR outcome —
// literally "created" — while writing nothing at all, because the ledger insert hit
// its conflict clause. Counting it would make a retry batch spend its cap on rows
// it did not write and then refuse the genuinely-new leads in its tail, every time,
// until UTC midnight.
func consumedHeadroom(res *IngestResult) bool {
	return res != nil && !res.Duplicate && res.Outcome == OutcomeCreated
}

// batchContext builds the per-item context map carrying the batch id.
//
// A COPY of the caller's map, never the caller's own: mutating it would let a
// caller's batch_id survive into places we did not put it, and stamping our id onto
// their object makes the two indistinguishable in the ledger.
func batchContext(src map[string]any, batchID string) map[string]any {
	out := make(map[string]any, len(src)+1)
	for k, v := range src {
		out[k] = v
	}
	out["batch_id"] = batchID
	return out
}

// refusedEvent builds a ledger row for an item the batch declined to attempt.
//
// Refusals leave evidence on purpose. Without a row, the per-item envelope and the
// delivery log tell different stories, and "we never received it" becomes
// unanswerable.
func refusedEvent(source *LeadSource, item batchItem, r batchItemResult, batchID string) *IntegrationEvent {
	raw, _ := json.Marshal(item.Fields)
	ctxJSON, _ := json.Marshal(batchContext(item.Context, batchID))
	var providerID *string
	if r.IdempotencyKey != "" {
		k := r.IdempotencyKey
		providerID = &k
	}
	return &IntegrationEvent{
		OrgID:           source.OrgID,
		SourceID:        &source.ID,
		ProviderEventID: providerID,
		Status:          EventStatusQuarantined, // rejected before any write
		Attempts:        1,
		RawPayload:      datatypes.JSON(raw),
		Context:         datatypes.JSON(ctxJSON),
		Error:           r.Message,
	}
}

// newBatchID mints the id stamped on every delivery in this run.
func newBatchID() string { return "batch_" + uuid.NewString() }
