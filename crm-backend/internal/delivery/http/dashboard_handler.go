package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DashboardHandler serves the caller's own dashboard widgets (P9 Phase B). No
// role gates: every route is scoped to the authenticated caller, and the
// pinned reports' data is fetched through the report run endpoint where
// OLS/FLS apply.
type DashboardHandler struct {
	uc domain.DashboardUseCase
}

func NewDashboardHandler(uc domain.DashboardUseCase) *DashboardHandler {
	return &DashboardHandler{uc: uc}
}

func (h *DashboardHandler) callerIDs(c *gin.Context) (orgID, userID uuid.UUID, ok bool) {
	orgID, okOrg := GetOrgID(c)
	userID, okUser := GetUserID(c)
	if !okOrg || !okUser {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, true
}

func (h *DashboardHandler) ListWidgets(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	widgets, err := h.uc.ListWidgets(c.Request.Context(), orgID, userID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": widgets, "error": nil})
}

func (h *DashboardHandler) AddWidget(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	var in domain.AddWidgetInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request: "+err.Error()))
		return
	}
	w, err := h.uc.AddWidget(c.Request.Context(), orgID, userID, in)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": w, "error": nil})
}

func (h *DashboardHandler) UpdateWidget(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid widget id"))
		return
	}
	var in domain.UpdateWidgetInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request: "+err.Error()))
		return
	}
	if err := h.uc.UpdateWidget(c.Request.Context(), orgID, userID, id, in); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"updated": true}, "error": nil})
}

func (h *DashboardHandler) RemoveWidget(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid widget id"))
		return
	}
	if err := h.uc.RemoveWidget(c.Request.Context(), orgID, userID, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}, "error": nil})
}

func (h *DashboardHandler) Reorder(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	var in domain.ReorderWidgetsInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request: "+err.Error()))
		return
	}
	if err := h.uc.Reorder(c.Request.Context(), orgID, userID, in); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"reordered": true}, "error": nil})
}
