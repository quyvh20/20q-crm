package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ReportCommentHandler serves a report's comment thread. Authorization (see the
// report to read; level >= comment to post; author-or-manage to delete) lives
// in the usecase.
type ReportCommentHandler struct {
	uc domain.ReportCommentUseCase
}

func NewReportCommentHandler(uc domain.ReportCommentUseCase) *ReportCommentHandler {
	return &ReportCommentHandler{uc: uc}
}

func (h *ReportCommentHandler) ids(c *gin.Context) (orgID, userID, reportID uuid.UUID, ok bool) {
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

func (h *ReportCommentHandler) List(c *gin.Context) {
	orgID, userID, reportID, ok := h.ids(c)
	if !ok {
		return
	}
	comments, err := h.uc.List(c.Request.Context(), orgID, userID, reportID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": comments, "error": nil})
}

func (h *ReportCommentHandler) Add(c *gin.Context) {
	orgID, userID, reportID, ok := h.ids(c)
	if !ok {
		return
	}
	var in domain.AddReportCommentInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid request: "+err.Error()))
		return
	}
	comment, err := h.uc.Add(c.Request.Context(), orgID, userID, reportID, in)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": comment, "error": nil})
}

func (h *ReportCommentHandler) Remove(c *gin.Context) {
	orgID, userID, reportID, ok := h.ids(c)
	if !ok {
		return
	}
	commentID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid comment id"))
		return
	}
	if err := h.uc.Delete(c.Request.Context(), orgID, userID, reportID, commentID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"removed": true}, "error": nil})
}
