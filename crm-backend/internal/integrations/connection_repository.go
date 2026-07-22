package integrations

import (
	"context"
	"errors"
	"strings"
	"time"

	"crm-backend/internal/integrations/envelope"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// Provider-connection persistence (L5.1). Every query is org-scoped except the
// two that cannot be: the cross-org claim probe (its whole job is to find a row
// in ANOTHER org) and the boot canary (it verifies key material across every
// org's rows before any request arrives).

// The exact index names from migration 000049 / the main.go boot guard. Matched
// by name, not just SQLSTATE 23505: the table carries two unique indexes with
// OPPOSITE meanings — a same-org duplicate (retry as an update) versus a
// cross-org claim (a friendly "already connected elsewhere") — and a
// constraint-blind check would confuse the two, which is the difference between
// refreshing your own connection and being told someone else owns your page.
const (
	connAccountUniqueIndex = "uix_integration_connections_org_account"
	connClaimUniqueIndex   = "uix_integration_connections_claim"
)

// ErrAccountClaimedElsewhere reports that another workspace holds the active
// claim on this provider account. Surfaced to the admin as a friendly message
// that never names the other workspace (that would leak which orgs use the
// product and who owns which page).
var ErrAccountClaimedElsewhere = errors.New("integrations: this account is already connected to another workspace")

func isConnClaimConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == connClaimUniqueIndex
}

func isConnAccountConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == connAccountUniqueIndex
}

// formSourceUniqueIndex is the one-facebook_form-source-per-(connection,form) index.
// formSourceUniqueIndexPrefix matches every provider's connection+form unique index
// (uix_lead_sources_conn_form for Facebook, …_tiktok for TikTok). A PREFIX rather than
// a list because the alternative already failed once: the constant named only
// Facebook's, so the TikTok enable-form race would have 500'd instead of resolving to
// the winning source the way the Facebook one does.
const formSourceUniqueIndexPrefix = "uix_lead_sources_conn_form"

// IsFormSourceConflict reports the enable-form idempotency race — a concurrent
// enable of the same (connection, form) hit the unique index. The handler resolves
// it to the existing source rather than erroring.
func IsFormSourceConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.HasPrefix(pgErr.ConstraintName, formSourceUniqueIndexPrefix)
}

// ── OAuth state ────────────────────────────────────────────────────────────

// CreateOAuthState persists a single-use OAuth state before the provider redirect.
func (r *Repository) CreateOAuthState(ctx context.Context, s *IntegrationOAuthState) error {
	return r.db.WithContext(ctx).Create(s).Error
}

// ConsumeOAuthState atomically marks a state consumed and returns it, or (nil,
// nil) when the state is unknown, already consumed, or expired.
//
// The mark and the read are ONE statement (UPDATE ... RETURNING) so two callback
// deliveries of the same state cannot both succeed: the second finds
// consumed_at already set and matches no row. This is what makes the state
// genuinely single-use rather than single-use-per-browser.
func (r *Repository) ConsumeOAuthState(ctx context.Context, stateHash string) (*IntegrationOAuthState, error) {
	if stateHash == "" {
		return nil, nil
	}
	var out []IntegrationOAuthState
	err := r.db.WithContext(ctx).Raw(`
		UPDATE integration_oauth_states
		   SET consumed_at = NOW()
		 WHERE state_hash = ? AND consumed_at IS NULL AND expires_at > NOW()
		RETURNING *`, stateHash).Scan(&out).Error
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// ── Pending connections ────────────────────────────────────────────────────

// CreatePendingConnection stores the sealed exchanged token plus the token-free
// candidate list between the callback and the admin's account choice.
func (r *Repository) CreatePendingConnection(ctx context.Context, p *IntegrationPendingConnection) error {
	return r.db.WithContext(ctx).Create(p).Error
}

// PeekPendingConnection reads a pending row by its selection-token hash WITHOUT
// consuming it — the account picker's load, which may be repeated, and the
// pre-write validation SelectAccount runs before it spends the token. Scoped to
// the owning caller (org AND user) IN the query, matching ConsumePending
// Connection, so a non-owner peek returns nothing rather than another
// workspace's candidate accounts. Returns (nil, nil) when unknown/expired/
// not-owned.
func (r *Repository) PeekPendingConnection(ctx context.Context, selectionHash string, orgID, userID uuid.UUID) (*IntegrationPendingConnection, error) {
	if selectionHash == "" {
		return nil, nil
	}
	var out []IntegrationPendingConnection
	err := r.db.WithContext(ctx).
		Raw(`SELECT * FROM integration_pending_connections
		      WHERE selection_token_hash = ? AND org_id = ? AND user_id = ?
		        AND consumed_at IS NULL AND expires_at > NOW()
		      LIMIT 1`, selectionHash, orgID, userID).
		Scan(&out).Error
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// ConsumePendingConnection atomically claims a pending row by its selection-token
// hash — but ONLY for the caller who owns it (org AND user) — or returns (nil,
// nil).
//
// The owner scope is IN the consuming statement, not a check after it, and that
// ordering is a security control, not a style choice. If consume happened first
// and the owner check second, a caller who merely LEARNED a selection token (a
// shared screen, a browser-history leak) could burn a legitimate user's pending
// connection: their select would fail authorization but still mark the row
// consumed, so the real user's next click gets "expired". Scoping the UPDATE
// means a non-owner matches no row, consumes nothing, and the real flow survives.
// Single-use for the owner is preserved (consumed_at guard), so a replay by the
// owner still promotes only one connection.
func (r *Repository) ConsumePendingConnection(ctx context.Context, selectionHash string, orgID, userID uuid.UUID) (*IntegrationPendingConnection, error) {
	if selectionHash == "" {
		return nil, nil
	}
	var out []IntegrationPendingConnection
	err := r.db.WithContext(ctx).Raw(`
		UPDATE integration_pending_connections
		   SET consumed_at = NOW()
		 WHERE selection_token_hash = ? AND org_id = ? AND user_id = ?
		   AND consumed_at IS NULL AND expires_at > NOW()
		RETURNING *`, selectionHash, orgID, userID).Scan(&out).Error
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// ── Connections ────────────────────────────────────────────────────────────

// FindLiveConnectionForAccount returns this org's non-deleted connection for a
// provider account, or (nil, nil). Includes revoked/disconnected rows (they are
// deleted_at IS NULL), so a same-org reconnect of a released page updates the
// existing row rather than colliding on the account unique index.
func (r *Repository) FindLiveConnectionForAccount(ctx context.Context, orgID uuid.UUID, provider, accountID string) (*IntegrationConnection, error) {
	var c IntegrationConnection
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND provider = ? AND external_account_id = ?", orgID, provider, accountID).
		First(&c).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// FindActiveClaim returns the connection currently HOLDING the exclusive claim on
// a provider account (any org), or (nil, nil). The service compares its OrgID to
// decide whether a connect is a same-org reconnect or a cross-org conflict.
//
// Not org-scoped, deliberately: its entire purpose is to detect a row in another
// workspace. It returns only the fields the conflict decision needs — never the
// credential blob.
func (r *Repository) FindActiveClaim(ctx context.Context, provider, accountID string) (*IntegrationConnection, error) {
	var out []IntegrationConnection
	err := r.db.WithContext(ctx).
		Raw(`SELECT id, org_id, provider, external_account_id, status
		       FROM integration_connections
		      WHERE provider = ? AND external_account_id = ?
		        AND deleted_at IS NULL AND status IN ('connected','degraded','error')
		      LIMIT 1`, provider, accountID).
		Scan(&out).Error
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// FindConnectionForWebhook resolves an app-level provider webhook to the ONE
// connection that should receive it: the live claim-holder for (provider,
// external account id).
//
// Filtered to the claim-holding statuses (connected/degraded/error), NOT just
// connected/degraded — `error` is a BADGE, not a gate, exactly as L3 settled for
// sources: refusing traffic to a flagged connection would drop every lead during
// a transient error window (a few slow fetches trip the counter), which is the
// silent lead loss this pipeline exists to prevent; a genuinely dead token fails
// the fetch loudly and the delivery lands in the ledger against its connection,
// recoverable, rather than orphaned. revoked/disconnected/deleted are excluded, so
// a released page's leads are quarantined, never written into the workspace that
// used to hold it. The organizations join keeps a soft-deleted workspace's page
// from receiving (workspace delete is a soft delete; the ON DELETE CASCADE never
// fires), matching the source-lookup revocation rule.
func (r *Repository) FindConnectionForWebhook(ctx context.Context, provider, externalAccountID string) (*IntegrationConnection, error) {
	if provider == "" || externalAccountID == "" {
		return nil, nil
	}
	var out []IntegrationConnection
	err := r.db.WithContext(ctx).Raw(`
		SELECT c.* FROM integration_connections c
		  JOIN organizations o ON o.id = c.org_id AND o.deleted_at IS NULL
		 WHERE c.provider = ? AND c.external_account_id = ?
		   AND c.deleted_at IS NULL AND c.status IN ('connected','degraded','error')
		 LIMIT 1`, provider, externalAccountID).Scan(&out).Error
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// InsertConnection creates a connection row. The caller detects the two unique
// violations (isConnClaimConflict / isConnAccountConflict) and reacts — the repo
// stays a thin persistence layer.
func (r *Repository) InsertConnection(ctx context.Context, c *IntegrationConnection) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// RefreshConnectionCredentials updates an existing connection's sealed credential
// and resets it to healthy — the same-org reconnect path.
//
// Targeted UPDATE, never a whole-struct Save: status, consecutive_failures and
// last_error are machine-written by SetConnectionStatus and the async worker, so
// a Save of a struct read earlier in the request could silently roll one of them
// back. Reconnecting is explicitly a "make this healthy again" action, so it
// clears the error state — but only for THIS connection, by id and org.
func (r *Repository) RefreshConnectionCredentials(ctx context.Context, orgID, id uuid.UUID, sealed string, keyVersion int, label string, webhookSecret *string) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE integration_connections
		   SET encrypted_credentials    = ?,
		       key_version              = ?,
		       external_account_label   = ?,
		       webhook_secret_encrypted = ?,
		       status                   = 'connected',
		       consecutive_failures     = 0,
		       last_error               = NULL,
		       updated_at               = NOW()
		 WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		sealed, keyVersion, label, webhookSecret, id, orgID).Error
}

// GetConnection returns one connection by id within an org, or (nil, nil).
func (r *Repository) GetConnection(ctx context.Context, orgID, id uuid.UUID) (*IntegrationConnection, error) {
	var c IntegrationConnection
	err := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).First(&c).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// ListConnections returns an org's connections, newest first.
func (r *Repository) ListConnections(ctx context.Context, orgID uuid.UUID) ([]IntegrationConnection, error) {
	var out []IntegrationConnection
	err := r.db.WithContext(ctx).Where("org_id = ?", orgID).Order("created_at DESC").Find(&out).Error
	return out, err
}

// SetConnectionSubscription records whether provider-side delivery is active,
// and a note (empty clears any prior one). jsonb_set on the `subscribed` key so a
// future config key can sit beside it. Org-scoped, by id.
func (r *Repository) SetConnectionSubscription(ctx context.Context, orgID, id uuid.UUID, subscribed bool, note string) error {
	val := "false"
	if subscribed {
		val = "true"
	}
	return r.db.WithContext(ctx).Exec(`
		UPDATE integration_connections
		   SET config = jsonb_set(COALESCE(config, '{}'::jsonb), '{subscribed}', ?::jsonb, true),
		       last_error = ?,
		       updated_at = NOW()
		 WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		val, note, id, orgID).Error
}

// SetConnectionStatus applies a status transition and records the reason.
//
// A blank status is a no-op guard against an accidental empty write. lastError is
// stored as-is (empty clears it) so a recovery transition can wipe a stale
// message. Targeted, org-scoped, by id.
func (r *Repository) SetConnectionStatus(ctx context.Context, orgID, id uuid.UUID, status, lastError string) error {
	if status == "" {
		return errors.New("integrations: refusing to set a blank connection status")
	}
	return r.db.WithContext(ctx).Exec(`
		UPDATE integration_connections
		   SET status = ?, last_error = ?, updated_at = NOW()
		 WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		status, lastError, id, orgID).Error
}

// connDegradeThreshold is how many consecutive RETRYABLE fetch failures move a
// connection to 'degraded'. Small on purpose: a webhook carries only ids, so every
// failed fetch is a lead we cannot see, and three of them is already a pattern.
const connDegradeThreshold = 4

// BumpConnectionFailure counts one post-signature fetch failure and reports the
// status this call moved the connection INTO, or "" when nothing changed.
//
// Two classes, deliberately kept apart all the way to the notification copy:
//
//   - permanent (a non-retryable 4xx, or credentials that will not open) → 'error'
//     immediately. A dead token is dead now; making an admin wait for a threshold
//     would delay the one alert that requires them to act.
//   - retryable (429 / 5xx / network) → counts toward 'degraded', and is CAPPED
//     there. This is the hole `degraded` was declared for and never filled: today a
//     Graph outage or a sustained rate limit is classified Retryable, so it exhausts
//     maxWebhookAttempts, drops the delivery, and leaves the row reading 'connected'
//     — silent lead loss behind a green badge. The cap is what keeps the `error` copy
//     honest: 'error' means "your credential stopped working, reconnect", and telling
//     someone to redo OAuth because Facebook was throttling us would be false.
//
// Both classes are reached only AFTER X-Hub-Signature-256 verification, so neither is
// forgeable — the same rule that governs the source-side counter.
//
// One atomic statement with a `prev` CTE: the row lock serialises concurrent
// deliveries so exactly one observes a given transition, which is what makes the
// notification once-only across replicas without a leader or a watermark column.
func (r *Repository) BumpConnectionFailure(ctx context.Context, orgID, id uuid.UUID, permanent bool, reason string) (band string, err error) {
	var out []string
	err = r.db.WithContext(ctx).Raw(`
		WITH prev AS (
		    SELECT id, status FROM integration_connections
		     WHERE id = ? AND org_id = ? AND deleted_at IS NULL FOR UPDATE
		)
		UPDATE integration_connections c
		   SET consecutive_failures = c.consecutive_failures + 1,
		       status = CASE
		           WHEN ? THEN 'error'
		           WHEN c.status = 'connected' AND c.consecutive_failures + 1 >= ? THEN 'degraded'
		           ELSE c.status END,
		       last_error = ?,
		       updated_at = NOW()
		  FROM prev
		 WHERE c.id = prev.id
		RETURNING CASE WHEN c.status = prev.status THEN '' ELSE c.status END`,
		id, orgID, permanent, connDegradeThreshold, reason).Scan(&out).Error
	if err != nil || len(out) == 0 {
		return "", err
	}
	return out[0], nil
}

// EaseConnectionHealth clears the failure count after a successful fetch and reports
// whether this call un-flipped the connection.
//
// Note what it does NOT do: it does not stamp last_synced_at. The heal fires right
// after FetchLead succeeds and BEFORE the form is resolved, so a connection whose
// every delivery is being quarantined for a disabled form reaches this line on each
// one. Stamping a sync time there would render the most confident green the UI has
// over a pipe that has produced no records at all. last_synced_at is written by
// MarkConnectionSynced, from the terminal-success path only.
func (r *Repository) EaseConnectionHealth(ctx context.Context, orgID, id uuid.UUID) (healed bool, err error) {
	var out []bool
	err = r.db.WithContext(ctx).Raw(`
		WITH prev AS (
		    SELECT id, status FROM integration_connections
		     WHERE id = ? AND org_id = ? AND deleted_at IS NULL FOR UPDATE
		)
		UPDATE integration_connections c
		   SET consecutive_failures = 0,
		       status = CASE WHEN c.status IN ('degraded', 'error') THEN 'connected' ELSE c.status END,
		       last_error = CASE WHEN c.status IN ('degraded', 'error') THEN '' ELSE c.last_error END,
		       updated_at = NOW()
		  FROM prev
		 WHERE c.id = prev.id
		RETURNING prev.status IN ('degraded', 'error')`,
		id, orgID).Scan(&out).Error
	if err != nil || len(out) == 0 {
		return false, err
	}
	return out[0], nil
}

// MarkConnectionSynced stamps last_synced_at — the column L5 declared, the API
// returns, and no code path has ever written, so every connection card in the fleet
// renders "never synced".
//
// Called only where a delivery reached a terminal SUCCESS, so the timestamp means
// what the label says: a lead arrived. Best-effort.
func (r *Repository) MarkConnectionSynced(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Exec(
		`UPDATE integration_connections SET last_synced_at = NOW(), updated_at = NOW()
		  WHERE id = ? AND deleted_at IS NULL`, id).Error
}

// SoftDeleteConnection retires a connection, releasing its claim (both the
// deleted_at predicate and the status leave the claim index).
//
// Soft, not hard: integration_events rows reference connection_id, and the ledger
// is the record of what happened to every lead this page produced — it must
// outlive the connection.
func (r *Repository) SoftDeleteConnection(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).Delete(&IntegrationConnection{}).Error
}

// ConnectionCanaryRows returns one CanaryRow per stored connection credential,
// across every org — the boot check's input.
//
// Not org-scoped: it runs before any request, to prove the configured
// INTEGRATION_ENC_KEY actually opens the credentials already at rest. Reads only
// the id, org and blob — exactly the binding the canary re-derives.
func (r *Repository) ConnectionCanaryRows(ctx context.Context) ([]envelope.CanaryRow, error) {
	type row struct {
		ID    uuid.UUID `gorm:"column:id"`
		OrgID uuid.UUID `gorm:"column:org_id"`
		Enc   string    `gorm:"column:encrypted_credentials"`
	}
	var rows []row
	if err := r.db.WithContext(ctx).Raw(`
		SELECT id, org_id, encrypted_credentials
		  FROM integration_connections
		 WHERE deleted_at IS NULL AND encrypted_credentials <> ''`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]envelope.CanaryRow, 0, len(rows))
	for _, x := range rows {
		out = append(out, envelope.CanaryRow{
			Binding: envelope.Binding{OrgID: x.OrgID, Purpose: envelope.PurposeConnectionCredentials, ID: x.ID},
			Blob:    x.Enc,
		})
	}
	return out, nil
}

// PurgeExpiredOAuthArtifacts deletes consumed or long-expired state and pending
// rows. Advisory housekeeping — a leftover expired row is harmless (every consume
// re-checks expiry), so this only keeps the two tables from growing without
// bound. Called on the reaper's tick.
func (r *Repository) PurgeExpiredOAuthArtifacts(ctx context.Context, olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan)
	if err := r.db.WithContext(ctx).Exec(`
		DELETE FROM integration_oauth_states
		 WHERE (consumed_at IS NOT NULL AND consumed_at < ?) OR expires_at < ?`, cutoff, cutoff).Error; err != nil {
		return err
	}
	return r.db.WithContext(ctx).Exec(`
		DELETE FROM integration_pending_connections
		 WHERE (consumed_at IS NOT NULL AND consumed_at < ?) OR expires_at < ?`, cutoff, cutoff).Error
}

// ── Workspace teardown (L6.4) ────────────────────────────────────────────────

// ListClaimedConnections returns the connections an org CURRENTLY holds the
// exclusive provider claim on — the only ones it may tell the provider to stop.
//
// Deliberately NOT every connection the org ever had. `Provider.Disconnect` is
// `DELETE /{account}/subscribed_apps`: it detaches our app from the ACCOUNT and knows
// nothing about connections, orgs or claims. So a teardown that swept every historical
// row would reach accounts this org disconnected long ago and another workspace has
// since connected — silently stopping that workspace's lead delivery, with its card
// still reading "connected, receiving leads" and no alert, because our health signal
// counts fetch failures and there would be no fetches.
//
// Credential DESTRUCTION is the opposite case and covers every row including
// soft-deleted ones (see PurgeConnectionSecrets): blanking a secret harms nobody,
// while calling a provider on another tenant's behalf does.
func (r *Repository) ListClaimedConnections(ctx context.Context, orgID uuid.UUID) ([]IntegrationConnection, error) {
	var out []IntegrationConnection
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND status IN ?", orgID,
			[]string{ConnStatusConnected, ConnStatusDegraded, ConnStatusError}).
		Order("created_at DESC").
		Find(&out).Error
	return out, err
}

// PurgeConnectionSecrets destroys the sealed credentials for every connection in an
// org and releases any page claims it still holds.
//
// One statement, Unscoped, covering live and soft-deleted rows alike. It also
// soft-deletes and flips status to `disconnected`: the claim index is partial on
// `deleted_at IS NULL AND status IN ('connected','degraded','error')`, so either
// alone would release the claim — doing both means the row cannot re-enter the index
// through any later status write, and a customer's Facebook page becomes connectable
// from another workspace immediately rather than being held hostage by a workspace
// that no longer exists.
//
// Note the credentials are blanked rather than the rows deleted: integration_events
// references connection_id, and the ledger is the record of what happened to every
// lead that page produced. What must go is the secret, not the history.
func (r *Repository) PurgeConnectionSecrets(ctx context.Context, orgID uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).Exec(`
		UPDATE integration_connections
		   SET encrypted_credentials    = '',
		       webhook_secret_encrypted = NULL,
		       status                   = ?,
		       deleted_at               = COALESCE(deleted_at, NOW()),
		       updated_at               = NOW()
		 WHERE org_id = ?`, ConnStatusDisconnected, orgID)
	return res.RowsAffected, res.Error
}

// DisableSourcesForOrg switches off every lead source in an org.
//
// Inbound is already refused at receipt (every token lookup joins organizations on
// deleted_at IS NULL), but the ASYNC backlog is not: ClaimPendingEvents is the global
// worker queue and has no org join, so deliveries already sitting `pending` when the
// workspace is deleted would still be claimed and fetched. Disabling the sources makes
// the processor's `source.IsLive()` check quarantine them instead of writing contacts
// into a workspace the customer deleted.
func (r *Repository) DisableSourcesForOrg(ctx context.Context, orgID uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).Exec(`
		UPDATE lead_sources
		   SET status = ?, disabled_at = COALESCE(disabled_at, NOW()), updated_at = NOW()
		 WHERE org_id = ? AND deleted_at IS NULL AND status <> ?`,
		SourceStatusDisabled, orgID, SourceStatusDisabled)
	return res.RowsAffected, res.Error
}

// PurgeOAuthArtifactsForOrg drops an org's in-flight connect attempts. They are
// short-lived and the sweeper would expire them anyway, but a half-finished OAuth
// handshake holding an exchanged token is not something to leave behind a deletion.
func (r *Repository) PurgeOAuthArtifactsForOrg(ctx context.Context, orgID uuid.UUID) error {
	if err := r.db.WithContext(ctx).Exec(
		`DELETE FROM integration_oauth_states WHERE org_id = ?`, orgID).Error; err != nil {
		return err
	}
	return r.db.WithContext(ctx).Exec(
		`DELETE FROM integration_pending_connections WHERE org_id = ?`, orgID).Error
}

// DeletedOrgsNeedingPurge returns workspaces that are soft-deleted but still hold a
// live provider credential or an enabled lead source.
//
// The repair half of the teardown, and it is what makes "best-effort" defensible.
// PurgeWorkspace is called from exactly one place and the org is gone immediately
// after, so there is NO second trigger: a transient deadlock there would otherwise
// leave a sealed token at rest and a customer's page claimed, permanently, behind a
// single log line. It also fixes the past — every workspace deleted before this
// shipped is in exactly that state, and no version of a delete-time-only cascade can
// reach them.
func (r *Repository) DeletedOrgsNeedingPurge(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var ids []uuid.UUID
	err := r.db.WithContext(ctx).Raw(`
		SELECT o.id FROM organizations o
		 WHERE o.deleted_at IS NOT NULL
		   AND (
		         EXISTS (SELECT 1 FROM integration_connections c
		                  WHERE c.org_id = o.id AND c.encrypted_credentials <> '')
		      OR EXISTS (SELECT 1 FROM lead_sources s
		                  WHERE s.org_id = o.id AND s.deleted_at IS NULL AND s.status <> ?)
		       )
		 ORDER BY o.deleted_at
		 LIMIT ?`, SourceStatusDisabled, limit).Scan(&ids).Error
	return ids, err
}
