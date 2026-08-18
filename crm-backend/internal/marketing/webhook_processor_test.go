package marketing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeConsumerStore struct {
	suppressions []*Suppression
	softInc      []string
	statusSet    []string
	paused       []uuid.UUID
	repended     int
	finished     []string // status values
	rates        DeliverabilityRates
	ratesErr     error
	addErr       error
	// Engagement-trigger support (arc G)
	campaignExists   bool
	campaignErr      error
	deliveredWithin  bool
	deliveredErr     error
	campaignQueries  int
	deliveredQueries int
	deferred         int
}

func (s *fakeConsumerStore) DeferEvent(_ context.Context, _, _ uuid.UUID, _ string) error {
	s.deferred++
	return nil
}

func (s *fakeConsumerStore) CampaignExists(_ context.Context, _, _ uuid.UUID) (bool, error) {
	s.campaignQueries++
	return s.campaignExists, s.campaignErr
}

func (s *fakeConsumerStore) HadDeliveredWithin(_ context.Context, _ uuid.UUID, _ string, _ uuid.UUID, _ time.Time, _ time.Duration) (bool, error) {
	s.deliveredQueries++
	return s.deliveredWithin, s.deliveredErr
}

func (f *fakeConsumerStore) ClaimPendingEvents(context.Context, int) ([]MarketingEmailEvent, error) {
	return nil, nil
}
func (f *fakeConsumerStore) RependEvent(context.Context, uuid.UUID, uuid.UUID, string) error {
	f.repended++
	return nil
}
func (f *fakeConsumerStore) FinishEvent(_ context.Context, _, _ uuid.UUID, status, _ string) error {
	f.finished = append(f.finished, status)
	return nil
}
func (f *fakeConsumerStore) AddSuppression(_ context.Context, s *Suppression) (bool, error) {
	if f.addErr != nil {
		return false, f.addErr
	}
	f.suppressions = append(f.suppressions, s)
	return true, nil
}
func (f *fakeConsumerStore) RecordSoftBounce(_ context.Context, _ uuid.UUID, email, _ string) (int, error) {
	f.softInc = append(f.softInc, email)
	return len(f.softInc), nil
}
func (f *fakeConsumerStore) SetMarketingStatus(_ context.Context, _ uuid.UUID, email, status string) error {
	f.statusSet = append(f.statusSet, email+"="+status)
	return nil
}
func (f *fakeConsumerStore) SetMarketingPaused(_ context.Context, orgID uuid.UUID, paused bool) error {
	if paused {
		f.paused = append(f.paused, orgID)
	}
	return nil
}
func (f *fakeConsumerStore) DeliverabilityRates(context.Context, uuid.UUID, time.Duration) (DeliverabilityRates, error) {
	return f.rates, f.ratesErr
}

func newProc(store resendConsumerStore) *ResendProcessor { return NewResendProcessor(store, nil) }

func evt(reason, email string) MarketingEmailEvent {
	return MarketingEmailEvent{ID: uuid.New(), OrgID: uuid.New(), EmailNormalized: email, Reason: reason}
}

func TestApply_Complaint(t *testing.T) {
	s := &fakeConsumerStore{}
	require.NoError(t, newProc(s).apply(context.Background(), evt(ReasonComplaint, "u@e.com")))
	require.Len(t, s.suppressions, 1)
	assert.Equal(t, ReasonComplaint, s.suppressions[0].Reason)
	assert.Empty(t, s.suppressions[0].Scope, "scope left empty so AddSuppression defaults it to 'all'")
}

func TestApply_HardBounce(t *testing.T) {
	s := &fakeConsumerStore{}
	require.NoError(t, newProc(s).apply(context.Background(), evt(ReasonHardBounce, "u@e.com")))
	require.Len(t, s.suppressions, 1)
	assert.Equal(t, ReasonHardBounce, s.suppressions[0].Reason)
}

func TestApply_SoftBounce(t *testing.T) {
	s := &fakeConsumerStore{}
	require.NoError(t, newProc(s).apply(context.Background(), evt(ReasonSoftBounce, "u@e.com")))
	assert.Empty(t, s.suppressions, "soft bounce must NOT write a direct suppression")
	require.Len(t, s.softInc, 1, "soft bounce must go through the accumulator")
	assert.Equal(t, "u@e.com", s.softInc[0])
}

func TestApply_Unsubscribe(t *testing.T) {
	s := &fakeConsumerStore{}
	require.NoError(t, newProc(s).apply(context.Background(), evt(ReasonUnsubscribe, "u@e.com")))
	require.Len(t, s.suppressions, 1)
	assert.Equal(t, ReasonUnsubscribe, s.suppressions[0].Reason)
	require.Len(t, s.statusSet, 1)
	assert.Equal(t, "u@e.com="+StatusUnsubscribed, s.statusSet[0])
}

func TestApply_DeliveredIsLedgerOnly(t *testing.T) {
	s := &fakeConsumerStore{}
	require.NoError(t, newProc(s).apply(context.Background(), evt("", "u@e.com")))
	assert.Empty(t, s.suppressions)
	assert.Empty(t, s.softInc)
	assert.Empty(t, s.statusSet)
}

func TestApply_SuppressionWithNoRecipient_NoOp(t *testing.T) {
	s := &fakeConsumerStore{}
	require.NoError(t, newProc(s).apply(context.Background(), evt(ReasonComplaint, "")))
	assert.Empty(t, s.suppressions, "a suppression event with no recipient is skipped, not retried forever")
}

func TestBreaker_AutoPauseAboveThresholdWithVolume(t *testing.T) {
	org := uuid.New()
	s := &fakeConsumerStore{rates: DeliverabilityRates{Delivered: 1000, ComplaintRate: 0.004}} // 0.4% > 0.3%
	newProc(s).checkBreaker(context.Background(), org)
	require.Len(t, s.paused, 1)
	assert.Equal(t, org, s.paused[0])
}

func TestBreaker_NoPauseBelowVolumeFloor(t *testing.T) {
	// 2 complaints / 3 delivered = 66% but only 3 delivered — must NOT pause.
	s := &fakeConsumerStore{rates: DeliverabilityRates{Delivered: 3, ComplaintRate: 0.66}}
	newProc(s).checkBreaker(context.Background(), uuid.New())
	assert.Empty(t, s.paused, "the minimum-volume floor must prevent a low-volume false pause")
}

func TestBreaker_WarnBandDoesNotPause(t *testing.T) {
	// 0.15% > warn(0.10%) but < pause(0.30%), volume above warn floor.
	s := &fakeConsumerStore{rates: DeliverabilityRates{Delivered: 1000, ComplaintRate: 0.0015}}
	newProc(s).checkBreaker(context.Background(), uuid.New())
	assert.Empty(t, s.paused, "the warn band warns but must not pause")
}

func TestBreaker_BounceLegPauses(t *testing.T) {
	s := &fakeConsumerStore{rates: DeliverabilityRates{Sent: 1000, BounceRate: 0.004}}
	newProc(s).checkBreaker(context.Background(), uuid.New())
	require.Len(t, s.paused, 1)
}

func TestProcess_TransientErrorRepends(t *testing.T) {
	s := &fakeConsumerStore{addErr: errors.New("db blip")}
	e := evt(ReasonComplaint, "u@e.com")
	e.Attempts = 1 // below max
	newProc(s).process(context.Background(), e)
	assert.Equal(t, 1, s.repended, "a transient error with retry budget must repend")
	assert.Empty(t, s.finished)
}

func TestProcess_ExhaustedBudgetFails(t *testing.T) {
	s := &fakeConsumerStore{addErr: errors.New("db blip")}
	e := evt(ReasonComplaint, "u@e.com")
	e.Attempts = resendMaxAttempts // at the cap
	newProc(s).process(context.Background(), e)
	assert.Equal(t, 0, s.repended)
	require.Len(t, s.finished, 1)
	assert.Equal(t, EventStatusFailed, s.finished[0])
}

func TestProcess_SuccessFinishesDone(t *testing.T) {
	s := &fakeConsumerStore{}
	newProc(s).process(context.Background(), evt("", "u@e.com"))
	require.Len(t, s.finished, 1)
	assert.Equal(t, EventStatusDone, s.finished[0])
}
