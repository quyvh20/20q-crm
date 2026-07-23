package usecase

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The GDPR-erasure collapse: deleting a contact must collapse their marketing
// consent provenance, keyed on EMAIL (captured before the soft delete, since the
// state key is email, not id), following the same actually-deleted-set scope
// discipline as the ledger erasure. Best-effort: a collapse failure never fails
// the deletion.

func emailPtr(s string) *string { return &s }

type marketingCollapseFakeRepo struct {
	// Embedded nil: any unmodelled call panics loudly.
	domain.ContactRepository

	byID        map[uuid.UUID]*domain.Contact
	bulkDeleted []domain.Contact
}

func (f *marketingCollapseFakeRepo) GetByID(_ context.Context, _, id uuid.UUID) (*domain.Contact, error) {
	return f.byID[id], nil
}
func (f *marketingCollapseFakeRepo) SoftDelete(_ context.Context, _, _ uuid.UUID) error { return nil }
func (f *marketingCollapseFakeRepo) BulkDeleteByIDs(_ context.Context, _ uuid.UUID, _ []uuid.UUID) ([]domain.Contact, error) {
	return f.bulkDeleted, nil
}

type recordingMarketingRedactor struct {
	emails []string
	err    error
}

func (r *recordingMarketingRedactor) RedactMarketingStateForEmail(_ context.Context, _ uuid.UUID, email string) error {
	r.emails = append(r.emails, email)
	return r.err
}

func newCollapseUC(repo domain.ContactRepository, red MarketingStateRedactor) domain.ContactUseCase {
	uc := NewContactUseCase(repo, nil)
	if red != nil {
		uc.(interface {
			SetMarketingStateRedactor(MarketingStateRedactor)
		}).SetMarketingStateRedactor(red)
	}
	return uc
}

func TestDelete_CollapsesMarketingStateForTheContactEmail(t *testing.T) {
	id := uuid.New()
	repo := &marketingCollapseFakeRepo{byID: map[uuid.UUID]*domain.Contact{
		id: {ID: id, Email: emailPtr("Person@Example.com")},
	}}
	red := &recordingMarketingRedactor{}
	uc := newCollapseUC(repo, red)

	require.NoError(t, uc.Delete(context.Background(), uuid.New(), id))
	require.Equal(t, []string{"Person@Example.com"}, red.emails,
		"the raw email is passed; the marketing repo normalizes it")
}

func TestDelete_NoEmailNoCollapse(t *testing.T) {
	id := uuid.New()
	repo := &marketingCollapseFakeRepo{byID: map[uuid.UUID]*domain.Contact{
		id: {ID: id, Email: nil},
	}}
	red := &recordingMarketingRedactor{}
	uc := newCollapseUC(repo, red)

	require.NoError(t, uc.Delete(context.Background(), uuid.New(), id))
	require.Empty(t, red.emails, "a contact with no email has no marketing state to collapse")
}

func TestDelete_CollapseFailureDoesNotFailTheDeletion(t *testing.T) {
	id := uuid.New()
	repo := &marketingCollapseFakeRepo{byID: map[uuid.UUID]*domain.Contact{
		id: {ID: id, Email: emailPtr("a@b.com")},
	}}
	red := &recordingMarketingRedactor{err: context.DeadlineExceeded}
	uc := newCollapseUC(repo, red)

	require.NoError(t, uc.Delete(context.Background(), uuid.New(), id),
		"the contact is gone; a failed collapse must not report the deletion as failed")
}

func TestBulkDelete_CollapsesEveryDeletedEmailSkippingNil(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	repo := &marketingCollapseFakeRepo{bulkDeleted: []domain.Contact{
		{ID: a, Email: emailPtr("a@x.com")},
		{ID: b, Email: nil},               // no email → skipped
		{ID: c, Email: emailPtr("")},      // empty email → skipped
	}}
	red := &recordingMarketingRedactor{}
	uc := newCollapseUC(repo, red)

	_, err := uc.BulkAction(context.Background(), uuid.New(), domain.BulkActionInput{
		Action: "delete", ContactIDs: []uuid.UUID{a, b, c},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"a@x.com"}, red.emails)
}

func TestBulkDelete_CollapsesOnlyTheActuallyDeletedSet(t *testing.T) {
	mine, notMine := uuid.New(), uuid.New()
	// The write scope only let `mine` through, so only its email may be collapsed —
	// collapsing notMine's would touch a contact the caller could not delete.
	repo := &marketingCollapseFakeRepo{bulkDeleted: []domain.Contact{
		{ID: mine, Email: emailPtr("mine@x.com")},
	}}
	red := &recordingMarketingRedactor{}
	uc := newCollapseUC(repo, red)

	_, err := uc.BulkAction(context.Background(), uuid.New(), domain.BulkActionInput{
		Action: "delete", ContactIDs: []uuid.UUID{mine, notMine},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"mine@x.com"}, red.emails)
}
