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
		AllowOrigins:     delivery.AllowedOrigins(cfg.FrontendURL),
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-CSRF-Token"},
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
		// U2 My Account: personal preferences + server-side onboarding flag.
		db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS timezone VARCHAR(64) NOT NULL DEFAULT ''`)
		db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS locale VARCHAR(16) NOT NULL DEFAULT ''`)
		db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS onboarding_completed BOOLEAN NOT NULL DEFAULT false`)
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

		// Auth + admin event log (migration 000025, P0) — boot guard, since
		// golang-migrate is authoritative only for fresh DBs and existing prod
		// schema is maintained here. Mirrors migrations/000025_auth_events.up.sql.
		db.Exec(`CREATE TABLE IF NOT EXISTS auth_events (
			id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id      UUID REFERENCES organizations(id) ON DELETE CASCADE,
			actor_id    UUID REFERENCES users(id) ON DELETE SET NULL,
			target_id   UUID,
			category    VARCHAR(20)  NOT NULL,
			event_type  VARCHAR(60)  NOT NULL,
			ip          INET,
			user_agent  TEXT,
			metadata    JSONB NOT NULL DEFAULT '{}',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_auth_events_org   ON auth_events(org_id, created_at DESC)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_auth_events_actor ON auth_events(actor_id, created_at DESC)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_auth_events_type  ON auth_events(org_id, event_type, created_at DESC)`)
		db.Exec(`ALTER TABLE auth_events ENABLE ROW LEVEL SECURITY`)

		// Account recovery + email verification (migration 000026, P1) — boot
		// guard. Mirrors migrations/000026_account_recovery.up.sql. The DO block
		// adds email_verified_at AND grandfathers existing users as verified in
		// ONE guarded step, so the backfill runs exactly once (on first creation
		// of the column) and never re-verifies accounts made afterwards.
		db.Exec(`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'users' AND column_name = 'email_verified_at'
			) THEN
				ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;
				UPDATE users SET email_verified_at = NOW();
			END IF;
		END $$`)
		db.Exec(`CREATE TABLE IF NOT EXISTS password_reset_tokens (
			id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash  VARCHAR(255) NOT NULL,
			expires_at  TIMESTAMPTZ NOT NULL,
			used_at     TIMESTAMPTZ,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_password_reset_token ON password_reset_tokens(token_hash)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_password_reset_user  ON password_reset_tokens(user_id)`)
		db.Exec(`ALTER TABLE password_reset_tokens ENABLE ROW LEVEL SECURITY`)
		db.Exec(`CREATE TABLE IF NOT EXISTS email_verification_tokens (
			id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash  VARCHAR(255) NOT NULL,
			expires_at  TIMESTAMPTZ NOT NULL,
			used_at     TIMESTAMPTZ,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_email_verification_token ON email_verification_tokens(token_hash)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_email_verification_user  ON email_verification_tokens(user_id)`)
		db.Exec(`ALTER TABLE email_verification_tokens ENABLE ROW LEVEL SECURITY`)

		// Token version + device-aware refresh tokens (migration 000027, P2) — boot
		// guard. Mirrors migrations/000027_token_version_and_device.up.sql. token_version
		// backfills to 0 (matching old JWTs' absent `tv` claim, so no forced logouts);
		// the refresh_tokens columns add the rotation chain + device metadata.
		db.Exec(`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'users' AND column_name = 'token_version'
			) THEN
				ALTER TABLE users ADD COLUMN token_version INTEGER NOT NULL DEFAULT 0;
			END IF;
		END $$`)
		db.Exec(`ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS device_label VARCHAR(255)`)
		db.Exec(`ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS ip           INET`)
		db.Exec(`ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS user_agent   TEXT`)
		db.Exec(`ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ`)
		db.Exec(`ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS rotated_from UUID REFERENCES refresh_tokens(id) ON DELETE SET NULL`)

		// Make custom roles real (migration 000028, P3) — boot guard. Mirrors
		// migrations/000028_custom_roles_real.up.sql. roles.data_scope generalizes
		// the sales_rep='own' row-scope; role_permissions.org_id scopes custom-role
		// capability rows; the dead legacy capability vocabulary is purged so
		// SeedSystemRoles (below) repopulates the real capability codes; and the
		// vestigial users.role column is dropped (the user_role enum type is kept).
		db.Exec(`ALTER TABLE roles ADD COLUMN IF NOT EXISTS data_scope VARCHAR(10) NOT NULL DEFAULT 'all'`)
		db.Exec(`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'roles_data_scope_check') THEN
				ALTER TABLE roles ADD CONSTRAINT roles_data_scope_check CHECK (data_scope IN ('own', 'all'));
			END IF;
		END $$`)
		db.Exec(`UPDATE roles SET data_scope = 'own' WHERE name = 'sales_rep' AND is_system = TRUE`)
		db.Exec(`ALTER TABLE role_permissions ADD COLUMN IF NOT EXISTS org_id UUID REFERENCES organizations(id) ON DELETE CASCADE`)
		// Purge only the dead legacy vocabulary (colon-format codes like
		// 'deal:read:team' / 'all:all:all'); real capability codes use dots
		// ('members.manage'), so admin edits to capabilities are never touched.
		db.Exec(`DELETE FROM role_permissions WHERE permission_code LIKE '%:%'`)
		db.Exec(`ALTER TABLE users DROP COLUMN IF EXISTS role`)

		// Reports (migration 000029, P9) — boot guard. Mirrors
		// migrations/000029_reports.up.sql exactly. Saved report definitions:
		// object slug + config JSONB, re-run per viewer so OLS/FLS always apply.
		db.Exec(`CREATE TABLE IF NOT EXISTS reports (
			id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			name        VARCHAR(255) NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			object_slug VARCHAR(100) NOT NULL,
			config      JSONB NOT NULL DEFAULT '{}',
			visibility  VARCHAR(10) NOT NULL DEFAULT 'private',
			created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at  TIMESTAMPTZ
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_reports_org ON reports(org_id) WHERE deleted_at IS NULL`)
		db.Exec(`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'reports_visibility_check') THEN
				ALTER TABLE reports ADD CONSTRAINT reports_visibility_check CHECK (visibility IN ('private', 'org'));
			END IF;
		END $$`)
		db.Exec(`ALTER TABLE reports ENABLE ROW LEVEL SECURITY`)

		// Dashboard widgets (migration 000030, P9 Phase B) — boot guard. Mirrors
		// migrations/000030_dashboard_widgets.up.sql exactly. Per-user pinned
		// reports; the unique index makes pinning idempotent.
		db.Exec(`CREATE TABLE IF NOT EXISTS dashboard_widgets (
			id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			report_id  UUID NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
			position   INT NOT NULL DEFAULT 0,
			size       VARCHAR(10) NOT NULL DEFAULT 'half',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
		db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uix_dashboard_widgets_user_report ON dashboard_widgets(org_id, user_id, report_id)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_dashboard_widgets_user ON dashboard_widgets(org_id, user_id)`)
		db.Exec(`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'dashboard_widgets_size_check') THEN
				ALTER TABLE dashboard_widgets ADD CONSTRAINT dashboard_widgets_size_check CHECK (size IN ('half', 'full'));
			END IF;
		END $$`)
		db.Exec(`ALTER TABLE dashboard_widgets ENABLE ROW LEVEL SECURITY`)

		// User groups (migration 000031) — boot guard. Mirrors
		// migrations/000031_user_groups.up.sql exactly. Named org-scoped member
		// groups; first used by granular report sharing.
		db.Exec(`CREATE TABLE IF NOT EXISTS user_groups (
			id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			name        VARCHAR(120) NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at  TIMESTAMPTZ
		)`)
		db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uix_user_groups_org_name ON user_groups(org_id, lower(name)) WHERE deleted_at IS NULL`)
		db.Exec(`CREATE TABLE IF NOT EXISTS user_group_members (
			group_id   UUID NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
			user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (group_id, user_id)
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_user_group_members_user ON user_group_members(user_id)`)
		db.Exec(`ALTER TABLE user_groups ENABLE ROW LEVEL SECURITY`)
		db.Exec(`ALTER TABLE user_group_members ENABLE ROW LEVEL SECURITY`)

		// Report shares (migration 000032) — boot guard. Mirrors
		// migrations/000032_report_shares.up.sql exactly. Granular sharing of a
		// report with a user/role/group at a view/comment/edit level.
		db.Exec(`CREATE TABLE IF NOT EXISTS report_shares (
			id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			report_id   UUID NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
			target_type VARCHAR(10) NOT NULL,
			target_id   UUID NOT NULL,
			level       VARCHAR(10) NOT NULL DEFAULT 'view',
			created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
		db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uix_report_shares_target ON report_shares(report_id, target_type, target_id)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_report_shares_report ON report_shares(report_id)`)
		db.Exec(`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'report_shares_target_type_check') THEN
				ALTER TABLE report_shares ADD CONSTRAINT report_shares_target_type_check CHECK (target_type IN ('user','role','group'));
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'report_shares_level_check') THEN
				ALTER TABLE report_shares ADD CONSTRAINT report_shares_level_check CHECK (level IN ('view','comment','edit'));
			END IF;
		END $$`)
		db.Exec(`ALTER TABLE report_shares ENABLE ROW LEVEL SECURITY`)

		// Report comments (migration 000033) — boot guard. Mirrors
		// migrations/000033_report_comments.up.sql exactly. A comment thread on a
		// saved report; read = can see the report, post = level >= comment, delete
		// = author or manage (enforced in the usecase).
		db.Exec(`CREATE TABLE IF NOT EXISTS report_comments (
			id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			report_id  UUID NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
			author_id  UUID REFERENCES users(id) ON DELETE SET NULL,
			body       TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_report_comments_report ON report_comments(report_id) WHERE deleted_at IS NULL`)
		db.Exec(`ALTER TABLE report_comments ENABLE ROW LEVEL SECURITY`)

		// Authorization hardening (P10 P0) — boot guard. Mirrors
		// migrations/000034_authz_p0_hardening.up.sql. roles gain is_owner
		// (enforcement reads the flag via domain.IsOwnerRole, never the name
		// string) plus the P6 metadata columns whose DDL lands early
		// (description/template_key/seeded_from_role_id); users gain
		// default_org_id (R2 default workspace) and a LOWER(email) index (P2
		// case-insensitive lookup); password_reset_tokens gain initiated_by
		// (admin-sent links); org_invitations gain resend/revoke stamps.
		// Integrity guards follow the log-and-skip rule: report offenders at
		// ERROR and skip the constraint — never crash boot on tenant data.
		db.Exec(`ALTER TABLE roles ADD COLUMN IF NOT EXISTS is_owner BOOLEAN NOT NULL DEFAULT FALSE`)
		db.Exec(`ALTER TABLE roles ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT ''`)
		db.Exec(`ALTER TABLE roles ADD COLUMN IF NOT EXISTS template_key VARCHAR(40)`)
		db.Exec(`ALTER TABLE roles ADD COLUMN IF NOT EXISTS seeded_from_role_id UUID REFERENCES roles(id) ON DELETE SET NULL`)
		db.Exec(`UPDATE roles SET is_owner = TRUE WHERE name = 'owner' AND is_system = TRUE AND is_owner = FALSE`)
		db.Exec(`UPDATE roles SET template_key = name WHERE is_system = TRUE AND template_key IS NULL`)

		// Role-name uniqueness: the permission caches are keyed by role name
		// until the P5 id re-key, so duplicate names silently merge grants.
		var dupNames int64
		db.Raw(`SELECT COUNT(*) FROM (
			SELECT name FROM roles WHERE org_id IS NULL GROUP BY name HAVING COUNT(*) > 1
			UNION ALL
			SELECT name FROM roles WHERE org_id IS NOT NULL GROUP BY org_id, name HAVING COUNT(*) > 1
		) d`).Scan(&dupNames)
		if dupNames > 0 {
			log.Error("roles: duplicate role names exist — uniqueness indexes skipped; merge the duplicates manually", zap.Int64("names", dupNames))
		} else {
			if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uq_roles_global_name ON roles(name) WHERE org_id IS NULL`).Error; err != nil {
				log.Error("roles: failed to create uq_roles_global_name", zap.Error(err))
			}
			if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uq_roles_org_name ON roles(org_id, name) WHERE org_id IS NOT NULL`).Error; err != nil {
				log.Error("roles: failed to create uq_roles_org_name", zap.Error(err))
			}
		}

		// At most one owner role per org scope (the global system owner is the
		// org_id IS NULL singleton).
		var dupOwners int64
		db.Raw(`SELECT COUNT(*) FROM (SELECT org_id FROM roles WHERE is_owner AND org_id IS NOT NULL GROUP BY org_id HAVING COUNT(*) > 1) d`).Scan(&dupOwners)
		if dupOwners > 0 {
			log.Error("roles: orgs with multiple is_owner roles exist — uq_roles_one_owner skipped", zap.Int64("orgs", dupOwners))
		} else if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uq_roles_one_owner ON roles(org_id) WHERE is_owner AND org_id IS NOT NULL`).Error; err != nil {
			log.Error("roles: failed to create uq_roles_one_owner", zap.Error(err))
		}

		// Shadow guard: a non-system role named 'owner' would hit the name-keyed
		// owner bypass (until P5) — forbid at the DB, not just in role_usecase.
		// NOT VALID protects new rows immediately; VALIDATE only when clean.
		db.Exec(`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'roles_no_owner_shadow') THEN
				ALTER TABLE roles ADD CONSTRAINT roles_no_owner_shadow CHECK (is_system OR name <> 'owner') NOT VALID;
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'roles_owner_lineage') THEN
				ALTER TABLE roles ADD CONSTRAINT roles_owner_lineage CHECK (NOT is_owner OR template_key = 'owner') NOT VALID;
			END IF;
		END $$`)
		var shadowRows int64
		db.Raw(`SELECT COUNT(*) FROM roles WHERE NOT is_system AND name = 'owner'`).Scan(&shadowRows)
		if shadowRows > 0 {
			log.Error("roles: org-scoped roles named 'owner' exist — roles_no_owner_shadow left NOT VALID; rename them", zap.Int64("rows", shadowRows))
		} else {
			db.Exec(`ALTER TABLE roles VALIDATE CONSTRAINT roles_no_owner_shadow`)
		}
		var lineageRows int64
		db.Raw(`SELECT COUNT(*) FROM roles WHERE is_owner AND template_key IS DISTINCT FROM 'owner'`).Scan(&lineageRows)
		if lineageRows > 0 {
			log.Error("roles: is_owner rows without owner lineage exist — roles_owner_lineage left NOT VALID", zap.Int64("rows", lineageRows))
		} else {
			db.Exec(`ALTER TABLE roles VALIDATE CONSTRAINT roles_owner_lineage`)
		}

		db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS default_org_id UUID REFERENCES organizations(id) ON DELETE SET NULL`)
		// LOWER(email) index for the P2 case-insensitive lookup, promoted to UNIQUE
		// (P9) so the DB — not just normalizeEmail — forbids casing-forked accounts.
		// uniqueEmailIndex confirms a UNIQUE index by that name actually exists (checks
		// pg_index.indisunique, not just the name, so a mis-created non-unique index of
		// the same name can't masquerade as the constraint). The whole block is
		// WORK-idempotent: once the unique index exists it is the canonical index and
		// the interim non-unique one is dropped and never rebuilt — a restart does the
		// cheap catalog check + a no-op DROP IF EXISTS, not a fresh index build.
		const uniqueEmailIndex = `SELECT EXISTS(SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid WHERE c.relname = 'uq_users_email_lower' AND i.indisunique)`
		var hasUniqueEmail bool
		db.Raw(uniqueEmailIndex).Scan(&hasUniqueEmail)
		if hasUniqueEmail {
			// Steady state: the unique index is canonical; clean up the interim
			// non-unique index if a prior boot was interrupted before dropping it.
			db.Exec(`DROP INDEX IF EXISTS idx_users_email_lower`)
		} else {
			// Interim: a non-unique index keeps the lookup fast while the promotion is
			// pending (e.g. blocked on case-dupes below).
			db.Exec(`CREATE INDEX IF NOT EXISTS idx_users_email_lower ON users(LOWER(email))`)
			var emailDups int64
			db.Raw(`SELECT COUNT(*) FROM (SELECT LOWER(email) FROM users GROUP BY LOWER(email) HAVING COUNT(*) > 1) d`).Scan(&emailDups)
			if emailDups > 0 {
				log.Error("users: case-insensitive duplicate emails exist — UNIQUE email index skipped; merge them so the P2 case-insensitive lookup can't fork one human into two accounts", zap.Int64("emails", emailDups))
			} else if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uq_users_email_lower ON users(LOWER(email))`).Error; err != nil {
				log.Error("users: failed to create uq_users_email_lower", zap.Error(err))
			} else {
				// Drop the redundant non-unique index only after the UNIQUE one is
				// confirmed present, so a swallowed CREATE error can never leave the
				// column index-less.
				db.Raw(uniqueEmailIndex).Scan(&hasUniqueEmail)
				if hasUniqueEmail {
					db.Exec(`DROP INDEX IF EXISTS idx_users_email_lower`)
				}
			}
		}

		db.Exec(`ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS initiated_by UUID REFERENCES users(id) ON DELETE SET NULL`)
		db.Exec(`DELETE FROM password_reset_tokens WHERE expires_at < NOW() - INTERVAL '30 days'`)

		db.Exec(`ALTER TABLE org_invitations ADD COLUMN IF NOT EXISTS resent_at TIMESTAMPTZ`)
		db.Exec(`ALTER TABLE org_invitations ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ`)

		// One-time sweep: report shares whose role/group target no longer exists
		// (report_shares.target_id has no FK; DeleteRole now cleans up in-tx).
		if res := db.Exec(`DELETE FROM report_shares WHERE target_type = 'role' AND NOT EXISTS (SELECT 1 FROM roles r WHERE r.id = report_shares.target_id)`); res.RowsAffected > 0 {
			log.Info("report_shares: swept orphaned role-targeted shares", zap.Int64("rows", res.RowsAffected))
		}
		if res := db.Exec(`DELETE FROM report_shares WHERE target_type = 'group' AND NOT EXISTS (SELECT 1 FROM user_groups g WHERE g.id = report_shares.target_id)`); res.RowsAffected > 0 {
			log.Info("report_shares: swept orphaned group-targeted shares", zap.Int64("rows", res.RowsAffected))
		}

		// In-app notifications (A6, migration 000036) — boot guard. Mirrors
		// migrations/000036_notifications.up.sql exactly. A platform table (not
		// automation-owned): produced by automation notify_user actions today,
		// consumed app-wide by the header bell. No soft-delete — a 90-day sweep
		// hard-deletes stale rows. The partial unread index keeps the bell's
		// unread-count query cheap. Keep both files in sync.
		db.Exec(`CREATE TABLE IF NOT EXISTS notifications (
			id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			type        VARCHAR(50) NOT NULL DEFAULT 'automation',
			title       VARCHAR(255) NOT NULL,
			body        TEXT NOT NULL DEFAULT '',
			link        VARCHAR(1024) NOT NULL DEFAULT '',
			entity_type VARCHAR(64) NOT NULL DEFAULT '',
			entity_id   UUID,
			read_at     TIMESTAMPTZ,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_notifications_inbox ON notifications(user_id, org_id, created_at DESC)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_notifications_unread ON notifications(user_id, org_id) WHERE read_at IS NULL`)
		db.Exec(`ALTER TABLE notifications ENABLE ROW LEVEL SECURITY`)

		log.Info("Seeding system roles...")
		// SeedSystemRoles is also the idempotent boot backfill for new capability
		// codes on existing installs: it inserts any DefaultRoleCapabilities row that
		// is missing for a system role. So the P7 `analytics.view` grant lands on the
		// existing admin+manager system roles here (org_id NULL). Pre-existing CUSTOM
		// role clones don't get it (release-noted) — an admin re-clones or grants it.
		if err := repository.SeedSystemRoles(db); err != nil {
			log.Error("Failed to seed system roles", zap.Error(err))
		}

		// Built-in role descriptions (U3) — boot guard. The seeder stamps them on
		// create; this backfills installs whose system roles predate descriptions,
		// touching ONLY still-empty rows so it stays idempotent.
		for name, desc := range repository.SystemRoleDescriptions {
			if err := db.Exec(`UPDATE roles SET description = ? WHERE is_system = TRUE AND name = ? AND description = ''`, desc, name).Error; err != nil {
				log.Error("Failed to backfill system role description", zap.String("role", name), zap.Error(err))
			}
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

		// Mailer is built before the auth usecase now that account recovery
		// (P1) sends reset/verification/alert emails from it.
		//
		// Fail-closed environment handling (P10 P1): anything outside the
		// explicit development/test allowlist is treated as production — the old
		// `appEnv == "production"` string match failed OPEN when APP_ENV was
		// unset (which it is on prod today). A production-like boot without a
		// mail provider REFUSES to start unless MAIL_DISABLED=true explicitly
		// accepts running without email: silently degrading to LogMailer meant
		// password-reset links went nowhere while users waited.
		appEnv := cfg.AppEnv
		devLike := appEnv == "development" || appEnv == "test"
		var mailerSvc domain.Mailer
		switch {
		case cfg.ResendAPIKey != "":
			mailerSvc = mailer.NewResendMailer(cfg.ResendAPIKey, cfg.MailFrom)
		case devLike || cfg.MailDisabled:
			if cfg.MailDisabled && !devLike {
				log.Warn("MAIL_DISABLED=true: email delivery is off — password reset, invites, and security alerts will only be logged (redacted)")
			}
			mailerSvc = mailer.NewLogMailer()
		default:
			log.Fatal("RESEND_API_KEY is not set and this is a production-like environment (APP_ENV is not development/test): refusing to boot, because account-recovery emails would silently go nowhere. Fix one of: set RESEND_API_KEY; set APP_ENV=development for local dev; or set MAIL_DISABLED=true to explicitly run without email.")
		}

		authUseCase := usecase.NewAuthUseCase(authRepo, stageRepo, cfg, mailerSvc, appEnv, redisClient)
		authHandler := delivery.NewAuthHandler(authUseCase, cfg)

		// Session evictor (P10 P0): membership/role mutations delete the target's
		// per-(user, org) session-cache entry so suspend/remove/demote apply on
		// the next request instead of after the 5-minute cache TTL.
		sessionEvictor := &usecase.RedisSessionEvictor{Client: redisClient}

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
		// authRepo is also the AuthEventWriter (P4): OLS/FLS grid edits are audited
		// to auth_events alongside member/role changes.
		permissionUC := usecase.NewPermissionUseCase(permissionRepo, objectRegistryUC, authRepo)
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
		// Record shares (P3, I2): the escape hatch that grants an 'own'-scoped role
		// access to specific records. Ownership is enforced by reusing the
		// scope-aware RecordService.Get inside the usecase.
		shareRepo := repository.NewRecordShareRepository(db)
		shareUC := usecase.NewShareUseCase(recordService, shareRepo, authRepo, permissionUC)
		// The extra deps (registry, layouts, authorizer, tags, shares) feed the
		// composite record-page endpoint + the share controls.
		recordHandler := delivery.NewRecordHandler(recordService, relatedListsUC, objectRegistryUC, layoutUC, permissionUC, tagUseCase, shareUC)

		// Custom role management (P3): CRUD + capability editing, gated on
		// roles.manage. The fanout busts the layout cache alongside the OLS/FLS
		// cache (the layout role-map is name-keyed until P5), and the evictor
		// refreshes members' sessions on rename/rescope (P10 P0).
		roleRepo := repository.NewRoleRepository(db)
		roleUC := usecase.NewRoleUseCase(roleRepo,
			&usecase.PermissionCacheFanout{Perm: permissionUC, Layouts: layoutUC},
			authRepo, sessionEvictor)
		roleHandler := delivery.NewRoleHandler(roleUC)
		// Role detail "effective access" (U3): merges role identity (roleUC), the
		// OLS/FLS view (permissionUC), and layout assignments (layoutRepo) into the
		// GET /api/roles/:id/access payload.
		roleAccessHandler := delivery.NewRoleAccessHandler(roleUC, permissionUC, layoutRepo)

		// Workspace/member management. Built here (after permissionUC + roleRepo) so
		// it can enforce escalation guard #2 (P6): permissionUC checks the caller's
		// capabilities and roleRepo reads the target role's, so assigning/inviting
		// into a role that can manage roles/members requires the caller's roles.manage.
		workspaceUseCase := usecase.NewWorkspaceUseCase(authRepo, mailerSvc, appEnv, cfg.FrontendURL, sessionEvictor, permissionUC, roleRepo,
			repository.NewOffboardRepository(db))
		workspaceHandler := delivery.NewWorkspaceHandler(workspaceUseCase, authUseCase, cfg)

		// Admin + auth audit log (P4): read-only view over the append-only
		// auth_events written by the auth/admin usecases.
		auditUC := usecase.NewAuditUseCase(authRepo)
		auditHandler := delivery.NewAuditHandler(auditUC)

		// Reports (P9): saved report definitions + a stateless runner that
		// re-executes per viewer. permissionUC serves as both the OLS/FLS
		// authorizer and the capability checker (reports.manage oversight).
		reportRepo := repository.NewReportRepository(db)
		reportRunner := repository.NewReportRunnerRepository(db)
		reportShareRepo := repository.NewReportShareRepository(db)
		reportUC := usecase.NewReportUseCase(reportRepo, reportRunner, objectRegistryRepo, permissionUC, permissionUC, reportShareRepo)
		reportHandler := delivery.NewReportHandler(reportUC)
		// Granular report sharing (users/roles/groups × view/comment/edit).
		reportShareUC := usecase.NewReportShareUseCase(reportUC, reportShareRepo)
		reportShareHandler := delivery.NewReportShareHandler(reportShareUC)
		// Report comment thread — reuses reportUC.ResolveAccess to gate read/post/delete.
		reportCommentRepo := repository.NewReportCommentRepository(db)
		reportCommentUC := usecase.NewReportCommentUseCase(reportUC, reportCommentRepo)
		reportCommentHandler := delivery.NewReportCommentHandler(reportCommentUC)

		// Dashboard widgets (P9 Phase B): per-user pinned reports on the home page.
		// Uses reportUC so shared reports (not just own/org) resolve for the dashboard.
		dashboardUC := usecase.NewDashboardUseCase(repository.NewDashboardWidgetRepository(db), reportUC)
		dashboardHandler := delivery.NewDashboardHandler(dashboardUC)

		// User groups: named member groups, first used as a report-sharing target.
		userGroupRepo := repository.NewUserGroupRepository(db)
		userGroupUC := usecase.NewUserGroupUseCase(userGroupRepo)
		userGroupHandler := delivery.NewUserGroupHandler(userGroupUC)

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

		// permissionUC binds the AI to the unified capability + OLS/FLS stack (P7):
		// the AI now enforces the same object/field/own-scope rules and the
		// analytics.view forecast gate as REST, instead of a parallel role-name RBAC.
		commandCenter := ai.NewCommandCenter(gateway, kbBuilder, contactRepo, dealRepo, taskRepo, activityRepo, chatSessionRepo, customObjUC, permissionUC, log)
		commandHandler := delivery.NewCommandHandler(commandCenter)

		eventsHandler := delivery.NewEventsHandler(redisClient)

		// In-app notifications (A6): the inbox usecase publishes each new
		// notification on the recipient's per-user SSE channel (redisClient), which
		// eventsHandler.Stream now subscribes alongside the org channel. Nil-safe
		// without Redis (the row still lands in the inbox, just no live push).
		notificationRepo := repository.NewNotificationRepository(db)
		notificationUC := usecase.NewNotificationUseCase(notificationRepo, redisClient)
		notificationHandler := delivery.NewNotificationHandler(notificationUC)
		// 90-day retention sweep: hard-delete stale notifications daily (and once at
		// boot). Best-effort background loop; a failed pass just retries next tick.
		go func() {
			sweep := func() {
				if n, err := notificationUC.SweepOld(context.Background()); err != nil {
					log.Warn("notifications: retention sweep failed", zap.Error(err))
				} else if n > 0 {
					log.Info("notifications: retention sweep", zap.Int64("deleted", n))
				}
			}
			sweep()
			ticker := time.NewTicker(24 * time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				sweep()
			}
		}()

		voiceNoteRepo := repository.NewVoiceNoteRepository(db)
		voiceNoteUC := usecase.NewVoiceNoteUseCase(voiceNoteRepo, aiJobQueue, cfg, contactRepo)
		voiceHandler := delivery.NewVoiceHandler(voiceNoteUC)

		delivery.RegisterRoutes(router, authHandler, contactHandler, companyHandler, tagHandler, dealHandler, pipelineHandler, activityHandler, taskHandler, userHandler, aiHandler, settingsHandler, customObjHandler, objectRegistryHandler, recordHandler, permissionHandler, searchHandler, kbHandler, commandHandler, eventsHandler, workspaceHandler, chatSessionHandler, voiceHandler, layoutHandler, roleHandler, roleAccessHandler, auditHandler, reportHandler, reportShareHandler, reportCommentHandler, dashboardHandler, userGroupHandler, notificationHandler, cfg, db, redisClient, authRepo, permissionUC)

		// --- Workflow Automation Engine ---
		memHandler := logger.NewMemoryHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
		autoLogger := slog.New(memHandler)
		slog.SetDefault(autoLogger)
		// Run-as-creator actor model (P8, §3.5): the engine resolves each workflow's
		// author into a full Caller (callerResolver) and enforces the SAME OLS/FLS +
		// audit chokepoint REST/AI use (permissionUC) as that author — so an own-scope
		// role holding workflows.manage can no longer escalate past its OLS by
		// authoring a workflow, and automation writes are attributed in the audit trail.
		autoEngine = automation.NewEngine(db, autoLogger,
			automation.WithWorkers(5),
			automation.WithEmailExecutor(cfg.ResendAPIKey, cfg.MailFrom),
			// notify_user (A6): writes through the platform NotificationUseCase, which
			// inserts the inbox row and pushes it over the recipient's per-user SSE
			// channel so the header bell updates in real time.
			automation.WithNotificationExecutor(notificationUC),
			// create_record (A6): writes through RecordService, so the create gets the
			// same uniform validation + OLS/FLS (as the workflow author) + audit +
			// {slug}_created event as REST/AI.
			automation.WithCreateRecordExecutor(recordService),
			// find_records + enroll_records (A6): read through RecordService (OLS/FLS as
			// the author); enroll creates runs in a target workflow via the engine.
			automation.WithRecordActions(recordService),
			// ai_generate (A7): bounded AI text generation into the action output,
			// attributed to the workflow author under the org's AI budget.
			automation.WithAIGenerator(usecase.NewAIWorkflowGenerator(gateway)),
			automation.WithAuthorizer(permissionUC),
			automation.WithCallerResolver(usecase.NewCallerResolver(authRepo)),
		)
		autoEngine.Start()
		// capChecker is REQUIRED (P8): Run Now / Retry authorize on workflows.run_any
		// with no role-name fallback. authz (permissionUC) stamps the webhook-inbound
		// contact upsert audit as the system actor.
		autoHandler := automation.NewHandler(autoEngine, db, autoLogger, permissionUC, permissionUC)
		// AI copilot (A7): the NL→draft endpoint runs a tool loop on the shared AI
		// gateway (never saves; the client applies the draft through the same zod
		// validation as a manual edit).
		autoHandler.SetDraftAI(gateway)
		autoHandler.RegisterRoutes(router,
			delivery.AuthMiddleware(cfg.JWTSecret, authRepo, redisClient),
			func(code string) gin.HandlerFunc { return delivery.RequireCapability(permissionUC, code) },
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
