package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

var errTestSend = errors.New("send failed")

// --- fakes for the U5 preference / email / digest paths ---

func prefKey(org, user uuid.UUID) string { return org.String() + "|" + user.String() }

type fakePrefRepo struct {
	prefs  map[string]*domain.NotificationPreference
	marked []uuid.UUID
}

func newFakePrefRepo() *fakePrefRepo {
	return &fakePrefRepo{prefs: map[string]*domain.NotificationPreference{}}
}
func (f *fakePrefRepo) put(p *domain.NotificationPreference) {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	f.prefs[prefKey(p.OrgID, p.UserID)] = p
}
func (f *fakePrefRepo) Get(_ context.Context, org, user uuid.UUID) (*domain.NotificationPreference, error) {
	return f.prefs[prefKey(org, user)], nil
}
func (f *fakePrefRepo) Upsert(_ context.Context, p *domain.NotificationPreference) error {
	f.put(p)
	return nil
}
// ListDailyDigestDue returns all daily, non-muted prefs as CANDIDATES; the atomic
// TryClaimDailyDigest below is the authoritative due gate (mirrors the real design,
// where the SELECT is a coarse candidate filter and the CAS claim decides).
func (f *fakePrefRepo) ListDailyDigestDue(_ context.Context, _ time.Time) ([]domain.NotificationPreference, error) {
	var out []domain.NotificationPreference
	for _, p := range f.prefs {
		if p.EmailDigest == domain.DigestDaily && !p.MuteAll {
			out = append(out, *p)
		}
	}
	return out, nil
}
func (f *fakePrefRepo) TryClaimDailyDigest(_ context.Context, id uuid.UUID, sentBefore, at time.Time) (bool, error) {
	for _, p := range f.prefs {
		if p.ID == id {
			if p.LastDigestSentAt == nil || p.LastDigestSentAt.Before(sentBefore) {
				t := at
				p.LastDigestSentAt = &t
				f.marked = append(f.marked, id)
				return true, nil
			}
			return false, nil // already claimed by another pass
		}
	}
	return false, nil
}

type fakeNotifUsers struct{ byID map[uuid.UUID]*domain.User }

func (f fakeNotifUsers) GetUserByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	return f.byID[id], nil
}

type recordingMailer struct {
	mu      sync.Mutex
	fail    bool     // when true, SendNotificationDigest returns an error
	notes   []string // recipients of immediate notification emails
	digests []int    // per-digest item counts
}

func (m *recordingMailer) SendInvite(context.Context, string, string, string, string) error { return nil }
func (m *recordingMailer) SendPasswordReset(context.Context, string, string) error          { return nil }
func (m *recordingMailer) SendVerification(context.Context, string, string) error           { return nil }
func (m *recordingMailer) SendSecurityAlert(context.Context, string, string, string) error  { return nil }
func (m *recordingMailer) SendNotification(_ context.Context, to, _, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notes = append(m.notes, to)
	return nil
}
func (m *recordingMailer) SendNotificationDigest(_ context.Context, _ string, items []domain.NotificationDigestItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errTestSend
	}
	m.digests = append(m.digests, len(items))
	return nil
}
func (m *recordingMailer) noteCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.notes)
}

// digestNotifRepo lets a test set the exact rows ListPendingDigest returns and
// records which rows were marked digested.
type digestNotifRepo struct {
	fakeNotificationRepo
	pending  []domain.Notification
	digested []uuid.UUID
}

func (r *digestNotifRepo) ListPendingDigest(context.Context, uuid.UUID, uuid.UUID, time.Time, int) ([]domain.Notification, error) {
	return r.pending, nil
}
func (r *digestNotifRepo) MarkNotificationsDigested(_ context.Context, ids []uuid.UUID, _ time.Time) error {
	r.digested = append(r.digested, ids...)
	return nil
}

func overridePref(org, user uuid.UUID, muteAll bool, digest string, key string, ch domain.ChannelPref) *domain.NotificationPreference {
	return &domain.NotificationPreference{
		ID: uuid.New(), OrgID: org, UserID: user, MuteAll: muteAll, EmailDigest: digest,
		Overrides: map[string]domain.ChannelPref{key: ch},
	}
}

func waitForCount(t *testing.T, fn func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := fn(); got != want {
		t.Fatalf("expected count %d, got %d after waiting", want, got)
	}
}

// --- Create gating ---

func TestCreate_InAppOff_SuppressesRow(t *testing.T) {
	repo := &fakeNotificationRepo{}
	prefs := newFakePrefRepo()
	org, user := uuid.New(), uuid.New()
	prefs.put(overridePref(org, user, false, domain.DigestOff, "automation", domain.ChannelPref{InApp: false, Email: false}))
	uc := NewNotificationUseCase(repo, prefs, nil, nil, nil, "")

	n, err := uc.Create(context.Background(), domain.NotificationCreateInput{OrgID: org, UserID: user, Title: "hi"})
	require.NoError(t, err)
	require.Nil(t, n, "in-app off must suppress the bell row (nil, no error)")
	require.Len(t, repo.created, 0, "no row should be stored")
}

func TestCreate_MuteAll_SuppressesEverything(t *testing.T) {
	repo := &fakeNotificationRepo{}
	prefs := newFakePrefRepo()
	mailer := &recordingMailer{}
	org, user := uuid.New(), uuid.New()
	// email on for the type, but mute_all wins.
	p := overridePref(org, user, true, domain.DigestOff, "automation", domain.ChannelPref{InApp: true, Email: true})
	prefs.put(p)
	users := fakeNotifUsers{byID: map[uuid.UUID]*domain.User{user: {ID: user, Email: "a@x.com"}}}
	uc := NewNotificationUseCase(repo, prefs, users, mailer, nil, "")

	n, err := uc.Create(context.Background(), domain.NotificationCreateInput{OrgID: org, UserID: user, Title: "hi"})
	require.NoError(t, err)
	require.Nil(t, n)
	require.Len(t, repo.created, 0)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 0, mailer.noteCount(), "mute-all must suppress email too")
}

func TestCreate_EmailImmediate_WhenEnabled(t *testing.T) {
	repo := &fakeNotificationRepo{}
	prefs := newFakePrefRepo()
	mailer := &recordingMailer{}
	org, user := uuid.New(), uuid.New()
	prefs.put(overridePref(org, user, false, domain.DigestOff, "automation", domain.ChannelPref{InApp: true, Email: true}))
	users := fakeNotifUsers{byID: map[uuid.UUID]*domain.User{user: {ID: user, Email: "a@x.com"}}}
	uc := NewNotificationUseCase(repo, prefs, users, mailer, nil, "https://app.example")

	n, err := uc.Create(context.Background(), domain.NotificationCreateInput{OrgID: org, UserID: user, Title: "hi", Link: "/deals/1"})
	require.NoError(t, err)
	require.NotNil(t, n, "in-app on → the bell row is still stored")
	require.Len(t, repo.created, 1)
	waitForCount(t, mailer.noteCount, 1) // immediate email fired (async)
}

// Review HIGH fix: in-app OFF + email ON + digest='daily' must NOT silently drop the
// notification. The row is stored (in_app=false, hidden from the bell) so the daily
// digest has something to summarize, and no immediate email is sent.
func TestCreate_EmailOnlyDigest_StoresDigestRowHiddenFromBell(t *testing.T) {
	repo := &fakeNotificationRepo{}
	prefs := newFakePrefRepo()
	mailer := &recordingMailer{}
	org, user := uuid.New(), uuid.New()
	prefs.put(overridePref(org, user, false, domain.DigestDaily, "automation", domain.ChannelPref{InApp: false, Email: true}))
	users := fakeNotifUsers{byID: map[uuid.UUID]*domain.User{user: {ID: user, Email: "a@x.com"}}}
	uc := NewNotificationUseCase(repo, prefs, users, mailer, nil, "")

	n, err := uc.Create(context.Background(), domain.NotificationCreateInput{OrgID: org, UserID: user, Title: "hi"})
	require.NoError(t, err)
	require.NotNil(t, n, "a digest-only row must be stored, not dropped")
	require.True(t, n.DigestOnly, "the row is hidden from the bell (digest_only=true)")
	require.Len(t, repo.created, 1)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 0, mailer.noteCount(), "digest=daily → no immediate email; the digest job sends it")
}

func TestCreate_NoImmediateEmail_WhenDigestDaily(t *testing.T) {
	repo := &fakeNotificationRepo{}
	prefs := newFakePrefRepo()
	mailer := &recordingMailer{}
	org, user := uuid.New(), uuid.New()
	prefs.put(overridePref(org, user, false, domain.DigestDaily, "automation", domain.ChannelPref{InApp: true, Email: true}))
	users := fakeNotifUsers{byID: map[uuid.UUID]*domain.User{user: {ID: user, Email: "a@x.com"}}}
	uc := NewNotificationUseCase(repo, prefs, users, mailer, nil, "")

	_, err := uc.Create(context.Background(), domain.NotificationCreateInput{OrgID: org, UserID: user, Title: "hi"})
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 0, mailer.noteCount(), "digest=daily defers email to the digest job, no immediate send")
}

func TestCreate_DefaultPrefs_StoresRowNoEmail(t *testing.T) {
	repo := &fakeNotificationRepo{}
	mailer := &recordingMailer{}
	org, user := uuid.New(), uuid.New()
	// No prefs row at all → defaults (in-app on, email off).
	uc := NewNotificationUseCase(repo, newFakePrefRepo(), fakeNotifUsers{byID: map[uuid.UUID]*domain.User{}}, mailer, nil, "")

	n, err := uc.Create(context.Background(), domain.NotificationCreateInput{OrgID: org, UserID: user, Title: "hi"})
	require.NoError(t, err)
	require.NotNil(t, n)
	require.Len(t, repo.created, 1)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 0, mailer.noteCount(), "email is opt-in — default prefs send no email")
}

// --- Preferences read/update ---

func TestGetPreferences_DefaultsWhenNoRow(t *testing.T) {
	uc := NewNotificationUseCase(&fakeNotificationRepo{}, newFakePrefRepo(), nil, nil, nil, "")
	view, err := uc.GetPreferences(context.Background(), uuid.New(), uuid.New())
	require.NoError(t, err)
	require.False(t, view.MuteAll)
	require.Equal(t, domain.DigestOff, view.EmailDigest)
	require.NotEmpty(t, view.Types)
	for _, tp := range view.Types {
		require.True(t, tp.InApp, "default in-app on")
		require.False(t, tp.Email, "default email off")
		require.NotEmpty(t, tp.Label, "catalog label present")
	}
}

func TestUpdatePreferences_SetsValidatesAndIgnoresUnknownTypes(t *testing.T) {
	prefs := newFakePrefRepo()
	uc := NewNotificationUseCase(&fakeNotificationRepo{}, prefs, nil, nil, nil, "")
	org, user := uuid.New(), uuid.New()

	mute := true
	daily := domain.DigestDaily
	view, err := uc.UpdatePreferences(context.Background(), org, user, domain.NotificationPreferenceUpdate{
		MuteAll:     &mute,
		EmailDigest: &daily,
		Types: []domain.NotificationChannelSet{
			{Key: "automation", InApp: false, Email: true},
			{Key: "does-not-exist", InApp: true, Email: true}, // ignored (not in catalog)
		},
	})
	require.NoError(t, err)
	require.True(t, view.MuteAll)
	require.Equal(t, domain.DigestDaily, view.EmailDigest)

	stored, _ := prefs.Get(context.Background(), org, user)
	require.NotNil(t, stored)
	require.Equal(t, domain.ChannelPref{InApp: false, Email: true}, stored.Overrides["automation"])
	_, junk := stored.Overrides["does-not-exist"]
	require.False(t, junk, "unknown catalog keys must not be persisted")

	bad := "weekly"
	_, err = uc.UpdatePreferences(context.Background(), org, user, domain.NotificationPreferenceUpdate{EmailDigest: &bad})
	require.Error(t, err, "an invalid digest mode must be rejected")
}

// --- Daily digest ---

func TestRunDailyDigest_SendsEligibleAndConsumesAll(t *testing.T) {
	org, user := uuid.New(), uuid.New()
	autoID, otherID := uuid.New(), uuid.New()
	repo := &digestNotifRepo{pending: []domain.Notification{
		{ID: autoID, OrgID: org, UserID: user, Type: "automation", Title: "One", CreatedAt: time.Now()},
		{ID: otherID, OrgID: org, UserID: user, Type: "other", Title: "Two", CreatedAt: time.Now()}, // not email-eligible
	}}
	prefs := newFakePrefRepo()
	prefs.put(overridePref(org, user, false, domain.DigestDaily, "automation", domain.ChannelPref{InApp: true, Email: true}))
	mailer := &recordingMailer{}
	users := fakeNotifUsers{byID: map[uuid.UUID]*domain.User{user: {ID: user, Email: "a@x.com"}}}
	uc := NewNotificationUseCase(repo, prefs, users, mailer, nil, "https://app.example")

	sent, err := uc.RunDailyDigest(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, sent)
	require.Equal(t, []int{1}, mailer.digests, "only the email-eligible 'automation' item is in the email")
	require.Len(t, prefs.marked, 1, "the member's digest slot is claimed")
	require.ElementsMatch(t, []uuid.UUID{autoID, otherID}, repo.digested, "all fetched rows are consumed on success, so none is reconsidered")
}

func TestRunDailyDigest_SendFailure_LeavesRowsForRetry(t *testing.T) {
	org, user := uuid.New(), uuid.New()
	repo := &digestNotifRepo{pending: []domain.Notification{
		{ID: uuid.New(), OrgID: org, UserID: user, Type: "automation", Title: "One", CreatedAt: time.Now()},
	}}
	prefs := newFakePrefRepo()
	prefs.put(overridePref(org, user, false, domain.DigestDaily, "automation", domain.ChannelPref{InApp: true, Email: true}))
	mailer := &recordingMailer{fail: true}
	users := fakeNotifUsers{byID: map[uuid.UUID]*domain.User{user: {ID: user, Email: "a@x.com"}}}
	uc := NewNotificationUseCase(repo, prefs, users, mailer, nil, "")

	sent, err := uc.RunDailyDigest(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, sent)
	require.Empty(t, repo.digested, "a send failure must NOT consume the rows — they retry next pass")
}

func TestRunDailyDigest_NoEligibleItems_ConsumesWithoutSending(t *testing.T) {
	org, user := uuid.New(), uuid.New()
	only := uuid.New()
	repo := &digestNotifRepo{pending: []domain.Notification{
		{ID: only, OrgID: org, UserID: user, Type: "automation", Title: "One", CreatedAt: time.Now()},
	}}
	prefs := newFakePrefRepo()
	// digest=daily but the automation type has email OFF → nothing to email.
	prefs.put(overridePref(org, user, false, domain.DigestDaily, "automation", domain.ChannelPref{InApp: true, Email: false}))
	mailer := &recordingMailer{}
	users := fakeNotifUsers{byID: map[uuid.UUID]*domain.User{user: {ID: user, Email: "a@x.com"}}}
	uc := NewNotificationUseCase(repo, prefs, users, mailer, nil, "")

	sent, err := uc.RunDailyDigest(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, sent)
	require.Empty(t, mailer.digests)
	require.Equal(t, []uuid.UUID{only}, repo.digested, "non-eligible rows are consumed so they aren't rescanned forever")
}

func TestRunDailyDigest_AlreadyClaimed_DoesNotSend(t *testing.T) {
	org, user := uuid.New(), uuid.New()
	repo := &digestNotifRepo{pending: []domain.Notification{
		{ID: uuid.New(), OrgID: org, UserID: user, Type: "automation", Title: "One", CreatedAt: time.Now()},
	}}
	prefs := newFakePrefRepo()
	p := overridePref(org, user, false, domain.DigestDaily, "automation", domain.ChannelPref{InApp: true, Email: true})
	recent := time.Now() // claimed just now → not due
	p.LastDigestSentAt = &recent
	prefs.put(p)
	mailer := &recordingMailer{}
	users := fakeNotifUsers{byID: map[uuid.UUID]*domain.User{user: {ID: user, Email: "a@x.com"}}}
	uc := NewNotificationUseCase(repo, prefs, users, mailer, nil, "")

	// ListDailyDigestDue in the fake still returns it, but TryClaimDailyDigest must
	// refuse the claim (last_digest_sent_at is recent), so nothing is emailed.
	sent, err := uc.RunDailyDigest(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, sent)
	require.Empty(t, mailer.digests)
}
