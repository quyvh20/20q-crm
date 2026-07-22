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

// CreateConnectionSource inserts a connection-backed source (facebook_form) that
// has NO bearer credential — it is resolved by connection_id + form_id, never a
// token. token_hash/token_prefix are omitted so token_hash stays NULL: Postgres
// treats NULLs as distinct on its UNIQUE index, whereas the empty string every
// such source would otherwise carry collides across the second one.
func (r *Repository) CreateConnectionSource(ctx context.Context, s *LeadSource) error {
	return r.db.WithContext(ctx).Omit("token_hash", "token_prefix").Create(s).Error
}

// FindSourceByKind returns an org's single source of a kind, or nil when there is
// none. Used for the kinds an org has exactly one of (webhook_inbound), where the
// kind itself is the identifier.
func (r *Repository) FindSourceByKind(ctx context.Context, orgID uuid.UUID, kind string) (*LeadSource, error) {
	var s LeadSource
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND kind = ?", orgID, kind).
		Order("created_at ASC").
		First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// CreateLegacySource inserts the org's webhook_inbound source.
//
// token_hash/token_prefix are OMITTED for the reason CreateConnectionSource gives —
// the column must stay NULL, not empty, or the second such source in the fleet
// collides on the UNIQUE index. Here it matters twice over: FindSourceByTokenHash has
// no kind filter, so a token_hash on this row would silently open a second,
// capture-API ingress into an org whose only intended credential is its org token.
//
// ON CONFLICT DO NOTHING with NO target is deliberate: it is a no-op when no unique
// constraint exists and a loss-to-the-winner when one does, so this write never
// depends on an index whose boot guard the loop is allowed to log-and-continue past.
// A conflicted insert leaves the ID zero, which is how the caller knows to re-read.
func (r *Repository) CreateLegacySource(ctx context.Context, s *LeadSource) error {
	return r.db.WithContext(ctx).
		Omit("token_hash", "token_prefix").
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(s).Error
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
// source that had tripped to 'error'. It reports whether THIS call performed the
// un-flip, which is the recovery edge L6 notifies on. Best-effort: a failure here
// must never fail a lead that was already written.
//
// The `prev` CTE exists only to read the status the row held BEFORE the update —
// RETURNING otherwise yields the new value, which is 'active' whether or not this
// call changed anything, so a recovery notification would fire on every successful
// lead forever. FOR UPDATE serialises concurrent deliveries on the row, so exactly
// one of them ever observes the transition: that is what makes the edge once-only
// across replicas with no leader election, and it is the same property
// ownerTicketSQL already depends on.
//
// This is deliberately a STATEMENT change and not a new "was notified" column. A
// statement that is wrong fails in every environment at build or test time; an
// ALTER-added column fails silently on prod alone, because the boot-guard loop logs
// and boots on — and this particular statement is load-bearing for two features that
// already work (last_used_at, and the self-heal), so breaking it there would freeze
// last_used_at fleet-wide and make `error` permanent, with no error anywhere.
//
// The un-flip is what makes `error` a SELF-HEALING badge rather than a gate. The
// alternative — refusing traffic while flagged — was designed and rejected: a
// refusal answered before the body is read leaves NO ledger row, so when Google's
// (undocumented, finite) retry budget expires the lead is gone without a trace,
// and the source can never demonstrate recovery because nothing is ever attempted.
// A transient blip would brick the source until a human noticed, with L6 alerting
// not yet built. Processed-normally-but-flagged loses nothing and heals itself.
// Deliberately never touches 'disabled': that is an admin's explicit choice.
func (r *Repository) TouchSourceUsed(ctx context.Context, id uuid.UUID) (healed bool, err error) {
	var out []bool
	err = r.db.WithContext(ctx).Raw(
		`WITH prev AS (
		     SELECT id, status FROM lead_sources WHERE id = ? FOR UPDATE
		 )
		 UPDATE lead_sources s
		    SET last_used_at = NOW(),
		        consecutive_failures = 0,
		        status = CASE WHEN s.status = 'error' THEN 'active' ELSE s.status END
		   FROM prev
		  WHERE s.id = prev.id
		 RETURNING prev.status = 'error'`, id).Scan(&out).Error
	if err != nil || len(out) == 0 {
		return false, err
	}
	return out[0], nil
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

// ReapStrandedEvents releases deliveries whose process died mid-write. Two arms,
// split by HOW the row is recovered — which is decided by claimed_at:
//
//   - SYNC deliveries (the capture API) are inserted directly as `processing` with
//     claimed_at NULL; there is a CLIENT who retries, so a stranded one goes to
//     `failed` and Ingest's failed-row branch re-runs it on the next retry of the
//     same Idempotency-Key. Left at `processing`, the replay switch would answer
//     409 forever — the key becoming what makes the lead unrecoverable.
//   - ASYNC deliveries (provider webhooks, L5) are claimed from `pending` and carry
//     claimed_at; there is NO client to retry — the async worker is the only thing
//     that will ever touch them again — so a stranded one goes BACK to `pending`
//     to be re-claimed, UNLESS it has already used its attempt budget, in which
//     case it fails (a poison delivery that crashes the worker each claim must not
//     loop pending→processing→pending forever).
//
// `result_record_id IS NULL` is load-bearing on both arms: a row that already
// produced a record must never be re-run, or the retry writes the same lead twice.
func (r *Repository) ReapStrandedEvents(ctx context.Context, grace time.Duration) (int64, error) {
	cutoff := time.Now().Add(-grace)
	var total int64

	// Async, budget remaining → re-claimable.
	rep := r.db.WithContext(ctx).Exec(`
		UPDATE integration_events
		   SET status = ?, claimed_at = NULL, error = ?
		 WHERE status = ? AND result_record_id IS NULL
		   AND claimed_at IS NOT NULL AND claimed_at < ? AND attempts < ?`,
		EventStatusPending,
		"this delivery was interrupted before it finished (the server restarted or crashed); it will be retried",
		EventStatusProcessing, cutoff, maxWebhookAttempts)
	if rep.Error != nil {
		return total, rep.Error
	}
	total += rep.RowsAffected

	// Async, budget exhausted → failed.
	af := r.db.WithContext(ctx).Exec(`
		UPDATE integration_events
		   SET status = ?, error = ?, processed_at = NOW()
		 WHERE status = ? AND result_record_id IS NULL
		   AND claimed_at IS NOT NULL AND claimed_at < ? AND attempts >= ?`,
		EventStatusFailed,
		"this delivery could not be processed after repeated attempts (the fetch kept failing or the worker kept dying)",
		EventStatusProcessing, cutoff, maxWebhookAttempts)
	if af.Error != nil {
		return total, af.Error
	}
	total += af.RowsAffected

	// Sync → failed (a client retries via its Idempotency-Key).
	sf := r.db.WithContext(ctx).Exec(`
		UPDATE integration_events
		   SET status = ?, error = ?, processed_at = NOW()
		 WHERE status = ? AND result_record_id IS NULL
		   AND claimed_at IS NULL AND created_at < ?`,
		EventStatusFailed,
		"this delivery was interrupted before it finished (the server restarted or crashed); retrying the same Idempotency-Key will re-run it",
		EventStatusProcessing, cutoff)
	if sf.Error != nil {
		return total, sf.Error
	}
	total += sf.RowsAffected
	return total, nil
}

// ClaimPendingEvents atomically claims up to `limit` pending webhook deliveries
// for the async worker: pending → processing, attempts++, claimed_at stamped.
//
// FOR UPDATE SKIP LOCKED in the inner select is what lets several worker replicas
// run concurrently and each grab a DISJOINT set — a locked (already-claimed-in-
// this-transaction) row is skipped rather than blocked on. RETURNING the claimed
// rows means no second read. Ordered by created_at so the oldest delivery is
// processed first.
func (r *Repository) ClaimPendingEvents(ctx context.Context, limit int) ([]IntegrationEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []IntegrationEvent
	err := r.db.WithContext(ctx).Raw(`
		UPDATE integration_events
		   SET status = ?, claimed_at = NOW(), attempts = attempts + 1
		 WHERE id IN (
		   SELECT id FROM integration_events
		    WHERE status = ?
		    ORDER BY created_at
		    LIMIT ?
		    FOR UPDATE SKIP LOCKED
		 )
		RETURNING *`,
		EventStatusProcessing, EventStatusPending, limit).Scan(&out).Error
	return out, err
}

// RependEvent returns a claimed event to `pending` for a later retry — the async
// worker's response to a RETRYABLE fetch failure that still has attempt budget.
// Targeted, clears claimed_at so the reaper's grace does not immediately re-touch
// it, and records the transient reason.
//
// ORG-SCOPED, and that predicate is not decoration. The statement was `WHERE id = ?`
// alone, which was survivable only because its three callers are all the worker
// (which supplies a row it just claimed). The moment anything reachable from a
// request re-pends an event — L6.2's retry — an unscoped id becomes a cross-tenant
// write, so the scope is added here rather than remembered at each new call site.
func (r *Repository) RependEvent(ctx context.Context, orgID, eventID uuid.UUID, note string) error {
	// processed_at is cleared too: a write-path retry re-pends a row that IngestClaimed
	// already ran through failEvent (which stamps processed_at), and a `pending` row
	// carrying a processed_at reads as a finished delivery in the ledger.
	return r.db.WithContext(ctx).Exec(`
		UPDATE integration_events SET status = ?, claimed_at = NULL, processed_at = NULL, error = ?
		 WHERE id = ? AND org_id = ?`, EventStatusPending, note, eventID, orgID).Error
}

// SetSourceConnection stamps a source's connection_id (an ALTER-added column
// deliberately not on the LeadSource struct — see the migration). Targeted UPDATE,
// org-scoped.
func (r *Repository) SetSourceConnection(ctx context.Context, orgID, sourceID, connID uuid.UUID) error {
	return r.db.WithContext(ctx).Exec(
		`UPDATE lead_sources SET connection_id = ?, updated_at = NOW() WHERE id = ? AND org_id = ? AND deleted_at IS NULL`,
		connID, sourceID, orgID).Error
}

// SourceConnectionID reads a source's connection_id (uuid.Nil when unset). The
// column is unmapped on the struct, so it is read by targeted SQL; the NOT NULL
// filter avoids scanning a NULL into a non-pointer uuid.
func (r *Repository) SourceConnectionID(ctx context.Context, orgID, sourceID uuid.UUID) (uuid.UUID, error) {
	var out []uuid.UUID
	if err := r.db.WithContext(ctx).Raw(
		`SELECT connection_id FROM lead_sources WHERE id = ? AND org_id = ? AND deleted_at IS NULL AND connection_id IS NOT NULL`,
		sourceID, orgID).Scan(&out).Error; err != nil {
		return uuid.Nil, err
	}
	if len(out) == 0 {
		return uuid.Nil, nil
	}
	return out[0], nil
}

// EnabledFormIDs maps each provider form id that already has a facebook_form source
// on this connection to that source's id — so the form picker can show which forms
// are enabled and enabling is idempotent (re-enabling returns the existing source
// rather than creating a duplicate).
func (r *Repository) EnabledFormIDs(ctx context.Context, orgID, connID uuid.UUID, kind, provider string) (map[string]uuid.UUID, error) {
	type row struct {
		ID     uuid.UUID `gorm:"column:id"`
		FormID string    `gorm:"column:form_id"`
	}
	var rows []row
	if err := r.db.WithContext(ctx).Raw(`
		SELECT id, config->?->>'form_id' AS form_id
		  FROM lead_sources
		 WHERE org_id = ? AND connection_id = ? AND kind = ? AND deleted_at IS NULL
		   AND config->?->>'form_id' IS NOT NULL`,
		provider, orgID, connID, kind, provider).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]uuid.UUID, len(rows))
	for _, x := range rows {
		out[x.FormID] = x.ID
	}
	return out, nil
}

// FindFacebookFormSource resolves the facebook_form lead source for a connection +
// provider form id, or (nil, nil) when the form has not been enabled (L5.4 creates
// these rows). The form id lives in config.facebook.form_id — config is inside the
// original CREATE TABLE, so unlike an ALTER-added column it cannot be missing where
// the table exists. GORM adds `deleted_at IS NULL`.
func (r *Repository) FindConnectionFormSource(ctx context.Context, connectionID uuid.UUID, kind, provider, formID string) (*LeadSource, error) {
	if formID == "" {
		return nil, nil
	}
	var s LeadSource
	// `config -> ? ->> 'form_id'` with the provider as a bound parameter, so one
	// statement serves every adapter. Before L7.5 the namespace was the literal
	// 'facebook' in four places, which is what made a second provider a rewrite
	// rather than an adapter.
	err := r.db.WithContext(ctx).
		Where("connection_id = ? AND kind = ? AND config->?->>'form_id' = ?",
			connectionID, kind, provider, formID).
		First(&s).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
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
		   SET raw_payload        = '{}'::jsonb,
		       context            = '{}'::jsonb,
		       quarantined_fields = '{}'::jsonb,
		       consent            = CASE WHEN consent IS NULL THEN NULL ELSE ?::jsonb END,
		       redacted_at        = COALESCE(redacted_at, NOW())
		 WHERE org_id = ? AND result_record_id = ?`,
		consentTombstone, orgID, recordID).Error
}

// RedactForRecords is the bulk erasure path: the same strip, for many contacts, in
// one statement.
//
// It exists because the single-contact hook was only ever wired to the single-contact
// delete, and the bulk action an admin actually reaches for when honouring a
// data-protection request over a list of people wrote nothing at all — so exactly the
// case with the most subjects in it was the case that erased none of them.
//
// One UPDATE rather than a loop: a bulk delete can carry hundreds of ids, and a
// round trip each would make erasure the slowest thing in the request by an order of
// magnitude, which is how it ends up being made asynchronous and then unreliable.
func (r *Repository) RedactForRecords(ctx context.Context, orgID uuid.UUID, recordIDs []uuid.UUID) error {
	if len(recordIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Exec(`
		UPDATE integration_events
		   SET raw_payload        = '{}'::jsonb,
		       context            = '{}'::jsonb,
		       quarantined_fields = '{}'::jsonb,
		       consent            = CASE WHEN consent IS NULL THEN NULL ELSE ?::jsonb END,
		       redacted_at        = COALESCE(redacted_at, NOW())
		 WHERE org_id = ? AND result_record_id IN ?`,
		consentTombstone, orgID, recordIDs).Error
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
// too, so calling it with an empty hash would stamp ” (not NULL) on a form source
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

// SetEventRouting records what routing decided for a delivery: who it was assigned
// to (nil for nobody) and, when routing had to fall back, why.
//
// One statement for both, and a targeted UPDATE rather than a Save — the note used to
// be assigned to an already-inserted struct and silently never persisted, which meant
// the one disclosure explaining an unowned lead reached nobody.
func (r *Repository) SetEventRouting(ctx context.Context, eventID uuid.UUID, owner *uuid.UUID, note string) error {
	fields := map[string]any{"assigned_owner_id": owner}
	if note != "" {
		fields["note"] = note
	}
	return r.db.WithContext(ctx).Model(&IntegrationEvent{}).Where("id = ?", eventID).Updates(fields).Error
}

// FinishLegacyEvent closes a legacy webhook delivery.
//
// A targeted UPDATE rather than FinishEvent's wholesale Save, because the legacy
// caller holds only the delivery id — it does not carry the loaded row the ingest
// path does. Handing Save a partial struct would write the ZERO value of every mapped
// column: org_id nil, raw_payload NULL, the very payload the row exists to preserve.
// (That is not theoretical — it was the first implementation, and it survived only
// because the NOT NULL on org_id rejected the statement and the error was logged.)
func (r *Repository) FinishLegacyEvent(ctx context.Context, id uuid.UUID, status, outcome, errText string, recordID *uuid.UUID) error {
	fields := map[string]any{
		"status":       status,
		"error":        errText,
		"processed_at": time.Now(),
	}
	if recordID != nil {
		fields["outcome"] = outcome
		fields["result_slug"] = "contact"
		fields["result_record_id"] = *recordID
	}
	return r.db.WithContext(ctx).Model(&IntegrationEvent{}).Where("id = ?", id).Updates(fields).Error
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

// EventFilter narrows the org-wide ledger query.
//
// A struct rather than a widening of ListEvents' signature: ListEvents backs the
// SHIPPED source-detail log and is deliberately left untouched, because the only
// thing standing between an envelope change and a silently-empty delivery log is the
// frontend's asList coercion (which turns any unexpected shape into `[]`, not an
// error). A new reader gets a new method.
type EventFilter struct {
	// SourceID and ConnectionID are FILTERS, not scopes — org_id is the scope. That
	// is what makes a soft-deleted source's ledger reachable: loadSource 404s it, but
	// its rows are still the org's, and the soft delete exists precisely so they
	// survive. It is also the only way to see rows with source_id IS NULL, which is
	// every provider delivery that failed BEFORE ingest could stamp a source.
	SourceID     *uuid.UUID
	ConnectionID *uuid.UUID
	// Statuses is an OR set. The caller validates membership; an unknown status here
	// simply matches nothing.
	Statuses []string
	// Unresolved selects deliveries that produced no record — "show me what did not
	// land", the question the log exists to answer.
	Unresolved bool
	// CursorAt/CursorID are the keyset position (the last row of the previous page).
	// Keyset, not OFFSET: the ledger is append-heavy, and an OFFSET page shifts under
	// the reader as new deliveries arrive, silently skipping rows between pages.
	CursorAt *time.Time
	CursorID *uuid.UUID
	Limit    int
}

// ListEventsFiltered returns one keyset page of the org's ledger, newest first.
//
// It returns limit+1 rows when more exist so the caller can mint a cursor without a
// second COUNT — no total is computed, and none is shown, because a total over a
// filtered append-heavy table is stale the moment it is rendered.
func (r *Repository) ListEventsFiltered(ctx context.Context, orgID uuid.UUID, f EventFilter) ([]IntegrationEvent, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	q := r.db.WithContext(ctx).Where("org_id = ?", orgID)
	if f.SourceID != nil {
		q = q.Where("source_id = ?", *f.SourceID)
	}
	if f.ConnectionID != nil {
		q = q.Where("connection_id = ?", *f.ConnectionID)
	}
	if len(f.Statuses) > 0 {
		q = q.Where("status IN ?", f.Statuses)
	}
	if f.Unresolved {
		q = q.Where("result_record_id IS NULL")
	}
	if f.CursorAt != nil && f.CursorID != nil {
		// Row-value comparison, matching the ORDER BY exactly. Comparing created_at
		// alone would drop rows that share a timestamp — deliveries arrive in bursts,
		// so ties are routine rather than theoretical.
		q = q.Where("(created_at, id) < (?, ?)", *f.CursorAt, *f.CursorID)
	}
	var out []IntegrationEvent
	err := q.Order("created_at DESC, id DESC").Limit(f.Limit + 1).Find(&out).Error
	return out, err
}

// GetEvent returns one delivery within an org, or (nil, nil) when absent.
//
// Org-scoped by construction: every mutating route resolves its row through this, so
// a foreign event id is indistinguishable from a missing one.
func (r *Repository) GetEvent(ctx context.Context, orgID, id uuid.UUID) (*IntegrationEvent, error) {
	var e IntegrationEvent
	err := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).First(&e).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

// RequeueEventForRetry hands a failed provider delivery back to the async worker,
// atomically, and reports whether THIS call did it.
//
// One conditional UPDATE rather than check-then-act, and every guard rides in the
// WHERE because each one is a race in disguise:
//
//   - `result_record_id IS NULL` — the anti-double-write guard the reaper also
//     depends on. A row that already produced a record must never re-run.
//   - `status IN ('failed','quarantined')` — the same safe set Ingest's replay switch
//     re-runs in place. A `processing`/`pending` row is in flight; re-pending it
//     races the worker that holds it.
//   - `connection_id IS NOT NULL` — this door re-FETCHES from the provider, so it
//     only exists for rows that have a provider to fetch from. A sync row flipped to
//     `pending` would be claimed by the worker and immediately failed with "delivery
//     has no connection".
//   - `org_id = ?` — without it this is a cross-tenant write primitive.
//
// `error` is deliberately NOT cleared: it is the only record of WHY the delivery was
// quarantined, and an admin who retries and fails again has more use for the original
// reason than for a blank field. `attempts` is deliberately NOT reset either — a
// requeue buys exactly one more claim, and the existing maxWebhookAttempts budget
// then terminates a poison delivery instead of looping it forever.
func (r *Repository) RequeueEventForRetry(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).Exec(`
		UPDATE integration_events
		   SET status = ?, claimed_at = NULL, processed_at = NULL
		 WHERE id = ? AND org_id = ?
		   AND result_record_id IS NULL
		   AND connection_id IS NOT NULL
		   AND status IN (?, ?)`,
		EventStatusPending, id, orgID, EventStatusFailed, EventStatusQuarantined)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
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

// SourceLabels names sources for the ledger view, INCLUDING soft-deleted ones.
//
// Unscoped by design and used from exactly one place. The org-wide ledger exists
// partly to make a deleted source's history readable again — the soft delete is there
// so the rows survive — and a page that could show the rows but not their names would
// have brought back the data and left it unreadable. Org-scoped by predicate; only
// name and kind are exposed, both of which the source list already shows.
func (r *Repository) SourceLabels(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]eventSourceLabel, error) {
	out := map[uuid.UUID]eventSourceLabel{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID      uuid.UUID
		Name    string
		Kind    string
		Deleted bool
	}
	err := r.db.WithContext(ctx).Raw(`
		SELECT id, name, kind, (deleted_at IS NOT NULL) AS deleted
		  FROM lead_sources
		 WHERE org_id = ? AND id IN ?`, orgID, ids).Scan(&rows).Error
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out[r.ID] = eventSourceLabel{Name: r.Name, Kind: r.Kind, Deleted: r.Deleted}
	}
	return out, nil
}

// WithLedgerPruneLock runs fn holding a Postgres advisory lock, reporting whether
// the lock was obtained. A caller that gets false did nothing and should skip.
//
// The lock is taken on a PINNED connection, not on the pooled handle. A session-level
// advisory lock belongs to the CONNECTION that took it, and a pool hands successive
// statements to whichever connection is free — so locking and unlocking through the
// pool can release nothing (the unlock lands on a connection that never held it) and
// leak the lock until that backend is recycled, after which no replica ever sweeps
// again. The automation scheduler's timer job already established the pinned-conn
// shape for exactly this reason; its no-activity job did not, and is the counterexample.
func (r *Repository) WithLedgerPruneLock(ctx context.Context, fn func() error) (bool, error) {
	sqlDB, err := r.db.DB()
	if err != nil {
		return false, err
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	var locked bool
	if err := conn.QueryRowContext(ctx,
		"SELECT pg_try_advisory_lock(hashtext('integrations_ledger_prune'))").Scan(&locked); err != nil {
		return false, err
	}
	if !locked {
		return false, nil
	}
	// Released explicitly rather than left to connection teardown: conn.Close()
	// returns the connection to the POOL rather than closing the backend, so an
	// un-released session lock would ride that connection back into general use and
	// still be held.
	defer func() {
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock(hashtext('integrations_ledger_prune'))")
	}()

	return true, fn()
}

// PruneExpiredPayloads redacts one batch of ledger rows whose personal data has no
// other way out, and returns how many it changed.
//
// TWO arms, because "unreachable by contact-keyed erasure" turned out to have two
// causes and only one of them is age:
//
//  1. ORPHANS — a failed or quarantined delivery that never produced a record. No
//     contact exists to key an erasure off, so nothing can ever target it on request
//     and retention is the only answer. Gated on age.
//  2. FAILED ERASURES — a delivery that DID produce a record whose contact is now
//     gone, but whose redaction never happened. Contact deletion calls the redactor
//     best-effort and only logs on failure, and a second delete is impossible (the
//     soft-delete finds no live row and returns before reaching the redactor), so a
//     missed redaction is PERMANENT today. This arm is a repair, not a policy, so it
//     applies the same write contact deletion would have — consent tombstone included
//     — and needs no age gate: the erasure was already due.
//
// Scoped to `result_slug = 'contact'` because arm 2 asks the contacts table whether
// the subject still exists. Leads may only target system objects backed by an
// adapter, and contact is the only one today; a future slug must get its own arm
// rather than silently borrow this one's liveness check.
//
// Never touches pending/processing rows, or rows with no processed_at: their payload
// is the input a worker is about to use, and FinishEvent's wholesale Save could write
// it straight back over a redaction anyway.
func (r *Repository) PruneExpiredPayloads(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	res := r.db.WithContext(ctx).Exec(`
		UPDATE integration_events e
		   SET raw_payload        = '{}'::jsonb,
		       -- Context is PROJECTED, not blanked. form_id and page_id are provider
		       -- routing identifiers, not subject data, and the retry path reads
		       -- context->>'form_id' to resolve which form a quarantined lead belongs
		       -- to. Blanking it would make every retry answer "this lead's form is not
		       -- enabled" forever — on a form the admin had just enabled — and destroy
		       -- the one recovery path the retry button exists to provide.
		       context            = COALESCE(
		           (SELECT jsonb_object_agg(k, v) FROM jsonb_each(e.context) AS kv(k, v)
		             WHERE k IN ('form_id', 'page_id')),
		           '{}'::jsonb),
		       -- quarantined_fields holds the VALUES the allowlist refused, not just
		       -- their names — the free-text a visitor typed into a field nothing maps.
		       -- Nothing redacted it anywhere before this.
		       quarantined_fields = '{}'::jsonb,
		       -- Tombstoned rather than assumed absent. A consent envelope is only
		       -- written after a record exists, so an orphan "cannot" have one — but
		       -- FinishEvent's wholesale Save can null a committed result_record_id
		       -- while consent, being unmapped, survives. The CASE writes nothing when
		       -- the invariant holds and erases the residue when it does not.
		       consent            = CASE WHEN e.consent IS NULL THEN NULL ELSE ?::jsonb END,
		       redacted_at        = NOW()
		 WHERE e.id IN (
		       SELECT x.id FROM integration_events x
		        WHERE x.redacted_at IS NULL
		          AND x.processed_at IS NOT NULL
		          AND x.status NOT IN (?, ?)
		          AND x.result_slug = 'contact'
		          AND (
		                -- arm 1: orphan, expired
		                (x.result_record_id IS NULL AND x.status IN (?, ?) AND x.created_at < ?)
		                -- arm 2: the subject's contact is gone but the erasure did not happen
		             OR (x.result_record_id IS NOT NULL AND NOT EXISTS (
		                    SELECT 1 FROM contacts c
		                     WHERE c.id = x.result_record_id AND c.deleted_at IS NULL))
		              )
		        ORDER BY x.created_at
		        LIMIT ?
		 )`,
		consentTombstone,
		EventStatusPending, EventStatusProcessing,
		EventStatusFailed, EventStatusQuarantined, cutoff,
		limit)
	return res.RowsAffected, res.Error
}

// RedactedAtForEvents reads the retention marker for a page of deliveries.
//
// A separate targeted read for the same reason ConsentForEvents is one: the column is
// ALTER-added and unmapped, so a boot guard that never ran must degrade this one
// display rather than take down the delivery log.
func (r *Repository) RedactedAtForEvents(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]time.Time, error) {
	out := map[uuid.UUID]time.Time{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID         uuid.UUID
		RedactedAt time.Time
	}
	if err := r.db.WithContext(ctx).Raw(
		`SELECT id, redacted_at FROM integration_events
		  WHERE id IN ? AND redacted_at IS NOT NULL`, ids).Scan(&rows).Error; err != nil {
		return out, err
	}
	for _, row := range rows {
		out[row.ID] = row.RedactedAt
	}
	return out, nil
}

// CountLiveFormSources counts the enabled per-form sources behind a connection —
// whether anything on OUR side is ready to receive.
//
// The diagnose action's last layer, and the one that most often explains "it isn't
// working": a connection can be perfectly healthy and produce nothing because no form
// was ever enabled, which no provider-side probe can see.
func (r *Repository) CountLiveFormSources(ctx context.Context, orgID, connectionID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&LeadSource{}).
		Where("org_id = ? AND connection_id = ? AND status <> ?", orgID, connectionID, SourceStatusDisabled).
		Count(&n).Error
	return n, err
}

// DailyIngestCount is one day's deliveries for a source, split by what became of them.
type DailyIngestCount struct {
	Day     string `json:"day"` // YYYY-MM-DD, UTC
	Written int64  `json:"written"`
	Failed  int64  `json:"failed"`
	Skipped int64  `json:"skipped"`
}

// DailyIngestCounts returns per-day delivery counts for a source over the last N days.
//
// Read-only and for DISPLAY only. It splits `test` deliveries out of `written`, which
// CountCreatedToday deliberately refuses to do — and the difference matters: that
// function backs the daily CAP, where excluding a status a caller can set on the wire
// (google_ads accepts is_test) would hand a leaked key cap-free record creation. A
// chart gates nothing, so the same split is safe here and is what an admin actually
// wants to see. Do not reuse this for any limit.
//
// generate_series so a day with no deliveries is a zero rather than a gap: a sparkline
// that silently omits quiet days compresses time and makes an outage look like normal
// spacing.
func (r *Repository) DailyIngestCounts(ctx context.Context, orgID, sourceID uuid.UUID, days int) ([]DailyIngestCount, error) {
	if days <= 0 || days > 90 {
		days = 30
	}
	var out []DailyIngestCount
	err := r.db.WithContext(ctx).Raw(`
		SELECT to_char(d.day::date, 'YYYY-MM-DD') AS day,
		       COALESCE(SUM(CASE WHEN e.outcome IN ('created','updated') AND e.status <> 'test' THEN 1 ELSE 0 END), 0) AS written,
		       COALESCE(SUM(CASE WHEN e.status = 'failed' THEN 1 ELSE 0 END), 0) AS failed,
		       COALESCE(SUM(CASE WHEN e.status = 'quarantined' THEN 1 ELSE 0 END), 0) AS skipped
		  FROM generate_series(
		           ((NOW() AT TIME ZONE 'UTC')::date - (?::int))::timestamp,
		           ((NOW() AT TIME ZONE 'UTC')::date)::timestamp,
		           interval '1 day') AS d(day)
		  LEFT JOIN integration_events e
		         ON e.org_id = ? AND e.source_id = ?
		        AND (e.created_at AT TIME ZONE 'UTC')::date = d.day::date
		 GROUP BY d.day
		 ORDER BY d.day`, days-1, orgID, sourceID).Scan(&out).Error
	return out, err
}
