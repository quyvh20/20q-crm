package integrations

import (
	"context"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// The batch endpoint's promise is "retry exactly the failed rows". These tests pin
// the properties that promise depends on.

func items(keys ...string) []batchItem {
	out := make([]batchItem, len(keys))
	for i, k := range keys {
		out[i] = batchItem{
			IdempotencyKey: k,
			Fields:         map[string]any{"email": "lead" + itoa(i) + "@customer-example.com"},
		}
	}
	return out
}

// TestPrescan_ReturnsOneResultPerItem is the plan's literal Done-when: a row that
// vanishes from the envelope is a lead the integrator will never know to resend.
func TestPrescan_ReturnsOneResultPerItem(t *testing.T) {
	got := prescan(items("a", "b", "", "d"))
	if len(got) != 4 {
		t.Fatalf("prescan returned %d results for 4 items", len(got))
	}
	for i, r := range got {
		if r.Index != i {
			t.Errorf("result %d carries index %d — the caller matches rows by index", i, r.Index)
		}
	}
}

// TestPrescan_RequiresAPerItemKey. Without one, a retry cannot tell this lead from
// any other — and on a phone-matching source that is a self-perpetuating duplicate
// factory, because each new duplicate raises the match count and makes the next
// retry ambiguous again.
func TestPrescan_RequiresAPerItemKey(t *testing.T) {
	got := prescan(items("a", ""))

	if got[1].Status != ItemNotAttempted || got[1].Code != CodeMissingKey {
		t.Errorf("a keyless item must be refused before any write: %+v", got[1])
	}
	if got[0].Status != "" {
		t.Error("one bad row must not poison the batch — item 0 should still be pending")
	}
}

// TestPrescan_IntraBatchDuplicateKeys is the trap the ledger cannot catch for us.
//
// Today two items sharing a key would silently lose the second person: the second
// insert conflicts, the replay switch finds the FIRST delivery and hands back its
// record id, and the second payload is never mapped, matched or written. Catching it
// here is what stops the envelope from reporting a contact that is somebody else.
func TestPrescan_IntraBatchDuplicateKeys(t *testing.T) {
	got := prescan(items("dup", "dup"))

	if got[0].Status != "" {
		t.Error("the first use of a key must proceed")
	}
	if got[1].Status != ItemNotAttempted || got[1].Code != CodeDuplicateKey {
		t.Fatalf("the second use must be refused, got %+v", got[1])
	}
	if !got[1].Retryable {
		t.Error("retryable: the row provably had no side effect, and the defect is in the caller's key scheme, not the lead")
	}
	if got[1].RecordID != "" {
		t.Error("a refused duplicate must NEVER carry item 0's record id — that is a different person")
	}
}

// TestPrescan_TrimsKeys: " k" and "k" are the same delivery to the dedupe index, so
// they must collide here too rather than slipping past into the DB.
func TestPrescan_TrimsKeys(t *testing.T) {
	got := prescan([]batchItem{
		{IdempotencyKey: "k", Fields: map[string]any{"email": "a@x.invalid"}},
		{IdempotencyKey: " k ", Fields: map[string]any{"email": "b@x.invalid"}},
	})
	if got[1].Code != CodeDuplicateKey {
		t.Errorf("whitespace variants must collide, got %+v", got[1])
	}
	if got[1].IdempotencyKey != "k" {
		t.Errorf("the echoed key must be the trimmed one the index sees, got %q", got[1].IdempotencyKey)
	}
}

// TestPrescan_RejectsShapeProblemsPerItem — never as a whole-batch failure. A 4xx is
// terminal for Make and Zapier, so one blank row in a provider export would mean the
// recovery run simply does not happen and the leads stay lost.
func TestPrescan_RejectsShapeProblemsPerItem(t *testing.T) {
	huge := map[string]any{}
	for i := 0; i < maxItemKeys+1; i++ {
		huge["k"+itoa(i)] = "v"
	}
	got := prescan([]batchItem{
		{IdempotencyKey: "ok", Fields: map[string]any{"email": "a@x.invalid"}},
		{IdempotencyKey: "empty", Fields: map[string]any{}},
		{IdempotencyKey: "huge", Fields: huge},
	})

	if got[0].Status != "" {
		t.Error("the good row must survive its neighbours")
	}
	if got[1].Code != CodeNoFields || got[2].Code != CodeTooManyKeys {
		t.Errorf("shape problems must be per-item: %+v %+v", got[1], got[2])
	}
}

// TestConsumedHeadroom_IgnoresReplays is the sharpest bug this endpoint can have, and
// it only bites on the second run — which is exactly the recovery shape.
//
// A replayed delivery returns the PRIOR outcome, literally "created", while writing
// nothing (the ledger insert hit its conflict clause). Counting that against the
// daily cap would make a retry batch spend its allowance on rows it did not write and
// then refuse the genuinely-new leads in its tail, every time, until UTC midnight.
func TestConsumedHeadroom_IgnoresReplays(t *testing.T) {
	created := &IngestResult{Outcome: OutcomeCreated}
	replayed := &IngestResult{Outcome: OutcomeCreated, Duplicate: true}
	updated := &IngestResult{Outcome: OutcomeUpdated}

	if !consumedHeadroom(created) {
		t.Error("a real create must spend headroom")
	}
	if consumedHeadroom(replayed) {
		t.Fatal("a REPLAY wrote nothing — counting it makes a retry batch starve its own tail")
	}
	if consumedHeadroom(updated) {
		t.Error("an update creates no record, so it spends no creation headroom")
	}
	if consumedHeadroom(nil) {
		t.Error("no result means no write")
	}
}

// TestBatchContext_CopiesTheCallersMap. Stamping our batch_id into the caller's own
// map would mutate a value they still own, and make their context indistinguishable
// from ours in the ledger.
func TestBatchContext_CopiesTheCallersMap(t *testing.T) {
	caller := map[string]any{"page_url": "https://example.com/x"}
	got := batchContext(caller, "batch_123")

	if got["batch_id"] != "batch_123" || got["page_url"] != "https://example.com/x" {
		t.Errorf("the batch id must ride alongside the caller's own context: %v", got)
	}
	if _, mutated := caller["batch_id"]; mutated {
		t.Error("the caller's map must not be mutated")
	}
}

// TestRefusedEvent_LeavesEvidence: a refusal with no ledger row means the envelope
// and the delivery log tell different stories, and "we never received it" becomes
// unanswerable.
func TestRefusedEvent_LeavesEvidence(t *testing.T) {
	src := testSource(t, "", "")
	r := batchItemResult{Index: 0, IdempotencyKey: "k", Message: "ran out of time"}
	ev := refusedEvent(src, items("k")[0], r, "batch_1")

	if ev.Status != EventStatusQuarantined {
		t.Errorf("a refused item is rejected BEFORE any write: status = %s", ev.Status)
	}
	if ev.ProviderEventID == nil || *ev.ProviderEventID != "k" {
		t.Error("the ledger row must carry the key, so a resend of the same key is recognised")
	}
	if ev.Error == "" || !strings.Contains(string(ev.Context), "batch_1") {
		t.Errorf("the row must say why, and which run: %+v", ev)
	}
	if ev.SourceID == nil || *ev.SourceID != src.ID {
		t.Error("the row must be attributed to its source")
	}
}

// TestApplyResult_MarksReplaysAsDuplicates so an integrator can tell a row that
// landed now from one that had already landed.
func TestApplyResult_MarksReplaysAsDuplicates(t *testing.T) {
	id, ev := uuid.New(), uuid.New()

	var ok batchItemResult
	applyResult(&ok, &IngestResult{RecordID: id, EventID: ev, Outcome: OutcomeCreated})
	if ok.Status != ItemOK || ok.RecordID != id.String() {
		t.Errorf("a fresh create must report ok with its record: %+v", ok)
	}

	var dup batchItemResult
	applyResult(&dup, &IngestResult{RecordID: id, EventID: ev, Outcome: OutcomeCreated, Duplicate: true})
	if dup.Status != ItemDuplicate {
		t.Errorf("a replay must report duplicate, not ok: %+v", dup)
	}
}

// TestBatchTimeouts_LedgerOutlivesTheWrite pins the ordering the bookkeeping depends
// on. The ledger context is armed FIRST and every ledger call hangs against it, so if
// it expired before the write's, a slow write would make post-write bookkeeping fail
// systematically — stranding rows at `processing`, the exact state the reaper exists
// to clean up.
func TestBatchTimeouts_LedgerOutlivesTheWrite(t *testing.T) {
	if batchLedgerTimeout <= batchWriteTimeout {
		t.Fatalf("ledger timeout (%s) must exceed the write timeout (%s), or FinishEvent loses the race it must win",
			batchLedgerTimeout, batchWriteTimeout)
	}
	if batchBudget <= batchLedgerTimeout {
		t.Error("the whole-batch budget must exceed one item's ceiling")
	}
}

// TestBatchDeliveryIsSuppressedUnlessOptedIn: 100 recovered leads must not enrol 100
// contacts into every contact_created workflow. The unsuppressed control is the
// load-bearing half — a positive-only assertion cannot catch an always-on flag.
func TestBatchDeliveryIsSuppressedUnlessOptedIn(t *testing.T) {
	src := testSource(t, "", "")

	off, cancelOff := newIngestContext(src, RawLead{DeliveryMode: DeliveryBatch}, batchWriteTimeout)
	defer cancelOff()
	if !isSuppressed(off) {
		t.Error("a batch delivery must not enrol workflows unless the source opted in")
	}

	on, cancelOn := newIngestContext(src, RawLead{DeliveryMode: DeliveryBatch, EnrollAutomation: true}, batchWriteTimeout)
	defer cancelOn()
	if isSuppressed(on) {
		t.Error("an opted-in source MUST enrol — otherwise the toggle is decorative")
	}

	direct, cancelDirect := newIngestContext(src, RawLead{}, ingestTimeout)
	defer cancelDirect()
	if isSuppressed(direct) {
		t.Error("the direct capture endpoint must be untouched by this change")
	}
}

// isSuppressed is a test-local reader so the assertion names the property rather
// than the plumbing.
func isSuppressed(ctx context.Context) bool { return domain.IsAutomationSuppressed(ctx) }
