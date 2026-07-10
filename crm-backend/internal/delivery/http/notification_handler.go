package http

import (
	"net/http"
	"strconv"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// NotificationHandler serves the caller's own in-app inbox (the header bell).
// There is no capability gate: every operation is scoped to (org, caller) from
// the request context, so a member can only ever see or mark their own rows.
type NotificationHandler struct {
	uc domain.NotificationUseCase
}

func NewNotificationHandler(uc domain.NotificationUseCase) *NotificationHandler {
	return &NotificationHandler{uc: uc}
}

// List returns a newest-first page of the caller's notifications.
// Query: limit (<=100), cursor (opaque), unread=true (unread-only).
func (h *NotificationHandler) List(c *gin.Context) {
	orgID, userID, ok := h.identity(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	page, err := h.uc.List(c.Request.Context(), orgID, userID, domain.NotificationListInput{
		Limit:      limit,
		Cursor:     c.Query("cursor"),
		UnreadOnly: c.Query("unread") == "true",
	})
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": page, "error": nil})
}

func (h *NotificationHandler) UnreadCount(c *gin.Context) {
	orgID, userID, ok := h.identity(c)
	if !ok {
		return
	}
	n, err := h.uc.UnreadCount(c.Request.Context(), orgID, userID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"unread_count": n}, "error": nil})
}

func (h *NotificationHandler) MarkRead(c *gin.Context) {
	orgID, userID, ok := h.identity(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid notification id"))
		return
	}
	if err := h.uc.MarkRead(c.Request.Context(), orgID, userID, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"read": true}, "error": nil})
}

func (h *NotificationHandler) MarkAllRead(c *gin.Context) {
	orgID, userID, ok := h.identity(c)
	if !ok {
		return
	}
	n, err := h.uc.MarkAllRead(c.Request.Context(), orgID, userID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"marked": n}, "error": nil})
}

// identity extracts the (org, caller) pair every handler scopes to. Writes the
// 401 and returns ok=false when either is absent.
func (h *NotificationHandler) identity(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return uuid.Nil, uuid.Nil, false
	}
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, true
}
