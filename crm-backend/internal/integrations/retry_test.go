package integrations

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// classifyRetry is a pure function guarding a callerless contact write, so its arms
// are asserted individually rather than through the handler. The ORDER of the arms is
// as load-bearing as the arms themselves — a row can satisfy several at once, and
// which one wins decides whether a lead is written twice.

func evt(mut func(*IntegrationEvent)) *IntegrationEvent {
	conn := uuid.New()
	e := &IntegrationEvent{
		ID:           uuid.New(),
		OrgID:        uuid.New(),
		ConnectionID: &conn,
		Status:       EventStatusQuarantined,
	}
	mut(e)
	return e
}

// The anti-double-write guard must beat every other arm, including the ones that
// would otherwise say "retryable". A row that produced a record is never re-run.
func TestClassifyRetry_AlreadyWrittenBeatsEverythingElse(t *testing.T) {
	rec := uuid.New()
	for _, status := range []string{EventStatusFailed, EventStatusQuarantined, EventStatusProcessed} {
		plan := classifyRetry(evt(func(e *IntegrationEvent) {
			e.Status = status
			e.ResultRecordID = &rec // a record exists
		}))
		require.Equal(t, RetryModeNone, plan.Mode, "status %s", status)
		require.Equal(t, RetryReasonAlreadyWritten, plan.Reason,
			"a delivery that already wrote a record must never be retried, whatever its status says")
	}
}

// The flagship case: a Facebook lead quarantined because its form was not enabled.
// Pre-ingest, so source_id is NULL — and that is exactly what makes it retryable.
func TestClassifyRetry_PreIngestProviderFailureIsRetryable(t *testing.T) {
	for _, status := range []string{EventStatusFailed, EventStatusQuarantined} {
		plan := classifyRetry(evt(func(e *IntegrationEvent) {
			e.Status = status
			e.SourceID = nil
		}))
		require.Equal(t, RetryModeRefetch, plan.Mode, "status %s", status)
	}
}

// A sync delivery has no provider to fetch from. Flipping one to `pending` hands it
// to a worker that immediately fails it with "delivery has no connection", so the row
// would be destroyed by the button offered to save it.
func TestClassifyRetry_SyncRowsAreNotRetryable(t *testing.T) {
	plan := classifyRetry(evt(func(e *IntegrationEvent) {
		e.Status = EventStatusFailed
		e.ConnectionID = nil
		src := uuid.New()
		e.SourceID = &src
	}))
	require.Equal(t, RetryModeNone, plan.Mode)
	require.Equal(t, RetryReasonSyncChannel, plan.Reason)
}

// THE EMAIL-BLAST GUARD. A backfill row carries a connection_id like any provider
// delivery and would otherwise qualify — but re-running it through the webhook door
// drops the bulk delivery mode that is the only thing suppressing automation on a
// historical import. One click could enrol months of old leads into every
// contact_created workflow and mail all of them.
//
// The discriminator is structural (source_id set at insert) rather than a marker,
// because rows written before any marker existed would be indistinguishable — and
// "we cannot tell" must not resolve to "send the emails".
func TestClassifyRetry_BackfillRowsAreRefused(t *testing.T) {
	src := uuid.New()
	plan := classifyRetry(evt(func(e *IntegrationEvent) {
		e.Status = EventStatusFailed
		e.SourceID = &src // backfill stamps this at insert; a pre-ingest webhook row never has it
	}))
	require.Equal(t, RetryModeNone, plan.Mode)
	require.Equal(t, RetryReasonWriteFailure, plan.Reason)
}

// An in-flight row must not be re-queued: it races the worker holding it.
func TestClassifyRetry_InFlightRowsAreRefused(t *testing.T) {
	for _, status := range []string{EventStatusPending, EventStatusProcessing} {
		plan := classifyRetry(evt(func(e *IntegrationEvent) { e.Status = status }))
		require.Equal(t, RetryModeNone, plan.Mode)
		require.Equal(t, RetryReasonInFlight, plan.Reason, "status %s", status)
	}
}

// A terminal-success status with NO record is a LOST LEAD, and the codebase already
// treats that combination as a bookkeeping bug rather than a success to confirm.
// Reporting it as "nothing to retry" would file the one row worth investigating under
// the heading that means "all good".
func TestClassifyRetry_TerminalSuccessWithoutRecordReadsAsIncomplete(t *testing.T) {
	for _, status := range []string{EventStatusProcessed, EventStatusTest, EventStatusDuplicate} {
		plan := classifyRetry(evt(func(e *IntegrationEvent) {
			e.Status = status
			e.ResultRecordID = nil
		}))
		require.Equal(t, RetryModeNone, plan.Mode)
		require.Equal(t, RetryReasonIncomplete, plan.Reason, "status %s", status)
	}
}

// The status filter must not offer `duplicate`. It is declared and badged, but no
// code path writes it — so the option would always answer empty and teach an operator
// the log is broken at the moment they depend on it.
func TestEventStatusFilter_ExcludesTheStatusNothingWrites(t *testing.T) {
	require.False(t, eventStatusFilter[EventStatusDuplicate],
		"duplicate is never written; a filter offering it always returns zero rows")
	for _, s := range []string{
		EventStatusPending, EventStatusProcessing, EventStatusProcessed,
		EventStatusFailed, EventStatusQuarantined, EventStatusTest,
	} {
		require.True(t, eventStatusFilter[s], "status %s is written and must be filterable", s)
	}
}

// The cursor is opaque, but it must survive a round trip exactly — a lossy timestamp
// would make the row-value comparison skip or repeat rows silently between pages.
func TestEventCursor_RoundTripsExactly(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC().Truncate(time.Nanosecond)
	at, gotID, err := decodeEventCursor(encodeEventCursor(now, id))
	require.NoError(t, err)
	require.Equal(t, id, gotID)
	require.True(t, at.Equal(now), "a cursor that loses precision silently skips rows between pages")
}

func TestEventCursor_RejectsGarbage(t *testing.T) {
	for _, bad := range []string{"not-base64!!", "", "eyJib2d1cyI6MX0"} {
		_, _, err := decodeEventCursor(bad)
		require.Error(t, err, "cursor %q", bad)
	}
}
