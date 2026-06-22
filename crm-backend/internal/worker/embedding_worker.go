package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"crm-backend/internal/ai"
	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// EmbedJob is queued whenever a contact is created/updated.
type EmbedJob struct {
	ContactID    uuid.UUID
	OrgID        uuid.UUID
	FirstName    string
	LastName     string
	Email        *string
	Phone        *string
	CompanyName  *string
	CustomFields json.RawMessage
}

// recordEmbedJob is queued whenever a searchable record (any object — custom
// objects first, P6) is created or updated. It carries the pre-built content text
// so the worker only has to embed + persist into the generic record_embeddings
// index.
type recordEmbedJob struct {
	OrgID    uuid.UUID
	Slug     string
	RecordID uuid.UUID
	Content  string
}

// EmbeddingWorker manages a pool of goroutines that embed records. It serves two
// indexes that never overlap: contacts on their native contacts.embedding column
// (the existing fast path, untouched) and every other searchable object on the
// generic record_embeddings table (P6) via embedRepo.
type EmbeddingWorker struct {
	queue       chan EmbedJob
	recordQueue chan recordEmbedJob
	embedSvc    *ai.EmbeddingService
	embedRepo   domain.RecordEmbeddingRepository
	db          *gorm.DB
	logger      *zap.Logger
}

// NewEmbeddingWorker creates a worker pool.
// Call Start() to launch goroutines; Enqueue()/EnqueueRecord() to send jobs.
// embedRepo backs the generic record_embeddings index (P6); a nil repo disables
// the generic path while leaving contact embedding intact.
func NewEmbeddingWorker(embedSvc *ai.EmbeddingService, embedRepo domain.RecordEmbeddingRepository, db *gorm.DB, logger *zap.Logger, bufSize int) *EmbeddingWorker {
	return &EmbeddingWorker{
		queue:       make(chan EmbedJob, bufSize),
		recordQueue: make(chan recordEmbedJob, bufSize),
		embedSvc:    embedSvc,
		embedRepo:   embedRepo,
		db:          db,
		logger:      logger,
	}
}

// Start launches n worker goroutines. Blocks until ctx is cancelled.
func (w *EmbeddingWorker) Start(ctx context.Context, n int) {
	for i := 0; i < n; i++ {
		go w.run(ctx)
	}
	w.logger.Info("Embedding worker pool started", zap.Int("workers", n))
}

// Enqueue adds a job to the channel (non-blocking; drops if full).
func (w *EmbeddingWorker) Enqueue(job EmbedJob) {
	select {
	case w.queue <- job:
	default:
		w.logger.Warn("embedding queue full, dropping job", zap.String("contact_id", job.ContactID.String()))
	}
}

// EnqueueContact adapts domain.Contact to EmbedJob and queues it.
func (w *EmbeddingWorker) EnqueueContact(c *domain.Contact) {
	if c == nil {
		return
	}
	var compName *string
	if c.Company != nil {
		compName = &c.Company.Name
	}
	job := EmbedJob{
		ContactID:    c.ID,
		OrgID:        c.OrgID,
		FirstName:    c.FirstName,
		LastName:     c.LastName,
		Email:        c.Email,
		Phone:        c.Phone,
		CompanyName:  compName,
	}
	if len(c.CustomFields) > 0 {
		job.CustomFields = json.RawMessage(c.CustomFields)
	}
	w.Enqueue(job)
}

// ============================================================
// Internal
// ============================================================

func (w *EmbeddingWorker) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-w.queue:
			w.process(ctx, job)
		case rjob := <-w.recordQueue:
			w.processRecord(ctx, rjob)
		}
	}
}

func (w *EmbeddingWorker) process(ctx context.Context, job EmbedJob) {
	// Parse custom fields for richer text
	var customMap map[string]interface{}
	if job.CustomFields != nil {
		json.Unmarshal(job.CustomFields, &customMap) //nolint:errcheck
	}

	text := ai.EmbedContact(job.FirstName, job.LastName, job.Email, job.Phone, job.CompanyName, customMap)
	if text == "" {
		return
	}

	vec, err := w.embedSvc.EmbedText(ctx, text)
	if err != nil {
		w.logger.Error("failed to embed contact", zap.String("contact_id", job.ContactID.String()), zap.Error(err))
		return
	}

	pgVec := pgvector.NewVector(vec)
	result := w.db.WithContext(ctx).
		Exec(fmt.Sprintf("UPDATE contacts SET embedding = '%v' WHERE id = ? AND org_id = ?", pgVec),
			job.ContactID, job.OrgID)
	if result.Error != nil {
		w.logger.Error("failed to update contact embedding", zap.Error(result.Error))
		return
	}

	w.logger.Debug("embedded contact", zap.String("contact_id", job.ContactID.String()))
}

// ============================================================
// Generic record index (P6) — implements domain.RecordIndexer
// ============================================================

// EnqueueRecord schedules (re)indexing of a searchable record's content into the
// generic record_embeddings index. Non-blocking: drops the job if the queue is
// full (the same back-pressure policy as contact embedding) so a write is never
// blocked by indexing.
func (w *EmbeddingWorker) EnqueueRecord(orgID uuid.UUID, slug string, recordID uuid.UUID, content string) {
	select {
	case w.recordQueue <- recordEmbedJob{OrgID: orgID, Slug: slug, RecordID: recordID, Content: content}:
	default:
		w.logger.Warn("record embedding queue full, dropping job",
			zap.String("object", slug), zap.String("record_id", recordID.String()))
	}
}

// RemoveRecord deletes a record's row from the generic index (on record delete).
// Synchronous and idempotent — removing a missing row is a no-op.
func (w *EmbeddingWorker) RemoveRecord(ctx context.Context, orgID uuid.UUID, slug string, recordID uuid.UUID) error {
	if w.embedRepo == nil {
		return nil
	}
	return w.embedRepo.Delete(ctx, orgID, slug, recordID)
}

// processRecord embeds a record's content and upserts it into record_embeddings.
// Embedding is best-effort: if it fails (or the embed service is unconfigured),
// the content is still stored so fulltext search keeps working — only semantic
// search waits for a later successful embed. Empty content removes any stale row.
func (w *EmbeddingWorker) processRecord(ctx context.Context, job recordEmbedJob) {
	if w.embedRepo == nil {
		return
	}

	content := strings.TrimSpace(job.Content)
	if content == "" {
		if err := w.embedRepo.Delete(ctx, job.OrgID, job.Slug, job.RecordID); err != nil {
			w.logger.Error("failed to remove empty record embedding",
				zap.String("object", job.Slug), zap.String("record_id", job.RecordID.String()), zap.Error(err))
		}
		return
	}

	entry := domain.RecordEmbedding{
		OrgID:      job.OrgID,
		ObjectSlug: job.Slug,
		RecordID:   job.RecordID,
		Content:    content,
	}

	if w.embedSvc != nil {
		vec, err := w.embedSvc.EmbedText(ctx, content)
		if err != nil {
			w.logger.Warn("failed to embed record; storing content-only for fulltext",
				zap.String("object", job.Slug), zap.String("record_id", job.RecordID.String()), zap.Error(err))
		} else {
			entry.Embedding = vec
		}
	}

	if err := w.embedRepo.Upsert(ctx, entry); err != nil {
		w.logger.Error("failed to upsert record embedding",
			zap.String("object", job.Slug), zap.String("record_id", job.RecordID.String()), zap.Error(err))
		return
	}
	w.logger.Debug("indexed record",
		zap.String("object", job.Slug), zap.String("record_id", job.RecordID.String()), zap.Bool("vector", len(entry.Embedding) > 0))
}
