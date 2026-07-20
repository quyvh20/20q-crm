package integrations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"crm-backend/internal/domain"
	"crm-backend/internal/integrations/envelope"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// ConnectionService orchestrates the provider OAuth connect flow (L5.1):
// initiate → provider redirect → callback custody → account selection → an
// encrypted connection row. It is the one place the envelope codec is used, so
// the crypto decisions (what is bound to what) live in a single file.
//
// The actor rule for the whole flow: org and user are taken from the SERVER-side
// state and pending rows, never from the callback request (which arrives with no
// session) and re-verified against the authenticated caller at account
// selection. That is the U4 capture-vulnerability lesson applied to provider
// connect.
type ConnectionService struct {
	repo     *Repository
	codec    *envelope.Codec
	registry *Registry
	// publicBaseURL is the origin providers redirect back to — the OAuth
	// redirect_uri. Config-derived (PUBLIC_API_BASE_URL), never c.Request.Host: it
	// must byte-match what is registered in the provider console.
	publicBaseURL string
	// frontendBaseURL is where the browser lands after the callback (the account
	// picker, or an error). Config-derived (FRONTEND_URL).
	frontendBaseURL string
	logger          *slog.Logger
}

const (
	// oauthStateTTL / pendingTTL bound the two custody windows. The provider round
	// trip is seconds; ten minutes tolerates a slow consent screen without leaving
	// a replayable artifact around for long.
	oauthStateTTL = 10 * time.Minute
	pendingTTL    = 10 * time.Minute
)

// NewConnectionService builds the service. A nil codec is tolerated (the routes
// answer 503 with an actionable message) so a deployment without
// INTEGRATION_ENC_KEY still boots.
func NewConnectionService(repo *Repository, codec *envelope.Codec, registry *Registry, publicBaseURL, frontendBaseURL string, logger *slog.Logger) *ConnectionService {
	return &ConnectionService{
		repo:            repo,
		codec:           codec,
		registry:        registry,
		publicBaseURL:   strings.TrimRight(publicBaseURL, "/"),
		frontendBaseURL: strings.TrimRight(frontendBaseURL, "/"),
		logger:          logger,
	}
}

// redirectURI is the provider callback URL for a provider. Derived from
// publicBaseURL so it survives the Cloudflare proxy that strips Host — a
// host-derived URL would render the wrong origin and break every registered
// callback.
//
// The `providers/` segment is load-bearing and must byte-match the registered
// route (connection_handlers.go mounts the callback at
// /api/integrations/providers/:provider/callback, under a STATIC providers/
// prefix so it cannot collide with the sources/connections/pending siblings in
// gin's tree). Omitting it here would build a redirect_uri that resolves to NO
// route — every provider callback would 404 after consent, and the failure is
// invisible until a real adapter is registered.
func (s *ConnectionService) redirectURI(provider string) string {
	return s.publicBaseURL + "/api/integrations/providers/" + provider + "/callback"
}

// PickerRedirect is where the browser goes after a successful callback: the
// integrations page, told to open the account picker for this pending selection.
//
// The selection token rides in the FRAGMENT (mirroring the Google-login
// precedent) so this REDIRECT leg keeps it out of the frontend's Referer header
// and browser history. It does NOT keep the token out of logs entirely — the
// picker then sends it back in an authenticated API path — and that is an
// accepted trade: the token is single-use, expires in ten minutes, and is scoped
// to the owning org AND user, so it is useless to anyone who does not also hold
// the victim's session (who would need no token at all). The fragment is the
// cheap win on the one leg where it helps; the token's own properties are what
// actually bound it.
func (s *ConnectionService) PickerRedirect(provider, selectionToken string) string {
	return s.frontendBaseURL + "/settings/integrations?connect=" + provider + "#selection=" + selectionToken
}

// ErrorRedirect is where the browser goes when the callback fails. The reason is
// a short machine code, never a raw error string (which could carry provider
// detail or, worse, fragments of a token).
func (s *ConnectionService) ErrorRedirect(reason string) string {
	return s.frontendBaseURL + "/settings/integrations?connect_error=" + reason
}

// StartConnect begins a provider connect: it persists a single-use state (and a
// PKCE verifier when the provider uses one) and returns the provider consent URL
// for a full-page redirect.
func (s *ConnectionService) StartConnect(ctx context.Context, orgID, userID uuid.UUID, provider, returnTo string) (string, error) {
	p, ok := s.registry.Get(provider)
	if !ok {
		return "", domain.NewAppError(http.StatusNotFound, "unknown provider: "+provider)
	}
	// Every provider stores an encrypted credential at the end of this flow, so a
	// missing codec is a hard stop at the START — better than exchanging a real
	// token and then discovering we cannot store it, stranding a grant.
	if !s.codec.Configured() {
		return "", domain.NewAppError(http.StatusServiceUnavailable,
			"provider connections are not configured on this deployment (INTEGRATION_ENC_KEY is unset)")
	}

	statePlain, stateHash, err := randToken()
	if err != nil {
		return "", err
	}

	// The state row id is generated in Go, not by the DB default, because the PKCE
	// verifier is sealed BOUND to it and the binding needs the id before the insert.
	row := &IntegrationOAuthState{
		ID:        uuid.New(),
		StateHash: stateHash,
		OrgID:     orgID,
		UserID:    userID,
		Provider:  provider,
		ReturnTo:  safeReturnTo(returnTo),
		ExpiresAt: time.Now().Add(oauthStateTTL),
	}

	challenge := ""
	if p.Info().UsesPKCE {
		verifier, ch, perr := pkcePair()
		if perr != nil {
			return "", perr
		}
		sealed, serr := s.codec.SealString(envelope.Binding{OrgID: orgID, Purpose: envelope.PurposeOAuthCodeVerifier, ID: row.ID}, verifier)
		if serr != nil {
			return "", serr
		}
		row.CodeVerifier = &sealed
		row.KeyVersion = s.codec.PrimaryVersion()
		challenge = ch
	}

	if err := s.repo.CreateOAuthState(ctx, row); err != nil {
		return "", err
	}
	return p.AuthURL(statePlain, s.redirectURI(provider), challenge), nil
}

// HandleCallback consumes the state, exchanges the code with the provider, and
// stores the exchanged accounts in pending custody. It returns the single-use
// selection token the browser carries to the account picker.
//
// It authenticates entirely from the state row: there is no session on a provider
// callback, and org/user come from the row the connect step wrote — never from
// the request.
func (s *ConnectionService) HandleCallback(ctx context.Context, provider, code, state string) (selectionToken string, err error) {
	if !s.codec.Configured() {
		return "", domain.NewAppError(http.StatusServiceUnavailable, "provider connections are not configured")
	}
	p, ok := s.registry.Get(provider)
	if !ok {
		return "", domain.NewAppError(http.StatusNotFound, "unknown provider")
	}
	if code == "" || state == "" {
		return "", domain.NewAppError(http.StatusBadRequest, "missing code or state")
	}

	stateRow, err := s.repo.ConsumeOAuthState(ctx, hashToken(state))
	if err != nil {
		return "", err
	}
	if stateRow == nil {
		// Unknown, already consumed, or expired — indistinguishable on purpose, and
		// all mean the same thing: do not proceed.
		return "", domain.NewAppError(http.StatusBadRequest, "invalid or expired state")
	}
	// A state minted for one provider must not be spent on another's callback.
	if stateRow.Provider != provider {
		return "", domain.NewAppError(http.StatusBadRequest, "state does not match this provider")
	}

	verifier := ""
	if stateRow.CodeVerifier != nil && *stateRow.CodeVerifier != "" {
		v, verr := s.codec.OpenString(envelope.Binding{OrgID: stateRow.OrgID, Purpose: envelope.PurposeOAuthCodeVerifier, ID: stateRow.ID}, *stateRow.CodeVerifier)
		if verr != nil {
			return "", domain.NewAppError(http.StatusInternalServerError, "could not read the PKCE verifier")
		}
		verifier = v
	}

	accounts, err := p.ExchangeCallback(ctx, code, s.redirectURI(provider), verifier)
	if err != nil {
		s.logf("integrations: provider token exchange failed", "provider", provider, "error", err)
		return "", domain.NewAppError(http.StatusBadGateway, "the provider rejected the connection")
	}
	if len(accounts) == 0 {
		return "", domain.NewAppError(http.StatusUnprocessableEntity, "the provider returned no connectable accounts")
	}

	// Pending row id first, so the sealed accounts blob binds to it.
	pendingID := uuid.New()
	blob, err := json.Marshal(accounts)
	if err != nil {
		return "", err
	}
	sealed, err := s.codec.SealString(envelope.Binding{OrgID: stateRow.OrgID, Purpose: envelope.PurposePendingToken, ID: pendingID}, string(blob))
	if err != nil {
		return "", err
	}
	choices, err := json.Marshal(choicesOf(accounts))
	if err != nil {
		return "", err
	}

	selPlain, selHash, err := randToken()
	if err != nil {
		return "", err
	}
	pending := &IntegrationPendingConnection{
		ID:                 pendingID,
		OrgID:              stateRow.OrgID,
		UserID:             stateRow.UserID,
		Provider:           provider,
		EncryptedToken:     sealed,
		KeyVersion:         s.codec.PrimaryVersion(),
		CandidateAccounts:  datatypes.JSON(choices),
		SelectionTokenHash: selHash,
		ExpiresAt:          time.Now().Add(pendingTTL),
	}
	if err := s.repo.CreatePendingConnection(ctx, pending); err != nil {
		return "", err
	}
	return selPlain, nil
}

// Candidates returns the token-free account choices for a pending selection,
// after verifying the caller owns it. Reads without consuming — the picker may be
// reloaded, and only the select POST spends the token.
func (s *ConnectionService) Candidates(ctx context.Context, orgID, userID uuid.UUID, selectionToken string) (string, []AccountChoice, error) {
	// The custody check lives IN the scoped peek: only the org AND user that
	// started the flow can read the candidates, so a leaked selection token cannot
	// surface another workspace's candidate pages.
	pending, err := s.repo.PeekPendingConnection(ctx, hashToken(selectionToken), orgID, userID)
	if err != nil {
		return "", nil, err
	}
	if pending == nil {
		return "", nil, domain.NewAppError(http.StatusNotFound, "this connection request has expired — start again")
	}
	var choices []AccountChoice
	if err := json.Unmarshal(pending.CandidateAccounts, &choices); err != nil {
		return "", nil, domain.NewAppError(http.StatusInternalServerError, "could not read the candidate accounts")
	}
	return pending.Provider, choices, nil
}

// SelectAccount promotes one candidate to a stored connection, re-sealing its
// credentials bound to the new connection row and enforcing the exclusive
// page->workspace claim.
func (s *ConnectionService) SelectAccount(ctx context.Context, orgID, userID uuid.UUID, selectionToken, accountID string) (*IntegrationConnection, error) {
	if !s.codec.Configured() {
		return nil, domain.NewAppError(http.StatusServiceUnavailable, "provider connections are not configured")
	}
	hash := hashToken(selectionToken)

	// PEEK first, scoped to the owner. The token is single-use and precious: it is
	// the only key to a grant that cost a full OAuth round trip. So every failure
	// that is the CALLER's mistake — a bad account id, a page already claimed by
	// another workspace — is detected here, on a peek, WITHOUT consuming the token,
	// so the admin can pick a different page from the SAME picker instead of being
	// forced back through consent. The token is spent (consumed) only once we are
	// committed to writing.
	pending, err := s.repo.PeekPendingConnection(ctx, hash, orgID, userID)
	if err != nil {
		return nil, err
	}
	if pending == nil {
		// Unknown, expired, already consumed, or owned by someone else — all
		// indistinguishable, deliberately: telling a caller "right token, wrong user"
		// is an oracle a leaked token should not get.
		return nil, domain.NewAppError(http.StatusNotFound, "this connection request has expired — start again")
	}

	chosen, err := s.chosenAccount(pending, accountID)
	if err != nil {
		return nil, err // bad account id — token NOT consumed, the picker still works
	}

	// Pre-check the claim on a peek, so a page held by another workspace is reported
	// without burning the token. This is a friendly early-out only; the partial
	// unique index in upsertConnection is the authoritative guard against a race.
	if reserr := s.preflightClaim(ctx, orgID, pending.Provider, chosen.ID); reserr != nil {
		return nil, reserr
	}

	// Committed to writing: consume the token now (atomic single-use, scoped to the
	// owner), then upsert.
	consumed, err := s.repo.ConsumePendingConnection(ctx, hash, orgID, userID)
	if err != nil {
		return nil, err
	}
	if consumed == nil {
		// Raced with a concurrent select (or it expired in the window). Idempotent to
		// the caller: retrying starts a fresh flow.
		return nil, domain.NewAppError(http.StatusConflict, "this connection request was just completed elsewhere — reload to see the connection")
	}

	conn, err := s.upsertConnection(ctx, orgID, userID, consumed.Provider, *chosen)
	if err != nil {
		return nil, err
	}
	// A same-org reconnect re-reads the row after refreshing it, and that read can
	// come back nil if a concurrent Disconnect soft-deleted the row in the window
	// between the refresh and the re-read (the refresh UPDATE matches 0 rows without
	// erroring). Guard it: activateDelivery below and the handler's ViewOfConnection
	// would both dereference a nil conn and panic the request.
	if conn == nil {
		return nil, domain.NewAppError(http.StatusConflict, "this connection was just changed elsewhere — reload to see the current state")
	}
	// Activate delivery (Facebook: subscribe the page to leadgen). A failure here
	// does NOT undo the connection — the credential is stored and the page is
	// connected — but it IS recorded so the card can warn "connected, not receiving
	// leads yet" and a reconnect retries. Re-read so the returned view reflects the
	// subscription flag just written.
	if p, ok := s.registry.Get(consumed.Provider); ok {
		s.activateDelivery(ctx, p, conn, chosen.Credentials)
		if fresh, gerr := s.repo.GetConnection(ctx, orgID, conn.ID); gerr == nil && fresh != nil {
			conn = fresh
		}
	}
	return conn, nil
}

// activateDelivery subscribes a webhook-capable provider's account and records
// the result on the connection. Best-effort: a connect is not rolled back over a
// subscribe failure, because the credential is already stored and the failure is
// often a transient Graph blip or a missing permission the admin can fix by
// reconnecting — losing the whole grant over it would be the worse outcome.
func (s *ConnectionService) activateDelivery(ctx context.Context, p Provider, conn *IntegrationConnection, creds Credentials) {
	if !p.Info().SupportsWebhooks {
		return
	}
	if err := p.Subscribe(ctx, conn, creds); err != nil {
		s.logf("integrations: provider subscribe failed", "provider", conn.Provider, "connection_id", conn.ID.String(), "error", err)
		// FIXED, token-free note — never the raw provider error. Subscribe puts the
		// page token in the request, and a transport error's string embeds that URL;
		// storing err.Error() here would render a live token fragment into the
		// connection card (last_error is returned to the FE). This is the same
		// discipline ErrorRedirect applies to the OAuth callback: reduce the reason to
		// a safe message, keep the detail in logs only (and those are query-redacted
		// at the HTTP client). The detail an admin needs — "reconnect to retry" — does
		// not require the error text.
		const note = "Connected, but activating lead delivery failed — reconnect to retry."
		if serr := s.repo.SetConnectionSubscription(ctx, conn.OrgID, conn.ID, false, note); serr != nil {
			s.logf("integrations: could not record subscribe failure", "connection_id", conn.ID.String(), "error", serr)
		}
		return
	}
	if serr := s.repo.SetConnectionSubscription(ctx, conn.OrgID, conn.ID, true, ""); serr != nil {
		s.logf("integrations: could not record subscription", "connection_id", conn.ID.String(), "error", serr)
	}
}

// Providers lists the registered providers the FE may offer a connect button for.
// Empty when the codec is unconfigured: with no way to store a credential, a
// connect would only 503, so offering the button would be a dead end.
func (s *ConnectionService) Providers() []ProviderInfo {
	if !s.codec.Configured() {
		return nil
	}
	keys := s.registry.Keys()
	out := make([]ProviderInfo, 0, len(keys))
	for _, k := range keys {
		if p, ok := s.registry.Get(k); ok {
			out = append(out, p.Info())
		}
	}
	return out
}


// chosenAccount opens the sealed candidate blob and returns the requested
// account, or an error the caller can surface without consuming the token.
func (s *ConnectionService) chosenAccount(pending *IntegrationPendingConnection, accountID string) (*Account, error) {
	plain, err := s.codec.OpenString(envelope.Binding{OrgID: pending.OrgID, Purpose: envelope.PurposePendingToken, ID: pending.ID}, pending.EncryptedToken)
	if err != nil {
		return nil, domain.NewAppError(http.StatusInternalServerError, "could not open the pending connection")
	}
	var accounts []Account
	if err := json.Unmarshal([]byte(plain), &accounts); err != nil {
		return nil, domain.NewAppError(http.StatusInternalServerError, "could not read the pending connection")
	}
	for i := range accounts {
		if accounts[i].ID == accountID {
			return &accounts[i], nil
		}
	}
	return nil, domain.NewAppError(http.StatusBadRequest, "the chosen account is not one of the candidates")
}

// preflightClaim reports ErrAccountClaimedElsewhere when another workspace holds
// the active claim on this account and this org has no existing connection to
// refresh. A friendly early-out; upsertConnection re-checks and the unique index
// is the real race guard.
func (s *ConnectionService) preflightClaim(ctx context.Context, orgID uuid.UUID, provider, accountID string) error {
	existing, err := s.repo.FindLiveConnectionForAccount(ctx, orgID, provider, accountID)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil // same-org reconnect — no claim contest
	}
	claim, err := s.repo.FindActiveClaim(ctx, provider, accountID)
	if err != nil {
		return err
	}
	if claim != nil && claim.OrgID != orgID {
		return ErrAccountClaimedElsewhere
	}
	return nil
}

// upsertConnection stores or refreshes a connection for a chosen account.
//
// Same-org reconnect refreshes the existing row (re-sealing bound to its id);
// a first connect inserts a fresh row; a cross-org active claim is refused with a
// friendly error and never a raw constraint violation. The claim's real
// enforcement is the partial unique index — the pre-checks give a good message,
// the index is what actually holds under a race.
func (s *ConnectionService) upsertConnection(ctx context.Context, orgID, userID uuid.UUID, provider string, acc Account) (*IntegrationConnection, error) {
	existing, err := s.repo.FindLiveConnectionForAccount(ctx, orgID, provider, acc.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		sealed, kv, serr := s.sealCredentials(orgID, existing.ID, acc.Credentials)
		if serr != nil {
			return nil, serr
		}
		if err := s.repo.RefreshConnectionCredentials(ctx, orgID, existing.ID, sealed, kv, acc.Label, nil); err != nil {
			// A refresh that flips a released (revoked/disconnected) row back to
			// 'connected' can hit the claim index if another org grabbed the page in
			// between — that path opens once L5.3 wires status transitions. Translate
			// it to the friendly error, never a raw 23505 the handler would 500.
			if isConnClaimConflict(err) {
				return nil, ErrAccountClaimedElsewhere
			}
			return nil, err
		}
		return s.repo.GetConnection(ctx, orgID, existing.ID)
	}

	// No same-org row: a live claim by ANOTHER org blocks the connect.
	if claim, cerr := s.repo.FindActiveClaim(ctx, provider, acc.ID); cerr != nil {
		return nil, cerr
	} else if claim != nil && claim.OrgID != orgID {
		return nil, ErrAccountClaimedElsewhere
	}

	id := uuid.New()
	sealed, kv, serr := s.sealCredentials(orgID, id, acc.Credentials)
	if serr != nil {
		return nil, serr
	}
	conn := &IntegrationConnection{
		ID:                   id,
		OrgID:                orgID,
		Provider:             provider,
		ExternalAccountID:    acc.ID,
		ExternalAccountLabel: acc.Label,
		EncryptedCredentials: sealed,
		KeyVersion:           kv,
		Status:               ConnStatusConnected,
	}
	if userID != uuid.Nil {
		conn.CreatedBy = &userID
	}
	err = s.repo.InsertConnection(ctx, conn)
	switch {
	case err == nil:
		return conn, nil
	case isConnClaimConflict(err):
		// Lost a race to another org (or the pre-check's TOCTOU window).
		return nil, ErrAccountClaimedElsewhere
	case isConnAccountConflict(err):
		// Lost a race to a concurrent SAME-org connect (two tabs). Re-find and refresh
		// the winner's row rather than fail the admin.
		winner, ferr := s.repo.FindLiveConnectionForAccount(ctx, orgID, provider, acc.ID)
		if ferr != nil || winner == nil {
			return nil, err
		}
		reSealed, reKV, rerr := s.sealCredentials(orgID, winner.ID, acc.Credentials)
		if rerr != nil {
			return nil, rerr
		}
		if uerr := s.repo.RefreshConnectionCredentials(ctx, orgID, winner.ID, reSealed, reKV, acc.Label, nil); uerr != nil {
			if isConnClaimConflict(uerr) {
				return nil, ErrAccountClaimedElsewhere
			}
			return nil, uerr
		}
		return s.repo.GetConnection(ctx, orgID, winner.ID)
	default:
		return nil, err
	}
}

// Disconnect tears a connection down: best-effort provider-side teardown, then a
// soft delete that releases the claim.
func (s *ConnectionService) Disconnect(ctx context.Context, orgID, id uuid.UUID) error {
	conn, err := s.repo.GetConnection(ctx, orgID, id)
	if err != nil {
		return err
	}
	if conn == nil {
		return domain.NewAppError(http.StatusNotFound, "connection not found")
	}
	if p, ok := s.registry.Get(conn.Provider); ok && s.codec.Configured() {
		if creds, oerr := s.openCredentials(conn); oerr == nil {
			// Best-effort: a provider that refuses the teardown must not block the
			// disconnect — the customer asked to remove it, and the local row is the
			// authority for whether we still act on the page.
			if derr := p.Disconnect(ctx, conn, creds); derr != nil {
				s.logf("integrations: provider disconnect failed", "provider", conn.Provider, "connection_id", id.String(), "error", derr)
			}
		} else {
			s.logf("integrations: could not open credentials for disconnect", "connection_id", id.String(), "error", oerr)
		}
	}
	return s.repo.SoftDeleteConnection(ctx, orgID, id)
}

// Canary proves the configured key opens the credentials already at rest. Its
// only non-nil return is a genuine KEY MISMATCH — the one thing worth being fatal
// at boot (the key changed without a version bump).
//
// A failure to READ the rows is deliberately NOT surfaced as a canary error: it
// is an infrastructure blip, not a crypto misconfiguration, and returning it here
// would make the boot log.Fatal blame the encryption key for a transient DB
// hiccup — and crash-loop over something that will clear on its own. A real DB
// outage fails the app in a hundred louder places; this check just declines to be
// one of them. A wrong key still fails at first use.
func (s *ConnectionService) Canary(ctx context.Context) error {
	if !s.codec.Configured() {
		return nil // nothing configured to verify
	}
	rows, err := s.repo.ConnectionCanaryRows(ctx)
	if err != nil {
		s.logf("integrations: could not read connection rows for the credential canary (skipping the check)", "error", err)
		return nil
	}
	return s.codec.Canary(rows)
}

// ── crypto helpers ─────────────────────────────────────────────────────────

// sealCredentials seals a Credentials blob bound to a connection row.
func (s *ConnectionService) sealCredentials(orgID, connID uuid.UUID, creds Credentials) (string, int, error) {
	blob, err := json.Marshal(creds)
	if err != nil {
		return "", 0, err
	}
	sealed, err := s.codec.SealString(envelope.Binding{OrgID: orgID, Purpose: envelope.PurposeConnectionCredentials, ID: connID}, string(blob))
	if err != nil {
		return "", 0, err
	}
	return sealed, s.codec.PrimaryVersion(), nil
}

// openCredentials opens a connection's stored credentials.
func (s *ConnectionService) openCredentials(conn *IntegrationConnection) (Credentials, error) {
	plain, err := s.codec.OpenString(envelope.Binding{OrgID: conn.OrgID, Purpose: envelope.PurposeConnectionCredentials, ID: conn.ID}, conn.EncryptedCredentials)
	if err != nil {
		return Credentials{}, err
	}
	var creds Credentials
	if err := json.Unmarshal([]byte(plain), &creds); err != nil {
		return Credentials{}, err
	}
	return creds, nil
}

func (s *ConnectionService) logf(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Warn(msg, args...)
	}
}

// ── token helpers ──────────────────────────────────────────────────────────

// randToken mints an opaque random token and its SHA-256 hex hash. 32 bytes of
// CSPRNG, base64url unpadded — the api-token/lead-key shape. Only the hash is
// stored; the plaintext lives in a URL or the provider round trip and never in
// the DB.
func randToken() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	plaintext = base64.RawURLEncoding.EncodeToString(b)
	return plaintext, hashToken(plaintext), nil
}

func hashToken(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// pkcePair mints a PKCE verifier and its S256 challenge.
func pkcePair() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// safeReturnTo keeps a caller-supplied return path from becoming an open
// redirect: only a same-site absolute path is allowed, and "//host" (a
// protocol-relative URL to another origin) is rejected.
func safeReturnTo(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return ""
	}
	return p
}
