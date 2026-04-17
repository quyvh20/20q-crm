package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"encoding/json"
	"crm-backend/internal/ai"
	"crm-backend/internal/worker"
)

type ActivityHandler struct {
	activityUC domain.ActivityUseCase
	queue      *worker.AIJobQueue
}

func NewActivityHandler(activityUC domain.ActivityUseCase, queue *worker.AIJobQueue) *ActivityHandler {
	return &ActivityHandler{activityUC: activityUC, queue: queue}
}

// GET /api/activities?deal_id=...&contact_id=...
func (h *ActivityHandler) List(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	var filter domain.ActivityFilter
	if dealIDStr := c.Query("deal_id"); dealIDStr != "" {
		if id, err := uuid.Parse(dealIDStr); err == nil {
			filter.DealID = &id
		}
	}
	if contactIDStr := c.Query("contact_id"); contactIDStr != "" {
		if id, err := uuid.Parse(contactIDStr); err == nil {
			filter.ContactID = &id
		}
	}

	activities, err := h.activityUC.List(c.Request.Context(), orgID, filter)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(activities))
}

// POST /api/activities
func (h *ActivityHandler) Create(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	var input domain.CreateActivityInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	activity, err := h.activityUC.Create(c.Request.Context(), orgID, userID.(uuid.UUID), input)
	if err != nil {
		handleAppError(c, err)
		return
	}

	// Trigger sentiment analysis asynchronously if note has body
	if activity.Body != nil && *activity.Body != "" {
		payloadBytes, _ := json.Marshal(worker.SentimentPayload{ActivityID: activity.ID})
		job := &worker.AIJob{
			JobID:    uuid.New(),
			OrgID:    orgID,
			UserID:   userID.(uuid.UUID),
			TaskType: string(ai.TaskSentiment),
			Payload:  payloadBytes,
		}
		_ = h.queue.Enqueue(c.Request.Context(), job) // ignore err, non-critical background task
	}

	c.JSON(http.StatusCreated, domain.Success(activity))
}
