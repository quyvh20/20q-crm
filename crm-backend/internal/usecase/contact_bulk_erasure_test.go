package usecase

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The bulk delete never erased the lead ledger. Single-contact delete has redacted
// since L2.7, so a customer honouring a data-protection request one person at a time
// was covered and the same customer honouring it over a LIST was not — the raw
// payload, the capture context and the consent envelope all survived, for every
// subject in the request.
//
// The sharp part is WHICH ids get erased. BulkDeleteByIDs carries a row-level write
// scope, so an own-scoped caller's request silently skips records they do not own;
// keying erasure off the REQUESTED ids would let any member with bulk access destroy
// ledger evidence for contacts they were never allowed to touch.

type bulkDeleteFakeRepo struct {
	// Embedded nil: any unmodelled call panics loudly rather than quietly returning a
	// zero value and weakening the test.
	domain.ContactRepository

	requested []uuid.UUID
	// deleted models what the write scope actually let through.
	deleted []uuid.UUID
}

func (f *bulkDeleteFakeRepo) BulkDeleteByIDs(_ context.Context, _ uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	f.requested = ids
	return f.deleted, nil
}

type recordingRedactor struct {
	single []uuid.UUID
	batch  [][]uuid.UUID
	err    error
}

func (r *recordingRedactor) RedactForRecord(_ context.Context, _, recordID uuid.UUID) error {
	r.single = append(r.single, recordID)
	return r.err
}

func (r *recordingRedactor) RedactForRecords(_ context.Context, _ uuid.UUID, ids []uuid.UUID) error {
	r.batch = append(r.batch, ids)
	return r.err
}

func newBulkUC(repo domain.ContactRepository, red LeadLedgerRedactor) domain.ContactUseCase {
	uc := NewContactUseCase(repo, nil)
	if red != nil {
		uc.(interface{ SetLeadLedgerRedactor(LeadLedgerRedactor) }).SetLeadLedgerRedactor(red)
	}
	return uc
}

func TestBulkDelete_ErasesTheLedgerForEveryDeletedContact(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	repo := &bulkDeleteFakeRepo{deleted: []uuid.UUID{a, b}}
	red := &recordingRedactor{}
	uc := newBulkUC(repo, red)

	res, err := uc.BulkAction(context.Background(), uuid.New(), domain.BulkActionInput{
		Action: "delete", ContactIDs: []uuid.UUID{a, b},
	})
	require.NoError(t, err)
	require.Equal(t, 2, res.Affected)

	require.Len(t, red.batch, 1, "one statement, not one round trip per contact")
	require.ElementsMatch(t, []uuid.UUID{a, b}, red.batch[0])
}

// THE GUARD. An own-scoped rep asks to delete three contacts and owns one. The write
// scope deletes that one; erasure must follow the scope, not the request.
func TestBulkDelete_ErasesOnlyWhatTheWriteScopeActuallyDeleted(t *testing.T) {
	mine, notMine1, notMine2 := uuid.New(), uuid.New(), uuid.New()
	repo := &bulkDeleteFakeRepo{deleted: []uuid.UUID{mine}}
	red := &recordingRedactor{}
	uc := newBulkUC(repo, red)

	res, err := uc.BulkAction(context.Background(), uuid.New(), domain.BulkActionInput{
		Action: "delete", ContactIDs: []uuid.UUID{mine, notMine1, notMine2},
	})
	require.NoError(t, err)
	require.Equal(t, 1, res.Affected, "the count must report what was deleted, not what was asked for")

	require.Len(t, red.batch, 1)
	require.Equal(t, []uuid.UUID{mine}, red.batch[0],
		"erasing a contact the caller could not delete would destroy another rep's ledger evidence")
	require.NotContains(t, red.batch[0], notMine1)
	require.NotContains(t, red.batch[0], notMine2)
}

// Nothing deleted, nothing erased — and in particular no call with an empty slice,
// which downstream would be an `IN ()` syntax error.
func TestBulkDelete_DeletingNothingErasesNothing(t *testing.T) {
	repo := &bulkDeleteFakeRepo{deleted: nil}
	red := &recordingRedactor{}
	uc := newBulkUC(repo, red)

	res, err := uc.BulkAction(context.Background(), uuid.New(), domain.BulkActionInput{
		Action: "delete", ContactIDs: []uuid.UUID{uuid.New()},
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.Affected)
	require.Empty(t, red.batch)
}

// Best-effort, matching the single-delete path: the contacts ARE gone, so reporting
// the deletion as failed would be the one answer that makes a deletion request
// impossible to honour at all.
func TestBulkDelete_RedactionFailureDoesNotFailTheDeletion(t *testing.T) {
	a := uuid.New()
	repo := &bulkDeleteFakeRepo{deleted: []uuid.UUID{a}}
	red := &recordingRedactor{err: context.DeadlineExceeded}
	uc := newBulkUC(repo, red)

	res, err := uc.BulkAction(context.Background(), uuid.New(), domain.BulkActionInput{
		Action: "delete", ContactIDs: []uuid.UUID{a},
	})
	require.NoError(t, err)
	require.Equal(t, 1, res.Affected)
}

// A build with no integrations module wires no redactor; bulk delete must still work.
func TestBulkDelete_WorksWithNoRedactorWired(t *testing.T) {
	a := uuid.New()
	repo := &bulkDeleteFakeRepo{deleted: []uuid.UUID{a}}
	uc := newBulkUC(repo, nil)

	res, err := uc.BulkAction(context.Background(), uuid.New(), domain.BulkActionInput{
		Action: "delete", ContactIDs: []uuid.UUID{a},
	})
	require.NoError(t, err)
	require.Equal(t, 1, res.Affected)
}
