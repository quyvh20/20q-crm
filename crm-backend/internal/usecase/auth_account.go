package usecase

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// My Account self-service (U2): profile editing, in-app password change/set,
// and Google unlink. These are the first self-serve writes to the users row —
// before this, a typo'd name at signup was permanent without a DB console.

// UpdateProfile edits the caller's own identity + preferences. Pointer fields
// nil = unchanged. Email is deliberately not editable here (needs a
// re-verification flow); FullName is kept derived from first + last.
func (uc *authUseCase) UpdateProfile(ctx context.Context, userID uuid.UUID, input domain.UpdateProfileInput) (*domain.User, error) {
	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}

	if input.FirstName != nil {
		v := strings.TrimSpace(*input.FirstName)
		if v == "" {
			return nil, domain.NewAppError(http.StatusBadRequest, "first name can't be empty")
		}
		if len(v) > 100 {
			return nil, domain.NewAppError(http.StatusBadRequest, "first name must be at most 100 characters")
		}
		user.FirstName = v
	}
	if input.LastName != nil {
		v := strings.TrimSpace(*input.LastName)
		if len(v) > 100 {
			return nil, domain.NewAppError(http.StatusBadRequest, "last name must be at most 100 characters")
		}
		user.LastName = v
	}
	user.FullName = strings.TrimSpace(user.FirstName + " " + user.LastName)

	if input.AvatarURL != nil {
		v := strings.TrimSpace(*input.AvatarURL)
		if v == "" {
			user.AvatarURL = nil
		} else {
			// http(s) only: the value lands in an <img src>, so reject exotic
			// schemes outright instead of trusting every client.
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				return nil, domain.NewAppError(http.StatusBadRequest, "avatar_url must be an http(s) URL")
			}
			user.AvatarURL = &v
		}
	}

	if input.Timezone != nil {
		v := strings.TrimSpace(*input.Timezone)
		if v != "" {
			if _, err := time.LoadLocation(v); err != nil {
				return nil, domain.NewAppError(http.StatusBadRequest, "timezone must be a valid IANA name (e.g. Asia/Saigon)")
			}
		}
		user.Timezone = v
	}
	if input.Locale != nil {
		v := strings.TrimSpace(*input.Locale)
		// Column is varchar(16) — reject over-length here with a 400 rather than
		// letting Postgres turn it into an opaque 500.
		if len(v) > 16 {
			return nil, domain.NewAppError(http.StatusBadRequest, "locale must be a BCP-47 tag like en-US")
		}
		user.Locale = v
	}
	if input.OnboardingCompleted != nil {
		user.OnboardingCompleted = *input.OnboardingCompleted
	}

	// Column-scoped write (not the whole-row UpdateUser) so a profile save can't
	// revert a concurrent token_version bump / password change (U2 review).
	if err := uc.authRepo.UpdateUserProfile(ctx, user); err != nil {
		return nil, domain.ErrInternal
	}
	return user, nil
}

// ChangePassword rotates the password for a signed-in user: the CURRENT
// password is required (an unattended session must not be enough to lock the
// real owner out), then every other device is signed out and this one is
// re-minted — same posture as a public reset, minus the email round-trip.
func (uc *authUseCase) ChangePassword(ctx context.Context, userID, orgID uuid.UUID, input domain.ChangePasswordInput, meta domain.RequestMeta) (*domain.AuthResponse, error) {
	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}
	if user.PasswordHash == nil {
		return nil, domain.NewAppError(http.StatusConflict, "this account has no password yet — use 'Set a password' instead")
	}
	if bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(input.CurrentPassword)) != nil {
		return nil, domain.NewAppError(http.StatusForbidden, "current password is incorrect")
	}
	return uc.applyNewPassword(ctx, user, orgID, input.NewPassword, meta, "password.changed")
}

// SetPassword adds a password to an OAuth-only account (allowed ONLY while no
// password exists) so a Google-only user can survive losing Google access.
// The live authenticated session is the proof of ownership here.
func (uc *authUseCase) SetPassword(ctx context.Context, userID, orgID uuid.UUID, input domain.SetPasswordInput, meta domain.RequestMeta) (*domain.AuthResponse, error) {
	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}
	if user.PasswordHash != nil {
		return nil, domain.NewAppError(http.StatusConflict, "this account already has a password — use 'Change password' (it requires the current one)")
	}
	return uc.applyNewPassword(ctx, user, orgID, input.NewPassword, meta, "password.set")
}

// applyNewPassword is the shared tail of ChangePassword/SetPassword: policy
// check, hash, persist, then the SignOutEverywhere re-mint (revoke all refresh
// tokens + bump token_version + fresh session for THIS device), a security
// alert, and an audit row.
func (uc *authUseCase) applyNewPassword(ctx context.Context, user *domain.User, orgID uuid.UUID, newPassword string, meta domain.RequestMeta, eventType string) (*domain.AuthResponse, error) {
	if err := validatePassword(newPassword); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return nil, domain.ErrInternal
	}
	hashStr := string(hash)
	user.PasswordHash = &hashStr
	if err := uc.authRepo.UpdateUser(ctx, user); err != nil {
		return nil, domain.ErrInternal
	}

	// A personal access token is a long-lived credential minted from this account, and
	// the token_version bump below does NOT reach it (a token is not a JWT). Re-securing
	// an account while leaving its tokens working would defeat the point of the reset.
	if n, err := uc.authRepo.RevokeAllUserAPITokens(ctx, user.ID); err != nil {
		log.Printf("%s: failed to revoke API tokens for %s: %v", eventType, user.ID, err)
	} else if n > 0 {
		log.Printf("%s: revoked %d API token(s) for %s", eventType, n, user.ID)
	}

	// The password is now committed. Fire the alert + audit BEFORE the sign-out
	// re-mint so a transient failure there can't swallow the notification the
	// user must get about their own credential change (mirrors ResetPassword's
	// commit-then-side-effects posture; U2 review).
	subject := "Your password was changed"
	body := "Your Guerrilla CRM password was just changed from your account settings. If this was you, no action is needed. If not, reset your password immediately and contact your workspace admin."
	if eventType == "password.set" {
		subject = "A password was added to your account"
		body = "A password was just added to your Guerrilla CRM account, so you can now sign in with email and password. If this wasn't you, reset your password immediately and contact your workspace admin."
	}
	email := user.Email
	targetID := user.ID
	org := orgPtr(user.OrgID)
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := uc.mailer.SendSecurityAlert(bg, email, subject, body); err != nil {
			log.Printf("%s: failed to send security alert to %s: %v", eventType, email, err)
		}
		uc.recordAuthEvent(bg, "security", eventType, org, &targetID, &targetID, meta, nil)
	}()

	resp, err := uc.SignOutEverywhere(ctx, user.ID, orgID, "")
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// UnlinkGoogle disconnects Google sign-in. Refused while the account has no
// password — unlinking then would strand it with zero sign-in methods.
func (uc *authUseCase) UnlinkGoogle(ctx context.Context, userID uuid.UUID, meta domain.RequestMeta) error {
	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return domain.ErrUserNotFound
	}
	if user.GoogleID == nil {
		return domain.NewAppError(http.StatusConflict, "this account isn't linked to Google")
	}
	if user.PasswordHash == nil {
		return domain.NewAppError(http.StatusConflict, "set a password first — unlinking Google now would leave you with no way to sign in")
	}
	user.GoogleID = nil
	if err := uc.authRepo.UpdateUser(ctx, user); err != nil {
		return domain.ErrInternal
	}
	// Alert the owner: unlinking removes a sign-in method, and a stolen token
	// could chain set-password → unlink into a takeover, so this must be as
	// visible as the password changes (U2 review).
	email := user.Email
	targetID := user.ID
	org := orgPtr(user.OrgID)
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := uc.mailer.SendSecurityAlert(bg, email, "Google sign-in disconnected",
			"Google sign-in was just disconnected from your Guerrilla CRM account. If this wasn't you, reset your password immediately and contact your workspace admin."); err != nil {
			log.Printf("google.unlinked: failed to send security alert to %s: %v", email, err)
		}
		uc.recordAuthEvent(bg, "security", "google.unlinked", org, &targetID, &targetID, meta, nil)
	}()
	return nil
}
