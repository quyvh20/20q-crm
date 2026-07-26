package marketing

import (
	"testing"

	"github.com/gin-gonic/gin"
)

// TestAnalyticsRoutes_NoGinConflict proves the analytics routes register on the same
// engine as the campaign handler's /campaigns/:id tree WITHOUT a gin wildcard-name
// conflict (which would panic only at startup, not at build). The per-campaign analytics
// route shares the :id param name, so it slots into the existing tree cleanly.
func TestAnalyticsRoutes_NoGinConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	pass := func(c *gin.Context) { c.Next() }
	protected := []gin.HandlerFunc{pass}
	requireCap := func(string) gin.HandlerFunc { return pass }

	// The campaign handler owns /api/marketing/campaigns/:id and its sub-routes.
	cg := r.Group("/api/marketing/campaigns")
	cg.Use(protected...)
	cg.GET("/:id", pass)
	cg.GET("/:id/progress", pass)

	// If the analytics handler's /campaigns/:id/analytics used a different wildcard name
	// (or otherwise conflicted), this call would panic.
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("analytics route registration panicked (gin route conflict): %v", rec)
		}
	}()
	NewAnalyticsHandler(nil, nil).RegisterRoutes(r, protected, requireCap)
}
