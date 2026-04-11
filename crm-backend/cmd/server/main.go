package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	delivery "crm-backend/internal/delivery/http"
	"crm-backend/internal/ai"
	"crm-backend/internal/repository"
	"crm-backend/internal/usecase"
	"crm-backend/internal/worker"
	"crm-backend/pkg/cache"
	"crm-backend/pkg/config"
	"crm-backend/pkg/database"
	"crm-backend/pkg/logger"

	"github.com/getsentry/sentry-go"
	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func main() {
	// 1. Initialize Logger
	if err := logger.InitLogger(); err != nil {
		panic(err)
	}
	defer logger.Sync()
	log := logger.Log

	log.Info("Starting CRM backend")

	// 2. Load Config
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal("Failed to load config", zap.Error(err))
	}

	// 3. Init Sentry
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

	// 4. Init Database
	db, err := database.NewConnection(cfg.DatabaseURL)
	if err != nil {
		log.Fatal("Failed to connect to database", zap.Error(err))
	}
	if db != nil {
		log.Info("Database connection established")
	}

	// 5. Init Redis
	redisClient, err := cache.NewRedisClient(cfg.RedisURL)
	if err != nil {
		log.Fatal("Failed to connect to Redis", zap.Error(err))
	}
	if redisClient != nil {
		log.Info("Redis connection established")
	}

	// 6. Setup Gin Router
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()

	// Middleware
	router.Use(gin.Recovery())

	// Sentry middleware
	if cfg.SentryDSN != "" {
		router.Use(sentrygin.New(sentrygin.Options{Repanic: true}))
	}

	// Custom Zap Logger Middleware
	router.Use(func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		log.Info("HTTP Request",
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.String("ip", c.ClientIP()),
			zap.String("user-agent", c.Request.UserAgent()),
			zap.Duration("latency", time.Since(start)),
		)
	})

	// CORS Middleware
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{cfg.FrontendURL, "http://localhost:5173", "https://20q-crm.vercel.app"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// 7. Health Check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"version": "0.2.0",
		})
	})

	// 8. Wire Clean Architecture
	if db != nil {
		authRepo := repository.NewAuthRepository(db)
		authUseCase := usecase.NewAuthUseCase(authRepo, cfg)
		authHandler := delivery.NewAuthHandler(authUseCase, cfg)

		// Create AI config early to pass to contact usecase for embeddings
		budget := ai.NewBudgetGuard(db, redisClient)
		gateway := ai.NewAIGateway(
			cfg.CFAccountID, cfg.CFAIGatewayID, cfg.CFAIToken, cfg.AnthropicAPIKey,
			budget, log,
		)
		embedSvc := ai.NewEmbeddingService(cfg.CFAccountID, cfg.CFAIGatewayID, cfg.CFAIToken, cfg.CFAIGatewayToken)
		embedWorker := worker.NewEmbeddingWorker(embedSvc, db, log, 200)
		go embedWorker.Start(context.Background(), 5)

		contactRepo := repository.NewContactRepository(db)
		contactUseCase := usecase.NewContactUseCase(contactRepo, embedWorker)
		contactHandler := delivery.NewContactHandler(contactUseCase)

		companyRepo := repository.NewCompanyRepository(db)
		companyUseCase := usecase.NewCompanyUseCase(companyRepo)
		companyHandler := delivery.NewCompanyHandler(companyUseCase)

		tagRepo := repository.NewTagRepository(db)
		tagUseCase := usecase.NewTagUseCase(tagRepo)
		tagHandler := delivery.NewTagHandler(tagUseCase)

		stageRepo := repository.NewPipelineStageRepository(db)
		stageUseCase := usecase.NewPipelineStageUseCase(stageRepo)
		pipelineHandler := delivery.NewPipelineHandler(stageUseCase)

		activityRepo := repository.NewActivityRepository(db)
		activityUseCase := usecase.NewActivityUseCase(activityRepo)
		activityHandler := delivery.NewActivityHandler(activityUseCase)

		dealRepo := repository.NewDealRepository(db)
		dealUseCase := usecase.NewDealUseCase(dealRepo, stageRepo, activityRepo)
		dealHandler := delivery.NewDealHandler(dealUseCase)

		taskRepo := repository.NewTaskRepository(db)
		taskUseCase := usecase.NewTaskUseCase(taskRepo)
		taskHandler := delivery.NewTaskHandler(taskUseCase)

		userRepo := repository.NewUserRepository(db)
		userHandler := delivery.NewUserHandler(userRepo)

		aiHandler := delivery.NewAIHandler(gateway, budget, embedSvc)

		delivery.RegisterRoutes(router, authHandler, contactHandler, companyHandler, tagHandler, dealHandler, pipelineHandler, activityHandler, taskHandler, userHandler, aiHandler, cfg)
		log.Info("All routes registered (auth, contacts, deals, pipeline, activities, tasks, users, ai)")
	} else {
		log.Warn("Database not connected — routes skipped")
	}


	// 9. Server Setup
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

	// 10. Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown", zap.Error(err))
	}

	log.Info("Server exiting")
}
