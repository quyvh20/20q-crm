package usecase

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// itemRecordingMailer keeps the digest ITEMS, not just their count — the quota's
// whole behaviour is which items survive and what replaces the rest.
type itemRecordingMailer struct {
	recordingMailer
	mu    sync.Mutex
	items [][]domain.NotificationDigestItem
}

func (m *itemRecordingMailer) SendNotificationDigest(_ context.Context, _ string, items []domain.NotificationDigestItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = append(m.items, items)
	return nil
}

func (m *itemRecordingMailer) last() []domain.NotificationDigestItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.items) == 0 {
		return nil
	}
	return m.items[len(m.items)-1]
}

type digestUsers struct{ email string }

func (u digestUsers) GetUserByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	return &domain.User{ID: id, Email: u.email}, nil
}

// A member's digest must not be emptied of one producer's notifications by another
// producer's noise.
//
// The failure this guards is not "the email looks long" — it is permanent LOSS. The
// pending fetch is oldest-first and capped at 100, so a lead source flapping between
// healthy and failing fills the whole window; the workflow notifications behind it are
// never fetched, stay pending, and once they age past digestMaxLookback they fall out
// of the query's floor. Never emailed, never shown, never explained.
func TestRunDailyDigest_OneNoisyTypeCannotCrowdOutAnother(t *testing.T) {
	org, user := uuid.New(), uuid.New()

	var pending []domain.Notification
	base := time.Now().Add(-time.Hour)
	// 30 health rows FIRST (oldest), which is the shape that does the damage: they
	// are fetched first and would consume the whole budget.
	for i := 0; i < 30; i++ {
		pending = append(pending, domain.Notification{
			ID: uuid.New(), OrgID: org, UserID: user, Type: "integration_health",
			Title: "Lead source is failing", DigestOnly: true, CreatedAt: base.Add(time.Duration(i) * time.Second),
		})
	}
	// then the workflow notification the member actually needs to see.
	workflowID := uuid.New()
	pending = append(pending, domain.Notification{
		ID: workflowID, OrgID: org, UserID: user, Type: "automation",
		Title: "Deal needs your approval", DigestOnly: true, CreatedAt: base.Add(time.Hour),
	})

	repo := &digestNotifRepo{pending: pending}
	prefs := newFakePrefRepo()
	pref := overridePref(org, user, false, domain.DigestDaily, "automation", domain.ChannelPref{InApp: false, Email: true})
	pref.Overrides["integration_health"] = domain.ChannelPref{InApp: false, Email: true}
	prefs.put(pref)
	mailer := &itemRecordingMailer{}
	uc := NewNotificationUseCase(repo, prefs, digestUsers{email: "a@x.com"}, mailer, nil, "")

	sent, err := uc.RunDailyDigest(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, sent)

	items := mailer.last()
	require.NotNil(t, items)

	var health, automation, summary int
	for _, it := range items {
		switch {
		case strings.HasPrefix(it.Title, "+"):
			summary++
		case it.Title == "Deal needs your approval":
			automation++
		default:
			health++
		}
	}

	require.Equal(t, digestPerTypeCap, health, "one type may not exceed its quota")
	require.Equal(t, 1, automation, "THE BUG: the workflow notification must survive a noisy type")
	require.Equal(t, 1, summary, "the overflow is summarized, never silently dropped")

	// Every FETCHED row is consumed regardless of the quota. Holding the overflow back
	// would re-fetch it tomorrow and every day after, so the noisy type would clog the
	// queue permanently — the same crowding one layer down.
	require.Len(t, repo.digested, len(pending))
}

// The summary line must say how many were held back and what they were. A digest
// that quietly omits rows is the same failure as the crowding it fixes.
func TestRunDailyDigest_OverflowSummaryNamesTheCountAndType(t *testing.T) {
	org, user := uuid.New(), uuid.New()

	var pending []domain.Notification
	for i := 0; i < digestPerTypeCap+7; i++ {
		pending = append(pending, domain.Notification{
			ID: uuid.New(), OrgID: org, UserID: user, Type: "integration_health",
			Title: "Lead source is failing", DigestOnly: true, CreatedAt: time.Now(),
		})
	}

	repo := &digestNotifRepo{pending: pending}
	prefs := newFakePrefRepo()
	prefs.put(overridePref(org, user, false, domain.DigestDaily, "integration_health", domain.ChannelPref{InApp: false, Email: true}))
	mailer := &itemRecordingMailer{}
	uc := NewNotificationUseCase(repo, prefs, digestUsers{email: "a@x.com"}, mailer, nil, "")

	_, err := uc.RunDailyDigest(context.Background())
	require.NoError(t, err)

	var found string
	for _, it := range mailer.last() {
		if strings.HasPrefix(it.Title, "+") {
			found = it.Title
		}
	}
	require.Equal(t, "+7 more lead source health", found,
		"the count is exact and the type is named with its catalog label")
}

// The control: a member whose notifications fit under the quota must see NO summary
// line. A digest that always appends "+0 more" is noise, and noise is how the real
// signal stops being read.
func TestRunDailyDigest_NoSummaryWhenNothingOverflows(t *testing.T) {
	org, user := uuid.New(), uuid.New()
	repo := &digestNotifRepo{pending: []domain.Notification{
		{ID: uuid.New(), OrgID: org, UserID: user, Type: "automation", Title: "One", DigestOnly: true, CreatedAt: time.Now()},
		{ID: uuid.New(), OrgID: org, UserID: user, Type: "automation", Title: "Two", DigestOnly: true, CreatedAt: time.Now()},
	}}
	prefs := newFakePrefRepo()
	prefs.put(overridePref(org, user, false, domain.DigestDaily, "automation", domain.ChannelPref{InApp: false, Email: true}))
	mailer := &itemRecordingMailer{}
	uc := NewNotificationUseCase(repo, prefs, digestUsers{email: "a@x.com"}, mailer, nil, "")

	_, err := uc.RunDailyDigest(context.Background())
	require.NoError(t, err)

	items := mailer.last()
	require.Len(t, items, 2)
	for _, it := range items {
		require.False(t, strings.HasPrefix(it.Title, "+"), "no summary line when nothing was held back")
	}
}
