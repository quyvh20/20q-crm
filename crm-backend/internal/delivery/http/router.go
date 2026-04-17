package http

import (
	"fmt"
	"crm-backend/internal/domain"
	"crm-backend/internal/repository"
	"crm-backend/pkg/config"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func RegisterRoutes(router *gin.Engine, authHandler *AuthHandler, contactHandler *ContactHandler, companyHandler *CompanyHandler, tagHandler *TagHandler, dealHandler *DealHandler, pipelineHandler *PipelineHandler, activityHandler *ActivityHandler, taskHandler *TaskHandler, userHandler *UserHandler, aiHandler *AIHandler, settingsHandler *SettingsHandler, customObjectHandler *CustomObjectHandler, knowledgeHandler *KnowledgeHandler, commandHandler *CommandHandler, eventsHandler *EventsHandler, workspaceHandler *WorkspaceHandler, cfg *config.Config, db *gorm.DB, redisClient *redis.Client, authRepo domain.AuthRepository) {
	// Temporary endpoint to debug DB issues on deploy
	router.GET("/api/test/db-fix", func(c *gin.Context) {
		err1 := db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS full_name VARCHAR(255) DEFAULT ''`).Error
		err2 := db.Exec(`UPDATE users SET full_name = TRIM(first_name || ' ' || last_name) WHERE full_name = '' OR full_name IS NULL`).Error
		
		errMigrate := db.Exec(`
			CREATE TABLE IF NOT EXISTS roles (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
				name VARCHAR(255) NOT NULL,
				is_system BOOLEAN NOT NULL DEFAULT false,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
			CREATE TABLE IF NOT EXISTS role_permissions (
				role_id UUID REFERENCES roles(id) ON DELETE CASCADE,
				permission_code VARCHAR(255) NOT NULL,
				PRIMARY KEY (role_id, permission_code)
			);
			ALTER TABLE org_users ADD COLUMN IF NOT EXISTS role_id UUID REFERENCES roles(id) ON DELETE RESTRICT;
			ALTER TABLE org_users ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

			CREATE TABLE IF NOT EXISTS org_invitations (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				email VARCHAR(255) NOT NULL,
				org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
				role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
				token_hash VARCHAR(255) NOT NULL,
				expires_at TIMESTAMPTZ NOT NULL,
				status VARCHAR(50) NOT NULL DEFAULT 'pending',
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
			CREATE INDEX IF NOT EXISTS idx_org_invitations_token ON org_invitations(token_hash);
			
			CREATE TABLE IF NOT EXISTS record_shares (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				record_type VARCHAR(50) NOT NULL,
				record_id UUID NOT NULL,
				grantee_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				permission_level VARCHAR(50) NOT NULL DEFAULT 'read',
				created_by UUID REFERENCES users(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
		`).Error

		err3 := repository.SeedSystemRoles(db)

		c.JSON(200, gin.H{
			"status": "done",
			"err1": fmt.Sprintf("%v", err1),
			"err2": fmt.Sprintf("%v", err2),
			"errMigrate": fmt.Sprintf("%v", errMigrate),
			"seed_err": fmt.Sprintf("%v", err3),
		})
	})

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

			aiRoutes.GET("/jobs/:id", aiHandler.GetJobStatus)
			aiRoutes.POST("/email/compose", aiHandler.ComposeEmail)
			aiRoutes.POST("/meeting/summarize", aiHandler.SummarizeMeeting)
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

		kb := protected.Group("/knowledge-base")
		{
			kb.GET("", knowledgeHandler.ListSections)
			kb.GET("/ai-prompt", knowledgeHandler.GetAIPrompt)
			kb.GET("/:section", knowledgeHandler.GetSection)
			kb.PUT("/:section", RequireRole("admin", "manager"), knowledgeHandler.UpsertSection)
		}
	}
}
