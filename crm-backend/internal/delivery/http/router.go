package http

import (
	"crm-backend/internal/domain"
	"crm-backend/pkg/config"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func RegisterRoutes(router *gin.Engine, authHandler *AuthHandler, contactHandler *ContactHandler, companyHandler *CompanyHandler, tagHandler *TagHandler, dealHandler *DealHandler, pipelineHandler *PipelineHandler, activityHandler *ActivityHandler, taskHandler *TaskHandler, userHandler *UserHandler, aiHandler *AIHandler, settingsHandler *SettingsHandler, customObjectHandler *CustomObjectHandler, objectRegistryHandler *ObjectRegistryHandler, recordHandler *RecordHandler, knowledgeHandler *KnowledgeHandler, commandHandler *CommandHandler, eventsHandler *EventsHandler, workspaceHandler *WorkspaceHandler, sessionHandler *ChatSessionHandler, voiceHandler *VoiceHandler, cfg *config.Config, db *gorm.DB, redisClient *redis.Client, authRepo domain.AuthRepository) {
	api := router.Group("/api")

	auth := api.Group("/auth")
	{
		auth.POST("/register", authHandler.Register)
		auth.POST("/login", authHandler.Login)
		auth.POST("/refresh", authHandler.Refresh)
		auth.POST("/logout", authHandler.Logout)

		auth.GET("/google", authHandler.GoogleLogin)
		auth.GET("/google/callback", authHandler.GoogleCallback)

		auth.GET("/me", AuthMiddleware(cfg.JWTSecret, authRepo, redisClient), authHandler.Me)
		auth.POST("/switch-workspace", AuthMiddleware(cfg.JWTSecret, authRepo, redisClient), authHandler.SwitchWorkspace)
		auth.POST("/accept-invite", workspaceHandler.AcceptInvite)
	}

	protected := api.Group("/")
	protected.Use(AuthMiddleware(cfg.JWTSecret, authRepo, redisClient))
	{
		workspaces := protected.Group("/workspaces")
		{
			workspaces.GET("", authHandler.ListWorkspaces)
			workspaces.GET("/members", workspaceHandler.ListMembers)
			workspaces.POST("/invites", RequireRole("admin", "manager"), workspaceHandler.InviteMember)
			workspaces.PATCH("/members/:user_id/role", RequireRole("admin"), workspaceHandler.UpdateMemberRole)
			workspaces.POST("/members/:user_id/suspend", RequireRole("admin"), workspaceHandler.SuspendMember)
			workspaces.POST("/members/:user_id/reinstate", RequireRole("admin"), workspaceHandler.ReinstateMember)
			workspaces.POST("/members/:user_id/transfer", RequireRole("admin"), workspaceHandler.TransferOwnership)
			workspaces.DELETE("/members/:user_id", RequireRole("admin"), workspaceHandler.RemoveMember)
		}

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

		companies := protected.Group("/companies")
		{
			companies.GET("", companyHandler.List)
			companies.GET("/:id", companyHandler.GetByID)
			companies.POST("", RequireRole("admin", "manager", "sales"), companyHandler.Create)
			companies.PUT("/:id", RequireRole("admin", "manager", "sales"), companyHandler.Update)
			companies.DELETE("/:id", RequireRole("admin", "manager"), companyHandler.Delete)
		}

		tags := protected.Group("/tags")
		{
			tags.GET("", tagHandler.List)
			tags.GET("/:id", tagHandler.GetByID)
			tags.POST("", RequireRole("admin", "manager", "sales"), tagHandler.Create)
			tags.PUT("/:id", RequireRole("admin", "manager", "sales"), tagHandler.Update)
			tags.DELETE("/:id", RequireRole("admin", "manager"), tagHandler.Delete)
		}

		deals := protected.Group("/deals")
		{
			deals.GET("", dealHandler.List)
			deals.GET("/:id", dealHandler.GetByID)
			deals.POST("", RequireRole("admin", "manager", "sales"), dealHandler.Create)
			deals.PUT("/:id", RequireRole("admin", "manager", "sales"), dealHandler.Update)
			deals.DELETE("/:id", RequireRole("admin", "manager"), dealHandler.Delete)
			deals.PATCH("/:id/stage", RequireRole("admin", "manager", "sales"), dealHandler.ChangeStage)
		}

		pipeline := protected.Group("/pipeline")
		{
			pipeline.GET("/stages", pipelineHandler.ListStages)
			pipeline.POST("/stages", RequireRole("admin", "manager"), pipelineHandler.CreateStage)
			pipeline.PUT("/stages/:id", RequireRole("admin", "manager"), pipelineHandler.UpdateStage)
			pipeline.DELETE("/stages/:id", RequireRole("admin", "manager"), pipelineHandler.DeleteStage)
			pipeline.POST("/stages/seed-defaults", RequireRole("admin"), pipelineHandler.SeedDefaultStages)
			pipeline.GET("/forecast", dealHandler.Forecast)
		}

		activities := protected.Group("/activities")
		{
			activities.GET("", activityHandler.List)
			activities.POST("", RequireRole("admin", "manager", "sales"), activityHandler.Create)
		}

		tasks := protected.Group("/tasks")
		{
			tasks.GET("", taskHandler.List)
			tasks.POST("", RequireRole("admin", "manager", "sales"), taskHandler.Create)
			tasks.PUT("/:id", RequireRole("admin", "manager", "sales"), taskHandler.Update)
			tasks.DELETE("/:id", RequireRole("admin", "manager"), taskHandler.Delete)
		}

		protected.GET("/users", userHandler.List)

		aiRoutes := protected.Group("/ai")
		{
			aiRoutes.GET("/usage", aiHandler.GetUsage)
			aiRoutes.GET("/usage/top", aiHandler.GetTopUsage)
			aiRoutes.GET("/usage/stats", aiHandler.GetUsageStats)
			aiRoutes.POST("/chat", aiHandler.Chat)
			aiRoutes.POST("/embed", aiHandler.Embed)
			aiRoutes.POST("/command", commandHandler.Command)
			aiRoutes.POST("/command-sync", commandHandler.CommandSync)

			aiRoutes.GET("/jobs/:id", aiHandler.GetJobStatus)
			aiRoutes.POST("/email/compose", aiHandler.ComposeEmail)
			aiRoutes.POST("/meeting/summarize", aiHandler.SummarizeMeeting)

			// Chat session management
			aiRoutes.POST("/sessions/:id/end", sessionHandler.EndSession)
			aiRoutes.GET("/sessions", RequireRole("admin", "owner"), sessionHandler.ListSessions)
			aiRoutes.GET("/sessions/:id/messages", RequireRole("admin", "owner"), sessionHandler.GetSessionMessages)
			aiRoutes.DELETE("/sessions/:id", RequireRole("admin", "owner"), sessionHandler.DeleteSession)
		}

		deals.GET("/:id/score", aiHandler.ScoreDeal)
		protected.GET("/events", eventsHandler.Stream)

		settings := protected.Group("/settings")
		{
			settings.GET("/fields", settingsHandler.ListFieldDefs)
			settings.POST("/fields", RequireRole("admin"), settingsHandler.CreateFieldDef)
			settings.PUT("/fields/:key", RequireRole("admin"), settingsHandler.UpdateFieldDef)
			settings.DELETE("/fields/:key", RequireRole("admin"), settingsHandler.DeleteFieldDef)
		}

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

		// Object Registry (P2 read schema + P3 uniform records). Strictly additive
		// to the per-object routes above: one uniform view and one CRUD surface over
		// system + custom objects. Promoted to /api/objects in P7 once the old paths
		// retire.
		registerObjectRegistryRoutes(protected, objectRegistryHandler, recordHandler)

		kb := protected.Group("/knowledge-base")
		{
			kb.GET("", knowledgeHandler.ListSections)
			kb.GET("/ai-prompt", knowledgeHandler.GetAIPrompt)
			kb.GET("/:section", knowledgeHandler.GetSection)
			kb.PUT("/:section", RequireRole("admin", "manager"), knowledgeHandler.UpsertSection)
		}

		voice := protected.Group("/voice")
		{
			voice.POST("/upload", RequireRole("admin", "manager", "sales"), voiceHandler.Upload)
			voice.GET("", voiceHandler.List)
			voice.GET("/preview/:filename", voiceHandler.PreviewVoiceNote)
			voice.GET("/:id", voiceHandler.GetByID)
			voice.POST("/:id/apply-updates", RequireRole("admin", "manager", "sales"), voiceHandler.ApplyUpdates)
			voice.POST("/:id/analyze", RequireRole("admin", "manager", "sales"), voiceHandler.Analyze)
			voice.DELETE("/:id", RequireRole("admin", "manager", "sales"), voiceHandler.Delete)
		}
	}
}

// registerObjectRegistryRoutes mounts the P2 read schema and P3 uniform record
// CRUD under /registry/objects. Record writes mirror the per-object role gates
// (create/edit for sales+, delete for manager+); RecordService re-checks org
// scope centrally. Extracted so the route shape is unit-testable on its own.
func registerObjectRegistryRoutes(parent *gin.RouterGroup, objectRegistryHandler *ObjectRegistryHandler, recordHandler *RecordHandler) {
	registry := parent.Group("/registry/objects")
	registry.GET("", objectRegistryHandler.ListObjects)
	registry.GET("/:slug/schema", objectRegistryHandler.GetSchema)

	registry.GET("/:slug/records", recordHandler.List)
	registry.GET("/:slug/records/:id", recordHandler.Get)
	registry.POST("/:slug/records", RequireRole("admin", "manager", "sales"), recordHandler.Create)
	registry.PATCH("/:slug/records/:id", RequireRole("admin", "manager", "sales"), recordHandler.Update)
	registry.DELETE("/:slug/records/:id", RequireRole("admin", "manager"), recordHandler.Delete)
}
