package repository

import (
	"context"
	"errors"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// notificationPreferenceRepository persists per-member notification settings (U5).
// One row per (org_id, user_id); an absent row means "all defaults".
type notificationPreferenceRepository struct {
	db *gorm.DB
}

func NewNotificationPreferenceRepository(db *gorm.DB) domain.NotificationPreferenceRepository {
	return &notificationPreferenceRepository{db: db}
}

func (r *notificationPreferenceRepository) Get(ctx context.Context, orgID, userID uuid.UUID) (*domain.NotificationPreference, error) {
	var p domain.NotificationPreference
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND user_id = ?", orgID, userID).
		First(&p).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// Upsert inserts or updates the member's row, keyed by the (org_id, user_id) unique
// index. Overrides is serialized to jsonb by the model's serializer tag; a nil map
// is normalized to an empty object so the column is never NULL.
func (r *notificationPreferenceRepository) Upsert(ctx context.Context, p *domain.NotificationPreference) error {
	if p.Overrides == nil {
		p.Overrides = map[string]domain.ChannelPref{}
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "org_id"}, {Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"mute_all", "email_digest", "overrides", "updated_at"}),
		}).
		Create(p).Error
}

// ListDailyDigestDue returns every email_digest='daily' preference whose last digest
// was sent before `sentBefore` (or never) — the digest job's cross-org work list.
//
// The liveness EXISTS is load-bearing, not cosmetic: a preference row outlives its
// membership (neither workspace deletion nor member offboarding prunes
// notification_preferences), and the send path only re-hydrates the GLOBAL user row,
// so without this gate a deleted workspace's owner — or a suspended/removed member —
// still receives one final digest. One correlated subquery excludes all three at the
// source: a deleted org stamps org_users status='deleted'+deleted_at, a suspension
// flips status, and a removal hard-deletes the row.
func (r *notificationPreferenceRepository) ListDailyDigestDue(ctx context.Context, sentBefore time.Time) ([]domain.NotificationPreference, error) {
	var rows []domain.NotificationPreference
	err := r.db.WithContext(ctx).
		Where("email_digest = ? AND mute_all = false AND (last_digest_sent_at IS NULL OR last_digest_sent_at < ?)", domain.DigestDaily, sentBefore).
		Where(LiveMemberExists("notification_preferences.org_id", "notification_preferences.user_id")).
		Find(&rows).Error
	return rows, err
}

// TryClaimDailyDigest is a compare-and-swap claim: it advances last_digest_sent_at
// to `at` only if the row is still due (never sent, or sent before `sentBefore`).
// RowsAffected==1 means this caller won the claim; 0 means another pass already took
// it — so two concurrent digest passes can't both email the same member (U5).
func (r *notificationPreferenceRepository) TryClaimDailyDigest(ctx context.Context, id uuid.UUID, sentBefore, at time.Time) (bool, error) {
	res := r.db.WithContext(ctx).
		Model(&domain.NotificationPreference{}).
		Where("id = ? AND (last_digest_sent_at IS NULL OR last_digest_sent_at < ?)", id, sentBefore).
		Update("last_digest_sent_at", at)
	return res.RowsAffected == 1, res.Error
}
