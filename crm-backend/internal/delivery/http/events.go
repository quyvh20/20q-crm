package http

import (
	"context"
	"net/http"
	"time"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type EventsHandler struct {
	redis *redis.Client
}

func NewEventsHandler(redisClient *redis.Client) *EventsHandler {
	return &EventsHandler{redis: redisClient}
}

// Stream handles GET /api/events and streams Redis Pub/Sub messages via SSE.
func (h *EventsHandler) Stream(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Set SSE headers
	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")
	// Flush immediately to establish connection
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	// Subscribe to BOTH the org-wide channel (AI/voice job events, broadcast to
	// every member) and the caller's per-user channel (in-app notifications,
	// A6 — private to this member so payloads never leak across the org). A
	// missing user id (shouldn't happen behind AuthMiddleware) degrades to
	// org-only rather than failing the stream.
	channels := []string{domain.OrgNotificationChannel(orgID)}
	if userID, ok := GetUserID(c); ok {
		channels = append(channels, domain.UserNotificationChannel(orgID, userID))
	}
	pubsub := h.redis.Subscribe(ctx, channels...)
	defer pubsub.Close()

	ch := pubsub.Channel()

	// Keep-alive ticker (prevent load balancer drops)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			return
		case msg := <-ch:
			// Send JSON payload from Redis directly down SSE pipe
			w.Write([]byte("data: " + msg.Payload + "\n\n"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		case <-ticker.C:
			// Send empty comment to keep connection alive
			w.Write([]byte(":\n\n"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}
