package integrations

import (
	"context"
	"errors"
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
func (r *Repository) UpdateSource(ctx context.Context, s *LeadSource) error {
	return r.db.WithContext(ctx).Save(s).Error
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

// TouchSourceUsed stamps last_used_at and resets the failure counter. Best-effort:
// a failure here must never fail a lead that was already written.
func (r *Repository) TouchSourceUsed(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Model(&LeadSource{}).Where("id = ?", id).
		Updates(map[string]any{"last_used_at": time.Now(), "consecutive_failures": 0}).Error
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
