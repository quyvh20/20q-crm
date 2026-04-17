package http

import (
	"context"
	"net/http"
	"time"

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

	// Subscribe to org's SSE channel
	channelName := "sse:" + orgID.String()
	pubsub := h.redis.Subscribe(ctx, channelName)
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
