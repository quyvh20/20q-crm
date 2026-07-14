package usecase

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/png"
	"log"
	"net/http"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

// auth_2fa.go implements TOTP two-factor auth (U6.4): enrollment, the login
// challenge, backup codes, and the org "require 2FA" policy.
//
// The shape of the login flow is the security-critical part. A correct password
// no longer mints a session for an enrolled user — it mints a CHALLENGE, and only
// a correct code exchanges that challenge for tokens. Everything that mints a
// session without a password (invite auto-login, workspace create/switch, refresh)
// is covered by the policy claim + middleware backstop rather than by this file,
// because those paths have no credential to gate on.

// issueTwoFactorChallenge mints the half-authenticated state after a correct
// password/Google sign-in. The raw token is returned once; only its hash is
// stored.
func (uc *authUseCase) issueTwoFactorChallenge(ctx context.Context, userID uuid.UUID) (string, error) {
	raw, err := generateSecureToken()
	if err != nil {
		return "", domain.ErrInternal
	}
	ch := &domain.TwoFactorChallenge{
		UserID:    userID,
		TokenHash: hashToken(raw),
		ExpiresAt: time.Now().Add(domain.TwoFactorChallengeTTL),
	}
	if err := uc.authRepo.CreateTwoFactorChallenge(ctx, ch); err != nil {
		return "", domain.ErrInternal
	}
	return raw, nil
}

// twoFactorGate decides what a successfully-authenticated user gets: a session, a
// code prompt, or a demand to enroll. It is called AFTER the credential check and
// BEFORE any token is minted.
//
//	enrolled                      → challenge (prompt for a code)
//	not enrolled, org requires 2FA → session, but the policy claim marks it
//	                                 unsatisfied and the middleware confines it to
//	                                 the enrollment endpoints
//	otherwise                      → session
//
// The middle case deliberately issues a session rather than blocking: with no
// session there is no authenticated way to REACH the enrollment endpoints, and the
// user would be locked out of the very action the policy is demanding of them.
func (uc *authUseCase) twoFactorChallengeRequired(user *domain.User) bool {
	return user != nil && user.TotpEnabledAt != nil
}

// orgRequiresTwoFactor reports whether the org's policy demands 2FA. A lookup
// failure returns false: an unreachable org row must not lock every member out of
// a workspace they are already members of.
func (uc *authUseCase) orgRequiresTwoFactor(ctx context.Context, orgID uuid.UUID) bool {
	if orgID == uuid.Nil {
		return false
	}
	org, err := uc.authRepo.GetOrganizationByID(ctx, orgID)
	if err != nil || org == nil {
		return false
	}
	return org.RequireTwoFactor
}

// twoFactorUnsatisfied is the value of the JWT's `2fa` claim: true when this
// workspace requires 2FA and this user has not enrolled. The middleware confines
// such a session to the enrollment endpoints.
//
// Evaluated at MINT time, not per request — so it costs one org read per token
// rather than one per API call. The trade-off is a bounded grace: a session minted
// before an admin flipped the policy on keeps its old claim until its access token
// expires (≤2h) and the refresh re-evaluates. Documented, and far cheaper than
// re-reading the policy on every request.
func (uc *authUseCase) twoFactorUnsatisfied(ctx context.Context, user *domain.User, orgID uuid.UUID) bool {
	if user == nil || user.TotpEnabledAt != nil {
		return false
	}
	return uc.orgRequiresTwoFactor(ctx, orgID)
}

// ============================================================
// Enrollment
// ============================================================

// StartTwoFactorSetup generates a secret, stores it UNCONFIRMED (secret set,
// totp_enabled_at still nil), and returns the QR + otpauth URI. 2FA is not on
// until EnableTwoFactor proves the authenticator actually works — otherwise a
// user could lock themselves out by scanning a code that never registered.
func (uc *authUseCase) StartTwoFactorSetup(ctx context.Context, userID uuid.UUID) (*domain.TwoFactorSetup, error) {
	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}
	if user.TotpEnabledAt != nil {
		return nil, domain.NewAppError(http.StatusConflict, "two-factor authentication is already on for this account")
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Guerrilla CRM",
		AccountName: user.Email,
	})
	if err != nil {
		return nil, domain.ErrInternal
	}

	enc, err := encryptTOTPSecret(key.Secret(), uc.cfg.TOTPEncKey, uc.cfg.JWTSecret)
	if err != nil {
		log.Printf("2fa: cannot encrypt secret: %v", err)
		return nil, domain.ErrInternal
	}
	if err := uc.authRepo.SetTOTPSecret(ctx, userID, enc); err != nil {
		return nil, domain.ErrInternal
	}

	// Render the QR server-side so the browser needs no QR library.
	img, err := key.Image(240, 240)
	if err != nil {
		return nil, domain.ErrInternal
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, domain.ErrInternal
	}

	return &domain.TwoFactorSetup{
		Secret:     key.Secret(),
		OtpAuthURL: key.URL(),
		QRDataURI:  "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()),
	}, nil
}

// EnableTwoFactor confirms enrollment: the code must validate against the pending
// secret. It then generates backup codes, which are returned ONCE.
func (uc *authUseCase) EnableTwoFactor(ctx context.Context, userID, orgID uuid.UUID, code string, meta domain.RequestMeta) (*domain.BackupCodesResult, error) {
	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}
	if user.TotpEnabledAt != nil {
		return nil, domain.NewAppError(http.StatusConflict, "two-factor authentication is already on for this account")
	}
	if user.TotpSecret == nil || *user.TotpSecret == "" {
		return nil, domain.NewAppError(http.StatusBadRequest, "start the setup first")
	}

	secret, err := decryptTOTPSecret(*user.TotpSecret, uc.cfg.TOTPEncKey, uc.cfg.JWTSecret)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if !validateTOTP(code, secret) {
		return nil, domain.NewAppError(http.StatusBadRequest, "that code isn't right — check your authenticator app and try again")
	}

	codes, hashes, err := newBackupCodeSet()
	if err != nil {
		return nil, domain.ErrInternal
	}
	if err := uc.authRepo.EnableTOTP(ctx, userID, hashes); err != nil {
		return nil, domain.ErrInternal
	}

	// The caller's access token carries the "2fa pending" claim, evaluated at mint
	// time — so a user who just complied with the workspace policy would otherwise
	// stay confined to the enrollment screen until that token expired (up to 2h).
	// Bumping the token version forces their next request to refresh and re-mint with
	// the claim cleared, so complying takes effect immediately.
	uc.bumpTokenVersion(ctx, userID)

	uc.notifySecurityChange(user, orgID, meta, "two_factor.enabled",
		"Two-factor authentication was turned on",
		"Two-factor authentication was just turned on for your Guerrilla CRM account. If this wasn't you, contact your workspace admin immediately.")

	return &domain.BackupCodesResult{Codes: codes}, nil
}

// DisableTwoFactor turns 2FA off. A current code (TOTP or backup) is required:
// holding a live session is not proof of possessing the second factor, and a
// stolen session must not be able to strip it.
func (uc *authUseCase) DisableTwoFactor(ctx context.Context, userID, orgID uuid.UUID, code string, meta domain.RequestMeta) error {
	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return domain.ErrUserNotFound
	}
	if user.TotpEnabledAt == nil {
		return domain.NewAppError(http.StatusBadRequest, "two-factor authentication is not on for this account")
	}
	// The workspace policy outranks the individual: if the org requires 2FA, a
	// member cannot opt out of it.
	if uc.orgRequiresTwoFactor(ctx, orgID) {
		return domain.NewAppError(http.StatusForbidden, "this workspace requires two-factor authentication, so it can't be turned off")
	}
	ok, err := uc.consumeSecondFactor(ctx, user, code)
	if err != nil {
		return err
	}
	if !ok {
		return domain.NewAppError(http.StatusBadRequest, "that code isn't right")
	}
	if err := uc.authRepo.DisableTOTP(ctx, userID); err != nil {
		return domain.ErrInternal
	}

	uc.notifySecurityChange(user, orgID, meta, "two_factor.disabled",
		"Two-factor authentication was turned off",
		"Two-factor authentication was just turned off for your Guerrilla CRM account. If this wasn't you, turn it back on and change your password immediately.")
	return nil
}

// RegenerateBackupCodes replaces the whole set (any unused old codes die). Proof
// of the second factor is required, for the same reason as Disable.
func (uc *authUseCase) RegenerateBackupCodes(ctx context.Context, userID, orgID uuid.UUID, code string, meta domain.RequestMeta) (*domain.BackupCodesResult, error) {
	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}
	if user.TotpEnabledAt == nil {
		return nil, domain.NewAppError(http.StatusBadRequest, "two-factor authentication is not on for this account")
	}
	ok, err := uc.consumeSecondFactor(ctx, user, code)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, domain.NewAppError(http.StatusBadRequest, "that code isn't right")
	}
	codes, hashes, err := newBackupCodeSet()
	if err != nil {
		return nil, domain.ErrInternal
	}
	if err := uc.authRepo.ReplaceBackupCodes(ctx, userID, hashes); err != nil {
		return nil, domain.ErrInternal
	}
	uc.notifySecurityChange(user, orgID, meta, "two_factor.backup_regenerated",
		"Your backup codes were regenerated",
		"A new set of two-factor backup codes was just generated for your Guerrilla CRM account. Your previous codes no longer work. If this wasn't you, contact your workspace admin immediately.")
	return &domain.BackupCodesResult{Codes: codes}, nil
}

// GetTwoFactorStatus backs the Security settings panel.
func (uc *authUseCase) GetTwoFactorStatus(ctx context.Context, userID, orgID uuid.UUID) (*domain.TwoFactorStatus, error) {
	user, err := uc.authRepo.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, domain.ErrUserNotFound
	}
	left := 0
	if user.TotpEnabledAt != nil {
		if n, err := uc.authRepo.CountBackupCodesRemaining(ctx, userID); err == nil {
			left = n
		}
	}
	return &domain.TwoFactorStatus{
		Enabled:             user.TotpEnabledAt != nil,
		EnabledAt:           user.TotpEnabledAt,
		BackupCodesLeft:     left,
		RequiredByWorkspace: uc.orgRequiresTwoFactor(ctx, orgID),
	}, nil
}

// ResetMemberTwoFactor is the admin break-glass: clear a member's 2FA so they can
// re-enroll after losing their device AND their backup codes. members.manage-gated
// at the route, and audited — it is, by construction, an admin removing someone
// else's second factor.
func (uc *authUseCase) ResetMemberTwoFactor(ctx context.Context, orgID, actorID, targetUserID uuid.UUID, meta domain.RequestMeta) error {
	// An admin may not reset their OWN second factor here: this endpoint deliberately
	// takes no code, so a self-reset would be a way to strip 2FA from a stolen session
	// without ever proving possession of the factor — which is exactly what
	// DisableTwoFactor demands. Break-glass is for OTHER people.
	if targetUserID == actorID {
		return domain.NewAppError(http.StatusForbidden,
			"to turn off your own two-factor authentication, use your security settings — it needs a code")
	}
	ou, err := uc.authRepo.GetOrgUser(ctx, targetUserID, orgID)
	if err != nil {
		return domain.ErrInternal
	}
	if ou == nil {
		return domain.NewAppError(http.StatusNotFound, "that person is not a member of this workspace")
	}
	if err := uc.authRepo.DisableTOTP(ctx, targetUserID); err != nil {
		return domain.ErrInternal
	}
	// Their live sessions were minted when they WERE enrolled. Bump the token version
	// so those sessions re-mint and pick up the correct state (and, in a workspace that
	// requires 2FA, get confined to re-enrolling) instead of running on for hours.
	uc.bumpTokenVersion(ctx, targetUserID)
	target := targetUserID
	actor := actorID
	org := orgID
	user := ou.User
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if user != nil {
			if err := uc.mailer.SendSecurityAlert(bg, user.Email,
				"Two-factor authentication was reset by an admin",
				"An administrator of your workspace has reset two-factor authentication on your Guerrilla CRM account. You'll be asked to set it up again next time you sign in. If you didn't request this, contact your admin.",
			); err != nil {
				log.Printf("2fa reset: failed to alert %s: %v", user.Email, err)
			}
		}
		uc.recordAuthEvent(bg, "security", "two_factor.reset_by_admin", &org, &actor, &target, meta, nil)
	}()
	return nil
}

// ============================================================
// The login challenge
// ============================================================

// VerifyTwoFactor exchanges a login challenge + a code for a real session. It is
// the ONLY way an enrolled user gets tokens.
func (uc *authUseCase) VerifyTwoFactor(ctx context.Context, challengeToken, code string, meta domain.RequestMeta) (*domain.AuthResponse, error) {
	if challengeToken == "" {
		return nil, domain.NewAppError(http.StatusBadRequest, "your sign-in session expired — please sign in again")
	}
	ch, err := uc.authRepo.GetTwoFactorChallengeByHash(ctx, hashToken(challengeToken))
	if err != nil {
		return nil, domain.ErrInternal
	}
	if ch == nil || ch.UsedAt != nil || time.Now().After(ch.ExpiresAt) {
		return nil, domain.NewAppError(http.StatusUnauthorized, "your sign-in session expired — please sign in again")
	}
	// A 6-digit code is guessable at scale, and the per-IP limiter fails open when
	// there is no Redis — so the per-challenge counter is the bound that has to hold
	// unconditionally. Spend the attempt ATOMICALLY, before validating the code: a
	// read-then-check against ch.Attempts lets N concurrent requests all pass the same
	// snapshot, turning a 5-try cap into 5-tries-per-request.
	claimed, err := uc.authRepo.ClaimChallengeAttempt(ctx, ch.ID, domain.MaxTwoFactorAttempts)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if !claimed {
		_, _ = uc.authRepo.ConsumeTwoFactorChallenge(ctx, ch.ID)
		uc.recordAuthEvent(ctx, "security", "two_factor.locked", nil, nil, &ch.UserID, meta, nil)
		return nil, domain.NewAppError(http.StatusTooManyRequests, "too many incorrect codes — please sign in again")
	}

	user, err := uc.authRepo.GetUserByID(ctx, ch.UserID)
	if err != nil || user == nil {
		return nil, domain.ErrInvalidCredentials
	}

	ok, err := uc.consumeSecondFactor(ctx, user, code)
	if err != nil {
		return nil, err
	}
	if !ok {
		uc.recordAuthEvent(ctx, "auth", "two_factor.failed", orgPtr(user.OrgID), nil, &user.ID, meta, nil)
		return nil, domain.NewAppError(http.StatusUnauthorized, "that code isn't right")
	}

	// Single use: burn the challenge before minting anything, and only mint if THIS
	// request was the one that burned it — two concurrent verifies holding the same
	// valid challenge must not both walk away with a session.
	burned, err := uc.authRepo.ConsumeTwoFactorChallenge(ctx, ch.ID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if !burned {
		return nil, domain.NewAppError(http.StatusUnauthorized, "your sign-in session expired — please sign in again")
	}

	return uc.completeLogin(ctx, user, nil, meta)
}

// consumeSecondFactor validates a TOTP code or burns a backup code. The backup
// path is atomic (UPDATE ... WHERE used_at IS NULL), so the same code can never be
// spent twice, even by two concurrent requests.
func (uc *authUseCase) consumeSecondFactor(ctx context.Context, user *domain.User, code string) (bool, error) {
	if user.TotpSecret != nil && *user.TotpSecret != "" {
		secret, err := decryptTOTPSecret(*user.TotpSecret, uc.cfg.TOTPEncKey, uc.cfg.JWTSecret)
		if err == nil && validateTOTP(code, secret) {
			return true, nil
		}
	}

	// Backup code: bcrypt hashes can't be looked up by value, so each unused hash is
	// compared. There are at most 10, and this path is rate-limited by the challenge
	// attempt counter.
	hashes, err := uc.authRepo.ListUnusedBackupCodes(ctx, user.ID)
	if err != nil {
		return false, domain.ErrInternal
	}
	candidate := normalizeBackupCode(code)
	if candidate == "" {
		return false, nil
	}
	for _, bc := range hashes {
		if bcrypt.CompareHashAndPassword([]byte(bc.CodeHash), []byte(candidate)) == nil {
			used, err := uc.authRepo.ConsumeBackupCode(ctx, bc.ID)
			if err != nil {
				return false, domain.ErrInternal
			}
			return used, nil // false ⇒ a concurrent request already spent it
		}
	}
	return false, nil
}

// validateTOTP checks a code with ±1 time step of skew (±30s), which absorbs the
// ordinary clock drift between a phone and a server without meaningfully widening
// the window.
func validateTOTP(code, secret string) bool {
	ok, err := totp.ValidateCustom(code, secret, time.Now(), totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}

// newBackupCodeSet returns the plaintext codes (shown once) and their bcrypt
// hashes (stored).
func newBackupCodeSet() ([]string, []string, error) {
	codes, err := generateBackupCodes()
	if err != nil {
		return nil, nil, err
	}
	hashes := make([]string, 0, len(codes))
	for _, c := range codes {
		h, err := bcrypt.GenerateFromPassword([]byte(normalizeBackupCode(c)), bcryptCost)
		if err != nil {
			return nil, nil, err
		}
		hashes = append(hashes, string(h))
	}
	return codes, hashes, nil
}

// notifySecurityChange fires the alert email + audit row for a 2FA change on a
// detached context, so a slow mailer can't fail the request that already committed.
func (uc *authUseCase) notifySecurityChange(user *domain.User, orgID uuid.UUID, meta domain.RequestMeta, event, subject, body string) {
	email := user.Email
	target := user.ID
	org := orgPtr(orgID)
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := uc.mailer.SendSecurityAlert(bg, email, subject, body); err != nil {
			log.Printf("%s: failed to send security alert to %s: %v", event, email, err)
		}
		uc.recordAuthEvent(bg, "security", event, org, &target, &target, meta, nil)
	}()
}
