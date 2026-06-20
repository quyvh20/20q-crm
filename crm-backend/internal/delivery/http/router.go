package http

import (
	"crm-backend/internal/domain"
	"crm-backend/pkg/config"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func RegisterRoutes(router *gin.Engine, authHandler *AuthHandler, contactHandler *ContactHandler, companyHandler *CompanyHandler, tagHandler *TagHandler, dealHandler *DealHandler, pipelineHandler *PipelineHandler, activityHandler *ActivityHandler, taskHandler *TaskHandler, userHandler *UserHandler, aiHandler *AIHandler, settingsHandler *SettingsHandler, customObjectHandler *CustomObjectHandler, objectRegistryHandler *ObjectRegistryHandler, recordHandler *RecordHandler, permissionHandler *PermissionHandler, knowledgeHandler *KnowledgeHandler, commandHandler *CommandHandler, eventsHandler *EventsHandler, workspaceHandler *WorkspaceHandler, sessionHandler *ChatSessionHandler, voiceHandler *VoiceHandler, cfg *config.Config, db *gorm.DB, redisClient *redis.Client, authRepo domain.AuthRepository) {
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
			workspaces.POST("/invites", RequireRole(domain.RoleAdmin, domain.RoleManager), workspaceHandler.InviteMember)
			workspaces.PATCH("/members/:user_id/role", RequireRole(domain.RoleAdmin), workspaceHandler.UpdateMemberRole)
			workspaces.POST("/members/:user_id/suspend", RequireRole(domain.RoleAdmin), workspaceHandler.SuspendMember)
			workspaces.POST("/members/:user_id/reinstate", RequireRole(domain.RoleAdmin), workspaceHandler.ReinstateMember)
			workspaces.POST("/members/:user_id/transfer", RequireRole(domain.RoleAdmin), workspaceHandler.TransferOwnership)
			workspaces.DELETE("/members/:user_id", RequireRole(domain.RoleAdmin), workspaceHandler.RemoveMember)
		}

		contacts := protected.Group("/contacts")
		{
			contacts.GET("", contactHandler.List)
			contacts.GET("/:id", contactHandler.GetByID)
			contacts.POST("", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), contactHandler.Create)
			contacts.PUT("/:id", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), contactHandler.Update)
			contacts.DELETE("/:id", RequireRole(domain.RoleAdmin, domain.RoleManager), contactHandler.Delete)
			contacts.POST("/import", RequireRole(domain.RoleAdmin, domain.RoleManager), contactHandler.Import)
			contacts.POST("/bulk-action", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), contactHandler.BulkAction)
		}

		companies := protected.Group("/companies")
		{
			companies.GET("", companyHandler.List)
			companies.GET("/:id", companyHandler.GetByID)
			companies.POST("", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), companyHandler.Create)
			companies.PUT("/:id", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), companyHandler.Update)
			companies.DELETE("/:id", RequireRole(domain.RoleAdmin, domain.RoleManager), companyHandler.Delete)
		}

		tags := protected.Group("/tags")
		{
			tags.GET("", tagHandler.List)
			tags.GET("/:id", tagHandler.GetByID)
			tags.POST("", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), tagHandler.Create)
			tags.PUT("/:id", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), tagHandler.Update)
			tags.DELETE("/:id", RequireRole(domain.RoleAdmin, domain.RoleManager), tagHandler.Delete)
		}

		deals := protected.Group("/deals")
		{
			deals.GET("", dealHandler.List)
			deals.GET("/:id", dealHandler.GetByID)
			deals.POST("", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), dealHandler.Create)
			deals.PUT("/:id", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), dealHandler.Update)
			deals.DELETE("/:id", RequireRole(domain.RoleAdmin, domain.RoleManager), dealHandler.Delete)
			deals.PATCH("/:id/stage", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), dealHandler.ChangeStage)
		}

		pipeline := protected.Group("/pipeline")
		{
			pipeline.GET("/stages", pipelineHandler.ListStages)
			pipeline.POST("/stages", RequireRole(domain.RoleAdmin, domain.RoleManager), pipelineHandler.CreateStage)
			pipeline.PUT("/stages/:id", RequireRole(domain.RoleAdmin, domain.RoleManager), pipelineHandler.UpdateStage)
			pipeline.DELETE("/stages/:id", RequireRole(domain.RoleAdmin, domain.RoleManager), pipelineHandler.DeleteStage)
			pipeline.POST("/stages/seed-defaults", RequireRole(domain.RoleAdmin), pipelineHandler.SeedDefaultStages)
			pipeline.GET("/forecast", dealHandler.Forecast)
		}

		activities := protected.Group("/activities")
		{
			activities.GET("", activityHandler.List)
			activities.POST("", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), activityHandler.Create)
		}

		tasks := protected.Group("/tasks")
		{
			tasks.GET("", taskHandler.List)
			tasks.POST("", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), taskHandler.Create)
			tasks.PUT("/:id", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), taskHandler.Update)
			tasks.DELETE("/:id", RequireRole(domain.RoleAdmin, domain.RoleManager), taskHandler.Delete)
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
			aiRoutes.GET("/sessions", RequireRole(domain.RoleAdmin, domain.RoleOwner), sessionHandler.ListSessions)
			aiRoutes.GET("/sessions/:id/messages", RequireRole(domain.RoleAdmin, domain.RoleOwner), sessionHandler.GetSessionMessages)
			aiRoutes.DELETE("/sessions/:id", RequireRole(domain.RoleAdmin, domain.RoleOwner), sessionHandler.DeleteSession)
		}

		deals.GET("/:id/score", aiHandler.ScoreDeal)
		protected.GET("/events", eventsHandler.Stream)

		settings := protected.Group("/settings")
		{
			settings.GET("/fields", settingsHandler.ListFieldDefs)
			settings.POST("/fields", RequireRole(domain.RoleAdmin), settingsHandler.CreateFieldDef)
			settings.PUT("/fields/:key", RequireRole(domain.RoleAdmin), settingsHandler.UpdateFieldDef)
			settings.DELETE("/fields/:key", RequireRole(domain.RoleAdmin), settingsHandler.DeleteFieldDef)
		}

		objects := protected.Group("/objects")
		{
			objects.GET("", customObjectHandler.ListDefs)
			objects.GET("/:slug", customObjectHandler.GetDef)
			objects.POST("", RequireRole(domain.RoleAdmin), customObjectHandler.CreateDef)
			objects.PUT("/:slug", RequireRole(domain.RoleAdmin), customObjectHandler.UpdateDef)
			objects.DELETE("/:slug", RequireRole(domain.RoleAdmin), customObjectHandler.DeleteDef)

			objects.GET("/:slug/records", customObjectHandler.ListRecords)
			objects.GET("/:slug/records/:id", customObjectHandler.GetRecord)
			objects.POST("/:slug/records", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), customObjectHandler.CreateRecord)
			objects.PUT("/:slug/records/:id", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), customObjectHandler.UpdateRecord)
			objects.DELETE("/:slug/records/:id", RequireRole(domain.RoleAdmin, domain.RoleManager), customObjectHandler.DeleteRecord)
		}

		// Object Registry (P2 read schema + P3 uniform records). Strictly additive
		// to the per-object routes above: one uniform view and one CRUD surface over
		// system + custom objects. Promoted to /api/objects in P7 once the old paths
		// retire.
		registerObjectRegistryRoutes(protected, objectRegistryHandler, recordHandler, permissionHandler)

		kb := protected.Group("/knowledge-base")
		{
			kb.GET("", knowledgeHandler.ListSections)
			kb.GET("/ai-prompt", knowledgeHandler.GetAIPrompt)
			kb.GET("/:section", knowledgeHandler.GetSection)
			kb.PUT("/:section", RequireRole(domain.RoleAdmin, domain.RoleManager), knowledgeHandler.UpsertSection)
		}

		voice := protected.Group("/voice")
		{
			voice.POST("/upload", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), voiceHandler.Upload)
			voice.GET("", voiceHandler.List)
			voice.GET("/preview/:filename", voiceHandler.PreviewVoiceNote)
			voice.GET("/:id", voiceHandler.GetByID)
			voice.POST("/:id/apply-updates", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), voiceHandler.ApplyUpdates)
			voice.POST("/:id/analyze", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), voiceHandler.Analyze)
			voice.DELETE("/:id", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), voiceHandler.Delete)
		}
	}
}

// registerObjectRegistryRoutes mounts the P2 read schema and P3 uniform record
// CRUD under /registry/objects. Record writes mirror the per-object role gates
// (create/edit for sales+, delete for manager+); RecordService re-checks org
// scope centrally. Extracted so the route shape is unit-testable on its own.
func registerObjectRegistryRoutes(parent *gin.RouterGroup, objectRegistryHandler *ObjectRegistryHandler, recordHandler *RecordHandler, permissionHandler *PermissionHandler) {
	registry := parent.Group("/registry/objects")
	registry.GET("", objectRegistryHandler.ListObjects)
	registry.GET("/:slug/schema", objectRegistryHandler.GetSchema)

	registry.GET("/:slug/records", recordHandler.List)
	registry.GET("/:slug/records/:id", recordHandler.Get)
	registry.POST("/:slug/records", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), recordHandler.Create)
	registry.PATCH("/:slug/records/:id", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), recordHandler.Update)
	registry.DELETE("/:slug/records/:id", RequireRole(domain.RoleAdmin, domain.RoleManager), recordHandler.Delete)

	// Per-record audit trail (P5a). Viewing the change history is manager+; the
	// coarse RequireRole gate here is the floor, while OLS read access to the
	// object is what RecordService would enforce on the record itself.
	registry.GET("/:slug/records/:id/audit", RequireRole(domain.RoleAdmin, domain.RoleManager), permissionHandler.ListAudit)

	// Universal relationships + tags (P4). Relating/tagging a record is an edit-
	// level operation (sales+), distinct from deleting the record (manager+).
	// RecordService re-checks org scope centrally and enforces the contact_tags
	// vs object_links split so the API is identical for every object.
	registry.GET("/:slug/records/:id/links", recordHandler.ListLinks)
	registry.POST("/:slug/records/:id/links", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), recordHandler.AddLink)
	registry.GET("/:slug/records/:id/tags", recordHandler.ListTags)
	registry.POST("/:slug/records/:id/tags", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), recordHandler.AddTag)
	registry.DELETE("/:slug/records/:id/tags/:tagId", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), recordHandler.RemoveTag)

	// Link removal is addressed by edge id, so it lives alongside the object
	// registry rather than under a specific record (the plan's /api/links/:id,
	// kept under /registry until the P7 promotion).
	parent.DELETE("/registry/links/:id", RequireRole(domain.RoleAdmin, domain.RoleManager, domain.RoleSales), recordHandler.RemoveLink)

	// Object-Level Security grid (P5a) — admin-only configuration of the role ×
	// object access matrix that RecordService enforces.
	perms := parent.Group("/registry/permissions", RequireRole(domain.RoleAdmin))
	perms.GET("", permissionHandler.GetGrid)
	perms.PUT("", permissionHandler.SetPermission)
}
