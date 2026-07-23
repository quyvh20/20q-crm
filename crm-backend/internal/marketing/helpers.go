package marketing

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// actorFromCtx reads the authenticated org + user set by the auth middleware.
// Aborts 401 when absent. Shared by the marketing handlers.
func actorFromCtx(c *gin.Context) (orgID, userID uuid.UUID, ok bool) {
	o, exists := c.Get("org_id")
	if !exists {
		abortErr(c, http.StatusUnauthorized, "unauthorized")
		return uuid.Nil, uuid.Nil, false
	}
	u, _ := c.Get("user_id")
	orgID, _ = o.(uuid.UUID)
	userID, _ = u.(uuid.UUID)
	if orgID == uuid.Nil {
		abortErr(c, http.StatusUnauthorized, "unauthorized")
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, true
}

func abortErr(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}
