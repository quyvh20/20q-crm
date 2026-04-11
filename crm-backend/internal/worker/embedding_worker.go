package worker

import (
	"context"
	"encoding/json"
	"fmt"

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

// EmbeddingWorker manages a pool of goroutines that embed contacts.
type EmbeddingWorker struct {
	queue   chan EmbedJob
	embedSvc *ai.EmbeddingService
	db      *gorm.DB
	logger  *zap.Logger
}

// NewEmbeddingWorker creates a worker pool.
// Call Start() to launch goroutines; Enqueue() to send jobs.
func NewEmbeddingWorker(embedSvc *ai.EmbeddingService, db *gorm.DB, logger *zap.Logger, bufSize int) *EmbeddingWorker {
	return &EmbeddingWorker{
		queue:    make(chan EmbedJob, bufSize),
		embedSvc: embedSvc,
		db:       db,
		logger:   logger,
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
