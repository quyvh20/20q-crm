package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type TaskHandler struct {
	taskUC domain.TaskUseCase
}

func NewTaskHandler(taskUC domain.TaskUseCase) *TaskHandler {
	return &TaskHandler{taskUC: taskUC}
}

// GET /api/tasks?deal_id=...&contact_id=...&assigned_to=...&completed=true|false
func (h *TaskHandler) List(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	var filter domain.TaskFilter
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
	if assignedStr := c.Query("assigned_to"); assignedStr != "" {
		if id, err := uuid.Parse(assignedStr); err == nil {
			filter.AssignedTo = &id
		}
	}
	if completedStr := c.Query("completed"); completedStr != "" {
		v := completedStr == "true"
		filter.Completed = &v
	}

	tasks, err := h.taskUC.List(c.Request.Context(), orgID, filter)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(tasks))
}

// POST /api/tasks
func (h *TaskHandler) Create(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	var input domain.CreateTaskInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	task, err := h.taskUC.Create(c.Request.Context(), orgID, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, domain.Success(task))
}

// PUT /api/tasks/:id
func (h *TaskHandler) Update(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid task ID"))
		return
	}

	var input domain.UpdateTaskInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	task, err := h.taskUC.Update(c.Request.Context(), orgID, id, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(task))
}

// DELETE /api/tasks/:id
func (h *TaskHandler) Delete(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid task ID"))
		return
	}

	if err := h.taskUC.Delete(c.Request.Context(), orgID, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(nil))
}
