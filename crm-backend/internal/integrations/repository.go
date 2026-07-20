package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository is the integrations persistence layer. Every query is org-scoped;
// the only exception is the capture-time token probe, which resolves the org FROM
// the credential (there is no caller to scope by).
type Repository struct {
	db *gorm.DB
}

// NewRepository builds the repository.
func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// ── Lead sources ─────────────────────────────────────────────────────────────

// CreateSource inserts a source.
func (r *Repository) CreateSource(ctx context.Context, s *LeadSource) error {
	return r.db.WithContext(ctx).Create(s).Error
}

// UpdateSource saves a source's mutable fields. It is org-guarded by the caller's
// prior GetSource, and Save writes by primary key.
//
// `config` is OMITTED and written only by SetDealConfig's targeted jsonb_set. The
// column is mapped (it has been in the CREATE TABLE since the table existed, so
// unlike an ALTER-added column it can never be missing from the capture path's
// SELECT), but a wholesale Save of it would be a read-modify-write of a blob that
// several features are about to share: L3 and L5 put source-kind config beside the
// deal key, and two admins editing different settings would then silently delete
// each other's. Closing that before the connectors that trigger it exist is cheaper
// than diagnosing it afterwards.
// status, consecutive_failures, last_used_at and disabled_at are omitted for the
// owner_cursor reason one shelf over: they are MACHINE-written (TouchSourceUsed,
// IncrementSourceFailure) while this Save writes a struct read at page load, so an
// admin renaming a source mid-failure-storm would silently un-flip an error badge
// or wipe the counter. Status transitions go through SetSourceStatus.
func (r *Repository) UpdateSource(ctx context.Context, s *LeadSource) error {
	return r.db.WithContext(ctx).
		Omit("config", "status", "consecutive_failures", "last_used_at", "disabled_at").
		Save(s).Error
}

// SetSourceStatus applies an admin's explicit enable/disable, resetting the
// failure counter on enable so a recovered source starts clean. Targeted, never
// Save — see UpdateSource.
func (r *Repository) SetSourceStatus(ctx context.Context, orgID, sourceID uuid.UUID, status string) error {
	if status == SourceStatusActive {
		return r.db.WithContext(ctx).Exec(
			`UPDATE lead_sources
			    SET status = ?, disabled_at = NULL, consecutive_failures = 0, updated_at = NOW()
			  WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
			status, sourceID, orgID).Error
	}
	return r.db.WithContext(ctx).Exec(
		`UPDATE lead_sources SET status = ?, disabled_at = NOW(), updated_at = NOW()
		  WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		status, sourceID, orgID).Error
}

// SetDealConfig writes ONE key inside a source's config blob.
//
// jsonb_set rather than a whole-blob write, for the reason on UpdateSource: this
// must not disturb keys it does not own. Advisory — a failure degrades this one
// setting and is reported to the admin who made it, never to a lead.
func (r *Repository) SetDealConfig(ctx context.Context, orgID, sourceID uuid.UUID, cfg DealConfig) error {
	body, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Exec(
		`UPDATE lead_sources
		    SET config = jsonb_set(COALESCE(config, '{}'::jsonb), '{deal}', ?::jsonb, true),
		        updated_at = NOW()
		  WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		string(body), sourceID, orgID,
	).Error
}

// GetSource returns one source by id within an org, or (nil, nil) when absent.
func (r *Repository) GetSource(ctx context.Context, orgID, id uuid.UUID) (*LeadSource, error) {
	var s LeadSource
	err := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).First(&s).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// ListSources returns an org's sources, newest first.
func (r *Repository) ListSources(ctx context.Context, orgID uuid.UUID) ([]LeadSource, error) {
	var out []LeadSource
	err := r.db.WithContext(ctx).Where("org_id = ?", orgID).Order("created_at DESC").Find(&out).Error
	return out, err
}

// SoftDeleteSource retires a source. Soft, not hard: hard-deleting would orphan or
// cascade away its ledger rows — the very history the source exists to explain.
// Token lookups exclude soft-deleted rows via gorm's DeletedAt, so the credential
// dies immediately even though the record remains.
func (r *Repository) SoftDeleteSource(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).Delete(&LeadSource{}).Error
}

// FindSourceByTokenHash resolves a capture credential to its source, and only to
// a source whose workspace still exists.
//
// Deliberately NOT org-scoped by a caller: the credential IS the org claim —
// there is no caller to scope by. The returned source's OrgID is therefore the
// authority for everything downstream, and callers must never take an org from the
// request.
//
// The organizations join is load-bearing, not decoration. Workspace deletion is a
// SOFT delete, so the organizations row survives and the `ON DELETE CASCADE` on
// lead_sources.org_id can never fire; the source row keeps status='active' and its
// key would go on writing contacts — with billable side effects — into a workspace
// the customer deleted. Nobody could stop it either: deletion evicts every member,
// so no one can authenticate to reach the management API and disable the source.
// The credential must not outlive the workspace.
//
// Soft-deleted sources are excluded too (gorm's DeletedAt), so retiring a source
// revokes its key immediately.
func (r *Repository) FindSourceByTokenHash(ctx context.Context, hash string) (*LeadSource, error) {
	if hash == "" {
		return nil, nil
	}
	var s LeadSource
	err := r.db.WithContext(ctx).
		Joins("JOIN organizations o ON o.id = lead_sources.org_id AND o.deleted_at IS NULL").
		Where("lead_sources.token_hash = ?", hash).
		First(&s).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// TouchSourceUsed stamps last_used_at, resets the failure counter, and un-flips a
// source that had tripped to 'error'. Best-effort: a failure here must never fail
// a lead that was already written.
//
// The un-flip is what makes `error` a SELF-HEALING badge rather than a gate. The
// alternative — refusing traffic while flagged — was designed and rejected: a
// refusal answered before the body is read leaves NO ledger row, so when Google's
// (undocumented, finite) retry budget expires the lead is gone without a trace,
// and the source can never demonstrate recovery because nothing is ever attempted.
// A transient blip would brick the source until a human noticed, with L6 alerting
// not yet built. Processed-normally-but-flagged loses nothing and heals itself.
// Deliberately never touches 'disabled': that is an admin's explicit choice.
func (r *Repository) TouchSourceUsed(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Exec(
		`UPDATE lead_sources
		    SET last_used_at = NOW(),
		        consecutive_failures = 0,
		        status = CASE WHEN status = 'error' THEN 'active' ELSE status END
		  WHERE id = ?`, id).Error
}

// CountCreatedToday counts records this source has created since UTC midnight —
// the daily cap's backstop. Indexed by (source_id, created_at).
//
// A test-lead create IS counted here, deliberately, and this is NOT the oversight it
// looks like. The obvious "fix" — AND status <> 'test' — would fix a real but bounded
// bug (an admin's first test click on a source sitting exactly at its cap costs one
// real lead a 429 that day) by opening an unbounded one: L3's Google Ads capture
// accepts a caller-supplied is_test on the wire (a leaked google_key's known abuse
// path), and those deliveries also land as status='test'. A blanket status exclusion
// would hand that forged flag cap-free record creation — the exact amplification the
// cap exists to bound. The admin button cannot amplify (its identity is stable, so it
// creates one contact per source, ever), so the residual cost of counting it is at
// most one slot per source per test-contact lifetime; the forgeable path's cost is
// unbounded. The cheap mistake is the one we keep. If L6 later needs test events off
// this count, distinguish them by a PERSISTED origin (an unforgeable admin marker),
// never by status.
func (r *Repository) CountCreatedToday(ctx context.Context, sourceID uuid.UUID, now time.Time) (int64, error) {
	midnight := now.UTC().Truncate(24 * time.Hour)
	var n int64
	err := r.db.WithContext(ctx).Model(&IntegrationEvent{}).
		Where("source_id = ? AND created_at >= ? AND outcome = ?", sourceID, midnight, OutcomeCreated).
		Count(&n).Error
	return n, err
}

// ReapStrandedEvents releases deliveries whose process died mid-write.
//
// A row left at `processing` is not just untidy — the replay switch reads it as
// "still in flight" and answers 409 to every retry, so the Idempotency-Key becomes
// the thing that makes the lead permanently unrecoverable. Moving it to `failed` is
// what lets Ingest's failed-row branch re-run the pipeline against it.
//
// `result_record_id IS NULL` is load-bearing: a row that already produced a record
// must never be re-run, or the retry writes the same lead twice.
func (r *Repository) ReapStrandedEvents(ctx context.Context, grace time.Duration) (int64, error) {
	res := r.db.WithContext(ctx).Exec(`
		UPDATE integration_events
		   SET status = ?, error = ?, processed_at = NOW()
		 WHERE status = ? AND result_record_id IS NULL AND created_at < ?`,
		EventStatusFailed,
		"this delivery was interrupted before it finished (the server restarted or crashed); retrying the same Idempotency-Key will re-run it",
		EventStatusProcessing, time.Now().Add(-grace))
	return res.RowsAffected, res.Error
}

// SetEventConsent records the verbatim consent envelope on a delivery.
//
// Targeted UPDATE rather than a model save: the column is unmapped, and FinishEvent's
// db.Save runs AFTER this — a mapped-but-unset field would blank the envelope on
// every successful delivery, which is the silent discard this column exists to fix,
// reintroduced one layer down.
//
// Returns RowsAffected so a no-op cannot pass silently. A best-effort write that
// cannot report failure is the original defect wearing an UPDATE.
func (r *Repository) SetEventConsent(ctx context.Context, eventID uuid.UUID, raw []byte) (int64, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	res := r.db.WithContext(ctx).Exec(
		`UPDATE integration_events SET consent = ?::jsonb WHERE id = ?`, string(raw), eventID)
	return res.RowsAffected, res.Error
}

// consentTombstone is what replaces an erased envelope.
//
// NOT null and NOT '{}': either would make the ledger assert that no consent was
// ever obtained, which is a different — and false — claim than "consent was obtained
// and the record of it was erased on request".
const consentTombstone = `{"_crm":{"redacted":true,"enforced":false}}`

// RedactForRecord strips the personal data this pipeline stored about one contact.
//
// The ledger row and its status survive — the delivery history is what the customer
// needs to answer "what happened to this lead" — but everything the subject
// themselves supplied goes. Contact-keyed, which is why consent is only ever written
// on deliveries that produced a record: an envelope on a row with no
// result_record_id could never be reached by this.
func (r *Repository) RedactForRecord(ctx context.Context, orgID, recordID uuid.UUID) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE integration_events
		   SET raw_payload = '{}'::jsonb,
		       context     = '{}'::jsonb,
		       consent     = CASE WHEN consent IS NULL THEN NULL ELSE ?::jsonb END
		 WHERE org_id = ? AND result_record_id = ?`,
		consentTombstone, orgID, recordID).Error
}

// ConsentForEvents reads the envelopes for a page of deliveries in one query.
//
// Separate from ListEvents so a missing column degrades the consent display rather
// than the ledger: the delivery log is how a customer answers "what happened to this
// lead", and it must survive a boot guard that did not run.
func (r *Repository) ConsentForEvents(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]datatypes.JSON, error) {
	out := map[uuid.UUID]datatypes.JSON{}
	if len(ids) == 0 {
		return out, nil
	}
	type row struct {
		ID      uuid.UUID      `gorm:"column:id"`
		Consent datatypes.JSON `gorm:"column:consent"`
	}
	var rows []row
	if err := r.db.WithContext(ctx).Raw(
		`SELECT id, consent FROM integration_events WHERE id IN ? AND consent IS NOT NULL`, ids).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, x := range rows {
		out[x.ID] = x.Consent
	}
	return out, nil
}

// GetBatchEnrollAutomation reports whether batch deliveries for this source may
// enrol workflows. Read through a targeted statement, never the model — see the
// unmapped-column note on LeadSource.
func (r *Repository) GetBatchEnrollAutomation(ctx context.Context, orgID, sourceID uuid.UUID) (bool, error) {
	var out []bool
	if err := r.db.WithContext(ctx).Raw(
		`SELECT batch_enroll_automation FROM lead_sources WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		sourceID, orgID).Scan(&out).Error; err != nil {
		return false, err
	}
	if len(out) == 0 {
		return false, nil
	}
	return out[0], nil
}

// SetBatchEnrollAutomation stores the toggle.
func (r *Repository) SetBatchEnrollAutomation(ctx context.Context, orgID, sourceID uuid.UUID, on bool) error {
	return r.db.WithContext(ctx).Exec(
		`UPDATE lead_sources SET batch_enroll_automation = ?, updated_at = NOW() WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		on, sourceID, orgID).Error
}

// ── google_ads credentials (L3) ──────────────────────────────────────────────
//
// public_token and google_key_hash are ALTER-added columns and therefore unmapped
// (see LeadSource). Everything below names them ONLY in targeted statements, so a
// failed boot guard breaks the google_ads route alone — never the bearer capture
// path every org depends on.

// GoogleSource is a source plus the google_ads credential columns the struct
// deliberately does not carry.
type GoogleSource struct {
	LeadSource
	GoogleKeyHash string
}

// FindSourceByPublicToken resolves a google_ads webhook URL to its source.
//
// The same two revocation properties as FindSourceByTokenHash, for the same
// reasons: the organizations join keeps a deleted workspace's URL dead (workspace
// deletion is soft, evicts every member, and nobody could disable the source), and
// deleted_at IS NULL — explicit, because raw SQL bypasses GORM's soft-delete scope
// — revokes a retired source immediately.
//
// NOT org-scoped: the token is the org claim, and the returned OrgID is the
// authority for everything downstream.
//
// Deliberately NO status filter: the handler must tell `error` (503 — Google keeps
// retrying while the admin fixes it) apart from revoked (401 — Google should
// stop), and a WHERE clause would collapse both into "not found".
func (r *Repository) FindSourceByPublicToken(ctx context.Context, token string) (*GoogleSource, error) {
	if token == "" {
		return nil, nil
	}
	var out []GoogleSource
	// lead_sources.* never NAMES a column, so it survives any failed guard; only
	// google_key_hash is named, which is exactly the blast radius we want.
	err := r.db.WithContext(ctx).Raw(
		`SELECT lead_sources.*, lead_sources.google_key_hash
		   FROM lead_sources
		   JOIN organizations o ON o.id = lead_sources.org_id AND o.deleted_at IS NULL
		  WHERE lead_sources.public_token = ? AND lead_sources.deleted_at IS NULL
		  LIMIT 1`, token).Scan(&out).Error
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	out[0].PublicToken = token // hydrate the unmapped display field
	return &out[0], nil
}

// ── form_embed (L4) ──────────────────────────────────────────────────────────

// FormSource is a source plus the form_embed columns the struct does not carry.
type FormSource struct {
	LeadSource
	// AllowedOriginsRaw is the stored JSON array. Kept raw so the caller can tell a
	// READ FAILURE apart from an EMPTY LIST — the two have opposite outcomes and
	// collapsing them is how this feature would fail open.
	AllowedOriginsRaw datatypes.JSON
}

// FindFormSourceByPublicToken resolves a form-embed URL token to its source.
//
// A sibling of FindSourceByPublicToken rather than a widening of it: that one
// names google_key_hash and this one names allowed_origins, and keeping the two
// SELECTs disjoint is what keeps a failed boot guard scoped to one kind's route
// instead of both. Same two load-bearing joins — the organizations join (workspace
// deletion is soft and evicts every member, so nobody could disable the source)
// and the explicit deleted_at IS NULL (raw SQL bypasses GORM's soft-delete scope).
func (r *Repository) FindFormSourceByPublicToken(ctx context.Context, token string) (*FormSource, error) {
	if token == "" {
		return nil, nil
	}
	var out []FormSource
	err := r.db.WithContext(ctx).Raw(
		`SELECT lead_sources.*, COALESCE(lead_sources.allowed_origins, '[]'::jsonb) AS allowed_origins_raw
		   FROM lead_sources
		   JOIN organizations o ON o.id = lead_sources.org_id AND o.deleted_at IS NULL
		  WHERE lead_sources.public_token = ? AND lead_sources.deleted_at IS NULL
		  LIMIT 1`, token).Scan(&out).Error
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	out[0].PublicToken = token
	return &out[0], nil
}

// AllowedOrigins decodes the stored list. The error is NOT swallowed: on this one
// setting, "we could not read the allowlist" must not degrade to "the allowlist is
// empty" (which would look like a deliberate deny and send an admin hunting) nor
// to "allow everything" (a hole). The caller refuses the request and says why.
func (f *FormSource) AllowedOriginList() ([]string, error) {
	if len(f.AllowedOriginsRaw) == 0 {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal(f.AllowedOriginsRaw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SetAllowedOrigins stores a form source's browser origin allowlist.
func (r *Repository) SetAllowedOrigins(ctx context.Context, orgID, sourceID uuid.UUID, origins []string) error {
	raw, err := json.Marshal(origins)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Exec(
		`UPDATE lead_sources SET allowed_origins = ?::jsonb, updated_at = NOW()
		  WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		string(raw), sourceID, orgID).Error
}

// GetAllowedOrigins hydrates the management view.
func (r *Repository) GetAllowedOrigins(ctx context.Context, orgID, sourceID uuid.UUID) ([]string, error) {
	var out []string
	var raw []string
	if err := r.db.WithContext(ctx).Raw(
		`SELECT COALESCE(allowed_origins, '[]'::jsonb)::text FROM lead_sources
		  WHERE id = ? AND org_id = ? AND deleted_at IS NULL`, sourceID, orgID).Scan(&raw).Error; err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(raw[0]), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetTurnstileSecret reads the private half of a source's Turnstile pair.
//
// Its own targeted read, and never part of any struct or view: it is sent verbatim
// to Cloudflare so it cannot be hashed, which makes "never serialize it" the only
// protection it has. The management API reports whether one is set, never its value.
func (r *Repository) GetTurnstileSecret(ctx context.Context, orgID, sourceID uuid.UUID) (string, error) {
	var out []string
	if err := r.db.WithContext(ctx).Raw(
		`SELECT COALESCE(turnstile_secret, '') FROM lead_sources
		  WHERE id = ? AND org_id = ? AND deleted_at IS NULL`, sourceID, orgID).Scan(&out).Error; err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", nil
	}
	return out[0], nil
}

// SetTurnstileSecret stores or clears the private key. Write-only by design.
func (r *Repository) SetTurnstileSecret(ctx context.Context, orgID, sourceID uuid.UUID, secret string) error {
	var val any
	if strings.TrimSpace(secret) == "" {
		val = nil // an explicit clear, distinguishable from "never set"
	} else {
		val = secret
	}
	return r.db.WithContext(ctx).Exec(
		`UPDATE lead_sources SET turnstile_secret = ?, updated_at = NOW()
		  WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		val, sourceID, orgID).Error
}

// HasTurnstileSecret reports whether one is configured, without revealing it.
func (r *Repository) HasTurnstileSecret(ctx context.Context, orgID, sourceID uuid.UUID) (bool, error) {
	s, err := r.GetTurnstileSecret(ctx, orgID, sourceID)
	return strings.TrimSpace(s) != "", err
}

// SetPublicToken stores a source's URL token without touching any credential.
//
// Separate from SetGoogleCredentials deliberately: that one writes google_key_hash
// too, so calling it with an empty hash would stamp '' (not NULL) on a form source
// — harmless only by accident, since the google route happens to test for "".
func (r *Repository) SetPublicToken(ctx context.Context, orgID, sourceID uuid.UUID, token string) error {
	return r.db.WithContext(ctx).Exec(
		`UPDATE lead_sources SET public_token = ?, updated_at = NOW()
		  WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		token, sourceID, orgID).Error
}

// SetFormConfig writes ONE key inside a source's config blob — jsonb_set, so the
// deal option living next door survives.
func (r *Repository) SetFormConfig(ctx context.Context, orgID, sourceID uuid.UUID, cfg FormConfig) error {
	body, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Exec(
		`UPDATE lead_sources
		    SET config = jsonb_set(COALESCE(config, '{}'::jsonb), '{form}', ?::jsonb, true),
		        updated_at = NOW()
		  WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		string(body), sourceID, orgID).Error
}

// GetPublicToken hydrates a source view's public_token (empty for non-google kinds).
func (r *Repository) GetPublicToken(ctx context.Context, orgID, sourceID uuid.UUID) (string, error) {
	var out []string
	if err := r.db.WithContext(ctx).Raw(
		`SELECT COALESCE(public_token, '') FROM lead_sources WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		sourceID, orgID).Scan(&out).Error; err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", nil
	}
	return out[0], nil
}

// SetGoogleCredentials stores a google_ads source's URL token and key hash.
// Targeted UPDATE, never Save: rotation must not ride a struct read at page load.
func (r *Repository) SetGoogleCredentials(ctx context.Context, orgID, sourceID uuid.UUID, publicToken, keyHash string) error {
	return r.db.WithContext(ctx).Exec(
		`UPDATE lead_sources SET public_token = ?, google_key_hash = ?, updated_at = NOW()
		  WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		publicToken, keyHash, sourceID, orgID).Error
}

// SetGoogleKeyHash rotates only the key, leaving the pasted URL valid — the point
// of rotation: the advertiser swaps one field in Google's editor, not two.
func (r *Repository) SetGoogleKeyHash(ctx context.Context, orgID, sourceID uuid.UUID, keyHash string) error {
	return r.db.WithContext(ctx).Exec(
		`UPDATE lead_sources SET google_key_hash = ?, updated_at = NOW()
		  WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		keyHash, sourceID, orgID).Error
}

// errorFlipThreshold is how many CONSECUTIVE post-auth processing failures flip a
// source to status='error'. High enough that one poisoned lead retried by Google
// does not immediately kill a healthy source, low enough that a genuinely broken
// source flips within one retry storm. L6 alerts on the flip.
const errorFlipThreshold = 10

// IncrementSourceFailure counts one processing failure and reports whether this
// increment flipped the source to 'error'.
//
// ONE atomic statement, deliberately: the column is mapped nowhere and a Go
// read-modify-write would race concurrent deliveries. The flip only ever moves
// active→error — it must not resurrect a source an admin disabled.
//
// Callers must invoke this ONLY on post-key-verification failures. Pre-auth
// failures (unknown token, bad key, rate limit) are attacker-forgeable, and
// counting them would let anyone who read the URL off a landing page flip a
// healthy source into refusing Google's real traffic.
func (r *Repository) IncrementSourceFailure(ctx context.Context, sourceID uuid.UUID) (flipped bool, err error) {
	var out []bool
	err = r.db.WithContext(ctx).Raw(
		`UPDATE lead_sources
		    SET consecutive_failures = consecutive_failures + 1,
		        status = CASE WHEN consecutive_failures + 1 >= ? AND status = 'active'
		                      THEN 'error' ELSE status END,
		        updated_at = NOW()
		  WHERE id = ? AND deleted_at IS NULL
		  RETURNING status = 'error' AND consecutive_failures = ?`,
		errorFlipThreshold, sourceID, errorFlipThreshold).Scan(&out).Error
	if err != nil || len(out) == 0 {
		return false, err
	}
	return out[0], nil
}

// InsertRefusedEvents records deliveries the batch declined to attempt (daily cap
// exhausted, budget spent, client hung up) in one multi-row insert.
//
// Refusals must leave evidence. Without a ledger row an integrator who reads the
// per-item envelope and an admin who reads the delivery log see different histories,
// and "we never received it" becomes unanswerable.
func (r *Repository) InsertRefusedEvents(ctx context.Context, events []*IntegrationEvent) error {
	if len(events) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&events).Error
}

// ── Owner routing ────────────────────────────────────────────────────────────
//
// owner_pool, owner_cursor and integration_events.assigned_owner_id are read and
// written ONLY by the targeted statements below — none of them is a field on the
// GORM models. See the note on LeadSource for why that is load-bearing rather than
// stylistic.

// ownerTicketSQL claims the next place in a source's rotation.
//
// ONE statement does three jobs: it takes a unique ticket under a row lock, returns
// the pool so no second read is needed, and — via the array predicates — performs NO
// WRITE AT ALL for a source with no rotation configured, which is ~every source.
//
// Postgres row-locks lead_sources for this statement's duration, so a second
// concurrent lead blocks and then reads the committed value: no two leads can take
// the same ticket. A read-modify-write in Go could not do this (both would read the
// same cursor), and this stays correct across replicas because the DB holds the
// state. Nothing is held across the RecordService write — a SELECT ... FOR UPDATE
// spanning that would serialize a source's entire pipeline behind its slowest
// contact write, inside a 30s ingest timeout.
//
// RETURNING the PRE-increment value so the first lead lands on pool[0], mirroring
// record_number_repository.go's `RETURNING next_seq - 1`.
const ownerTicketSQL = `
	UPDATE lead_sources
	   SET owner_cursor = owner_cursor + 1
	 WHERE id = ? AND org_id = ? AND deleted_at IS NULL
	   AND jsonb_typeof(owner_pool) = 'array'
	   AND jsonb_array_length(owner_pool) > 0
	RETURNING owner_cursor - 1 AS ticket, owner_pool`

type ownerTicketRow struct {
	Ticket    int64          `gorm:"column:ticket"`
	OwnerPool datatypes.JSON `gorm:"column:owner_pool"`
}

// NextOwnerTicket takes this lead's turn. pooled=false means the source has no
// rotation (and no row was written).
//
// Never wrap this in a transaction that also spans the record write, and never
// decrement on failure: a burned ticket costs one rep one turn, whereas a
// "rollback" reintroduces the race the atomic bump exists to kill.
func (r *Repository) NextOwnerTicket(ctx context.Context, orgID, sourceID uuid.UUID) (int64, datatypes.JSON, bool, error) {
	var row ownerTicketRow
	res := r.db.WithContext(ctx).Raw(ownerTicketSQL, sourceID, orgID).Scan(&row)
	if res.Error != nil {
		return 0, nil, false, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, nil, false, nil // no rotation configured
	}
	return row.Ticket, row.OwnerPool, true, nil
}

// PeekOwnerTicket reads the next turn WITHOUT consuming it — the test-lead path.
//
// A synthetic lead must be able to report which rep a real lead would land on
// without spending that rep's turn on a contact the admin is told to delete.
func (r *Repository) PeekOwnerTicket(ctx context.Context, orgID, sourceID uuid.UUID) (int64, datatypes.JSON, bool, error) {
	var row ownerTicketRow
	res := r.db.WithContext(ctx).Raw(`
		SELECT owner_cursor AS ticket, owner_pool
		  FROM lead_sources
		 WHERE id = ? AND org_id = ? AND deleted_at IS NULL
		   AND jsonb_typeof(owner_pool) = 'array'
		   AND jsonb_array_length(owner_pool) > 0`, sourceID, orgID).Scan(&row)
	if res.Error != nil {
		return 0, nil, false, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, nil, false, nil
	}
	return row.Ticket, row.OwnerPool, true, nil
}

// SetEventAssignedOwner records which rep this DELIVERY was routed to, so an
// Idempotency-Key retry reuses the answer instead of taking a second ticket.
// Best-effort: losing it costs retry fidelity, never the lead.
func (r *Repository) SetEventAssignedOwner(ctx context.Context, eventID uuid.UUID, owner *uuid.UUID) error {
	if owner == nil {
		return nil
	}
	return r.db.WithContext(ctx).Exec(
		`UPDATE integration_events SET assigned_owner_id = ? WHERE id = ?`, *owner, eventID).Error
}

// GetEventAssignedOwner reads back a prior attempt's routing decision.
func (r *Repository) GetEventAssignedOwner(ctx context.Context, eventID uuid.UUID) (*uuid.UUID, error) {
	var ids []uuid.UUID
	if err := r.db.WithContext(ctx).Raw(
		`SELECT assigned_owner_id FROM integration_events WHERE id = ? AND assigned_owner_id IS NOT NULL`,
		eventID).Scan(&ids).Error; err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return &ids[0], nil
}

// GetOwnerPool reads one source's rotation.
func (r *Repository) GetOwnerPool(ctx context.Context, orgID, sourceID uuid.UUID) (datatypes.JSON, error) {
	var raw []datatypes.JSON
	if err := r.db.WithContext(ctx).Raw(
		`SELECT owner_pool FROM lead_sources WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		sourceID, orgID).Scan(&raw).Error; err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return datatypes.JSON(`[]`), nil
	}
	return raw[0], nil
}

// SetOwnerPool replaces a source's rotation. Targeted UPDATE rather than a model
// save: UpdateSource writes every mapped column, so routing config must not travel
// through it.
func (r *Repository) SetOwnerPool(ctx context.Context, orgID, sourceID uuid.UUID, pool datatypes.JSON) error {
	return r.db.WithContext(ctx).Exec(
		`UPDATE lead_sources SET owner_pool = ?, updated_at = NOW() WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		pool, sourceID, orgID).Error
}

// PoolsForOrg returns every source's rotation in the org, keyed by source id — one
// query for the list view rather than one per row.
func (r *Repository) PoolsForOrg(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]datatypes.JSON, error) {
	type row struct {
		ID        uuid.UUID      `gorm:"column:id"`
		OwnerPool datatypes.JSON `gorm:"column:owner_pool"`
	}
	var rows []row
	if err := r.db.WithContext(ctx).Raw(
		`SELECT id, owner_pool FROM lead_sources WHERE org_id = ? AND deleted_at IS NULL`, orgID).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]datatypes.JSON, len(rows))
	for _, r := range rows {
		out[r.ID] = r.OwnerPool
	}
	return out, nil
}

// SourcesRoutingTo names the sources that would send new leads to this person —
// what an offboarding admin needs to see before removing them.
func (r *Repository) SourcesRoutingTo(ctx context.Context, orgID, userID uuid.UUID) ([]string, error) {
	var names []string
	err := r.db.WithContext(ctx).Raw(`
		SELECT name FROM lead_sources
		 WHERE org_id = ? AND deleted_at IS NULL
		   AND (default_owner_id = ? OR owner_pool @> ?::jsonb)
		 ORDER BY name`, orgID, userID, `"`+userID.String()+`"`).Scan(&names).Error
	return names, err
}

// RemoveFromOwnerPools prunes a departing member from every rotation in the org.
//
// Removal, not suspension: a suspension is reversible and the rotation already skips
// it, but a removed member's id would silently re-arm if they were ever re-invited
// (org_users' PK is (user_id, org_id), so a re-invite flips the same row back to
// active).
func (r *Repository) RemoveFromOwnerPools(ctx context.Context, orgID, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE lead_sources
		   SET owner_pool = owner_pool - ?, updated_at = NOW()
		 WHERE org_id = ? AND deleted_at IS NULL AND owner_pool @> ?::jsonb`,
		userID.String(), orgID, `"`+userID.String()+`"`).Error
}

// ── Events ───────────────────────────────────────────────────────────────────

// InsertEventDeduped inserts a delivery, returning inserted=false when the
// provider already delivered it.
//
// Dedupe rides the partial unique indexes on (source_id, provider_event_id) and
// (connection_id, provider_event_id). Two indexes, not one, because Postgres
// treats NULLs as DISTINCT: a provider webhook resolves its connection but not yet
// its source, so a single source-scoped index would never fire for it and every
// retry would create a duplicate contact — on the highest-volume channel.
//
// An event with no ProviderEventID cannot dedupe (the index predicate excludes
// NULLs) and always inserts; that is correct — without a stable delivery id there
// is nothing to be idempotent against.
func (r *Repository) InsertEventDeduped(ctx context.Context, e *IntegrationEvent) (inserted bool, err error) {
	if e.ProviderEventID == nil || *e.ProviderEventID == "" {
		return true, r.db.WithContext(ctx).Create(e).Error
	}
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(e)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// FindEventByProviderID returns a prior delivery with this id for the source, used
// to answer a redelivery with the original result instead of re-running it.
func (r *Repository) FindEventByProviderID(ctx context.Context, sourceID uuid.UUID, providerEventID string) (*IntegrationEvent, error) {
	var e IntegrationEvent
	err := r.db.WithContext(ctx).
		Where("source_id = ? AND provider_event_id = ?", sourceID, providerEventID).
		Order("created_at ASC").First(&e).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

// FinishEvent records a delivery's outcome. The result id is persisted BEFORE any
// side effect fires, so a crash between the write and the notification cannot make
// a retry create a second contact.
func (r *Repository) FinishEvent(ctx context.Context, e *IntegrationEvent) error {
	now := time.Now()
	e.ProcessedAt = &now
	return r.db.WithContext(ctx).Save(e).Error
}

// ObservedKeys returns the distinct top-level keys this source has actually sent,
// newest deliveries first.
//
// This is what makes the mapping UI usable rather than a guessing game: the ledger
// already records every payload verbatim, so we can show an admin the real field
// names their provider uses instead of asking them to remember what Facebook calls
// a question. It is also why raw_payload is stored unmapped — a key we could not
// understand is exactly the key someone needs to map.
func (r *Repository) ObservedKeys(ctx context.Context, orgID, sourceID uuid.UUID, sampleSize int) ([]string, error) {
	if sampleSize <= 0 || sampleSize > 200 {
		sampleSize = 50
	}
	var keys []string
	err := r.db.WithContext(ctx).Raw(`
		SELECT DISTINCT k FROM (
			SELECT jsonb_object_keys(raw_payload) AS k
			FROM integration_events
			WHERE org_id = ? AND source_id = ?
			ORDER BY created_at DESC
			LIMIT ?
		) t
		ORDER BY k`, orgID, sourceID, sampleSize).Scan(&keys).Error
	return keys, err
}

// ListEvents returns a source's recent deliveries, newest first — the ledger view.
func (r *Repository) ListEvents(ctx context.Context, orgID uuid.UUID, sourceID *uuid.UUID, limit int) ([]IntegrationEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("org_id = ?", orgID)
	if sourceID != nil {
		q = q.Where("source_id = ?", *sourceID)
	}
	var out []IntegrationEvent
	err := q.Order("created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}
