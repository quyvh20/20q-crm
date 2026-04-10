package http

import (
	"net/http"
	"strings"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type DealHandler struct {
	dealUC domain.DealUseCase
}

func NewDealHandler(dealUC domain.DealUseCase) *DealHandler {
	return &DealHandler{dealUC: dealUC}
}

// GET /api/deals
func (h *DealHandler) List(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	var filter domain.DealFilter
	filter.Q = c.Query("q")

	if limitStr := c.Query("limit"); limitStr != "" {
		var limit int
		if _, err := parseIntParam(limitStr, &limit); err == nil {
			filter.Limit = limit
		}
	}
	filter.Cursor = c.Query("cursor")

	if stageIDStr := c.Query("stage_id"); stageIDStr != "" {
		if id, err := uuid.Parse(strings.TrimSpace(stageIDStr)); err == nil {
			filter.StageID = &id
		}
	}
	if ownerIDStr := c.Query("owner_user_id"); ownerIDStr != "" {
		if id, err := uuid.Parse(strings.TrimSpace(ownerIDStr)); err == nil {
			filter.OwnerUserID = &id
		}
	}

	deals, nextCursor, err := h.dealUC.List(c.Request.Context(), orgID, filter)
	if err != nil {
		handleAppError(c, err)
		return
	}

	total, _ := h.dealUC.Count(c.Request.Context(), orgID)
	meta := domain.CursorMeta{
		NextCursor: nextCursor,
		HasMore:    nextCursor != "",
		Total:      total,
	}
	c.JSON(http.StatusOK, domain.SuccessWithMeta(deals, meta))
}

// GET /api/deals/:id
func (h *DealHandler) GetByID(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid deal id"))
		return
	}
	deal, err := h.dealUC.GetByID(c.Request.Context(), orgID, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(deal))
}

// POST /api/deals
func (h *DealHandler) Create(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	var input domain.CreateDealInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	deal, err := h.dealUC.Create(c.Request.Context(), orgID, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, domain.Success(deal))
}

// PUT /api/deals/:id
func (h *DealHandler) Update(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid deal id"))
		return
	}
	var input domain.UpdateDealInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	deal, err := h.dealUC.Update(c.Request.Context(), orgID, id, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(deal))
}

// DELETE /api/deals/:id
func (h *DealHandler) Delete(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid deal id"))
		return
	}
	if err := h.dealUC.Delete(c.Request.Context(), orgID, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "deal deleted"}))
}

// PATCH /api/deals/:id/stage
func (h *DealHandler) ChangeStage(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid deal id"))
		return
	}
	var input domain.UpdateDealStageInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	deal, err := h.dealUC.ChangeStage(c.Request.Context(), orgID, id, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(deal))
}

// GET /api/pipeline/forecast
func (h *DealHandler) Forecast(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	rows, err := h.dealUC.Forecast(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(rows))
}
