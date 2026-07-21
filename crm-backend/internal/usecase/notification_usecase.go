package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// notificationRetention is how long a notification lives before the sweep hard-
// deletes it. In-app notifications carry no audit value once stale, so 90 days
// keeps the table small and the partial unread index tight.
const notificationRetention = 90 * 24 * time.Hour

// digestMinInterval is the minimum gap between a member's daily-digest emails. The
// digest job runs more often than this (hourly), but the compare-and-swap claim on
// last_digest_sent_at keeps each member to ~one digest a day and makes the job both
// restart-safe and safe under concurrent/multi-instance passes.
const digestMinInterval = 23 * time.Hour

// digestMaxLookback is the safety floor on how far back a digest reaches, so a job
// that was down for a while doesn't email an ancient backlog (idempotency comes from
// digested_at, not this window).
const digestMaxLookback = 7 * 24 * time.Hour

// digestPerTypeCap bounds how many rows of ONE event type may fill a single digest
// email. Without it a noisy producer silently deletes a member's other
// notifications: the fetch is oldest-first and capped at 100, so the rows behind it
// are never fetched, stay pending, and eventually age past digestMaxLookback — lost
// with no email and no trace. Overflow is summarized rather than dropped.
const digestPerTypeCap = 10

// digestTypeLabel renders an event type for the overflow summary line, preferring
// the catalog's human label. An unknown type falls back to the raw key rather than
// being hidden — a summary that cannot name what it is summarizing is not a summary.
func digestTypeLabel(typ string) string {
	for _, t := range domain.NotificationEventTypes {
		if t.Key == typ {
			return strings.ToLower(t.Label)
		}
	}
	if typ == "" {
		return "notifications"
	}
	return typ + " notifications"
}

// notifUserLookup resolves a recipient's email for the notification email channel
// (U5). domain.AuthRepository satisfies it; kept narrow so the notification usecase
// doesn't take a dependency on the whole auth surface.
type notifUserLookup interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
}

// notificationUseCase inserts notifications, fans them out over SSE, and (U5) gates
// delivery on each recipient's preferences + sends the email channel. The insert is
// authoritative; the SSE publish and email are best-effort.
type notificationUseCase struct {
	repo        domain.NotificationRepository
	prefRepo    domain.NotificationPreferenceRepository
	users       notifUserLookup
	mailer      domain.Mailer
	redis       *redis.Client // nil-safe: without Redis, Create simply skips the live push
	frontendURL string        // origin for absolute links in emails
}

// NewNotificationUseCase wires the inbox + preference + email-channel service. The
// U5 additions (prefRepo, users, mailer) are all nil-safe: without them Create falls
// back to pre-U5 behavior (always store the in-app row, no preference gating, no email).
func NewNotificationUseCase(repo domain.NotificationRepository, prefRepo domain.NotificationPreferenceRepository, users notifUserLookup, mailer domain.Mailer, redisClient *redis.Client, frontendURL string) domain.NotificationUseCase {
	return &notificationUseCase{repo: repo, prefRepo: prefRepo, users: users, mailer: mailer, redis: redisClient, frontendURL: frontendURL}
}

func (u *notificationUseCase) Create(ctx context.Context, in domain.NotificationCreateInput) (*domain.Notification, error) {
	if in.OrgID == uuid.Nil || in.UserID == uuid.Nil {
		return nil, domain.NewAppError(400, "notification requires org and recipient")
	}
	typ := in.Type
	if typ == "" {
		typ = "automation"
	}

	// Resolve the recipient's preferences for this event type (nil pref → defaults).
	var pref *domain.NotificationPreference
	if u.prefRepo != nil {
		pref, _ = u.prefRepo.Get(ctx, in.OrgID, in.UserID)
	}
	muted := pref != nil && pref.MuteAll
	ch := pref.Channels(typ) // nil-safe: returns DefaultChannelPref for a nil pref
	digest := domain.DigestOff
	if pref != nil {
		digest = pref.EmailDigest
	}

	// Email channel — immediate when the type has email on and the digest is off.
	// When digest='daily' the row is stored (below) and RunDailyDigest emails it.
	if !muted && ch.Email && digest == domain.DigestOff {
		u.sendNotificationEmail(ctx, in)
	}

	// Decide whether to persist a row. It's needed when the bell wants it (in-app on)
	// OR when it must feed the daily digest (email on + digest daily). If mute-all is
	// on, or no channel wants it, nothing is stored — callers treat a nil return with
	// no error as "delivered by preference to no surface".
	storeForDigest := ch.Email && digest == domain.DigestDaily
	if muted || (!ch.InApp && !storeForDigest) {
		return nil, nil
	}

	n := &domain.Notification{
		OrgID:      in.OrgID,
		UserID:     in.UserID,
		Type:       typ,
		Title:      in.Title,
		Body:       in.Body,
		Link:       in.Link,
		EntityType: in.EntityType,
		EntityID:   in.EntityID,
		// digest_only when the bell channel is off: the row exists ONLY to be digested;
		// the bell (List/UnreadCount) filters it out and nothing is pushed over SSE.
		DigestOnly: !ch.InApp,
	}
	if err := u.repo.Create(ctx, n); err != nil {
		return nil, err
	}
	if ch.InApp {
		u.publish(ctx, n)
	}
	return n, nil
}

// sendNotificationEmail emails a single notification to its recipient off the
// request path (the fire-and-forget lesson): the recipient's email is resolved
// synchronously; the send runs on a detached context so a slow/failed mail never
// affects the workflow run or the in-app row. Nil-safe on missing deps.
func (u *notificationUseCase) sendNotificationEmail(ctx context.Context, in domain.NotificationCreateInput) {
	if u.mailer == nil || u.users == nil {
		return
	}
	user, err := u.users.GetUserByID(ctx, in.UserID)
	if err != nil || user == nil || user.Email == "" {
		return
	}
	to, title, body, link := user.Email, in.Title, in.Body, u.absoluteLink(in.Link)
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := u.mailer.SendNotification(bg, to, title, body, link); err != nil {
			slog.Warn("notification: email send failed", "to", to, "error", err)
		}
	}()
}

// absoluteLink turns a stored relative in-app path ("/deals/<id>") into an absolute
// URL for email. Non-relative or empty links pass through unchanged.
func (u *notificationUseCase) absoluteLink(link string) string {
	if link == "" || u.frontendURL == "" || !strings.HasPrefix(link, "/") {
		return link
	}
	return strings.TrimRight(u.frontendURL, "/") + link
}

// publish pushes the new notification onto the recipient's per-user SSE channel
// with a fresh unread count so the header bell can update the list and badge in
// one message. Best-effort and nil-safe — a publish error never fails the insert.
func (u *notificationUseCase) publish(ctx context.Context, n *domain.Notification) {
	if u.redis == nil {
		return
	}
	unread, err := u.repo.UnreadCount(ctx, n.OrgID, n.UserID)
	if err != nil {
		slog.Warn("notification: unread count for publish failed", "error", err)
	}
	payload, err := json.Marshal(map[string]any{
		"type":         "notification",
		"notification": n,
		"unread_count": unread,
	})
	if err != nil {
		slog.Warn("notification: marshal for publish failed", "error", err)
		return
	}
	channel := domain.UserNotificationChannel(n.OrgID, n.UserID)
	if err := u.redis.Publish(ctx, channel, payload).Err(); err != nil {
		slog.Warn("notification: SSE publish failed", "channel", channel, "error", err)
	}
}

func (u *notificationUseCase) List(ctx context.Context, orgID, userID uuid.UUID, in domain.NotificationListInput) (*domain.NotificationPage, error) {
	rows, next, err := u.repo.List(ctx, orgID, userID, in)
	if err != nil {
		return nil, err
	}
	unread, err := u.repo.UnreadCount(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []domain.Notification{}
	}
	return &domain.NotificationPage{Notifications: rows, NextCursor: next, UnreadCount: unread}, nil
}

func (u *notificationUseCase) UnreadCount(ctx context.Context, orgID, userID uuid.UUID) (int64, error) {
	return u.repo.UnreadCount(ctx, orgID, userID)
}

func (u *notificationUseCase) MarkRead(ctx context.Context, orgID, userID, id uuid.UUID) error {
	return u.repo.MarkRead(ctx, orgID, userID, id)
}

func (u *notificationUseCase) MarkAllRead(ctx context.Context, orgID, userID uuid.UUID) (int64, error) {
	return u.repo.MarkAllRead(ctx, orgID, userID)
}

func (u *notificationUseCase) SweepOld(ctx context.Context) (int64, error) {
	return u.repo.DeleteOlderThan(ctx, time.Now().Add(-notificationRetention))
}

// --- Preferences + digest (U5) ---

func (u *notificationUseCase) GetPreferences(ctx context.Context, orgID, userID uuid.UUID) (*domain.NotificationPreferenceView, error) {
	var pref *domain.NotificationPreference
	if u.prefRepo != nil {
		var err error
		if pref, err = u.prefRepo.Get(ctx, orgID, userID); err != nil {
			return nil, err
		}
	}
	return buildPreferenceView(pref), nil
}

func (u *notificationUseCase) UpdatePreferences(ctx context.Context, orgID, userID uuid.UUID, in domain.NotificationPreferenceUpdate) (*domain.NotificationPreferenceView, error) {
	if u.prefRepo == nil {
		return nil, domain.NewAppError(503, "notification preferences are not available")
	}
	pref, err := u.prefRepo.Get(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	if pref == nil {
		pref = &domain.NotificationPreference{OrgID: orgID, UserID: userID, EmailDigest: domain.DigestOff, Overrides: map[string]domain.ChannelPref{}}
	}
	if pref.Overrides == nil {
		pref.Overrides = map[string]domain.ChannelPref{}
	}
	if in.MuteAll != nil {
		pref.MuteAll = *in.MuteAll
	}
	if in.EmailDigest != nil {
		if *in.EmailDigest != domain.DigestOff && *in.EmailDigest != domain.DigestDaily {
			return nil, domain.NewAppError(400, "email_digest must be 'off' or 'daily'")
		}
		pref.EmailDigest = *in.EmailDigest
	}
	for _, t := range in.Types {
		// Only known catalog types are stored, so a client can't stuff the overrides
		// blob with arbitrary keys.
		if isKnownEventType(t.Key) {
			pref.Overrides[t.Key] = domain.ChannelPref{InApp: t.InApp, Email: t.Email}
		}
	}
	if err := u.prefRepo.Upsert(ctx, pref); err != nil {
		return nil, err
	}
	return buildPreferenceView(pref), nil
}

// buildPreferenceView merges a (possibly nil) preference row with the event-type
// catalog + per-type defaults into the preference-center payload.
func buildPreferenceView(pref *domain.NotificationPreference) *domain.NotificationPreferenceView {
	view := &domain.NotificationPreferenceView{EmailDigest: domain.DigestOff}
	if pref != nil {
		view.MuteAll = pref.MuteAll
		if pref.EmailDigest != "" {
			view.EmailDigest = pref.EmailDigest
		}
	}
	for _, et := range domain.NotificationEventTypes {
		ch := pref.Channels(et.Key)
		view.Types = append(view.Types, domain.NotificationTypePref{
			Key: et.Key, Label: et.Label, Description: et.Description,
			InApp: ch.InApp, Email: ch.Email,
		})
	}
	return view
}

func isKnownEventType(key string) bool {
	for _, et := range domain.NotificationEventTypes {
		if et.Key == key {
			return true
		}
	}
	return false
}

// RunDailyDigest sends each due member (email_digest='daily', last digest > ~a day
// ago) a single email summarizing their recent unread, email-eligible notifications.
// Best-effort per member: a lookup/send failure is logged and skipped so one bad
// recipient can't stall the batch. Marking last_digest_sent_at holds the cadence and
// makes the job restart-safe.
func (u *notificationUseCase) RunDailyDigest(ctx context.Context) (int, error) {
	if u.prefRepo == nil || u.mailer == nil || u.users == nil {
		return 0, nil
	}
	now := time.Now()
	sentBefore := now.Add(-digestMinInterval)
	due, err := u.prefRepo.ListDailyDigestDue(ctx, sentBefore)
	if err != nil {
		return 0, err
	}
	sent := 0
	for i := range due {
		pref := due[i]
		// Atomically claim this member's digest slot (compare-and-swap on
		// last_digest_sent_at). Only the winner proceeds, so two concurrent passes
		// can't both email the same member.
		claimed, err := u.prefRepo.TryClaimDailyDigest(ctx, pref.ID, sentBefore, now)
		if err != nil {
			slog.Warn("notification digest: claim failed", "user", pref.UserID, "error", err)
			continue
		}
		if !claimed {
			continue // another pass already took this member
		}
		rows, err := u.repo.ListPendingDigest(ctx, pref.OrgID, pref.UserID, now.Add(-digestMaxLookback), 100)
		if err != nil {
			slog.Warn("notification digest: list failed", "user", pref.UserID, "error", err)
			continue
		}
		if len(rows) == 0 {
			continue
		}
		// Every fetched row is a candidate; only email-eligible types go in the email.
		//
		// Per-type quota, because one noisy producer could otherwise silently delete a
		// member's other notifications. The fetch is oldest-first and capped, so a
		// source flapping between healthy and failing fills the whole window; the
		// workflow notifications behind it are never fetched, stay pending, and after
		// digestMaxLookback they fall out of the query's floor and are lost outright —
		// never emailed, never explained. The quota bounds each type's share of the
		// EMAIL while `ids` still consumes every fetched row, so nothing clogs the
		// queue behind it either.
		ids := make([]uuid.UUID, 0, len(rows))
		items := make([]domain.NotificationDigestItem, 0, len(rows))
		perType := map[string]int{}
		overflow := map[string]int{}
		for _, r := range rows {
			ids = append(ids, r.ID)
			if !pref.Channels(r.Type).Email {
				continue
			}
			perType[r.Type]++
			if perType[r.Type] > digestPerTypeCap {
				overflow[r.Type]++
				continue
			}
			items = append(items, domain.NotificationDigestItem{
				Title: r.Title, Body: r.Body, Link: u.absoluteLink(r.Link), CreatedAt: r.CreatedAt,
			})
		}
		// Overflow is SUMMARIZED, never silently dropped. A digest that quietly omits
		// rows is the same failure as the crowding it fixes, one layer down: the member
		// would believe they had seen everything.
		for typ, n := range overflow {
			items = append(items, domain.NotificationDigestItem{
				Title:     fmt.Sprintf("+%d more %s", n, digestTypeLabel(typ)),
				Body:      "Only the most recent were listed above. Open the app to see the rest.",
				CreatedAt: now,
			})
		}
		// consumed = mark the fetched rows digested (so they're never reconsidered).
		// We hold it back ONLY on a real send failure, so the email-eligible rows are
		// retried on the next pass instead of being lost.
		consumed := true
		if len(items) > 0 {
			user, err := u.users.GetUserByID(ctx, pref.UserID)
			switch {
			case err != nil:
				// TRANSIENT recipient-lookup failure (pool exhaustion, replica
				// hiccup, bad connection). No email went out, so the rows must NOT
				// be consumed: stamping digested_at here would make ListPendingDigest
				// (digested_at IS NULL) skip them forever — a permanent drop on every
				// channel. Hold them; the claim already advanced last_digest_sent_at,
				// so they retry on the next daily pass rather than being lost.
				slog.Warn("notification digest: recipient lookup failed", "user", pref.UserID, "error", err)
				consumed = false
			case user == nil || user.Email == "":
				// Genuinely unmailable (no such user / no address): nothing to
				// retry, so consume the rows.
			default:
				if err := u.mailer.SendNotificationDigest(ctx, user.Email, items); err != nil {
					slog.Warn("notification digest: send failed", "to", user.Email, "error", err)
					consumed = false
				} else {
					sent++
				}
			}
		}
		if consumed {
			_ = u.repo.MarkNotificationsDigested(ctx, ids, now)
		}
	}
	return sent, nil
}
