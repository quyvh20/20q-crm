package http

import (
	"crm-backend/pkg/config"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes wires all API routes to the Gin engine.
func RegisterRoutes(router *gin.Engine, authHandler *AuthHandler, cfg *config.Config) {
	api := router.Group("/api")

	// ── Auth (public) ──────────────────────────────────
	auth := api.Group("/auth")
	{
		auth.POST("/register", authHandler.Register)
		auth.POST("/login", authHandler.Login)
		auth.POST("/refresh", authHandler.Refresh)
		auth.POST("/logout", authHandler.Logout)

		// Google OAuth
		auth.GET("/google", authHandler.GoogleLogin)
		auth.GET("/google/callback", authHandler.GoogleCallback)

		// Protected
		auth.GET("/me", AuthMiddleware(cfg.JWTSecret), authHandler.Me)
	}

	// ── Protected API routes (example structure) ───────
	// Uncomment and add handlers as features are built:
	//
	// protected := api.Group("/")
	// protected.Use(AuthMiddleware(cfg.JWTSecret))
	// {
	// 	// Contacts
	// 	contacts := protected.Group("/contacts")
	// 	{
	// 		contacts.GET("/", contactHandler.List)
	// 		contacts.POST("/", RequireRole("admin", "manager", "sales"), contactHandler.Create)
	// 	}
	//
	// 	// Deals
	// 	deals := protected.Group("/deals")
	// 	{
	// 		deals.GET("/", dealHandler.List)
	// 		deals.POST("/", RequireRole("admin", "manager", "sales"), dealHandler.Create)
	// 	}
	//
	// 	// Admin-only
	// 	admin := protected.Group("/admin")
	// 	admin.Use(RequireRole("admin"))
	// 	{
	// 		admin.GET("/users", userHandler.List)
	// 	}
	// }
}
