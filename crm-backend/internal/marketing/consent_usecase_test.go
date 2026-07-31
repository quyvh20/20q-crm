package marketing

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ── fakes ─────────────────────────────────────────────────────────────────────

type fakeConsentStore struct {
	granted     int64
	counts      GrantCounts
	grantCalls  int
	lastInput   GrantInput
	lastScope   GrantScope
	grantErr    error
}

func (f *fakeConsentStore) GrantLawfulBasis(_ context.Context, _ uuid.UUID, scope GrantScope, in GrantInput) (int64, error) {
	f.grantCalls++
	f.lastInput = in
	f.lastScope = scope
	return f.granted, f.grantErr
}

func (f *fakeConsentStore) PreviewLawfulBasisGrant(_ context.Context, _ uuid.UUID, _ GrantScope) (GrantCounts, error) {
	return f.counts, nil
}

type fakeSegments struct{ q *domain.AudienceQuery }

func (f *fakeSegments) AudienceQueryForSegments(_ context.Context, _ uuid.UUID, _, _ []uuid.UUID) (*domain.AudienceQuery, error) {
	if f.q == nil {
		return &domain.AudienceQuery{SelectSQL: "SELECT 1", Args: nil}, nil
	}
	return f.q, nil
}
func (f *fakeSegments) Get(_ context.Context, _, _ uuid.UUID) (*domain.Segment, error) { return nil, nil }

func newConsentUC(store *fakeConsentStore) *ConsentUseCase {
	uc := NewConsentUseCase(store, &fakeSegments{}, nil, nil)
	uc.now = func() time.Time { return time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC) }
	return uc
}

func codeOf(t *testing.T, err error) int {
	t.Helper()
	var appErr *domain.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected an AppError, got %v", err)
	}
	return appErr.Code
}

func validGrant() ConsentGrantInput {
	return ConsentGrantInput{
		Basis:      BasisExistingBusinessRelationship,
		Source:     "2024 customer import",
		SegmentIDs: []uuid.UUID{uuid.New()},
	}
}

// ── the rule that matters most ────────────────────────────────────────────────

// A basis HasLawfulBasis would reject must be refused outright. Accepting it would
// write a row, return 200, and leave every recipient just as unmailable — the exact
// silent-success failure this feature exists to end.
func TestGrant_RefusesBasesThatWouldNotMakeAnyoneMailable(t *testing.T) {
	for _, basis := range []string{BasisExpress, BasisDoubleOptIn} {
		t.Run(basis, func(t *testing.T) {
			store := &fakeConsentStore{}
			in := validGrant()
			in.Basis = basis

			_, err := newConsentUC(store).Grant(context.Background(), uuid.New(), uuid.New(), in)
			if err == nil {
				t.Fatal("want refusal")
			}
			if got := codeOf(t, err); got != http.StatusBadRequest {
				t.Fatalf("want 400, got %d", got)
			}
			if store.grantCalls != 0 {
				t.Fatal("nothing may be written for a basis that cannot take effect")
			}

			// Cross-check the refusal against the send-time gate, so the two cannot
			// drift: a granted basis at status 'pending' must actually be mailable.
			b := basis
			st := &ContactMarketingState{MarketingStatus: StatusPending, ConsentBasis: &b}
			if st.HasLawfulBasis(time.Now()) {
				t.Fatalf("%s IS mailable at pending — it should now be grantable", basis)
			}
		})
	}
}

// The mirror of the above: everything we DO allow must genuinely make an address
// mailable. If this fails, the grantable set and HasLawfulBasis have diverged.
func TestGrantableBasesAreActuallyMailable(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)
	for _, basis := range GrantableConsentBases() {
		b := basis
		st := &ContactMarketingState{MarketingStatus: StatusPending, ConsentBasis: &b}
		if CASLImpliedBasis(basis) {
			st.CASLExpiresAt = &future
		}
		if !st.HasLawfulBasis(time.Now()) {
			t.Fatalf("grantable basis %q does not satisfy HasLawfulBasis — granting it would be a no-op", basis)
		}
	}
}

// ── remaining validation ──────────────────────────────────────────────────────

func TestGrant_Validation(t *testing.T) {
	future := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		mut  func(*ConsentGrantInput)
		want int
	}{
		{"unknown basis", func(i *ConsentGrantInput) { i.Basis = "vibes" }, http.StatusBadRequest},
		{"missing source", func(i *ConsentGrantInput) { i.Source = "  " }, http.StatusBadRequest},
		{"no scope", func(i *ConsentGrantInput) { i.SegmentIDs = nil }, http.StatusBadRequest},
		{"both scopes", func(i *ConsentGrantInput) { i.ContactIDs = []uuid.UUID{uuid.New()} }, http.StatusBadRequest},
		{"casl basis without expiry", func(i *ConsentGrantInput) { i.Basis = BasisImpliedTransaction }, http.StatusBadRequest},
		{"casl expiry in the past", func(i *ConsentGrantInput) {
			i.Basis = BasisImpliedTransaction
			i.CASLExpiresAt = &past
		}, http.StatusBadRequest},
		{"expiry on a standing basis", func(i *ConsentGrantInput) { i.CASLExpiresAt = &future }, http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeConsentStore{}
			in := validGrant()
			tc.mut(&in)
			_, err := newConsentUC(store).Grant(context.Background(), uuid.New(), uuid.New(), in)
			if err == nil {
				t.Fatal("want rejection")
			}
			if got := codeOf(t, err); got != tc.want {
				t.Fatalf("want %d, got %d", tc.want, got)
			}
			if store.grantCalls != 0 {
				t.Fatal("a rejected request must not write")
			}
		})
	}
}

func TestGrant_AcceptsCASLBasisWithFutureExpiry(t *testing.T) {
	store := &fakeConsentStore{granted: 3, counts: GrantCounts{Candidates: 3}}
	future := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	in := validGrant()
	in.Basis = BasisImpliedTransaction
	in.CASLExpiresAt = &future

	got, err := newConsentUC(store).Grant(context.Background(), uuid.New(), uuid.New(), in)
	if err != nil {
		t.Fatalf("want success, got %v", err)
	}
	if got.Granted != 3 {
		t.Fatalf("want 3 granted, got %d", got.Granted)
	}
	if store.lastInput.CASLExpiresAt == nil || !store.lastInput.CASLExpiresAt.Equal(future) {
		t.Fatal("the expiry must reach the repository")
	}
}

// Consent is keyed per normalized email, and several contacts can share one
// address, so a row-scoped caller would still be writing rows governing contacts
// outside their scope. Refusing is the only coherent answer.
func TestGrant_RefusesRowScopedCaller(t *testing.T) {
	store := &fakeConsentStore{}
	ctx := domain.WithCallerIdentity(context.Background(), domain.Caller{
		UserID: uuid.New(), DataScope: domain.DataScopeOwn,
	})

	_, err := newConsentUC(store).Grant(ctx, uuid.New(), uuid.New(), validGrant())
	if err == nil {
		t.Fatal("want refusal")
	}
	if got := codeOf(t, err); got != http.StatusForbidden {
		t.Fatalf("want 403, got %d", got)
	}
	if store.grantCalls != 0 {
		t.Fatal("nothing may be written for a row-scoped caller")
	}
}

func TestGrant_AllowsOrgWideAndCallerlessCallers(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  func() context.Context
	}{
		{"all-scope", func() context.Context {
			return domain.WithCallerIdentity(context.Background(), domain.Caller{UserID: uuid.New(), DataScope: domain.DataScopeAll})
		}},
		{"owner", func() context.Context {
			return domain.WithCallerIdentity(context.Background(), domain.Caller{UserID: uuid.New(), IsOwner: true})
		}},
		{"callerless", context.Background},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeConsentStore{granted: 1, counts: GrantCounts{Candidates: 1}}
			if _, err := newConsentUC(store).Grant(tc.ctx(), uuid.New(), uuid.New(), validGrant()); err != nil {
				t.Fatalf("want success, got %v", err)
			}
		})
	}
}

func TestGrant_CapsExplicitContactList(t *testing.T) {
	store := &fakeConsentStore{}
	in := validGrant()
	in.SegmentIDs = nil
	in.ContactIDs = make([]uuid.UUID, maxGrantContactIDs+1)
	for i := range in.ContactIDs {
		in.ContactIDs[i] = uuid.New()
	}

	_, err := newConsentUC(store).Grant(context.Background(), uuid.New(), uuid.New(), in)
	if err == nil {
		t.Fatal("want rejection over the cap")
	}
	if got := codeOf(t, err); got != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", got)
	}
}

// The reported count must be what the DATABASE wrote, never the candidate
// estimate. A 200 reporting a number nothing wrote is the whole class of bug here.
func TestGrant_ReportsRowsActuallyWritten(t *testing.T) {
	store := &fakeConsentStore{
		granted: 4,
		counts:  GrantCounts{Candidates: 10, Suppressed: 2},
	}

	got, err := newConsentUC(store).Grant(context.Background(), uuid.New(), uuid.New(), validGrant())
	if err != nil {
		t.Fatalf("want success, got %v", err)
	}
	if got.Granted != 4 {
		t.Fatalf("Granted must come from RowsAffected: want 4, got %d", got.Granted)
	}
	if got.Candidates != 10 {
		t.Fatalf("want 10 candidates, got %d", got.Candidates)
	}
	if got.Skipped != 6 {
		t.Fatalf("Skipped must be candidates-granted: want 6, got %d", got.Skipped)
	}
	if got.Suppressed != 2 {
		t.Fatalf("want 2 suppressed surfaced, got %d", got.Suppressed)
	}
}

func TestGrant_PropagatesRepositoryFailure(t *testing.T) {
	store := &fakeConsentStore{grantErr: errors.New("boom")}
	if _, err := newConsentUC(store).Grant(context.Background(), uuid.New(), uuid.New(), validGrant()); err == nil {
		t.Fatal("a failed write must not report success")
	}
}

func TestPreview_WritesNothing(t *testing.T) {
	store := &fakeConsentStore{counts: GrantCounts{Candidates: 7, Suppressed: 1}}
	got, err := newConsentUC(store).Preview(context.Background(), uuid.New(), validGrant())
	if err != nil {
		t.Fatalf("want success, got %v", err)
	}
	if store.grantCalls != 0 {
		t.Fatal("preview must not write")
	}
	if got.Candidates != 7 || got.Granted != 0 {
		t.Fatalf("preview must report candidates and no grants, got %+v", got)
	}
}
