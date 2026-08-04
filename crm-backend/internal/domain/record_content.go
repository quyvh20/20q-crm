package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Indexed content — the ONE definition (R6.3).
// ============================================================
//
// The text that lands in record_embeddings.content is what both fulltext and the
// embedding are built from, so the online write path (RecordService.Create/Update
// → indexRecord) and the offline reconciliation sweep (worker.EmbeddingReconciler)
// MUST produce byte-identical content for the same row. Before R6.3 the builder
// lived unexported in package usecase, which meant the sweep would have had to
// re-implement it and drift the moment either side changed — so it lives here, on
// the domain types both layers already share, and usecase delegates to it.

// CustomRecordToUniform projects a JSONB-backed record into the uniform shape.
// The owner comes off its column and is surfaced BOTH as UniformRecord.OwnerUserID
// (the first-class field) and inside Fields, so the generic renderer, the report
// field catalog and the list filters can address it like any other value without
// the registry having to carry an owner field row.
//
// Note it does NOT apply the read-time display override (usecase.applyCustomDisplay,
// R8): the write path indexes the record straight off this projection, so the
// indexed title is the stored display_name. The sweep must match that exactly.
func CustomRecordToUniform(slug string, rec *CustomObjectRecord) *UniformRecord {
	fields := map[string]interface{}{}
	if len(rec.Data) > 0 {
		_ = json.Unmarshal(rec.Data, &fields)
	}
	if rec.OwnerUserID != nil {
		fields["owner_user_id"] = rec.OwnerUserID.String()
	}
	return &UniformRecord{
		ID:          rec.ID,
		Object:      slug,
		Display:     rec.DisplayName,
		OwnerUserID: rec.OwnerUserID,
		Fields:      fields,
		CreatedAt:   rec.CreatedAt,
		UpdatedAt:   rec.UpdatedAt,
	}
}

// BuildRecordContent renders a record into the text that gets embedded and
// fulltext-indexed: its display title followed by "key: value" for each non-empty
// field. Field keys are sorted so the content (and thus the embedding) is
// deterministic for a given record, independent of Go's map iteration order.
func BuildRecordContent(rec *UniformRecord) string {
	if rec == nil {
		return ""
	}
	parts := make([]string, 0, len(rec.Fields)+1)
	if rec.Display != "" {
		parts = append(parts, rec.Display)
	}

	keys := make([]string, 0, len(rec.Fields))
	for k := range rec.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v := FieldDisplayString(rec.Fields[k]); v != "" {
			parts = append(parts, k+": "+v)
		}
	}
	return strings.Join(parts, " ")
}

// FieldDisplayString renders a JSON-decoded field value as display text. Strings
// pass through; numbers/bools are stringified; nil becomes "".
func FieldDisplayString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// ============================================================
// Reconciliation backlog (R6.3)
// ============================================================

// RecordIndexCandidate is one custom record the generic search index is missing
// (or holds without a vector). It carries the whole row the content builder needs,
// so the sweep never has to re-read the record to index it.
type RecordIndexCandidate struct {
	OrgID       uuid.UUID  `gorm:"column:org_id"`
	ObjectSlug  string     `gorm:"column:object_slug"`
	RecordID    uuid.UUID  `gorm:"column:record_id"`
	DisplayName string     `gorm:"column:display_name"`
	Data        JSON       `gorm:"column:data"`
	OwnerUserID *uuid.UUID `gorm:"column:owner_user_id"`
}

// Content renders the candidate exactly as the online write path would, by going
// through the same projection + builder RecordService.indexRecord uses.
func (c RecordIndexCandidate) Content() string {
	rec := CustomObjectRecord{
		ID:          c.RecordID,
		OrgID:       c.OrgID,
		DisplayName: c.DisplayName,
		Data:        c.Data,
		OwnerUserID: c.OwnerUserID,
	}
	return BuildRecordContent(CustomRecordToUniform(c.ObjectSlug, &rec))
}

// RecordIndexBacklog finds rows that BELONG in a search index but are not usably
// in one. It exists because "re-embed everything with a NULL embedding" cannot
// heal the failure it was written for, and the two indexes fail differently:
//
//   - contacts embed IN PLACE (UPDATE contacts SET embedding = …), so a dropped
//     enqueue leaves the row present with a NULL embedding. UnvectoredContacts
//     finds it.
//   - custom records embed into a SEPARATE table, and the worker is what inserts
//     the row. A dropped EnqueueRecord means no record_embeddings row is ever
//     written at all, so "WHERE embedding IS NULL" can never see it — only an
//     ANTI-JOIN can. MissingRecords is that anti-join, and it is also what
//     backfills an object whose `searchable` flag was flipped ON after its records
//     already existed (custom_object_usecase.UpdateDef does no backfill, so those
//     records are otherwise invisible to search until someone edits each one).
//   - a record whose embed CALL failed is a third case: processRecord deliberately
//     upserts content-only so fulltext keeps working, leaving a row that DOES exist
//     with a NULL embedding. UnvectoredRecords finds those.
//
// Every method is bounded by BOTH a per-org and a global cap so one org's backlog
// can never starve the rest of the fleet, and so the paid embedding calls a single
// sweep can trigger have a hard ceiling.
type RecordIndexBacklog interface {
	// MissingRecords returns searchable records with no record_embeddings row.
	MissingRecords(ctx context.Context, perOrg, limit int) ([]RecordIndexCandidate, error)
	// UnvectoredRecords returns searchable records whose index row exists but
	// carries no vector (a failed/absent embed).
	UnvectoredRecords(ctx context.Context, perOrg, limit int) ([]RecordIndexCandidate, error)
	// UnvectoredContacts returns contacts whose native contacts.embedding is NULL,
	// with Company preloaded so the embedded text matches the write path's.
	UnvectoredContacts(ctx context.Context, perOrg, limit int) ([]Contact, error)
	// WithReconcileLock runs fn under a fleet-wide singleton advisory lock, taken
	// on a PINNED connection. Reports whether the lock was acquired; a false return
	// means another pod is already sweeping and this one must do nothing.
	WithReconcileLock(ctx context.Context, fn func() error) (bool, error)
}
