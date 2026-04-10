package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ActivityHandler struct {
	activityUC domain.ActivityUseCase
}

func NewActivityHandler(activityUC domain.ActivityUseCase) *ActivityHandler {
	return &ActivityHandler{activityUC: activityUC}
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
	c.JSON(http.StatusCreated, domain.Success(activity))
}
