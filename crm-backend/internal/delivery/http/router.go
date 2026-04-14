package http

import (
	"crm-backend/pkg/config"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes wires all API routes to the Gin engine.
func RegisterRoutes(router *gin.Engine, authHandler *AuthHandler, contactHandler *ContactHandler, companyHandler *CompanyHandler, tagHandler *TagHandler, dealHandler *DealHandler, pipelineHandler *PipelineHandler, activityHandler *ActivityHandler, taskHandler *TaskHandler, userHandler *UserHandler, aiHandler *AIHandler, settingsHandler *SettingsHandler, customObjectHandler *CustomObjectHandler, knowledgeHandler *KnowledgeHandler, commandHandler *CommandHandler, cfg *config.Config) {
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

		// Deals
		deals := protected.Group("/deals")
		{
			deals.GET("", dealHandler.List)
			deals.GET("/:id", dealHandler.GetByID)
			deals.POST("", RequireRole("admin", "manager", "sales"), dealHandler.Create)
			deals.PUT("/:id", RequireRole("admin", "manager", "sales"), dealHandler.Update)
			deals.DELETE("/:id", RequireRole("admin", "manager"), dealHandler.Delete)
			deals.PATCH("/:id/stage", RequireRole("admin", "manager", "sales"), dealHandler.ChangeStage)
		}

		// Pipeline
		pipeline := protected.Group("/pipeline")
		{
			pipeline.GET("/stages", pipelineHandler.ListStages)
			pipeline.POST("/stages", RequireRole("admin", "manager"), pipelineHandler.CreateStage)
			pipeline.PUT("/stages/:id", RequireRole("admin", "manager"), pipelineHandler.UpdateStage)
			pipeline.GET("/forecast", dealHandler.Forecast)
		}

		// Activities
		activities := protected.Group("/activities")
		{
			activities.GET("", activityHandler.List)
			activities.POST("", RequireRole("admin", "manager", "sales"), activityHandler.Create)
		}

		// Tasks
		tasks := protected.Group("/tasks")
		{
			tasks.GET("", taskHandler.List)
			tasks.POST("", RequireRole("admin", "manager", "sales"), taskHandler.Create)
			tasks.PUT("/:id", RequireRole("admin", "manager", "sales"), taskHandler.Update)
			tasks.DELETE("/:id", RequireRole("admin", "manager"), taskHandler.Delete)
		}

		// Users (for assignee dropdowns)
		protected.GET("/users", userHandler.List)

		// AI
		aiRoutes := protected.Group("/ai")
		{
			aiRoutes.GET("/usage", aiHandler.GetUsage)
			aiRoutes.POST("/chat", aiHandler.Chat)
			aiRoutes.POST("/embed", aiHandler.Embed)
			aiRoutes.POST("/command", commandHandler.Command)
		}

		// Settings (Custom Fields)
		settings := protected.Group("/settings")
		{
			settings.GET("/fields", settingsHandler.ListFieldDefs)
			settings.POST("/fields", RequireRole("admin"), settingsHandler.CreateFieldDef)
			settings.PUT("/fields/:key", RequireRole("admin"), settingsHandler.UpdateFieldDef)
			settings.DELETE("/fields/:key", RequireRole("admin"), settingsHandler.DeleteFieldDef)
		}

		// Custom Objects
		objects := protected.Group("/objects")
		{
			objects.GET("", customObjectHandler.ListDefs)
			objects.GET("/:slug", customObjectHandler.GetDef)
			objects.POST("", RequireRole("admin"), customObjectHandler.CreateDef)
			objects.PUT("/:slug", RequireRole("admin"), customObjectHandler.UpdateDef)
			objects.DELETE("/:slug", RequireRole("admin"), customObjectHandler.DeleteDef)

			objects.GET("/:slug/records", customObjectHandler.ListRecords)
			objects.GET("/:slug/records/:id", customObjectHandler.GetRecord)
			objects.POST("/:slug/records", RequireRole("admin", "manager", "sales"), customObjectHandler.CreateRecord)
			objects.PUT("/:slug/records/:id", RequireRole("admin", "manager", "sales"), customObjectHandler.UpdateRecord)
			objects.DELETE("/:slug/records/:id", RequireRole("admin", "manager"), customObjectHandler.DeleteRecord)
		}

		// Knowledge Base
		kb := protected.Group("/knowledge-base")
		{
			kb.GET("", knowledgeHandler.ListSections)
			kb.GET("/ai-prompt", knowledgeHandler.GetAIPrompt)
			kb.GET("/:section", knowledgeHandler.GetSection)
			kb.PUT("/:section", RequireRole("admin", "manager"), knowledgeHandler.UpsertSection)
		}
	}
}
