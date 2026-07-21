package integrations

import (
	"context"
	"sync"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type fakeNotifier struct {
	mu   sync.Mutex
	sent []domain.NotificationCreateInput
	// suppress models a recipient whose preferences silence the row: Create returns
	// (nil, nil) — NOT an error — and a caller that checks only err nil-panics.
	suppress bool
}

func (f *fakeNotifier) Create(_ context.Context, in domain.NotificationCreateInput) (*domain.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, in)
	if f.suppress {
		return nil, nil
	}
	return &domain.Notification{OrgID: in.OrgID, UserID: in.UserID}, nil
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeNotifier) inputs() []domain.NotificationCreateInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.NotificationCreateInput, len(f.sent))
	copy(out, f.sent)
	return out
}

type fakeAudience struct{ users []uuid.UUID }

func (f *fakeAudience) IntegrationAdmins(context.Context, uuid.UUID) ([]uuid.UUID, error) {
	return f.users, nil
}

// runReporter builds a reporter, drains it synchronously, and returns once every
// enqueued event has been dispatched.
func runReporter(t *testing.T, n Notifier, a HealthAudience, m MemberChecker, fn func(*HealthReporter)) {
	t.Helper()
	r := NewHealthReporter(n, a, m, nil)
	require.NotNil(t, r)
	done := make(chan struct{})
	go func() { defer close(done); r.Start(context.Background()) }()
	fn(r)
	r.Stop()
	<-done
}

// TestHealthReporter_NilIsSafeEverywhere is the most important test in this file.
//
// The reporter is called from the lead-capture success and failure paths. If a nil
// receiver panicked, a workspace with notifications unwired would not merely lose
// alerting — every captured lead would take down the request that carried it. Nil is
// the state a deployment is in before this is configured, so it must be ordinary.
func TestHealthReporter_NilIsSafeEverywhere(t *testing.T) {
	var r *HealthReporter
	require.Nil(t, NewHealthReporter(nil, &fakeAudience{}, nil, nil), "no notifier means no reporter")
	require.Nil(t, NewHealthReporter(&fakeNotifier{}, nil, nil, nil), "no audience means no reporter")

	id, org := uuid.New(), uuid.New()
	require.NotPanics(t, func() {
		r.SourceFailing(org, id, "s", nil)
		r.SourceRecovered(org, id, "s", nil)
		r.SourceKeyMismatch(org, id, "s", nil)
		r.ConnectionDegraded(org, id, "p", nil)
		r.ConnectionError(org, id, "p", nil)
		r.ConnectionRecovered(org, id, "p", nil)
		r.Start(context.Background())
		r.Stop()
	})
}

// TestHealthReporter_SuppressedRecipientIsNotAnError pins the (nil, nil) contract.
func TestHealthReporter_SuppressedRecipientIsNotAnError(t *testing.T) {
	n := &fakeNotifier{suppress: true}
	admin := uuid.New()
	require.NotPanics(t, func() {
		runReporter(t, n, &fakeAudience{users: []uuid.UUID{admin}}, nil, func(r *HealthReporter) {
			r.SourceFailing(uuid.New(), uuid.New(), "Website form", nil)
		})
	})
	require.Equal(t, 1, n.count())
}

// TestHealthReporter_DedupesPerBandNotPerEntity — an escalation and a recovery for the
// same row must never suppress each other, or a source that breaks, recovers and
// breaks again inside the window reports only the first event.
func TestHealthReporter_DedupesPerBandNotPerEntity(t *testing.T) {
	n := &fakeNotifier{}
	org, src := uuid.New(), uuid.New()
	runReporter(t, n, &fakeAudience{users: []uuid.UUID{uuid.New()}}, nil, func(r *HealthReporter) {
		r.SourceFailing(org, src, "Website form", nil)
		r.SourceFailing(org, src, "Website form", nil) // same band: suppressed
		r.SourceRecovered(org, src, "Website form", nil)
	})
	require.Equal(t, 2, n.count(), "the repeat is suppressed; the different band is not")
}

func TestHealthDedupe_WindowExpires(t *testing.T) {
	d := newHealthDedupe()
	base := time.Now()
	require.True(t, d.claim("a|b", base, time.Minute))
	require.False(t, d.claim("a|b", base.Add(30*time.Second), time.Minute))
	require.True(t, d.claim("a|b", base.Add(2*time.Minute), time.Minute))
}

// TestHealthReporter_FleetBreakerStopsCorrelatedStorm.
//
// Every channel now counts an Ingest 5xx, so one RecordService or database fault is a
// fleet-wide event: every source in every org crosses the threshold at once. Without
// this the fan-out mails every admin on the platform a message blaming their own
// integration — while hammering the database that is already failing.
func TestHealthReporter_FleetBreakerStopsCorrelatedStorm(t *testing.T) {
	n := &fakeNotifier{}
	runReporter(t, n, &fakeAudience{users: []uuid.UUID{uuid.New()}}, nil, func(r *HealthReporter) {
		for i := 0; i < healthFleetOrgLimit+5; i++ {
			r.SourceFailing(uuid.New(), uuid.New(), "Website form", nil)
		}
	})
	require.Equal(t, healthFleetOrgLimit, n.count(),
		"beyond the fleet limit this is our outage, not the customer's configuration")
}

// TestHealthReporter_SameOrgIsNotRateLimitedByTheFleetBreaker — the breaker counts
// DISTINCT orgs. One org with several broken sources is a real local problem and must
// still be told about all of them.
func TestHealthReporter_SameOrgIsNotRateLimitedByTheFleetBreaker(t *testing.T) {
	n := &fakeNotifier{}
	org := uuid.New()
	runReporter(t, n, &fakeAudience{users: []uuid.UUID{uuid.New()}}, nil, func(r *HealthReporter) {
		for i := 0; i < healthFleetOrgLimit+5; i++ {
			r.SourceFailing(org, uuid.New(), "Website form", nil)
		}
	})
	require.Equal(t, healthFleetOrgLimit+5, n.count())
}

// TestHealthReporter_NotificationShape pins the three fields that are wrong by default.
func TestHealthReporter_NotificationShape(t *testing.T) {
	n := &fakeNotifier{}
	org, src := uuid.New(), uuid.New()
	runReporter(t, n, &fakeAudience{users: []uuid.UUID{uuid.New()}}, nil, func(r *HealthReporter) {
		r.SourceFailing(org, src, "Website form", nil)
	})
	in := n.inputs()[0]

	// An empty Type is silently coerced to "automation", which would file integration
	// health under the member's Workflow notifications toggle.
	require.Equal(t, NotifyTypeIntegrationHealth, in.Type)
	// The bell calls navigate(link) directly, so an absolute URL breaks in-app routing.
	require.Equal(t, "/settings/integrations/"+src.String(), in.Link)
	require.Equal(t, org, in.OrgID)
	require.Contains(t, in.Title, "Website form")
}

// TestSourceCopy_DoesNotClaimTheSourceStopped.
//
// `error` is a self-healing BADGE, not a gate: IsLive() stays true, the endpoint keeps
// accepting, and the next success un-flips it. Copy saying the source stopped receiving
// leads would be false, and it is the single most tempting sentence to write here.
func TestSourceCopy_DoesNotClaimTheSourceStopped(t *testing.T) {
	n := &fakeNotifier{}
	runReporter(t, n, &fakeAudience{users: []uuid.UUID{uuid.New()}}, nil, func(r *HealthReporter) {
		r.SourceFailing(uuid.New(), uuid.New(), "Website form", nil)
	})
	body := n.inputs()[0].Body
	for _, forbidden := range []string{"stopped receiving", "no longer accepting", "rejected", "lost"} {
		require.NotContains(t, body, forbidden,
			"a flagged source still accepts and still records every delivery")
	}
	require.Contains(t, body, "nothing is being discarded")
}

// TestConnectionCopy_KeepsOutageAndCredentialDeathApart.
//
// The degraded band exists precisely so a provider outage is not reported as a dead
// credential. If the degraded body told an admin to reconnect, the band would have been
// pointless — and they would redo OAuth to fix a Facebook rate limit.
func TestConnectionCopy_KeepsOutageAndCredentialDeathApart(t *testing.T) {
	n := &fakeNotifier{}
	runReporter(t, n, &fakeAudience{users: []uuid.UUID{uuid.New()}}, nil, func(r *HealthReporter) {
		r.ConnectionDegraded(uuid.New(), uuid.New(), "My Page", nil)
		r.ConnectionError(uuid.New(), uuid.New(), "My Page", nil)
	})
	got := n.inputs()
	degraded, errored := got[0], got[1]

	require.Contains(t, degraded.Body, "reconnecting will not help")
	require.NotContains(t, degraded.Title, "Reconnect")
	require.Contains(t, errored.Title, "Reconnect")

	// Connection-side failures leave integration_events.source_id NULL, and the only
	// events route filters on source_id — so that log structurally cannot contain them
	// until L6.3 adds a connection-scoped view. Pointing an admin at it would send them
	// to an empty table to look for the delivery they are missing.
	for _, in := range got {
		require.NotContains(t, in.Body, "delivery log")
	}
}

// TestKeyMismatchCopy_NamesBothCauses — this is the one alert a stranger can trigger,
// so it must not assert the innocent explanation as fact, and must say plainly that no
// leads were lost.
func TestKeyMismatchCopy_NamesBothCauses(t *testing.T) {
	n := &fakeNotifier{}
	runReporter(t, n, &fakeAudience{users: []uuid.UUID{uuid.New()}}, nil, func(r *HealthReporter) {
		r.SourceKeyMismatch(uuid.New(), uuid.New(), "Google Ads form", nil)
	})
	body := n.inputs()[0].Body
	require.Contains(t, body, "probing")
	require.Contains(t, body, "No leads were lost")
}
