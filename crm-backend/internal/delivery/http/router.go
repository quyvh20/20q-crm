package http

import (
	"crm-backend/pkg/config"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes wires all API routes to the Gin engine.
func RegisterRoutes(router *gin.Engine, authHandler *AuthHandler, contactHandler *ContactHandler, companyHandler *CompanyHandler, tagHandler *TagHandler, cfg *config.Config) {
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

	// ── Protected API routes ───────────────────────────
	protected := api.Group("/")
	protected.Use(AuthMiddleware(cfg.JWTSecret))
	{
		// Contacts
		contacts := protected.Group("/contacts")
		{
			contacts.GET("", contactHandler.List)
			contacts.GET("/:id", contactHandler.GetByID)
			contacts.POST("", RequireRole("admin", "manager", "sales"), contactHandler.Create)
			contacts.PUT("/:id", RequireRole("admin", "manager", "sales"), contactHandler.Update)
			contacts.DELETE("/:id", RequireRole("admin", "manager"), contactHandler.Delete)
			contacts.POST("/import", RequireRole("admin", "manager"), contactHandler.Import)
			contacts.POST("/bulk-action", RequireRole("admin", "manager", "sales"), contactHandler.BulkAction)
		}

		// Companies
		companies := protected.Group("/companies")
		{
			companies.GET("", companyHandler.List)
			companies.GET("/:id", companyHandler.GetByID)
			companies.POST("", RequireRole("admin", "manager", "sales"), companyHandler.Create)
			companies.PUT("/:id", RequireRole("admin", "manager", "sales"), companyHandler.Update)
			companies.DELETE("/:id", RequireRole("admin", "manager"), companyHandler.Delete)
		}

		// Tags
		tags := protected.Group("/tags")
		{
			tags.GET("", tagHandler.List)
			tags.GET("/:id", tagHandler.GetByID)
			tags.POST("", RequireRole("admin", "manager", "sales"), tagHandler.Create)
			tags.PUT("/:id", RequireRole("admin", "manager", "sales"), tagHandler.Update)
			tags.DELETE("/:id", RequireRole("admin", "manager"), tagHandler.Delete)
		}
	}
}
