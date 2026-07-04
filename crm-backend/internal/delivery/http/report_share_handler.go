package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ReportShareHandler serves a report's share list. Authorization (see the report
// to list; manage it to add/remove) lives in the usecase.
type ReportShareHandler struct {
	uc domain.ReportShareUseCase
}

func NewReportShareHandler(uc domain.ReportShareUseCase) *ReportShareHandler {
	return &ReportShareHandler{uc: uc}
}

func (h *ReportShareHandler) ids(c *gin.Context) (orgID, userID, reportID uuid.UUID, ok bool) {
	orgID, okOrg := GetOrgID(c)
	userID, okUser := GetUserID(c)
	if !okOrg || !okUser {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	reportID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid report id"))
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, reportID, true
}

func (h *ReportShareHandler) List(c *gin.Context) {
	orgID, userID, reportID, ok := h.ids(c)
	if !ok {
		return
	}
	shares, err := h.uc.List(c.Request.Context(), orgID, userID, reportID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": shares, "error": nil})
}

func (h *ReportShareHandler) Add(c *gin.Context) {
	orgID, userID, reportID, ok := h.ids(c)
	if !ok {
		return
	}
	var in domain.AddReportShareInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request: "+err.Error()))
		return
	}
	if err := h.uc.Add(c.Request.Context(), orgID, userID, reportID, in); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": gin.H{"shared": true}, "error": nil})
}

func (h *ReportShareHandler) Remove(c *gin.Context) {
	orgID, userID, reportID, ok := h.ids(c)
	if !ok {
		return
	}
	shareID, err := uuid.Parse(c.Param("shareId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid share id"))
		return
	}
	if err := h.uc.Remove(c.Request.Context(), orgID, userID, reportID, shareID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"removed": true}, "error": nil})
}
