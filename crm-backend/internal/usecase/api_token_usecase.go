package usecase

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// apiTokenUseCase mints, lists and revokes personal access tokens (U6.5).
//
// The one rule that matters: a token can never do more than the person who made
// it. That is enforced twice — here, by refusing to mint a scope the creator's own
// role doesn't hold, and again at request time, where the middleware intersects the
// token's scopes with the role's capabilities. Minting-time validation alone would
// be a trap: roles change, and a token minted by an admin who was later demoted
// must not keep its old reach.
type apiTokenUseCase struct {
	repo   domain.APITokenRepository
	caps   domain.CapabilityChecker
	events domain.AuthEventWriter
}

func NewAPITokenUseCase(repo domain.APITokenRepository, caps domain.CapabilityChecker, events domain.AuthEventWriter) domain.APITokenUseCase {
	return &apiTokenUseCase{repo: repo, caps: caps, events: events}
}

func (uc *apiTokenUseCase) List(ctx context.Context, orgID, userID uuid.UUID) ([]domain.APIToken, error) {
	tokens, err := uc.repo.ListByUser(ctx, orgID, userID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	return tokens, nil
}

func (uc *apiTokenUseCase) Create(ctx context.Context, orgID, userID uuid.UUID, in domain.CreateAPITokenInput) (*domain.CreatedAPIToken, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, domain.NewAppError(http.StatusBadRequest, "give the token a name so you can recognize it later")
	}
	if len(in.Scopes) == 0 {
		return nil, domain.NewAppError(http.StatusBadRequest, "pick at least one thing this token may do")
	}

	// Validate every scope, and refuse any the creator's own role does not hold.
	// Without this check a viewer could mint themselves an admin token.
	seen := map[string]bool{}
	scopes := make([]string, 0, len(in.Scopes))
	for _, s := range in.Scopes {
		if !domain.IsAPITokenScope(s) {
			return nil, domain.NewAppError(http.StatusBadRequest, "unknown permission: "+s)
		}
		if seen[s] {
			continue
		}
		// records.read is a token-only scope with no role capability behind it —
		// reading records is governed by OLS, which still applies. Every OTHER scope
		// must be one the creator's own role actually holds, or a viewer could mint
		// themselves an admin token.
		if uc.caps != nil && s != domain.ScopeRecordsRead {
			// HasCapability reads the caller off the context and returns a 403 AppError
			// when they lack it (nil for the owner role, which holds everything).
			if err := uc.caps.HasCapability(ctx, orgID, s); err != nil {
				return nil, domain.NewAppError(http.StatusForbidden,
					"you can't give a token a permission you don't have yourself: "+domain.CapabilityLabel(s))
			}
		}
		seen[s] = true
		scopes = append(scopes, s)
	}

	live, err := uc.repo.CountLiveByUser(ctx, orgID, userID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if live >= domain.MaxAPITokensPerUser {
		return nil, domain.NewAppError(http.StatusConflict, "you've reached the limit for API tokens — revoke one first")
	}

	secret, err := generateAPITokenSecret()
	if err != nil {
		return nil, domain.ErrInternal
	}

	days := domain.DefaultAPITokenDays
	if in.ExpiresInDays != nil {
		days = *in.ExpiresInDays
	}
	var expiresAt *time.Time
	if days > 0 {
		t := time.Now().AddDate(0, 0, days)
		expiresAt = &t
	}
	// days <= 0 means "never expires" — allowed, but it is the caller's explicit
	// choice, not the default.

	tok := &domain.APIToken{
		OrgID:     orgID,
		UserID:    userID,
		Name:      name,
		TokenHash: hashToken(secret),
		Prefix:    apiTokenPrefixOf(secret),
		Scopes:    scopes,
		ExpiresAt: expiresAt,
	}
	if err := uc.repo.Create(ctx, tok); err != nil {
		return nil, domain.ErrInternal
	}

	recordSecurityEvent(ctx, uc.events, orgID, "apitoken.created", &userID, map[string]interface{}{
		"token_id": tok.ID.String(),
		"name":     tok.Name,
		"scopes":   scopes,
	})

	// The secret is returned here and never again — only its hash was stored.
	return &domain.CreatedAPIToken{Token: *tok, Secret: secret}, nil
}

func (uc *apiTokenUseCase) Revoke(ctx context.Context, orgID, userID, id uuid.UUID) error {
	n, err := uc.repo.Revoke(ctx, orgID, userID, id)
	if err != nil {
		return domain.ErrInternal
	}
	if n == 0 {
		return domain.NewAppError(http.StatusNotFound, "token not found")
	}
	recordSecurityEvent(ctx, uc.events, orgID, "apitoken.revoked", &userID, map[string]interface{}{
		"token_id": id.String(),
	})
	return nil
}

// generateAPITokenSecret mints the credential: a prefix the middleware forks on,
// plus 32 bytes of entropy.
func generateAPITokenSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return domain.APITokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// apiTokenPrefixOf is the display hint stored alongside the hash — enough to tell
// two tokens apart in a list, far too little to be a credential.
func apiTokenPrefixOf(secret string) string {
	const shown = 4
	body := strings.TrimPrefix(secret, domain.APITokenPrefix)
	if len(body) > shown {
		body = body[:shown]
	}
	return domain.APITokenPrefix + body + "…"
}

// HashAPIToken is the authentication probe's hash function, exported so the auth
// middleware can hash the presented secret without importing the token minting
// logic. SHA-256, not bcrypt: this runs on every API request, and a 256-bit random
// secret has no entropy problem a slow KDF would fix.
func HashAPIToken(secret string) string { return hashToken(secret) }
