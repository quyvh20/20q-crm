package repository

import (
	"context"
	"errors"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type authRepository struct {
	db *gorm.DB
}

func NewAuthRepository(db *gorm.DB) domain.AuthRepository {
	return &authRepository{db: db}
}

func (r *authRepository) CreateOrganization(ctx context.Context, org *domain.Organization) error {
	return r.db.WithContext(ctx).Create(org).Error
}

func (r *authRepository) GetOrganizationByID(ctx context.Context, id uuid.UUID) (*domain.Organization, error) {
	var org domain.Organization
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&org).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &org, nil
}

// UpdateOrganization writes an org's editable fields (name + workspace
// defaults) with a column-scoped Select so a partial save never clobbers
// unrelated columns (U4).
func (r *authRepository) UpdateOrganization(ctx context.Context, org *domain.Organization) error {
	return r.db.WithContext(ctx).
		Model(&domain.Organization{}).
		Where("id = ?", org.ID).
		Select("name", "currency", "locale", "timezone").
		Updates(map[string]interface{}{
			"name":     org.Name,
			"currency": org.Currency,
			"locale":   org.Locale,
			"timezone": org.Timezone,
		}).Error
}

// SoftDeleteOrganization soft-deletes the org AND deactivates every membership in
// one transaction (U4): org resolution (ListOrgsByUserID) filters status='active',
// so deactivating the memberships makes the workspace vanish cleanly from every
// member's chooser — a bare org soft-delete would strand active memberships with
// a nil (soft-deleted) Org preload.
func (r *authRepository) SoftDeleteOrganization(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.Organization{}).Where("id = ?", id).
			Update("deleted_at", time.Now()).Error; err != nil {
			return err
		}
		return tx.Model(&domain.OrgUser{}).
			Where("org_id = ? AND status != 'deleted'", id).
			Updates(map[string]interface{}{"status": domain.StatusDeleted, "deleted_at": time.Now()}).Error
	})
}

func (r *authRepository) CreateUser(ctx context.Context, user *domain.User) error {
	return r.db.WithContext(ctx).Create(user).Error
}

// UpdateUserProfile writes ONLY the self-serve profile columns (U2). The full
// UpdateUser is a whole-row gorm Save, so a profile PATCH racing a concurrent
// security write (password reset / sign-out-everywhere bumping token_version)
// would write the stale password_hash + token_version back and silently revert
// the security change. Scoping the UPDATE to profile columns removes that
// lost-update window entirely.
func (r *authRepository) UpdateUserProfile(ctx context.Context, user *domain.User) error {
	return r.db.WithContext(ctx).Model(&domain.User{}).
		Where("id = ?", user.ID).
		Select("first_name", "last_name", "full_name", "avatar_url", "timezone", "locale", "onboarding_completed", "updated_at").
		Updates(user).Error
}

// GetUserByEmail matches case-insensitively (LOWER(email)) so a user who signs
// up as "Sam@x.com" is found by "sam@x.com" — the #1 real-world "reset email
// never arrived" cause (P2). Backed by the idx_users_email_lower functional
// index. Writers normalize to lowercase, so this only rescues legacy rows.
func (r *authRepository) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	var user domain.User
	err := r.db.WithContext(ctx).Where("LOWER(email) = LOWER(?)", email).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (r *authRepository) GetUserByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	var user domain.User
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (r *authRepository) GetUserByGoogleID(ctx context.Context, googleID string) (*domain.User, error) {
	var user domain.User
	err := r.db.WithContext(ctx).Where("google_id = ?", googleID).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (r *authRepository) UpdateUser(ctx context.Context, user *domain.User) error {
	return r.db.WithContext(ctx).Save(user).Error
}

func (r *authRepository) CreateRefreshToken(ctx context.Context, token *domain.RefreshToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}

func (r *authRepository) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	var token domain.RefreshToken
	err := r.db.WithContext(ctx).
		Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", tokenHash, time.Now()).
		First(&token).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &token, nil
}

// GetRefreshTokenByHashAny returns the row for a hash regardless of revoked/expiry
// state (nil when the hash was never issued). Refresh uses this to tell a genuinely
// unknown token from an already-rotated one — the latter is a reuse/theft signal.
func (r *authRepository) GetRefreshTokenByHashAny(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	var token domain.RefreshToken
	err := r.db.WithContext(ctx).
		Where("token_hash = ?", tokenHash).
		First(&token).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &token, nil
}

func (r *authRepository) IncrementUserTokenVersion(ctx context.Context, userID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Model(&domain.User{}).
		Where("id = ?", userID).
		UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error
}

func (r *authRepository) GetUserTokenVersion(ctx context.Context, userID uuid.UUID) (int, error) {
	var tv int
	err := r.db.WithContext(ctx).
		Model(&domain.User{}).
		Where("id = ?", userID).
		Pluck("token_version", &tv).Error
	return tv, err
}

// RefreshTokenHasSuccessor reports whether any refresh token was rotated from the
// given one, i.e. this token was superseded in a refresh chain (theft signal when
// replayed) rather than deliberately revoked (logout/revoke-device). P4.
func (r *authRepository) RefreshTokenHasSuccessor(ctx context.Context, tokenID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&domain.RefreshToken{}).
		Where("rotated_from = ?", tokenID).
		Count(&count).Error
	return count > 0, err
}

func (r *authRepository) RevokeRefreshToken(ctx context.Context, tokenID uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&domain.RefreshToken{}).
		Where("id = ?", tokenID).
		Update("revoked_at", now).Error
}

func (r *authRepository) RevokeAllUserRefreshTokens(ctx context.Context, userID uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&domain.RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", now).Error
}

func (r *authRepository) CreateOrgUser(ctx context.Context, ou *domain.OrgUser) error {
	return r.db.WithContext(ctx).Create(ou).Error
}

func (r *authRepository) GetOrgUser(ctx context.Context, userID, orgID uuid.UUID) (*domain.OrgUser, error) {
	var ou domain.OrgUser
	err := r.db.WithContext(ctx).
		Preload("Role").
		Where("user_id = ? AND org_id = ?", userID, orgID).
		First(&ou).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ou, nil
}

func (r *authRepository) ListOrgsByUserID(ctx context.Context, userID uuid.UUID) ([]domain.OrgUser, error) {
	var orgUsers []domain.OrgUser
	// Deterministic order: login/refresh/OAuth all take orgUsers[0] as the active
	// workspace, so without an ORDER BY a multi-org user's default org is whatever
	// Postgres returns first (P10 P0).
	err := r.db.WithContext(ctx).
		Preload("Org").
		Preload("Role").
		Where("user_id = ? AND status = 'active'", userID).
		Order("joined_at ASC, org_id ASC").
		Find(&orgUsers).Error
	return orgUsers, err
}

// ListAllOrgMembershipsByUserID returns a user's memberships in ALL statuses
// (active first, then suspended), for DISPLAY only — the workspace chooser renders
// suspended memberships as disabled cards so a user understands why an org they
// remember is no longer selectable. It must NOT feed the org-selection path:
// login/refresh/switch use the active-only ListOrgsByUserID, so a suspended
// membership can never be minted into a token.
func (r *authRepository) ListAllOrgMembershipsByUserID(ctx context.Context, userID uuid.UUID) ([]domain.OrgUser, error) {
	var orgUsers []domain.OrgUser
	// Exclude 'deleted' memberships (a removed member, or a deleted workspace —
	// U4): the chooser shows active + suspended, never a tombstoned row (which
	// would render as an empty card since its org is soft-deleted → nil preload).
	err := r.db.WithContext(ctx).
		Preload("Org").
		Preload("Role").
		Where("user_id = ? AND status != 'deleted'", userID).
		Order("(status = 'active') DESC, joined_at ASC, org_id ASC").
		Find(&orgUsers).Error
	return orgUsers, err
}

// SetUserDefaultOrg sets users.default_org_id, or clears it (SQL NULL) when
// orgID is nil. UpdateColumn (not Update) so a nil pointer actually writes NULL
// rather than being skipped as a zero value (P3).
func (r *authRepository) SetUserDefaultOrg(ctx context.Context, userID uuid.UUID, orgID *uuid.UUID) error {
	return r.db.WithContext(ctx).
		Model(&domain.User{}).
		Where("id = ?", userID).
		UpdateColumn("default_org_id", orgID).Error
}

// CountActiveMembersByOrgs returns active-member counts keyed by org in one
// GROUP BY query (P3). Orgs with no active members are absent from the map (→ 0).
func (r *authRepository) CountActiveMembersByOrgs(ctx context.Context, orgIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	out := make(map[uuid.UUID]int, len(orgIDs))
	if len(orgIDs) == 0 {
		return out, nil
	}
	type row struct {
		OrgID uuid.UUID
		Cnt   int
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Model(&domain.OrgUser{}).
		Select("org_id, COUNT(*) AS cnt").
		Where("org_id IN ? AND status = 'active'", orgIDs).
		Group("org_id").
		Scan(&rows).Error
	for _, rw := range rows {
		out[rw.OrgID] = rw.Cnt
	}
	return out, err
}

func (r *authRepository) ListMembersByOrgID(ctx context.Context, orgID uuid.UUID) ([]domain.OrgUser, error) {
	var orgUsers []domain.OrgUser
	err := r.db.WithContext(ctx).
		Preload("User").
		Preload("Role").
		Where("org_id = ?", orgID).
		Order("joined_at ASC").
		Find(&orgUsers).Error
	return orgUsers, err
}

func (r *authRepository) UpdateOrgUserRole(ctx context.Context, userID, orgID, roleID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Model(&domain.OrgUser{}).
		Where("user_id = ? AND org_id = ?", userID, orgID).
		Update("role_id", roleID).Error
}

func (r *authRepository) UpdateOrgUserStatus(ctx context.Context, userID, orgID uuid.UUID, status string) error {
	return r.db.WithContext(ctx).
		Model(&domain.OrgUser{}).
		Where("user_id = ? AND org_id = ?", userID, orgID).
		Update("status", status).Error
}

func (r *authRepository) DeleteOrgUser(ctx context.Context, userID, orgID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND org_id = ?", userID, orgID).
		Delete(&domain.OrgUser{}).Error // GORM will soft-delete if DeletedAt exists
}

// GetOrgUserByEmail resolves membership by email case-insensitively (P2): the
// invite dup-check ran through here and was bypassable by casing before.
func (r *authRepository) GetOrgUserByEmail(ctx context.Context, email string, orgID uuid.UUID) (*domain.OrgUser, error) {
	var user domain.User
	err := r.db.WithContext(ctx).Where("LOWER(email) = LOWER(?)", email).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return r.GetOrgUser(ctx, user.ID, orgID)
}

func (r *authRepository) CountOrgUsersByRole(ctx context.Context, orgID, roleID uuid.UUID, status string) (int64, error) {
	var count int64
	query := r.db.WithContext(ctx).Model(&domain.OrgUser{}).Where("org_id = ? AND role_id = ?", orgID, roleID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	err := query.Count(&count).Error
	return count, err
}

func (r *authRepository) GetRoleByName(ctx context.Context, name string, orgID *uuid.UUID) (*domain.Role, error) {
	var role domain.Role
	query := r.db.WithContext(ctx).Where("name = ?", name)
	if orgID == nil {
		query = query.Where("org_id IS NULL AND is_system = true")
	} else {
		query = query.Where("org_id = ? OR (org_id IS NULL AND is_system = true)", orgID)
	}
	err := query.Preload("Permissions").First(&role).Error
	return &role, err
}

func (r *authRepository) GetRoleByID(ctx context.Context, id uuid.UUID) (*domain.Role, error) {
	var role domain.Role
	err := r.db.WithContext(ctx).Preload("Permissions").Where("id = ?", id).First(&role).Error
	return &role, err
}

func (r *authRepository) TransferOrgOwnership(ctx context.Context, orgID, fromUserID, toUserID, ownerRoleID, demoteRoleID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.OrgUser{}).
			Where("user_id = ? AND org_id = ?", fromUserID, orgID).
			Update("role_id", demoteRoleID).Error; err != nil {
			return err
		}
		return tx.Model(&domain.OrgUser{}).
			Where("user_id = ? AND org_id = ?", toUserID, orgID).
			Update("role_id", ownerRoleID).Error
	})
}

func (r *authRepository) CreateOrgInvitation(ctx context.Context, inv *domain.OrgInvitation) error {
	return r.db.WithContext(ctx).Create(inv).Error
}

func (r *authRepository) GetOrgInvitationByTokenHash(ctx context.Context, tokenHash string) (*domain.OrgInvitation, error) {
	var inv domain.OrgInvitation
	err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&inv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &inv, err
}

// GetOrgInvitationByID scopes the lookup to the org so one workspace's admin can
// never resend/revoke another workspace's invitation by id (P2).
func (r *authRepository) GetOrgInvitationByID(ctx context.Context, id, orgID uuid.UUID) (*domain.OrgInvitation, error) {
	var inv domain.OrgInvitation
	err := r.db.WithContext(ctx).Where("id = ? AND org_id = ?", id, orgID).First(&inv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &inv, err
}

// ListPendingInvitations returns the org's still-actionable invitations (P2):
// pending, unexpired, not revoked — newest first.
func (r *authRepository) ListPendingInvitations(ctx context.Context, orgID uuid.UUID) ([]domain.OrgInvitation, error) {
	var invites []domain.OrgInvitation
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND status = 'pending' AND revoked_at IS NULL AND expires_at > ?", orgID, time.Now()).
		Order("created_at DESC").
		Find(&invites).Error
	return invites, err
}

// ListOpenInvitations returns every outstanding invitation (status 'pending',
// not revoked) regardless of expiry, newest first — so the panel can surface an
// expired invite with a Resend badge instead of dropping it (U4).
func (r *authRepository) ListOpenInvitations(ctx context.Context, orgID uuid.UUID) ([]domain.OrgInvitation, error) {
	var invites []domain.OrgInvitation
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND status = 'pending' AND revoked_at IS NULL", orgID).
		Order("created_at DESC").
		Find(&invites).Error
	return invites, err
}

// GetPendingInvitationByEmail returns the org's outstanding (pending, not
// revoked) invite for an email — expired or not — or nil, for the re-invite
// dedupe (U4). Email is normalized+matched case-insensitively; newest wins if
// somehow more than one exists.
func (r *authRepository) GetPendingInvitationByEmail(ctx context.Context, orgID uuid.UUID, email string) (*domain.OrgInvitation, error) {
	var inv domain.OrgInvitation
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND LOWER(email) = LOWER(?) AND status = 'pending' AND revoked_at IS NULL", orgID, email).
		Order("created_at DESC").
		First(&inv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &inv, nil
}

func (r *authRepository) UpdateOrgInvitation(ctx context.Context, inv *domain.OrgInvitation) error {
	return r.db.WithContext(ctx).Save(inv).Error
}

// AcceptInvitation runs the full accept in one transaction (P2): create the
// invitee or set a password on the existing account, UPSERT the membership to
// active (reinstating a previously-removed row via ON CONFLICT), and mark the
// invitation accepted. org_users has a composite (user_id, org_id) PK, so the
// UPSERT is the clean way to reinstate without racing a check-then-insert.
func (r *authRepository) AcceptInvitation(ctx context.Context, inv *domain.OrgInvitation, user *domain.User, createUser bool, newPasswordHash *string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if createUser {
			if err := tx.Create(user).Error; err != nil {
				return err
			}
		} else if newPasswordHash != nil {
			if err := tx.Model(&domain.User{}).Where("id = ?", user.ID).
				Update("password_hash", *newPasswordHash).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec(`INSERT INTO org_users (user_id, org_id, role_id, status, joined_at)
			VALUES (?, ?, ?, 'active', now())
			ON CONFLICT (user_id, org_id)
			DO UPDATE SET role_id = EXCLUDED.role_id, status = 'active', deleted_at = NULL`,
			user.ID, inv.OrgID, inv.RoleID).Error; err != nil {
			return err
		}
		return tx.Model(&domain.OrgInvitation{}).Where("id = ?", inv.ID).
			Update("status", "accepted").Error
	})
}

// --- Account recovery (P1) ---

func (r *authRepository) CreatePasswordResetToken(ctx context.Context, t *domain.PasswordResetToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *authRepository) GetPasswordResetTokenByHash(ctx context.Context, tokenHash string) (*domain.PasswordResetToken, error) {
	var t domain.PasswordResetToken
	err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *authRepository) MarkPasswordResetTokenUsed(ctx context.Context, id uuid.UUID) (int64, error) {
	// Conditional on used_at IS NULL so exactly one caller can claim the token
	// even under concurrent requests — the atomic single-use gate.
	res := r.db.WithContext(ctx).
		Model(&domain.PasswordResetToken{}).
		Where("id = ? AND used_at IS NULL", id).
		Update("used_at", time.Now())
	return res.RowsAffected, res.Error
}

// CountAdminResetTokensSince counts admin-initiated reset links (initiated_by IS
// NOT NULL) minted for a user since `since` — the durable per-target daily cap,
// which works without Redis (unlike the IP rate limit) (P2).
func (r *authRepository) CountAdminResetTokensSince(ctx context.Context, userID uuid.UUID, since time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&domain.PasswordResetToken{}).
		Where("user_id = ? AND initiated_by IS NOT NULL AND created_at > ?", userID, since).
		Count(&count).Error
	return count, err
}

func (r *authRepository) GetLatestPasswordResetToken(ctx context.Context, userID uuid.UUID) (*domain.PasswordResetToken, error) {
	var t domain.PasswordResetToken
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *authRepository) VoidActivePasswordResetTokens(ctx context.Context, userID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Model(&domain.PasswordResetToken{}).
		Where("user_id = ? AND used_at IS NULL", userID).
		Update("used_at", time.Now()).Error
}

func (r *authRepository) CreateEmailVerificationToken(ctx context.Context, t *domain.EmailVerificationToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *authRepository) GetEmailVerificationTokenByHash(ctx context.Context, tokenHash string) (*domain.EmailVerificationToken, error) {
	var t domain.EmailVerificationToken
	err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *authRepository) MarkEmailVerificationTokenUsed(ctx context.Context, id uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).
		Model(&domain.EmailVerificationToken{}).
		Where("id = ? AND used_at IS NULL", id).
		Update("used_at", time.Now())
	return res.RowsAffected, res.Error
}

func (r *authRepository) GetLatestEmailVerificationToken(ctx context.Context, userID uuid.UUID) (*domain.EmailVerificationToken, error) {
	var t domain.EmailVerificationToken
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *authRepository) WriteAuthEvent(ctx context.Context, e *domain.AuthEvent) error {
	return r.db.WithContext(ctx).Create(e).Error
}

// --- Admin audit query + session management (P4) ---

// ListAuthEvents returns a filtered, paginated page of the org's audit log
// (newest first) with each actor's name/email resolved via a LEFT JOIN, plus the
// total matching count. Filter columns are table-qualified because the join adds a
// `users` table that also has a `created_at` column.
func (r *authRepository) ListAuthEvents(ctx context.Context, orgID uuid.UUID, f domain.AuthEventFilter) ([]domain.AuthEventView, int64, error) {
	apply := func(q *gorm.DB) *gorm.DB {
		q = q.Where("auth_events.org_id = ?", orgID)
		if f.Category != "" {
			q = q.Where("auth_events.category = ?", f.Category)
		}
		if f.EventType != "" {
			q = q.Where("auth_events.event_type = ?", f.EventType)
		}
		if f.ActorID != nil {
			q = q.Where("auth_events.actor_id = ?", *f.ActorID)
		}
		if f.From != nil {
			q = q.Where("auth_events.created_at >= ?", *f.From)
		}
		if f.To != nil {
			q = q.Where("auth_events.created_at <= ?", *f.To)
		}
		return q
	}

	var total int64
	if err := apply(r.db.WithContext(ctx).Model(&domain.AuthEvent{})).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	var rows []domain.AuthEventView
	q := apply(r.db.WithContext(ctx).Table("auth_events")).
		Select("auth_events.*, u.full_name AS actor_name, u.email AS actor_email").
		Joins("LEFT JOIN users u ON u.id = auth_events.actor_id").
		Order("auth_events.created_at DESC").
		Limit(limit).Offset(offset)
	if err := q.Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// LatestSessionActivityByUsers returns userID → most recent live-session
// activity for the members-table "Last active" column (U4): one GROUP BY over
// non-revoked, unexpired refresh tokens, using last_used_at when present and
// falling back to created_at. Users with no live session are absent.
func (r *authRepository) LatestSessionActivityByUsers(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]time.Time, error) {
	out := map[uuid.UUID]time.Time{}
	if len(userIDs) == 0 {
		return out, nil
	}
	type row struct {
		UserID   uuid.UUID
		LastSeen time.Time
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Model(&domain.RefreshToken{}).
		Select("user_id, MAX(COALESCE(last_used_at, created_at)) AS last_seen").
		Where("user_id IN ? AND revoked_at IS NULL AND expires_at > ?", userIDs, time.Now()).
		Group("user_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.UserID] = r.LastSeen
	}
	return out, nil
}

func (r *authRepository) ListActiveRefreshTokens(ctx context.Context, userID uuid.UUID) ([]domain.RefreshToken, error) {
	var tokens []domain.RefreshToken
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND revoked_at IS NULL AND expires_at > ?", userID, time.Now()).
		Order("last_used_at DESC NULLS LAST, created_at DESC").
		Find(&tokens).Error
	return tokens, err
}

// RevokeRefreshTokenForUser revokes one refresh token scoped to its owner and
// returns rows affected, so a caller can never revoke another user's session and
// can distinguish "revoked" (1) from "not found / already revoked" (0).
func (r *authRepository) RevokeRefreshTokenForUser(ctx context.Context, id, userID uuid.UUID) (int64, error) {
	now := time.Now()
	res := r.db.WithContext(ctx).
		Model(&domain.RefreshToken{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", id, userID).
		Update("revoked_at", now)
	return res.RowsAffected, res.Error
}
