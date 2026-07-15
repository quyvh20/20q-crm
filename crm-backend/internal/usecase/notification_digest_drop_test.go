package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// errLookup simulates a transient DB failure from the users table read
// (e.g. "driver: bad connection", pool exhaustion, replica hiccup).
// authRepository.GetUserByID collapses ONLY gorm.ErrRecordNotFound into
// (nil, nil) — every other error propagates as (nil, err), exactly like this.
var errLookup = errors.New("driver: bad connection")

type flakyNotifUsers struct{}

func (flakyNotifUsers) GetUserByID(_ context.Context, _ uuid.UUID) (*domain.User, error) {
	return nil, errLookup
}

// A transient recipient-lookup failure must behave like a transient send failure:
// no email went out, so the rows must survive for the next pass.
func TestRunDailyDigest_LookupFailure_MustLeaveRowsForRetry(t *testing.T) {
	org, user := uuid.New(), uuid.New()
	id1, id2 := uuid.New(), uuid.New()
	repo := &digestNotifRepo{pending: []domain.Notification{
		// digest_only rows: in_app=false + email=true + digest=daily.
		// Hidden from the bell (List/UnreadCount filter digest_only=false),
		// so email is their ONLY surface.
		{ID: id1, OrgID: org, UserID: user, Type: "automation", Title: "One", DigestOnly: true, CreatedAt: time.Now()},
		{ID: id2, OrgID: org, UserID: user, Type: "automation", Title: "Two", DigestOnly: true, CreatedAt: time.Now()},
	}}
	prefs := newFakePrefRepo()
	prefs.put(overridePref(org, user, false, domain.DigestDaily, "automation", domain.ChannelPref{InApp: false, Email: true}))
	mailer := &recordingMailer{} // mailer is healthy; the LOOKUP is what fails
	uc := NewNotificationUseCase(repo, prefs, flakyNotifUsers{}, mailer, nil, "")

	sent, err := uc.RunDailyDigest(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, sent)
	require.Empty(t, mailer.digests, "no email was sent")

	// The claim already advanced last_digest_sent_at, so nothing else retries.
	require.Empty(t, repo.digested,
		"a transient LOOKUP failure must NOT consume the rows (same as a send failure) — "+
			"but they were stamped digested_at, so ListPendingDigest (digested_at IS NULL) "+
			"can never return them again: permanently dropped on every channel")
}
