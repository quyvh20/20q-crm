package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	delivery "crm-backend/internal/delivery/http"
	"crm-backend/internal/ai"
	"crm-backend/internal/automation"
	"crm-backend/internal/domain"
	"crm-backend/internal/repository"
	"crm-backend/internal/usecase"
	"crm-backend/internal/worker"
	"crm-backend/pkg/cache"
	"crm-backend/pkg/config"
	"crm-backend/pkg/database"
	"crm-backend/pkg/logger"
	"crm-backend/pkg/mailer"

	"github.com/getsentry/sentry-go"
	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func main() {
	if err := logger.InitLogger(); err != nil {
		panic(err)
	}
	defer logger.Sync()
	log := logger.Log

	log.Info("Starting CRM backend")

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("Failed to load config", zap.Error(err))
	}

	if cfg.SentryDSN != "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:              cfg.SentryDSN,
			EnableTracing:    true,
			TracesSampleRate: 1.0,
			Environment:      os.Getenv("GIN_MODE"),
		})
		if err != nil {
			log.Error("Sentry initialization failed", zap.Error(err))
		} else {
			log.Info("Sentry initialized")
			defer sentry.Flush(2 * time.Second)
		}
	}

	db, err := database.NewConnection(cfg.DatabaseURL)
	if err != nil {
		log.Fatal("Failed to connect to database", zap.Error(err))
	}
	if db != nil {
		log.Info("Database connection established")
		db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS full_name VARCHAR(255) DEFAULT ''`)
		db.Exec(`UPDATE users SET full_name = TRIM(first_name || ' ' || last_name) WHERE full_name = '' OR full_name IS NULL`)
		db.Exec(`ALTER TABLE organizations ADD COLUMN IF NOT EXISTS type VARCHAR(50) DEFAULT 'company'`)
		db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_contacts_owner ON contacts(owner_user_id)`)

		db.AutoMigrate(&domain.Role{}, &domain.RolePermission{}, &domain.OrgUser{}, &domain.KnowledgeBaseEntry{}, &domain.AITokenUsage{}, &domain.RecordShare{}, &domain.OrgInvitation{}, &domain.ChatSession{}, &domain.ChatMessage{}, &domain.VoiceNote{})

		// Explicit column guards for voice_notes — AutoMigrate won't add columns to pre-existing tables that diverge
		db.Exec(`CREATE TABLE IF NOT EXISTS voice_notes (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id UUID NOT NULL,
			user_id UUID NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS org_id UUID`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS user_id UUID`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS contact_id UUID REFERENCES contacts(id) ON DELETE SET NULL`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS deal_id UUID REFERENCES deals(id) ON DELETE SET NULL`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS file_url TEXT NOT NULL DEFAULT ''`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS duration_seconds INT NOT NULL DEFAULT 0`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS language_code VARCHAR(10) DEFAULT 'en'`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'uploaded'`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS transcript TEXT`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS summary TEXT`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS key_points JSONB DEFAULT '[]'`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS action_items JSONB DEFAULT '[]'`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS extracted_contact_updates JSONB DEFAULT '{}'`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS sentiment VARCHAR(50)`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS error_message TEXT`)
		db.Exec(`ALTER TABLE voice_notes ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_voice_notes_org ON voice_notes(org_id)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_voice_notes_user ON voice_notes(user_id)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_voice_notes_contact ON voice_notes(contact_id)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_voice_notes_deal ON voice_notes(deal_id)`)

		log.Info("Seeding system roles...")
		if err := repository.SeedSystemRoles(db); err != nil {
			log.Error("Failed to seed system roles", zap.Error(err))
		}
	}

	redisClient, err := cache.NewRedisClient(cfg.RedisURL)
	if err != nil {
		log.Fatal("Failed to connect to Redis", zap.Error(err))
	}
	if redisClient != nil {
		log.Info("Redis connection established")
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.MaxMultipartMemory = 500 << 20 // 500 MB

	// Custom recovery middleware: return JSON on panic instead of gin's default HTML page.
	router.Use(func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered", zap.Any("panic", r))
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			}
		}()
		c.Next()
	})

	if cfg.SentryDSN != "" {
		router.Use(sentrygin.New(sentrygin.Options{Repanic: true}))
	}

	router.Use(func(c *gin.Context) {
		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			reqID = uuid.NewString()
		}
		c.Set("request_id", reqID)
		c.Header("X-Request-ID", reqID)
		
		ctx := context.WithValue(c.Request.Context(), "request_id", reqID)
		c.Request = c.Request.WithContext(ctx)

		start := time.Now()
		c.Next()

		log.Info("HTTP Request",
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.String("query", c.Request.URL.RawQuery),
			zap.String("ip", c.ClientIP()),
			zap.String("user-agent", c.Request.UserAgent()),
			zap.Duration("latency", time.Since(start)),
			zap.String("request_id", reqID),
		)
	})

	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{cfg.FrontendURL, "http://localhost:5173", "https://20q-crm.vercel.app"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"version": "0.3.0",
		})
	})

	var autoEngine *automation.Engine
	if db != nil {
		authRepo := repository.NewAuthRepository(db)
		stageRepo := repository.NewPipelineStageRepository(db)
		authUseCase := usecase.NewAuthUseCase(authRepo, stageRepo, cfg)
		authHandler := delivery.NewAuthHandler(authUseCase, cfg)

		var mailerSvc domain.Mailer
		if cfg.ResendAPIKey != "" {
			mailerSvc = mailer.NewResendMailer(cfg.ResendAPIKey, "onboarding@resend.dev")
		} else {
			mailerSvc = mailer.NewLogMailer()
		}

		appEnv := os.Getenv("APP_ENV")

		workspaceUseCase := usecase.NewWorkspaceUseCase(authRepo, mailerSvc, appEnv)
		workspaceHandler := delivery.NewWorkspaceHandler(workspaceUseCase)

		budget := ai.NewBudgetGuard(db, redisClient)
		gateway := ai.NewAIGateway(
			cfg.CFAccountID, cfg.CFAIGatewayID, cfg.CFAIToken, cfg.AnthropicAPIKey,
			budget, log, cfg.CFAIGatewayToken,
		)
		if cfg.VercelAIGatewayURL != "" && cfg.VercelAIGatewayKey != "" {
			gateway.SetVercelGateway(cfg.VercelAIGatewayURL, cfg.VercelAIGatewayKey)
			log.Info("Vercel AI Gateway configured", zap.String("url", cfg.VercelAIGatewayURL))
		}
		embedSvc := ai.NewEmbeddingService(cfg.CFAccountID, cfg.CFAIGatewayID, cfg.CFAIToken, cfg.CFAIGatewayToken)
		embedWorker := worker.NewEmbeddingWorker(embedSvc, db, log, 200)
		go embedWorker.Start(context.Background(), 5)

		orgSettingsRepo := repository.NewOrgSettingsRepository(db)
		orgSettingsUC := usecase.NewOrgSettingsUseCase(orgSettingsRepo)
		settingsHandler := delivery.NewSettingsHandler(orgSettingsUC)

		customObjRepo := repository.NewCustomObjectRepository(db)
		customObjUC := usecase.NewCustomObjectUseCase(customObjRepo)
		customObjHandler := delivery.NewCustomObjectHandler(customObjUC)

		contactRepo := repository.NewContactRepository(db)
		contactUseCase := usecase.NewContactUseCase(contactRepo, embedWorker, embedSvc)
		contactHandler := delivery.NewContactHandler(contactUseCase)

		companyRepo := repository.NewCompanyRepository(db)
		companyUseCase := usecase.NewCompanyUseCase(companyRepo)
		companyHandler := delivery.NewCompanyHandler(companyUseCase)

		tagRepo := repository.NewTagRepository(db)
		tagUseCase := usecase.NewTagUseCase(tagRepo)
		tagHandler := delivery.NewTagHandler(tagUseCase)

		stageUseCase := usecase.NewPipelineStageUseCase(stageRepo)
		pipelineHandler := delivery.NewPipelineHandler(stageUseCase)

		aiJobQueue := worker.NewAIJobQueue(redisClient, gateway, db, log)
		go aiJobQueue.Start(context.Background(), 3)

		activityRepo := repository.NewActivityRepository(db)
		activityUseCase := usecase.NewActivityUseCase(activityRepo)
		activityHandler := delivery.NewActivityHandler(activityUseCase, aiJobQueue)

		dealRepo := repository.NewDealRepository(db)
		dealUseCase := usecase.NewDealUseCase(dealRepo, stageRepo, activityRepo)
		dealHandler := delivery.NewDealHandler(dealUseCase)

		taskRepo := repository.NewTaskRepository(db)
		taskUseCase := usecase.NewTaskUseCase(taskRepo)
		taskHandler := delivery.NewTaskHandler(taskUseCase)

		userRepo := repository.NewUserRepository(db)
		userHandler := delivery.NewUserHandler(userRepo)

		kbRepo := repository.NewKnowledgeBaseRepository(db)
		kbBuilder := ai.NewKnowledgeBuilder(kbRepo, redisClient)
		kbUseCase := usecase.NewKnowledgeBaseUseCase(kbRepo, kbBuilder)
		kbHandler := delivery.NewKnowledgeHandler(kbUseCase)

		aiHandler := delivery.NewAIHandler(gateway, budget, embedSvc, kbBuilder, aiJobQueue, contactUseCase)

		chatSessionRepo := repository.NewChatSessionRepository(db)
		chatSessionHandler := delivery.NewChatSessionHandler(chatSessionRepo)

		commandCenter := ai.NewCommandCenter(gateway, kbBuilder, contactRepo, dealRepo, taskRepo, activityRepo, chatSessionRepo, log)
		commandHandler := delivery.NewCommandHandler(commandCenter)

		eventsHandler := delivery.NewEventsHandler(redisClient)

		voiceNoteRepo := repository.NewVoiceNoteRepository(db)
		voiceNoteUC := usecase.NewVoiceNoteUseCase(voiceNoteRepo, aiJobQueue, cfg, contactRepo)
		voiceHandler := delivery.NewVoiceHandler(voiceNoteUC)

		delivery.RegisterRoutes(router, authHandler, contactHandler, companyHandler, tagHandler, dealHandler, pipelineHandler, activityHandler, taskHandler, userHandler, aiHandler, settingsHandler, customObjHandler, kbHandler, commandHandler, eventsHandler, workspaceHandler, chatSessionHandler, voiceHandler, cfg, db, redisClient, authRepo)

		// --- Workflow Automation Engine ---
		autoLogger := slog.Default()
		autoEngine = automation.NewEngine(db, autoLogger,
			automation.WithWorkers(5),
			automation.WithEmailExecutor(cfg.ResendAPIKey, "onboarding@resend.dev"),
		)
		autoEngine.Start()
		autoHandler := automation.NewHandler(autoEngine, db, autoLogger)
		autoHandler.RegisterRoutes(router,
			delivery.AuthMiddleware(cfg.JWTSecret, authRepo, redisClient),
			delivery.RequireRole,
		)
		log.Info("All routes registered (including automation)")
	} else {
		log.Warn("Database not connected — routes skipped")
	}


	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Failed to start server", zap.Error(err))
		}
	}()
	log.Info("Server listening", zap.String("port", cfg.Port))

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("Shutting down server...")

	// Stop automation engine first (let in-flight runs finish)
	if autoEngine != nil {
		autoEngine.Stop()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown", zap.Error(err))
	}

	log.Info("Server exiting")
}
