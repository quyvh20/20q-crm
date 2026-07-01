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
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
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

	// ── Phase 1: Start HTTP server immediately (healthcheck must respond fast) ──
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
		AllowOrigins:     []string{cfg.FrontendURL, "http://localhost:5173", "https://20q-crm.pages.dev"},
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

	// Start the server immediately so the healthcheck passes while
	// we connect to DB/Redis and run migrations in the foreground.
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Failed to start server", zap.Error(err))
		}
	}()
	log.Info("Server listening (health endpoint ready)", zap.String("port", cfg.Port))

	// ── Phase 2: Connect to services (retry on cold start) ──
	var db *gorm.DB
	for attempt := 1; attempt <= 5; attempt++ {
		db, err = database.NewConnection(cfg.DatabaseURL)
		if err == nil {
			break
		}
		wait := time.Duration(1<<uint(attempt)) * time.Second // 2s, 4s, 8s, 16s, 32s
		log.Warn("Database connection failed, retrying...",
			zap.Int("attempt", attempt),
			zap.Duration("backoff", wait),
			zap.Error(err),
		)
		time.Sleep(wait)
	}
	if err != nil {
		log.Fatal("Failed to connect to database after 5 attempts", zap.Error(err))
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

		// Object Registry (migration 000015) — applied here as an idempotent boot
		// guard too, because golang-migrate is the source of truth only for fresh
		// DBs; existing prod schema is maintained by these guards. Mirrors
		// migrations/000015_object_registry.up.sql exactly.
		db.Exec(`CREATE TABLE IF NOT EXISTS object_defs (
			id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			slug             VARCHAR(100) NOT NULL,
			label            VARCHAR(255) NOT NULL,
			label_plural     VARCHAR(255) NOT NULL,
			icon             VARCHAR(50)  DEFAULT '📦',
			color            VARCHAR(20)  DEFAULT '#6B7280',
			is_system        BOOLEAN NOT NULL DEFAULT FALSE,
			storage          VARCHAR(10) NOT NULL DEFAULT 'jsonb',
			record_table     VARCHAR(63),
			display_field_id UUID,
			searchable       BOOLEAN NOT NULL DEFAULT FALSE,
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at       TIMESTAMPTZ
		)`)
		db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uix_object_defs_org_slug ON object_defs(org_id, slug) WHERE deleted_at IS NULL`)
		db.Exec(`ALTER TABLE object_defs ENABLE ROW LEVEL SECURITY`)
		db.Exec(`CREATE TABLE IF NOT EXISTS object_fields (
			id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id         UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			object_def_id  UUID NOT NULL REFERENCES object_defs(id) ON DELETE CASCADE,
			key            VARCHAR(100) NOT NULL,
			label          VARCHAR(255) NOT NULL,
			type           VARCHAR(30)  NOT NULL,
			options        JSONB DEFAULT '[]',
			target_slug    VARCHAR(100),
			is_required    BOOLEAN NOT NULL DEFAULT FALSE,
			is_unique      BOOLEAN NOT NULL DEFAULT FALSE,
			is_system      BOOLEAN NOT NULL DEFAULT FALSE,
			storage_kind   VARCHAR(10) NOT NULL DEFAULT 'jsonb',
			maps_to_column VARCHAR(63),
			position       INT NOT NULL DEFAULT 0,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at     TIMESTAMPTZ
		)`)
		db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uix_object_fields_def_key ON object_fields(object_def_id, key) WHERE deleted_at IS NULL`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_object_fields_def ON object_fields(object_def_id) WHERE deleted_at IS NULL`)
		db.Exec(`ALTER TABLE object_fields ENABLE ROW LEVEL SECURITY`)
		db.Exec(`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_object_defs_display_field') THEN
				ALTER TABLE object_defs ADD CONSTRAINT fk_object_defs_display_field
					FOREIGN KEY (display_field_id) REFERENCES object_fields(id) ON DELETE SET NULL;
			END IF;
		END $$`)

		// Universal relationships (migration 000016) — boot guard, since
		// golang-migrate is authoritative only for fresh DBs and existing prod
		// schema is maintained here. Mirrors
		// migrations/000016_object_links.up.sql exactly.
		db.Exec(`CREATE TABLE IF NOT EXISTS object_links (
			id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			from_slug    VARCHAR(100) NOT NULL,
			from_id      UUID NOT NULL,
			to_slug      VARCHAR(100) NOT NULL,
			to_id        UUID NOT NULL,
			relation_key VARCHAR(100) NOT NULL,
			created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at   TIMESTAMPTZ
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_object_links_from ON object_links(org_id, from_slug, from_id) WHERE deleted_at IS NULL`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_object_links_to ON object_links(org_id, to_slug, to_id) WHERE deleted_at IS NULL`)
		db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uix_object_links_unique ON object_links(org_id, from_slug, from_id, relation_key, to_slug, to_id) WHERE deleted_at IS NULL`)
		db.Exec(`ALTER TABLE object_links ENABLE ROW LEVEL SECURITY`)

		// Object-Level Security + audit (migration 000017, P5a) — boot guard, since
		// golang-migrate is authoritative only for fresh DBs and existing prod schema
		// is maintained here. Mirrors migrations/000017_object_security.up.sql exactly.
		// object_permissions is keyed by (org_id, role_id, object_slug): custom objects
		// aren't in object_defs until P7, and slug is the cross-stack identifier already
		// used by object_links/object_audit.
		db.Exec(`CREATE TABLE IF NOT EXISTS object_permissions (
			org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			role_id     UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
			object_slug VARCHAR(100) NOT NULL,
			can_read    BOOLEAN NOT NULL DEFAULT FALSE,
			can_create  BOOLEAN NOT NULL DEFAULT FALSE,
			can_edit    BOOLEAN NOT NULL DEFAULT FALSE,
			can_delete  BOOLEAN NOT NULL DEFAULT FALSE,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (org_id, role_id, object_slug)
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_object_permissions_org ON object_permissions(org_id)`)
		db.Exec(`ALTER TABLE object_permissions ENABLE ROW LEVEL SECURITY`)
		db.Exec(`CREATE TABLE IF NOT EXISTS object_audit (
			id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			object_slug VARCHAR(100) NOT NULL,
			record_id   UUID NOT NULL,
			actor_id    UUID REFERENCES users(id) ON DELETE SET NULL,
			action      VARCHAR(20) NOT NULL,
			changes     JSONB NOT NULL DEFAULT '{}',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_object_audit_record ON object_audit(org_id, object_slug, record_id, created_at DESC)`)
		db.Exec(`ALTER TABLE object_audit ENABLE ROW LEVEL SECURITY`)

		// Field-Level Security (migration 000017b, P5b) — boot guard, since
		// golang-migrate is authoritative only for fresh DBs and existing prod schema
		// is maintained here. Mirrors migrations/000017b_field_permissions.up.sql
		// exactly. Keyed by (org_id, role_id, object_slug, field_key) for the same
		// reason object_permissions is slug-keyed: a custom object's fields aren't in
		// object_fields until P7, and (slug, key) is the cross-stack field identifier.
		// Opt-in: a field with no row here is fully accessible, so FLS costs nothing
		// until an admin restricts a field.
		db.Exec(`CREATE TABLE IF NOT EXISTS field_permissions (
			org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			role_id     UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
			object_slug VARCHAR(100) NOT NULL,
			field_key   VARCHAR(100) NOT NULL,
			level       VARCHAR(10) NOT NULL DEFAULT 'edit',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (org_id, role_id, object_slug, field_key),
			CONSTRAINT chk_field_permissions_level CHECK (level IN ('hidden', 'read', 'edit'))
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_field_permissions_org ON field_permissions(org_id)`)
		db.Exec(`ALTER TABLE field_permissions ENABLE ROW LEVEL SECURITY`)

		// Generic search index (migration 000018, P6) — boot guard, since
		// golang-migrate is authoritative only for fresh DBs and existing prod schema
		// is maintained here. Mirrors migrations/000018_record_embeddings.up.sql
		// exactly. record_embeddings is the one additive index that makes any object
		// (custom objects first) semantically + fulltext searchable, keyed by
		// (org_id, object_slug, record_id) like the rest of the cross-stack surface.
		// Contacts keep their native embedding/fulltext path untouched (R1). The
		// custom_object_defs.searchable flag is the per-object opt-in for the objects
		// P6 targets, since a custom object's def isn't in object_defs until P7.
		db.Exec(`CREATE TABLE IF NOT EXISTS record_embeddings (
			org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			object_slug VARCHAR(100) NOT NULL,
			record_id   UUID NOT NULL,
			embedding   vector(768),
			content     TEXT,
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (org_id, object_slug, record_id)
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_record_embeddings_fts ON record_embeddings USING GIN (to_tsvector('simple', coalesce(content, '')))`)
		db.Exec(`ALTER TABLE record_embeddings ENABLE ROW LEVEL SECURITY`)
		db.Exec(`ALTER TABLE custom_object_defs ADD COLUMN IF NOT EXISTS searchable BOOLEAN NOT NULL DEFAULT FALSE`)

		// Per-role detail layouts (migration 000022, P8) — boot guard, since
		// golang-migrate is authoritative only for fresh DBs and existing prod schema
		// is maintained here. Mirrors migrations/000022_object_layouts.up.sql exactly.
		// Layout is presentation only; FLS (field_permissions) stays the security
		// boundary. The unique partial index ensures at most one is_default per object,
		// and the role-assignment index ensures one layout per role per object.
		db.Exec(`CREATE TABLE IF NOT EXISTS object_layouts (
			id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			object_slug  VARCHAR(100) NOT NULL,
			name         VARCHAR(255) NOT NULL,
			layout       JSONB NOT NULL DEFAULT '[]',
			is_default   BOOLEAN NOT NULL DEFAULT FALSE,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at   TIMESTAMPTZ
		)`)
		db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uix_object_layouts_default ON object_layouts(org_id, object_slug) WHERE is_default AND deleted_at IS NULL`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_object_layouts_org_slug ON object_layouts(org_id, object_slug) WHERE deleted_at IS NULL`)
		db.Exec(`ALTER TABLE object_layouts ENABLE ROW LEVEL SECURITY`)
		db.Exec(`CREATE TABLE IF NOT EXISTS object_layout_roles (
			id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			layout_id    UUID NOT NULL REFERENCES object_layouts(id) ON DELETE CASCADE,
			object_slug  VARCHAR(100) NOT NULL,
			role_id      UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE
		)`)
		db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uix_object_layout_roles_one_per_role ON object_layout_roles(org_id, object_slug, role_id)`)
		db.Exec(`ALTER TABLE object_layout_roles ENABLE ROW LEVEL SECURITY`)

		// Human-readable record numbers (migration 000023) — boot guard, since
		// golang-migrate is authoritative only for fresh DBs and existing prod schema
		// is maintained here. Mirrors migrations/000023_record_numbers.up.sql. Numbers
		// live in a side table keyed by (org, object_slug, record_id) so typed and
		// JSONB objects are numbered uniformly.
		db.Exec(`ALTER TABLE object_defs ADD COLUMN IF NOT EXISTS number_prefix VARCHAR(16)`)
		db.Exec(`CREATE TABLE IF NOT EXISTS object_number_seqs (
			org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			object_slug VARCHAR(100) NOT NULL,
			next_seq    BIGINT NOT NULL DEFAULT 1,
			PRIMARY KEY (org_id, object_slug)
		)`)
		db.Exec(`CREATE TABLE IF NOT EXISTS record_numbers (
			org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			object_slug VARCHAR(100) NOT NULL,
			record_id   UUID NOT NULL,
			seq         BIGINT NOT NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (org_id, object_slug, record_id)
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_record_numbers_org_slug ON record_numbers(org_id, object_slug)`)
		db.Exec(`ALTER TABLE object_number_seqs ENABLE ROW LEVEL SECURITY`)
		db.Exec(`ALTER TABLE record_numbers ENABLE ROW LEVEL SECURITY`)

		// Mirror fields (migration 000024) — boot guard. via_field/source_field
		// configure a read-only field that displays a value pulled from a linked
		// record. Mirrors migrations/000024_mirror_fields.up.sql.
		db.Exec(`ALTER TABLE object_fields ADD COLUMN IF NOT EXISTS via_field VARCHAR(100)`)
		db.Exec(`ALTER TABLE object_fields ADD COLUMN IF NOT EXISTS source_field VARCHAR(100)`)

		log.Info("Seeding system roles...")
		if err := repository.SeedSystemRoles(db); err != nil {
			log.Error("Failed to seed system roles", zap.Error(err))
		}

		// P7 convergence — make object_fields the single field-def store: copy each
		// org's admin-defined custom fields out of the legacy
		// org_settings.custom_field_defs blob into object_fields. Idempotent and
		// boot-guarded (golang-migrate is dead on prod). After this the blob is no
		// longer read or written; object_fields is authoritative.
		if n, err := repository.BackfillObjectFieldsFromBlob(db); err != nil {
			log.Error("Failed to backfill object_fields from custom_field_defs blob", zap.Error(err))
		} else if n > 0 {
			log.Info("Backfilled custom field defs into object_fields", zap.Int64("rows", n))
		}

		// P7 convergence — make object_links the single relationship store: copy the
		// hardcoded custom_object_records.contact_id/deal_id FKs into 'contact'/'deal'
		// edges, then drop those columns. Idempotent and boot-guarded; the column
		// guard makes a re-run a no-op after the columns are gone.
		if n, err := repository.BackfillObjectLinksFromRecordFKs(db); err != nil {
			log.Error("Failed to backfill object_links from custom-record FKs", zap.Error(err))
		} else if n > 0 {
			log.Info("Backfilled custom-record relations into object_links", zap.Int64("edges", n))
		}

		// P7 convergence — make object_defs/object_fields the single store for EVERY
		// object: move custom object defs + fields into the registry (reusing ids) and
		// repoint the records FK. Idempotent and boot-guarded. After this, one field
		// editor serves system and custom objects alike.
		if n, err := repository.ConvergeCustomObjectsToRegistry(db); err != nil {
			log.Error("Failed to converge custom objects into the registry", zap.Error(err))
		} else if n > 0 {
			log.Info("Converged custom objects into object_defs/object_fields", zap.Int64("defs", n))
		}

		// Human-readable record numbers — assign a number to every existing record
		// and seed the per-object counters. Idempotent and boot-guarded; runs after
		// custom objects are in object_defs so their records are numbered too.
		if n, err := repository.BackfillRecordNumbers(db); err != nil {
			log.Error("Failed to backfill record numbers", zap.Error(err))
		} else if n > 0 {
			log.Info("Backfilled human-readable record numbers", zap.Int64("records", n))
		}
	}

	var redisClient *redis.Client
	for attempt := 1; attempt <= 5; attempt++ {
		redisClient, err = cache.NewRedisClient(cfg.RedisURL)
		if err == nil {
			break
		}
		wait := time.Duration(1<<uint(attempt)) * time.Second
		log.Warn("Redis connection failed, retrying...",
			zap.Int("attempt", attempt),
			zap.Duration("backoff", wait),
			zap.Error(err),
		)
		time.Sleep(wait)
	}
	if err != nil {
		log.Fatal("Failed to connect to Redis after 5 attempts", zap.Error(err))
	}
	if redisClient != nil {
		log.Info("Redis connection established")
	}

	// ── Phase 3: Register DB-dependent routes ──
	var autoEngine *automation.Engine
	if db != nil {
		authRepo := repository.NewAuthRepository(db)
		stageRepo := repository.NewPipelineStageRepository(db)
		authUseCase := usecase.NewAuthUseCase(authRepo, stageRepo, cfg)
		authHandler := delivery.NewAuthHandler(authUseCase, cfg)

		var mailerSvc domain.Mailer
		if cfg.ResendAPIKey != "" {
			mailerSvc = mailer.NewResendMailer(cfg.ResendAPIKey, "noreply@twentyq.io")
		} else {
			mailerSvc = mailer.NewLogMailer()
		}

		appEnv := os.Getenv("APP_ENV")

		workspaceUseCase := usecase.NewWorkspaceUseCase(authRepo, mailerSvc, appEnv, cfg.FrontendURL)
		workspaceHandler := delivery.NewWorkspaceHandler(workspaceUseCase)

		budget := ai.NewBudgetGuard(db, redisClient)
		gateway := ai.NewAIGateway(
			cfg.CFAccountID, cfg.CFAIGatewayID, cfg.CFAIToken,
			budget, log, cfg.CFAIGatewayToken,
		)
		embedSvc := ai.NewEmbeddingService(cfg.CFAccountID, cfg.CFAIGatewayID, cfg.CFAIToken, cfg.CFAIGatewayToken)
		recordEmbeddingRepo := repository.NewRecordEmbeddingRepository(db)
		embedWorker := worker.NewEmbeddingWorker(embedSvc, recordEmbeddingRepo, db, log, 200)
		go embedWorker.Start(context.Background(), 5)

		// Object Registry repo (P2) — created early because OrgSettingsUseCase now
		// backs its custom-field defs onto object_fields (P7), so it depends on this.
		objectRegistryRepo := repository.NewObjectRegistryRepository(db)

		orgSettingsUC := usecase.NewOrgSettingsUseCase(objectRegistryRepo)
		settingsHandler := delivery.NewSettingsHandler(orgSettingsUC)

		customObjRepo := repository.NewCustomObjectRepository(db)

		// KB builder needs customObjUC and orgSettingsUC, but both need kbBuilder for cache busting.
		// Create kbBuilder first (without customObjUC), then inject dependencies.
		kbRepo := repository.NewKnowledgeBaseRepository(db)
		kbBuilder := ai.NewKnowledgeBuilder(kbRepo, orgSettingsUC, nil, redisClient) // customObjUC set below

		customObjUC := usecase.NewCustomObjectUseCase(customObjRepo, kbBuilder)
		customObjHandler := delivery.NewCustomObjectHandler(customObjUC)

		// Inject customObjUC into kbBuilder so it can read custom objects for schema
		kbBuilder.SetCustomObjectUC(customObjUC)

		// Object Registry (P2/P7): uniform view over system + custom objects, reading
		// every field from object_fields (no blob merge after the P7 cutover).
		objectRegistryUC := usecase.NewObjectRegistryUseCase(objectRegistryRepo)

		// Per-role detail layouts (P8): layout repo + usecase wired early so the
		// registry handler can inject them at construction time below.
		layoutRepo := repository.NewObjectLayoutRepository(db)
		layoutUC := usecase.NewObjectLayoutUseCase(layoutRepo)
		layoutHandler := delivery.NewObjectLayoutHandler(layoutUC)

		contactRepo := repository.NewContactRepository(db)
		contactUseCase := usecase.NewContactUseCase(contactRepo, embedWorker, embedSvc)
		contactHandler := delivery.NewContactHandler(contactUseCase)

		companyRepo := repository.NewCompanyRepository(db)
		companyUseCase := usecase.NewCompanyUseCase(companyRepo)
		companyHandler := delivery.NewCompanyHandler(companyUseCase)

		// RecordService (P3): the single read/write chokepoint over every object.
		// Dispatches typed objects to the contact/deal/company usecases and custom
		// objects to the generic usecase; wired below after dealUseCase exists.

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

		// Object-Level Security + audit (P5a): one type is both the authorizer
		// RecordService enforces through and the admin grid usecase. It needs the
		// registry usecase to list objects for the grid.
		permissionRepo := repository.NewPermissionRepository(db)
		permissionUC := usecase.NewPermissionUseCase(permissionRepo, objectRegistryUC)
		permissionHandler := delivery.NewPermissionHandler(permissionUC)

		// Object Registry handler — constructed here (after permissionUC) so it can
		// receive both the layout usecase (P8) and the OLS/FLS authorizer (P5a/P5b).
		// The authorizer is the same permissionUC value handed to RecordService.
		objectRegistryHandler := delivery.NewObjectRegistryHandler(objectRegistryUC, layoutUC, permissionUC)

		// RecordService now that every per-object usecase exists. linkRepo + tagRepo
		// back the universal relationship/tag surface (P4); permissionUC enforces
		// OLS + writes the audit trail (P5a) — the security chokepoint.
		linkRepo := repository.NewLinkRepository(db)
		recordService := usecase.NewRecordService(customObjUC, orgSettingsUC, contactUseCase, companyUseCase, dealUseCase, linkRepo, tagRepo, permissionUC)
		// Human-readable record numbers (DEAL-0001): allocate on create, resolve on read.
		recordService.SetNumberRepo(repository.NewRecordNumberRepository(db))
		// Reverse related lists compose the registry + record services (P-relationships):
		// they surface, on any record, the child records that point back at it.
		relatedListsUC := usecase.NewRelatedListsUseCase(objectRegistryUC, recordService)
		recordHandler := delivery.NewRecordHandler(recordService, relatedListsUC)

		// Global search (P6): spans searchable custom objects (record_embeddings)
		// plus contacts (native index), resolving every hit through RecordService so
		// OLS/FLS apply to search results too.
		searchUC := usecase.NewSearchUseCase(recordEmbeddingRepo, embedSvc, recordService, objectRegistryUC, contactUseCase)
		searchHandler := delivery.NewSearchHandler(searchUC)

		taskRepo := repository.NewTaskRepository(db)
		taskUseCase := usecase.NewTaskUseCase(taskRepo)
		taskHandler := delivery.NewTaskHandler(taskUseCase)

		userRepo := repository.NewUserRepository(db)
		userHandler := delivery.NewUserHandler(userRepo)

		kbUseCase := usecase.NewKnowledgeBaseUseCase(kbRepo, kbBuilder)
		kbHandler := delivery.NewKnowledgeHandler(kbUseCase)

		aiHandler := delivery.NewAIHandler(gateway, budget, embedSvc, kbBuilder, aiJobQueue, contactUseCase)

		chatSessionRepo := repository.NewChatSessionRepository(db)
		chatSessionHandler := delivery.NewChatSessionHandler(chatSessionRepo)

		commandCenter := ai.NewCommandCenter(gateway, kbBuilder, contactRepo, dealRepo, taskRepo, activityRepo, chatSessionRepo, customObjUC, log)
		commandHandler := delivery.NewCommandHandler(commandCenter)

		eventsHandler := delivery.NewEventsHandler(redisClient)

		voiceNoteRepo := repository.NewVoiceNoteRepository(db)
		voiceNoteUC := usecase.NewVoiceNoteUseCase(voiceNoteRepo, aiJobQueue, cfg, contactRepo)
		voiceHandler := delivery.NewVoiceHandler(voiceNoteUC)

		delivery.RegisterRoutes(router, authHandler, contactHandler, companyHandler, tagHandler, dealHandler, pipelineHandler, activityHandler, taskHandler, userHandler, aiHandler, settingsHandler, customObjHandler, objectRegistryHandler, recordHandler, permissionHandler, searchHandler, kbHandler, commandHandler, eventsHandler, workspaceHandler, chatSessionHandler, voiceHandler, layoutHandler, cfg, db, redisClient, authRepo)

		// --- Workflow Automation Engine ---
		memHandler := logger.NewMemoryHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
		autoLogger := slog.New(memHandler)
		slog.SetDefault(autoLogger)
		autoEngine = automation.NewEngine(db, autoLogger,
			automation.WithWorkers(5),
			automation.WithEmailExecutor(cfg.ResendAPIKey, "noreply@twentyq.io"),
		)
		autoEngine.Start()
		autoHandler := automation.NewHandler(autoEngine, db, autoLogger)
		autoHandler.RegisterRoutes(router,
			delivery.AuthMiddleware(cfg.JWTSecret, authRepo, redisClient),
			delivery.RequireRole,
		)

		// Wire schema cache invalidation: when stages, tags, custom fields,
		// or custom object defs change, the workflow schema cache is purged
		// for that org so the builder picks up fresh data.
		invalidator := autoHandler.InvalidateSchemaCache
		pipelineHandler.SetSchemaInvalidator(invalidator)
		tagHandler.SetSchemaInvalidator(invalidator)
		settingsHandler.SetSchemaInvalidator(invalidator)
		// Creating/editing/deleting a custom object also busts the OLS cache, so a
		// brand-new object's default permissions (seeded on the next load) take
		// effect immediately instead of after the cache TTL (P5a).
		customObjHandler.SetSchemaInvalidator(func(orgID uuid.UUID) {
			invalidator(orgID)
			permissionUC.Invalidate(orgID)
		})

		// Wire contact creation → automation trigger
		contactHandler.SetEventEmitter(autoEngine.TriggerEvent)

		// Wire custom object create/update → automation trigger
		customObjHandler.SetEventEmitter(autoEngine.TriggerEvent)

		// Wire the uniform RecordService write path → automation trigger, so
		// custom-object workflows keep firing now that the UI writes via the
		// uniform endpoint (parity with customObjHandler). SetEventEmitter is part
		// of the RecordService interface, so a signature change fails the build
		// rather than silently disabling automation.
		recordService.SetEventEmitter(autoEngine.TriggerEvent)

		// Wire the uniform write path → generic search index (P6), so creating or
		// updating a searchable custom object's record (re)indexes it for global
		// search, and deleting it removes the index row. The worker is the indexer;
		// SetSearchIndexer is on the RecordService interface, so a signature drift
		// fails the build rather than silently stopping indexing.
		recordService.SetSearchIndexer(embedWorker)

		// Wire deal stage change → automation trigger
		dealHandler.SetEventEmitter(autoEngine.TriggerEvent)

		log.Info("All routes registered (including automation)")
	} else {
		log.Warn("Database not connected — routes skipped")
	}

	log.Info("All services connected, routes registered")

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
