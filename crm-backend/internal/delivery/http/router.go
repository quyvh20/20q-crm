package http

import (
	"crm-backend/internal/domain"
	"crm-backend/pkg/config"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func RegisterRoutes(router *gin.Engine, authHandler *AuthHandler, contactHandler *ContactHandler, companyHandler *CompanyHandler, tagHandler *TagHandler, dealHandler *DealHandler, pipelineHandler *PipelineHandler, activityHandler *ActivityHandler, taskHandler *TaskHandler, userHandler *UserHandler, aiHandler *AIHandler, settingsHandler *SettingsHandler, customObjectHandler *CustomObjectHandler, objectRegistryHandler *ObjectRegistryHandler, recordHandler *RecordHandler, permissionHandler *PermissionHandler, searchHandler *SearchHandler, knowledgeHandler *KnowledgeHandler, commandHandler *CommandHandler, eventsHandler *EventsHandler, workspaceHandler *WorkspaceHandler, sessionHandler *ChatSessionHandler, voiceHandler *VoiceHandler, layoutHandler *ObjectLayoutHandler, roleHandler *RoleHandler, auditHandler *AuditHandler, reportHandler *ReportHandler, reportShareHandler *ReportShareHandler, reportCommentHandler *ReportCommentHandler, dashboardHandler *DashboardHandler, groupHandler *UserGroupHandler, notificationHandler *NotificationHandler, cfg *config.Config, db *gorm.DB, redisClient *redis.Client, authRepo domain.AuthRepository, permissionUC domain.PermissionUseCase) {
	// Mark every request context as HTTP-originated so the permission engine can
	// flag a callerless HTTP call reaching Authorize (a route mounted outside
	// AuthMiddleware) instead of silently treating it as a trusted in-process
	// call (U0.10). Must be registered before any route below.
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(domain.MarkHTTPTransport(c.Request.Context()))
		c.Next()
	})

	api := router.Group("/api")

	// Per-IP rate limit on the credential endpoints (P2). Reused across the auth
	// group; fails open and no-ops without Redis.
	authRateLimit := RateLimitByIP(redisClient, authIPRateLimit, authIPRateWindow)

	// CSRF for the cookie-authenticated auth routes validates the request Origin
	// against the trusted set (same list CORS uses).
	csrf := CSRFProtect(AllowedOrigins(cfg.FrontendURL))

	// P3 authorization gates. permissionUC is the single OLS + capability engine
	// (it implements both RecordAuthorizer and CapabilityChecker), so every layer —
	// data CRUD, admin powers, ancillary writes — keys off the caller's role_id
	// through ONE cache. This replaces the hardcoded RequireRole name lists, so a
	// custom role an admin invents flows through the same gates as a system role.
	cap := func(code string) gin.HandlerFunc { return RequireCapability(permissionUC, code) }                          // admin/workspace power
	ols := func(a domain.RecordAction) gin.HandlerFunc { return RequireObjectAccess(permissionUC, a) }                 // data CRUD, :slug param
	olsOn := func(slug string, a domain.RecordAction) gin.HandlerFunc { return RequireObjectAccessOn(permissionUC, slug, a) } // data CRUD, fixed slug
	// Collaboration objects (tasks, activities, voice, tags, links) have no OLS
	// grid of their own, so they're gated by the admin-configurable records.write
	// capability rather than any hardcoded role rule.
	recordsWrite := cap(domain.CapRecordsWrite)

	// Field-Level Security on the legacy per-object routes (U0.1): the registry
	// path strips hidden fields inside RecordService; these handlers strip at the
	// delivery boundary using the same engine, so the admin Field Security grid
	// is honest on both surfaces.
	contactHandler.SetFieldMasker(permissionUC)
	companyHandler.SetFieldMasker(permissionUC)
	dealHandler.SetFieldMasker(permissionUC)

	auth := api.Group("/auth")
	{
		auth.POST("/register", authRateLimit, authHandler.Register)
		auth.POST("/login", authRateLimit, authHandler.Login)
		// refresh + logout are cookie-authenticated, so they carry the CSRF
		// double-submit check (enforced only when the refresh cookie is present).
		auth.POST("/refresh", authRateLimit, csrf, authHandler.Refresh)
		auth.POST("/logout", csrf, authHandler.Logout)

		// Account recovery + verification (P1). forgot/reset/verify are public
		// (token-authenticated); resend-verification is for the logged-in user.
		auth.POST("/forgot-password", authRateLimit, authHandler.ForgotPassword)
		auth.POST("/reset-password", authRateLimit, authHandler.ResetPassword)
		auth.POST("/verify-email", authRateLimit, authHandler.VerifyEmail)
		auth.POST("/resend-verification", AuthMiddleware(cfg.JWTSecret, authRepo, redisClient), authHandler.ResendVerification)

		auth.GET("/google", authHandler.GoogleLogin)
		auth.GET("/google/callback", authHandler.GoogleCallback)

		auth.GET("/me", AuthMiddleware(cfg.JWTSecret, authRepo, redisClient), authHandler.Me)
		// Session / device management (P4). Bearer-authenticated (AuthMiddleware),
		// so these aren't cookie-CSRF vectors. A user manages only their own
		// sessions; sign-out-everywhere re-mints this device's session.
		auth.GET("/sessions", AuthMiddleware(cfg.JWTSecret, authRepo, redisClient), authHandler.ListSessions)
		auth.DELETE("/sessions/:id", AuthMiddleware(cfg.JWTSecret, authRepo, redisClient), authHandler.RevokeSession)
		auth.DELETE("/sessions", AuthMiddleware(cfg.JWTSecret, authRepo, redisClient), authHandler.SignOutEverywhere)
		// The caller's effective capabilities for the active org — drives
		// permission-aware UI (P3). Server-side gates remain authoritative.
		auth.GET("/capabilities", AuthMiddleware(cfg.JWTSecret, authRepo, redisClient), permissionHandler.GetMyCapabilities)
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
			// Inviting members needs the members.invite capability and (soft-gate,
			// plan D2) a verified email — a brand-new unverified signup can't spread
			// access until they confirm their inbox.
			workspaces.POST("/invites", cap(domain.CapMembersInvite), RequireVerifiedEmail(authRepo), workspaceHandler.InviteMember)
			// Pending-invitation lifecycle (P2). List/revoke need only members.invite
			// (you can un-send what you can send); resend re-emails, so it carries the
			// same verified-email gate as the initial invite.
			workspaces.GET("/invitations", cap(domain.CapMembersInvite), workspaceHandler.ListInvitations)
			workspaces.POST("/invitations/:id/resend", cap(domain.CapMembersInvite), RequireVerifiedEmail(authRepo), workspaceHandler.ResendInvitation)
			workspaces.DELETE("/invitations/:id", cap(domain.CapMembersInvite), workspaceHandler.RevokeInvitation)
			workspaces.PATCH("/members/:user_id/role", cap(domain.CapMembersManage), workspaceHandler.UpdateMemberRole)
			workspaces.POST("/members/:user_id/suspend", cap(domain.CapMembersManage), workspaceHandler.SuspendMember)
			workspaces.POST("/members/:user_id/reinstate", cap(domain.CapMembersManage), workspaceHandler.ReinstateMember)
			workspaces.POST("/members/:user_id/transfer", cap(domain.CapMembersManage), workspaceHandler.TransferOwnership)
			// Admin "Send reset link" (P2): emails the member a self-serve reset — the
			// admin never sees or sets the password (accounts span workspaces).
			workspaces.POST("/members/:user_id/send-reset-link", cap(domain.CapMembersManage), workspaceHandler.SendMemberResetLink)
			workspaces.DELETE("/members/:user_id", cap(domain.CapMembersManage), workspaceHandler.RemoveMember)
		}

		// Custom role management (P3) — CRUD + clone-from + capability editing, all
		// gated on roles.manage. The OLS/FLS grids (below) already enumerate roles,
		// so a role created here appears in them automatically.
		roles := protected.Group("/roles")
		{
			// /options + /catalog are any-member (no roles.manage): they carry no
			// grant data, only the role identities + capability labels every picker
			// and the report Share dialog need (P6). The full grants payload (List /
			// capabilities) stays gated.
			roles.GET("/options", roleHandler.Options)
			roles.GET("/catalog", roleHandler.Catalog)
			roles.GET("", cap(domain.CapRolesManage), roleHandler.List)
			roles.POST("", cap(domain.CapRolesManage), roleHandler.Create)
			roles.POST("/:id/duplicate", cap(domain.CapRolesManage), roleHandler.Duplicate)
			roles.PATCH("/:id", cap(domain.CapRolesManage), roleHandler.Update)
			roles.DELETE("/:id", cap(domain.CapRolesManage), roleHandler.Delete)
			roles.GET("/:id/capabilities", cap(domain.CapRolesManage), roleHandler.GetCapabilities)
			roles.PUT("/:id/capabilities", cap(domain.CapRolesManage), roleHandler.SetCapabilities)
		}

		// Admin + auth audit log (P4) — the append-only who-did-what over
		// auth_events (logins, member/role/permission changes, security events).
		// Read + export are gated on audit.view (owner/admin/manager by default).
		audit := protected.Group("/audit", cap(domain.CapAuditView))
		{
			audit.GET("/events", auditHandler.ListEvents)
			audit.GET("/events/export.csv", auditHandler.ExportCSV)
		}

		// Reports (P9). No route-level role gate on CRUD/run — any member may
		// build reports, definitions are visibility-checked in the usecase, and
		// report DATA is re-authorized per viewer (OLS → FLS → data scope) on
		// every run. CSV export carries the data.export capability like the
		// audit export above.
		reports := protected.Group("/reports")
		{
			reports.GET("", reportHandler.List)
			reports.POST("", reportHandler.Create)
			reports.POST("/preview", reportHandler.Preview)
			// The builder's field catalog: registry fields + report-only virtual
			// fields (created_at, owner, deal lifecycle), FLS-filtered.
			reports.GET("/objects/:slug/fields", reportHandler.ListFields)
			reports.GET("/:id", reportHandler.Get)
			reports.PATCH("/:id", reportHandler.Update)
			reports.DELETE("/:id", reportHandler.Delete)
			reports.GET("/:id/run", reportHandler.Run)
			reports.GET("/:id/export.csv", cap(domain.CapDataExport), reportHandler.ExportCSV)
			// Granular sharing — list is visible to anyone who can see the report;
			// add/remove require 'manage' (enforced in the usecase).
			reports.GET("/:id/shares", reportShareHandler.List)
			reports.POST("/:id/shares", reportShareHandler.Add)
			reports.DELETE("/:id/shares/:shareId", reportShareHandler.Remove)
			// Comment thread — reading is visible to anyone who can see the report;
			// posting requires level >= comment, deleting requires author/manage
			// (all enforced in the usecase).
			reports.GET("/:id/comments", reportCommentHandler.List)
			reports.POST("/:id/comments", reportCommentHandler.Add)
			reports.DELETE("/:id/comments/:commentId", reportCommentHandler.Remove)
		}

		// Dashboard widgets (P9 Phase B): each caller manages only their own
		// pinned reports, so there is no role gate — the usecase scopes every
		// query to (org, caller) and re-checks report visibility on read.
		dashboard := protected.Group("/dashboard")
		{
			dashboard.GET("/widgets", dashboardHandler.ListWidgets)
			dashboard.POST("/widgets", dashboardHandler.AddWidget)
			dashboard.PUT("/widgets/reorder", dashboardHandler.Reorder)
			dashboard.PATCH("/widgets/:id", dashboardHandler.UpdateWidget)
			dashboard.DELETE("/widgets/:id", dashboardHandler.RemoveWidget)
		}

		// User groups: named member groups (a report-sharing target). Listing is
		// open to any member (the share picker needs it); mutations need
		// groups.manage.
		groups := protected.Group("/groups")
		{
			groups.GET("", groupHandler.List)
			groups.POST("", cap(domain.CapGroupsManage), groupHandler.Create)
			groups.PATCH("/:id", cap(domain.CapGroupsManage), groupHandler.Update)
			groups.DELETE("/:id", cap(domain.CapGroupsManage), groupHandler.Delete)
			groups.POST("/:id/members", cap(domain.CapGroupsManage), groupHandler.AddMember)
			groups.DELETE("/:id/members/:userId", cap(domain.CapGroupsManage), groupHandler.RemoveMember)
		}

		// Data CRUD is now Object-Level Security-driven (default seed reproduces the
		// old role gates exactly: read all, create/edit sales+, delete manager+), so
		// custom roles' OLS grid governs these routes too.
		contacts := protected.Group("/contacts")
		{
			// Read routes carry the same OLS gate as the writes (U0.1) — without
			// it, revoking 'read' in the permissions grid only bit on /registry.
			contacts.GET("", olsOn("contact", domain.ActionRead), contactHandler.List)
			contacts.GET("/:id", olsOn("contact", domain.ActionRead), contactHandler.GetByID)
			contacts.POST("", olsOn("contact", domain.ActionCreate), contactHandler.Create)
			contacts.PUT("/:id", olsOn("contact", domain.ActionEdit), contactHandler.Update)
			contacts.DELETE("/:id", olsOn("contact", domain.ActionDelete), contactHandler.Delete)
			contacts.POST("/import", olsOn("contact", domain.ActionCreate), contactHandler.Import)
			contacts.POST("/bulk-action", olsOn("contact", domain.ActionEdit), contactHandler.BulkAction)
		}

		companies := protected.Group("/companies")
		{
			companies.GET("", olsOn("company", domain.ActionRead), companyHandler.List)
			companies.GET("/:id", olsOn("company", domain.ActionRead), companyHandler.GetByID)
			companies.POST("", olsOn("company", domain.ActionCreate), companyHandler.Create)
			companies.PUT("/:id", olsOn("company", domain.ActionEdit), companyHandler.Update)
			companies.DELETE("/:id", olsOn("company", domain.ActionDelete), companyHandler.Delete)
		}

		tags := protected.Group("/tags")
		{
			tags.GET("", tagHandler.List)
			tags.GET("/:id", tagHandler.GetByID)
			tags.POST("", recordsWrite, tagHandler.Create)
			tags.PUT("/:id", recordsWrite, tagHandler.Update)
			tags.DELETE("/:id", recordsWrite, tagHandler.Delete)
		}

		deals := protected.Group("/deals")
		{
			deals.GET("", olsOn("deal", domain.ActionRead), dealHandler.List)
			deals.GET("/:id", olsOn("deal", domain.ActionRead), dealHandler.GetByID)
			deals.POST("", olsOn("deal", domain.ActionCreate), dealHandler.Create)
			deals.PUT("/:id", olsOn("deal", domain.ActionEdit), dealHandler.Update)
			deals.DELETE("/:id", olsOn("deal", domain.ActionDelete), dealHandler.Delete)
			deals.PATCH("/:id/stage", olsOn("deal", domain.ActionEdit), dealHandler.ChangeStage)
		}

		pipeline := protected.Group("/pipeline")
		{
			pipeline.GET("/stages", pipelineHandler.ListStages)
			// Pipeline stages have their own capability (default: owner/admin/manager).
			pipeline.POST("/stages", cap(domain.CapPipelineManage), pipelineHandler.CreateStage)
			pipeline.PUT("/stages/:id", cap(domain.CapPipelineManage), pipelineHandler.UpdateStage)
			pipeline.DELETE("/stages/:id", cap(domain.CapPipelineManage), pipelineHandler.DeleteStage)
			pipeline.POST("/stages/seed-defaults", cap(domain.CapPipelineManage), pipelineHandler.SeedDefaultStages)
			// Forecast is org-wide analytics — same analytics.view gate the AI
		// forecast tool enforces (U0.6), so REST and AI agree on who sees it.
		pipeline.GET("/forecast", cap(domain.CapAnalyticsView), dealHandler.Forecast)
		}

		activities := protected.Group("/activities")
		{
			activities.GET("", activityHandler.List)
			activities.POST("", recordsWrite, activityHandler.Create)
		}

		tasks := protected.Group("/tasks")
		{
			tasks.GET("", taskHandler.List)
			tasks.POST("", recordsWrite, taskHandler.Create)
			tasks.PUT("/:id", recordsWrite, taskHandler.Update)
			tasks.DELETE("/:id", recordsWrite, taskHandler.Delete)
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

			// Chat session management. Viewing/removing other users' AI sessions is an
			// admin oversight power → members.manage (owner/admin).
			aiRoutes.POST("/sessions/:id/end", sessionHandler.EndSession)
			aiRoutes.GET("/sessions", cap(domain.CapMembersManage), sessionHandler.ListSessions)
			aiRoutes.GET("/sessions/:id/messages", cap(domain.CapMembersManage), sessionHandler.GetSessionMessages)
			aiRoutes.DELETE("/sessions/:id", cap(domain.CapMembersManage), sessionHandler.DeleteSession)
		}

		deals.GET("/:id/score", olsOn("deal", domain.ActionRead), aiHandler.ScoreDeal)
		protected.GET("/events", eventsHandler.Stream)

		// In-app notifications (A6): the caller's own inbox behind the header bell.
		// No capability gate — every query is scoped to (org, caller), so a member
		// sees and marks only their own rows. Static /unread-count + /read-all
		// coexist with /:id/read the same way /schema coexists with /:id elsewhere.
		notifications := protected.Group("/notifications")
		{
			notifications.GET("", notificationHandler.List)
			notifications.GET("/unread-count", notificationHandler.UnreadCount)
			notifications.POST("/read-all", notificationHandler.MarkAllRead)
			notifications.POST("/:id/read", notificationHandler.MarkRead)
		}

		settings := protected.Group("/settings")
		{
			settings.GET("/fields", settingsHandler.ListFieldDefs)
			settings.POST("/fields", cap(domain.CapObjectsManage), settingsHandler.CreateFieldDef)
			settings.PUT("/fields/:key", cap(domain.CapObjectsManage), settingsHandler.UpdateFieldDef)
			settings.DELETE("/fields/:key", cap(domain.CapObjectsManage), settingsHandler.DeleteFieldDef)
		}

		objects := protected.Group("/objects")
		{
			objects.GET("", customObjectHandler.ListDefs)
			objects.GET("/:slug", customObjectHandler.GetDef)
			objects.POST("", cap(domain.CapObjectsManage), customObjectHandler.CreateDef)
			objects.PUT("/:slug", cap(domain.CapObjectsManage), customObjectHandler.UpdateDef)
			objects.DELETE("/:slug", cap(domain.CapObjectsManage), customObjectHandler.DeleteDef)

			objects.GET("/:slug/records", customObjectHandler.ListRecords)
			objects.GET("/:slug/records/:id", customObjectHandler.GetRecord)
			objects.POST("/:slug/records", ols(domain.ActionCreate), customObjectHandler.CreateRecord)
			objects.PUT("/:slug/records/:id", ols(domain.ActionEdit), customObjectHandler.UpdateRecord)
			objects.DELETE("/:slug/records/:id", ols(domain.ActionDelete), customObjectHandler.DeleteRecord)
		}

		// Object Registry (P2 read schema + P3 uniform records). Strictly additive
		// to the per-object routes above: one uniform view and one CRUD surface over
		// system + custom objects. Promoted to /api/objects in P7 once the old paths
		// retire.
		registerObjectRegistryRoutes(protected, objectRegistryHandler, recordHandler, permissionHandler, searchHandler, layoutHandler, cap, ols, recordsWrite)

		kb := protected.Group("/knowledge-base")
		{
			kb.GET("", knowledgeHandler.ListSections)
			kb.GET("/ai-prompt", knowledgeHandler.GetAIPrompt)
			kb.GET("/:section", knowledgeHandler.GetSection)
			// Knowledge base has its own capability (default: owner/admin/manager).
			kb.PUT("/:section", cap(domain.CapKnowledgeManage), knowledgeHandler.UpsertSection)
		}

		voice := protected.Group("/voice")
		{
			voice.POST("/upload", recordsWrite, voiceHandler.Upload)
			voice.GET("", voiceHandler.List)
			voice.GET("/preview/:filename", voiceHandler.PreviewVoiceNote)
			voice.GET("/:id", voiceHandler.GetByID)
			voice.POST("/:id/apply-updates", recordsWrite, voiceHandler.ApplyUpdates)
			voice.POST("/:id/analyze", recordsWrite, voiceHandler.Analyze)
			voice.DELETE("/:id", recordsWrite, voiceHandler.Delete)
		}
	}
}

// registerObjectRegistryRoutes mounts the P2 read schema, P3 uniform record CRUD,
// record shares, and P8 layout admin routes under /registry/objects. Data writes
// are OLS-driven (ols); admin config is capability-gated (cap); ancillary edge
// operations use the records.write capability (recordsWrite). Extracted so the
// route shape is unit-testable on its own.
func registerObjectRegistryRoutes(parent *gin.RouterGroup, objectRegistryHandler *ObjectRegistryHandler, recordHandler *RecordHandler, permissionHandler *PermissionHandler, searchHandler *SearchHandler, layoutHandler *ObjectLayoutHandler,
	cap func(string) gin.HandlerFunc, ols func(domain.RecordAction) gin.HandlerFunc, recordsWrite gin.HandlerFunc) {
	registry := parent.Group("/registry/objects")
	registry.GET("", objectRegistryHandler.ListObjects)
	registry.GET("/:slug/schema", objectRegistryHandler.GetSchema)
	// Admin: set an object's record-number prefix (e.g. INV → INV-0001).
	registry.PUT("/:slug/number-prefix", cap(domain.CapObjectsManage), objectRegistryHandler.SetNumberPrefix)

	// Global, cross-object search (P6). No coarse role gate — OLS filters results
	// per object inside SearchUseCase, so a viewer only sees what they can read.
	parent.GET("/registry/search", searchHandler.Search)

	registry.GET("/:slug/records", recordHandler.List)
	registry.GET("/:slug/records/:id", recordHandler.Get)
	// Composite record page: schema + record + related lists + tags + resolved
	// relation/mirror labels in ONE response. Read-level, same as its parts.
	registry.GET("/:slug/records/:id/page", recordHandler.GetPage)
	registry.GET("/:slug/records/:id/related-lists", recordHandler.RelatedLists)
	registry.POST("/:slug/records", ols(domain.ActionCreate), recordHandler.Create)
	registry.PATCH("/:slug/records/:id", ols(domain.ActionEdit), recordHandler.Update)
	registry.DELETE("/:slug/records/:id", ols(domain.ActionDelete), recordHandler.Delete)

	// Per-record audit trail. Viewing change history needs the audit.view
	// capability (owner/admin/manager); OLS read on the object is re-checked in
	// the usecase as defense in depth.
	registry.GET("/:slug/records/:id/audit", cap(domain.CapAuditView), permissionHandler.ListAudit)

	// Record shares (P3, I2) — the escape hatch for 'own'-scoped roles. Granting a
	// share is an edit-level operation on the record's object; the usecase also
	// requires the caller own the record or hold members.manage.
	registry.POST("/:slug/records/:id/share", ols(domain.ActionEdit), recordHandler.Share)
	registry.DELETE("/:slug/records/:id/share/:shareId", ols(domain.ActionEdit), recordHandler.Unshare)
	registry.GET("/:slug/records/:id/shares", ols(domain.ActionEdit), recordHandler.ListShares)

	// Universal relationships + tags (P4). Relating/tagging a record is edit-level.
	registry.GET("/:slug/records/:id/links", recordHandler.ListLinks)
	registry.POST("/:slug/records/:id/links", ols(domain.ActionEdit), recordHandler.AddLink)
	registry.GET("/:slug/records/:id/tags", recordHandler.ListTags)
	registry.POST("/:slug/records/:id/tags", ols(domain.ActionEdit), recordHandler.AddTag)
	registry.DELETE("/:slug/records/:id/tags/:tagId", ols(domain.ActionEdit), recordHandler.RemoveTag)

	// Link removal is addressed by edge id (no :slug), so it uses the records.write
	// capability rather than a per-object OLS check.
	parent.DELETE("/registry/links/:id", recordsWrite, recordHandler.RemoveLink)

	// Field-Level Security grid — managing role×field visibility is roles.manage.
	registry.GET("/:slug/field-permissions", cap(domain.CapRolesManage), permissionHandler.GetFieldGrid)
	registry.PUT("/:slug/field-permissions", cap(domain.CapRolesManage), permissionHandler.SetFieldPermission)

	// Object-Level Security grid — the role × object access matrix is roles.manage.
	perms := parent.Group("/registry/permissions", cap(domain.CapRolesManage))
	perms.GET("", permissionHandler.GetGrid)
	perms.PUT("", permissionHandler.SetPermission)

	// Per-role detail layouts (P8) — object-model config → objects.manage.
	registry.GET("/:slug/layouts", cap(domain.CapObjectsManage), layoutHandler.ListLayouts)
	registry.POST("/:slug/layouts", cap(domain.CapObjectsManage), layoutHandler.CreateLayout)
	registry.PATCH("/:slug/layouts/:id", cap(domain.CapObjectsManage), layoutHandler.UpdateLayout)
	registry.DELETE("/:slug/layouts/:id", cap(domain.CapObjectsManage), layoutHandler.DeleteLayout)
	registry.PUT("/:slug/layouts/:id/roles", cap(domain.CapObjectsManage), layoutHandler.SetLayoutRoles)
}
