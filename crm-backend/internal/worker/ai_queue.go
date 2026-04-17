package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"crm-backend/internal/ai"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// AIJob represents an asynchronous AI task.
type AIJob struct {
	JobID     uuid.UUID       `json:"job_id"`
	OrgID     uuid.UUID       `json:"org_id"`
	UserID    uuid.UUID       `json:"user_id"`
	TaskType  string          `json:"task_type"` // e.g., "deal_score", "meeting_summary"
	Payload   json.RawMessage `json:"payload"`
	Status    string          `json:"status"` // "pending", "processing", "completed", "failed"
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// AIJobQueue manages enqueueing and processing AI jobs via Redis.
type AIJobQueue struct {
	redis   *redis.Client
	gateway *ai.AIGateway
	db      *gorm.DB
	logger  *zap.Logger
}

func NewAIJobQueue(redisClient *redis.Client, gateway *ai.AIGateway, db *gorm.DB, logger *zap.Logger) *AIJobQueue {
	return &AIJobQueue{
		redis:   redisClient,
		gateway: gateway,
		db:      db,
		logger:  logger,
	}
}

// Enqueue pushes a job to the Redis list and initializes its status hash.
func (q *AIJobQueue) Enqueue(ctx context.Context, job *AIJob) error {
	job.CreatedAt = time.Now()
	job.Status = "pending"

	jobData, err := json.Marshal(job)
	if err != nil {
		return err
	}

	queueKey := "ai:jobs"
	statusKey := fmt.Sprintf("ai:job:%s", job.JobID.String())

	// Set initial status (expire in 24 hours)
	err = q.redis.Set(ctx, statusKey, jobData, 24*time.Hour).Err()
	if err != nil {
		return err
	}

	// Push job to queue
	err = q.redis.LPush(ctx, queueKey, jobData).Err()
	if err != nil {
		return err
	}

	q.logger.Info("AI job enqueued", zap.String("job_id", job.JobID.String()), zap.String("task", job.TaskType))
	return nil
}

// GetStatus retrieves the current job status.
func (q *AIJobQueue) GetStatus(ctx context.Context, jobID string) (*AIJob, error) {
	statusKey := fmt.Sprintf("ai:job:%s", jobID)
	val, err := q.redis.Get(ctx, statusKey).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("job not found")
		}
		return nil, err
	}

	var job AIJob
	if err := json.Unmarshal([]byte(val), &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// Start spawns worker goroutines to process jobs from the queue.
func (q *AIJobQueue) Start(ctx context.Context, workers int) {
	for i := 0; i < workers; i++ {
		go q.runWorker(ctx, i)
	}
	q.logger.Info("AI job queue started", zap.Int("workers", workers))
}

func (q *AIJobQueue) runWorker(ctx context.Context, id int) {
	defer func() {
		if r := recover(); r != nil {
			q.logger.Error("AI worker panic recovered", zap.Any("panic", r), zap.Int("worker_id", id))
		}
	}()

	if q.redis == nil {
		q.logger.Warn("AI worker skipped: redis client is nil", zap.Int("worker_id", id))
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
			res, err := q.redis.BRPop(ctx, 5*time.Second, "ai:jobs").Result()
			if err != nil {
				if err != redis.Nil {
					q.logger.Error("BRPop error", zap.Error(err))
				}
				continue
			}

			if len(res) < 2 {
				continue
			}

			jobData := res[1]
			var job AIJob
			if err := json.Unmarshal([]byte(jobData), &job); err != nil {
				q.logger.Error("failed to unmarshal job", zap.Error(err))
				continue
			}

			q.processJob(ctx, &job)
		}
	}
}

func (q *AIJobQueue) processJob(ctx context.Context, job *AIJob) {
	// Update status to processing
	job.Status = "processing"
	q.updateJobStatus(ctx, job)

	var result json.RawMessage
	var err error

	// Route based on TaskType
	switch job.TaskType {
	case string(ai.TaskDealScore):
		result, err = ProcessDealScore(ctx, q, job)
	case string(ai.TaskMeetingSummary):
		result, err = ProcessMeetingSummary(ctx, q, job)
	case string(ai.TaskSentiment):
		result, err = ProcessSentimentAnalysis(ctx, q, job)
	default:
		err = fmt.Errorf("unknown task type: %s", job.TaskType)
	}

	// Update job record
	if err != nil {
		job.Status = "failed"
		job.Error = err.Error()
		q.logger.Error("AI job failed", zap.String("job_id", job.JobID.String()), zap.Error(err))
	} else {
		job.Status = "completed"
		job.Result = result
		q.logger.Info("AI job completed", zap.String("job_id", job.JobID.String()))
	}

	q.updateJobStatus(ctx, job)

	// Publish completion event for SSE subscribers
	sseChan := fmt.Sprintf("sse:%s", job.OrgID.String())
	eventPayload := map[string]interface{}{
		"type":      "job_complete",
		"job_id":    job.JobID,
		"task_type": job.TaskType,
		"status":    job.Status,
	}
	if job.Status == "completed" {
		eventPayload["result"] = job.Result
	} else {
		eventPayload["error"] = job.Error
	}

	eventData, _ := json.Marshal(eventPayload)
	q.redis.Publish(ctx, sseChan, eventData)
}

func (q *AIJobQueue) updateJobStatus(ctx context.Context, job *AIJob) {
	jobData, _ := json.Marshal(job)
	statusKey := fmt.Sprintf("ai:job:%s", job.JobID.String())
	q.redis.Set(ctx, statusKey, jobData, 24*time.Hour)
}

// GetDB returns the database connection (for workers needing it).
func (q *AIJobQueue) GetDB() *gorm.DB {
	return q.db
}

// GetGateway returns the AI gateway.
func (q *AIJobQueue) GetGateway() *ai.AIGateway {
	return q.gateway
}

// GetRedis returns the redis client (for workers needing it).
func (q *AIJobQueue) GetRedis() *redis.Client {
	return q.redis
}
