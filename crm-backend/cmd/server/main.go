package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	delivery "crm-backend/internal/delivery/http"
	"crm-backend/internal/ai"
	"crm-backend/internal/automation"
	"crm-backend/internal/domain"
	"crm-backend/internal/integrations"
	"crm-backend/internal/integrations/envelope"
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

	// Provider credential encryption (L5). Resolved HERE, immediately after the
	// config load, and deliberately not beside the integrations wiring ~1700
	// lines below: the HTTP server and /health start in a goroutine a few dozen
	// lines down, so a fatal raised late crash-loops AFTER the platform has
	// already recorded a healthy deploy — which is the shape that turns a
	// missing variable into a silent rollback nobody attributes to it.
	integrationCodec := buildIntegrationCodec(cfg, log)

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

	// Proxy trust. gin's default is ForwardedByClientIP with EVERY proxy trusted,
	// which makes c.ClientIP() return the LEFTMOST X-Forwarded-For entry — a value
	// the client types. Every limiter keyed on ClientIP() is then keyed on
	// attacker-chosen data: a caller rotating the XFF header mints a fresh bucket
	// per request and is never throttled. That silently defeats the auth limiter and
	// the public lead-capture limiter alike.
	//
	// TRUSTED_PROXIES is the operator's list of edge CIDRs to believe (Railway/
	// Cloudflare). Unset — the safe default — trusts NOTHING, so ClientIP() falls
	// back to the real peer address, which cannot be forged. Behind an edge that
	// peer is the edge itself, so all callers share one bucket: coarse, but a real
	// bound rather than a decorative one. Set TRUSTED_PROXIES in prod to get
	// per-client limiting back.
	if raw := strings.TrimSpace(cfg.TrustedProxies); raw != "" {
		var cidrs []string
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cidrs = append(cidrs, p)
			}
		}
		if err := router.SetTrustedProxies(cidrs); err != nil {
			log.Fatal("invalid TRUSTED_PROXIES", zap.Error(err))
		}
		log.Info("Trusted proxies configured", zap.Strings("cidrs", cidrs))
	} else if err := router.SetTrustedProxies(nil); err != nil {
		log.Fatal("failed to clear trusted proxies", zap.Error(err))
	}
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
		// An inbound X-Request-ID is honored so a request can be traced across a proxy
		// or a caller's own tracing — but it is client-controlled and gets reflected
		// into the response header AND every log line, so it is validated rather than
		// trusted: anything that is not a short, plain [A-Za-z0-9._-] token is replaced
		// with one we generate. Unbounded, arbitrary bytes here is how you get log
		// injection and a header-splitting attempt for free.
		reqID := sanitizeRequestID(c.GetHeader("X-Request-ID"))
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

	globalCORS := cors.New(cors.Config{
		AllowOrigins: delivery.AllowedOrigins(cfg.FrontendURL),
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders: []string{"Origin", "Content-Type", "Accept", "Authorization", "X-CSRF-Token", "X-Request-ID"},
		// X-Request-ID is EXPOSED (U7.4). The middleware above has always stamped it on
		// every response and logged it with zap — but a browser can only read a response
		// header the server explicitly exposes, so until now the id existed on both ends
		// of a failed request and was invisible to the one person who could quote it.
		// The SPA reads it off failures and prints it in the error banner, which is what
		// turns "I can't save" into a line the logs can actually be grepped for.
		ExposeHeaders:    []string{"Content-Length", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	})

	// Form embeds (L4) run on the CUSTOMER'S origin, which is by definition not in
	// AllowedOrigins — so the global handler would AbortWithStatus(403) them before
	// the route ever ran. They therefore get their own credentials-FREE handler,
	// mounted per-route in the integrations module.
	//
	// A SKIP, not a widening, and the difference is the whole security story. The
	// global config sets AllowCredentials: true, and gin-contrib/cors bakes
	// "Access-Control-Allow-Credentials: true" into its header maps at CONSTRUCTION
	// — there is no per-request way to turn it off. So widening this config to admit
	// customer origins (via AllowOriginWithContextFunc, which is additive-only and
	// receives no path context) would tell those origins they may send the victim's
	// ambient cookies to this API and READ THE RESPONSE — a cross-origin read of
	// authenticated CRM data on every route in the app. Echoing an arbitrary origin
	// is exactly the form that is dangerous; a bare "*" cannot do this, because
	// browsers reject "*" with credentials.
	//
	// `return` is a pass-through: gin's Next() loop advances when a handler returns.
	router.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, integrations.FormCapturePrefix) {
			return
		}
		globalCORS(c)
	})

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
		// Workspace defaults (U4) — golang-migrate is dead on prod, so boot-guard these.
		db.Exec(`ALTER TABLE organizations ADD COLUMN IF NOT EXISTS currency VARCHAR(8) NOT NULL DEFAULT ''`)
		db.Exec(`ALTER TABLE organizations ADD COLUMN IF NOT EXISTS locale VARCHAR(16) NOT NULL DEFAULT ''`)
		db.Exec(`ALTER TABLE organizations ADD COLUMN IF NOT EXISTS timezone VARCHAR(64) NOT NULL DEFAULT ''`)
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
		// Team data scope (U6.1, migration 000040). The constraint is DROPPED and
		// re-ADDED rather than guarded with IF NOT EXISTS: the old guard would find
		// roles_data_scope_check already present and skip, leaving IN ('own','all') in
		// force — so every team-scope write would fail at the DB layer, on prod only.
		// DROP + ADD is idempotent and re-runs safely on every boot.
		db.Exec(`ALTER TABLE roles DROP CONSTRAINT IF EXISTS roles_data_scope_check`)
		db.Exec(`ALTER TABLE roles ADD CONSTRAINT roles_data_scope_check CHECK (data_scope IN ('own', 'team', 'all'))`)
		db.Exec(`UPDATE roles SET data_scope = 'own' WHERE name = 'sales_rep' AND is_system = TRUE`)
		// Teams ARE user_groups (no second entity); the lead is display metadata.
		db.Exec(`ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS lead_user_id UUID REFERENCES users(id) ON DELETE SET NULL`)
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

		// Notification preferences (U5, migration 000037) — boot guard. Mirrors
		// migrations/000037_notification_preferences.up.sql exactly. One row per
		// (org_id, user_id): mute-all, email-digest mode, and a sparse jsonb of
		// per-event-type {in_app,email} overrides (absent keys use built-in defaults).
		// Gates in-app + email delivery in NotificationUseCase.Create and feeds the
		// daily-digest job. Keep both files in sync.
		db.Exec(`CREATE TABLE IF NOT EXISTS notification_preferences (
			id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id               UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			user_id              UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			mute_all             BOOLEAN NOT NULL DEFAULT FALSE,
			email_digest         VARCHAR(16) NOT NULL DEFAULT 'off',
			overrides            JSONB NOT NULL DEFAULT '{}',
			last_digest_sent_at  TIMESTAMPTZ,
			created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (org_id, user_id)
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_notif_prefs_digest ON notification_preferences(email_digest) WHERE email_digest = 'daily'`)
		db.Exec(`ALTER TABLE notification_preferences ENABLE ROW LEVEL SECURITY`)

		// U5 also adds two columns to the A6 notifications table (part of migration
		// 000037): digest_only (true = a row stored ONLY to be digested, hidden from
		// the bell) and digested_at (the daily-digest idempotency marker). ADD COLUMN
		// IF NOT EXISTS so this is safe on installs that already have the A6 table.
		db.Exec(`ALTER TABLE notifications ADD COLUMN IF NOT EXISTS digest_only BOOLEAN NOT NULL DEFAULT FALSE`)
		db.Exec(`ALTER TABLE notifications ADD COLUMN IF NOT EXISTS digested_at TIMESTAMPTZ`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_notifications_digest_pending ON notifications(user_id, org_id) WHERE read_at IS NULL AND digested_at IS NULL`)

		// Record-sharing parity (U6.2, migration 000038) — boot guard. Mirrors
		// migrations/000038_record_shares_parity.up.sql exactly. record_shares predates
		// report_shares and never caught up: user-only grantee, no org_id, no
		// uniqueness, and a level that could never change after the first grant. This
		// brings it to the report_shares model (target_type/target_id over
		// user|role|group at level view|edit) so ONE predicate — RecordAccessPredicate
		// — serves both. Keep both files in sync.
		db.Exec(`ALTER TABLE record_shares ADD COLUMN IF NOT EXISTS org_id UUID`)
		db.Exec(`ALTER TABLE record_shares ADD COLUMN IF NOT EXISTS target_type VARCHAR(10) NOT NULL DEFAULT 'user'`)
		db.Exec(`ALTER TABLE record_shares ADD COLUMN IF NOT EXISTS target_id UUID`)
		db.Exec(`UPDATE record_shares SET target_id = grantee_user_id WHERE target_id IS NULL AND grantee_user_id IS NOT NULL`)
		db.Exec(`UPDATE record_shares rs SET org_id = c.org_id FROM contacts c WHERE rs.org_id IS NULL AND rs.record_type = 'contact' AND rs.record_id = c.id`)
		db.Exec(`UPDATE record_shares rs SET org_id = d.org_id FROM deals d WHERE rs.org_id IS NULL AND rs.record_type = 'deal' AND rs.record_id = d.id`)
		db.Exec(`UPDATE record_shares rs SET org_id = cor.org_id FROM custom_object_records cor WHERE rs.org_id IS NULL AND rs.record_id = cor.id`)
		// Orphans (the shared record was hard-deleted): a NULL org_id defeats every
		// org-scoped query below, so drop the row rather than keep an unfilterable one.
		if res := db.Exec(`DELETE FROM record_shares WHERE org_id IS NULL OR target_id IS NULL`); res.RowsAffected > 0 {
			log.Info("record_shares: dropped orphaned shares (record no longer exists)", zap.Int64("rows", res.RowsAffected))
		}
		// 'read' → 'view' (the shared level ladder). Safe: no predicate compares
		// 'read' — only 'edit' is ever compared — so no query changes meaning.
		db.Exec(`UPDATE record_shares SET permission_level = 'view' WHERE permission_level = 'read'`)
		db.Exec(`UPDATE record_shares SET permission_level = 'view' WHERE permission_level NOT IN ('view', 'edit')`)
		// The table never had a uniqueness constraint, so duplicates may exist and
		// would block the unique index. Keep the newest row per target.
		if res := db.Exec(`DELETE FROM record_shares a USING record_shares b
			WHERE a.ctid < b.ctid
			  AND a.record_type = b.record_type AND a.record_id = b.record_id
			  AND a.target_type = b.target_type AND a.target_id = b.target_id`); res.RowsAffected > 0 {
			log.Info("record_shares: de-duplicated shares before unique index", zap.Int64("rows", res.RowsAffected))
		}
		db.Exec(`ALTER TABLE record_shares ALTER COLUMN target_id SET NOT NULL`)
		db.Exec(`ALTER TABLE record_shares ALTER COLUMN org_id SET NOT NULL`)
		db.Exec(`ALTER TABLE record_shares ALTER COLUMN grantee_user_id DROP NOT NULL`)
		db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uq_record_shares_target ON record_shares(record_type, record_id, target_type, target_id)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_record_shares_record ON record_shares(record_type, record_id)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_record_shares_target ON record_shares(target_type, target_id)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_record_shares_org ON record_shares(org_id)`)
		db.Exec(`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'record_shares_target_type_check') THEN
				ALTER TABLE record_shares ADD CONSTRAINT record_shares_target_type_check CHECK (target_type IN ('user', 'role', 'group'));
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'record_shares_level_check') THEN
				ALTER TABLE record_shares ADD CONSTRAINT record_shares_level_check CHECK (permission_level IN ('view', 'edit'));
			END IF;
		END $$`)
		// The group-share and team-scope predicates both hit user_group_members by
		// group_id; only the by-user index existed.
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_user_group_members_group ON user_group_members(group_id)`)
		db.Exec(`ALTER TABLE record_shares ENABLE ROW LEVEL SECURITY`)

		// Custom-object ownership (U6.3, migration 000039) — boot guard. Mirrors
		// migrations/000039_custom_record_owner.up.sql exactly. Custom records had no
		// owner, so row scope had nothing to filter on: every custom record was visible
		// org-wide to any role that could read the object, and shares written against a
		// custom slug were never read by anything. Keep both files in sync.
		db.Exec(`ALTER TABLE custom_object_records ADD COLUMN IF NOT EXISTS owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_custom_object_records_owner ON custom_object_records(owner_user_id)`)
		// One-time backfill: the creator becomes the owner. A record with no created_by
		// stays unowned and is reachable only by an 'all'-scoped role (fail closed).
		if res := db.Exec(`UPDATE custom_object_records SET owner_user_id = created_by WHERE owner_user_id IS NULL AND created_by IS NOT NULL`); res.RowsAffected > 0 {
			log.Info("custom_object_records: backfilled owner from creator", zap.Int64("rows", res.RowsAffected))
		}

		// Two-factor authentication (U6.4, migration 000041) — boot guard. Mirrors
		// migrations/000041_two_factor.up.sql exactly. totp_secret holds the
		// AES-GCM-ENCRYPTED seed (never the raw one); totp_enabled_at is what makes 2FA
		// ACTIVE, so a setup that was started but never confirmed can't lock anyone out.
		// two_factor_challenges is the half-authenticated state between "password
		// correct" and "code correct". Keep both files in sync.
		db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret TEXT`)
		db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled_at TIMESTAMPTZ`)
		db.Exec(`ALTER TABLE organizations ADD COLUMN IF NOT EXISTS require_two_factor BOOLEAN NOT NULL DEFAULT FALSE`)
		db.Exec(`CREATE TABLE IF NOT EXISTS two_factor_backup_codes (
			id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			code_hash  VARCHAR(255) NOT NULL,
			used_at    TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_2fa_backup_user ON two_factor_backup_codes(user_id) WHERE used_at IS NULL`)
		db.Exec(`ALTER TABLE two_factor_backup_codes ENABLE ROW LEVEL SECURITY`)
		db.Exec(`CREATE TABLE IF NOT EXISTS two_factor_challenges (
			id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash VARCHAR(255) NOT NULL UNIQUE,
			attempts   INT NOT NULL DEFAULT 0,
			expires_at TIMESTAMPTZ NOT NULL,
			used_at    TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_2fa_challenge_expiry ON two_factor_challenges(expires_at)`)
		db.Exec(`ALTER TABLE two_factor_challenges ENABLE ROW LEVEL SECURITY`)

		// Personal API tokens (U6.5, migration 000042) — boot guard. Mirrors
		// migrations/000042_api_tokens.up.sql exactly. The secret is stored as a SHA-256
		// hash (not bcrypt: this is on the hot path of every API request), and the
		// prefix is kept only so the UI can show the user which token is which.
		db.Exec(`CREATE TABLE IF NOT EXISTS api_tokens (
			id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name         VARCHAR(120) NOT NULL,
			token_hash   VARCHAR(64) NOT NULL UNIQUE,
			prefix       VARCHAR(24) NOT NULL,
			scopes       JSONB NOT NULL DEFAULT '[]',
			last_used_at TIMESTAMPTZ,
			expires_at   TIMESTAMPTZ,
			revoked_at   TIMESTAMPTZ,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_id, org_id) WHERE revoked_at IS NULL`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash)`)
		db.Exec(`ALTER TABLE api_tokens ENABLE ROW LEVEL SECURITY`)

		// ── Lead integration platform (L1.1) ─────────────────────────────
		// These guards are the ONLY path that reaches prod (golang-migrate is dirty
		// at v2 and start.sh swallows its failure), so migrations/000043_*.sql is the
		// fresh-install twin of this block — the two must agree.
		//
		// Errors are checked and logged here, against the house style of discarding
		// them: a typo in a guard for a table that does not exist yet means prod
		// boots green and the capture endpoint 500s on the first real lead, with
		// nothing in the logs to say why. Follows the roles-index precedent above.
		leadGuards := []struct {
			what string
			sql  string
		}{
			{"lead_sources", `CREATE TABLE IF NOT EXISTS lead_sources (
				id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				org_id               UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
				kind                 VARCHAR(32) NOT NULL,
				name                 VARCHAR(160) NOT NULL,
				token_hash           VARCHAR(64) UNIQUE,
				token_prefix         VARCHAR(24),
				target_slug          VARCHAR(64) NOT NULL DEFAULT 'contact',
				match_fields         JSONB NOT NULL DEFAULT '["email"]',
				field_map            JSONB NOT NULL DEFAULT '{}',
				update_policy        VARCHAR(24) NOT NULL DEFAULT 'fill_blank_only',
				default_owner_id     UUID REFERENCES users(id) ON DELETE SET NULL,
				config               JSONB NOT NULL DEFAULT '{}',
				status               VARCHAR(16) NOT NULL DEFAULT 'active',
				consecutive_failures INT NOT NULL DEFAULT 0,
				last_used_at         TIMESTAMPTZ,
				daily_cap            INT NOT NULL DEFAULT 0,
				created_by           UUID REFERENCES users(id) ON DELETE SET NULL,
				created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				deleted_at           TIMESTAMPTZ,
				disabled_at          TIMESTAMPTZ
			)`},
			{"lead_sources org index", `CREATE INDEX IF NOT EXISTS idx_lead_sources_org ON lead_sources(org_id) WHERE deleted_at IS NULL`},
			{"integration_events", `CREATE TABLE IF NOT EXISTS integration_events (
				id                 UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				org_id             UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
				source_id          UUID REFERENCES lead_sources(id) ON DELETE SET NULL,
				connection_id      UUID,
				provider_event_id  TEXT,
				status             VARCHAR(16) NOT NULL,
				claimed_at         TIMESTAMPTZ,
				attempts           INT NOT NULL DEFAULT 0,
				raw_payload        JSONB NOT NULL DEFAULT '{}',
				context            JSONB NOT NULL DEFAULT '{}',
				quarantined_fields JSONB NOT NULL DEFAULT '{}',
				result_slug        VARCHAR(64),
				result_record_id   UUID,
				outcome            VARCHAR(16),
				error              TEXT,
				note               TEXT,
				created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				processed_at       TIMESTAMPTZ
			)`},
			// Two dedupe indexes, not one: Postgres treats NULLs as DISTINCT, so a
			// source-scoped index alone never fires for a provider webhook (which has
			// a connection but no source yet) and every retry would duplicate.
			// The table may predate this column on any install that booted an earlier
			// build — CREATE TABLE IF NOT EXISTS never adds columns to an existing table.
			{"events note column", `ALTER TABLE integration_events ADD COLUMN IF NOT EXISTS note TEXT`},
			{"events source dedupe", `CREATE UNIQUE INDEX IF NOT EXISTS idx_integration_events_source_provider
				ON integration_events(source_id, provider_event_id)
				WHERE source_id IS NOT NULL AND provider_event_id IS NOT NULL`},
			{"events connection dedupe", `CREATE UNIQUE INDEX IF NOT EXISTS idx_integration_events_conn_provider
				ON integration_events(connection_id, provider_event_id)
				WHERE connection_id IS NOT NULL AND provider_event_id IS NOT NULL`},
			{"events pending index", `CREATE INDEX IF NOT EXISTS idx_integration_events_pending
				ON integration_events(created_at) WHERE status = 'pending'`},
			{"events source/created index", `CREATE INDEX IF NOT EXISTS idx_integration_events_source_created
				ON integration_events(source_id, created_at)`},
			{"events org/created index", `CREATE INDEX IF NOT EXISTS idx_integration_events_org_created
				ON integration_events(org_id, created_at DESC)`},
			// The org-wide ledger's keyset index (L6.2). A DISTINCT NAME is load-bearing:
			// `CREATE INDEX IF NOT EXISTS` matches on NAME ONLY and never compares the
			// column list, so reusing idx_integration_events_org_created above — whose
			// (org_id, created_at DESC) cannot serve the (created_at, id) tiebreak — would
			// be a silent no-op, and the ledger would seq-scan with nothing to show for it.
			// An INDEX rather than a column, so a guard that never ran degrades to a slow
			// query instead of breaking FinishEvent's wholesale Save.
			{"events org keyset index", `CREATE INDEX IF NOT EXISTS idx_integration_events_org_keyset
				ON integration_events(org_id, created_at DESC, id DESC)`},
			// L6.5b ledger retention. UNMAPPED on the model (like consent and
			// assigned_owner_id) because FinishEvent is a wholesale Save: a mapped
			// redacted_at whose in-memory value is zero would be written back as NULL on
			// the next write to the row, un-marking a delivery whose payload really was
			// erased. A guard that never ran degrades to "the sweep errors and logs" —
			// it is in no capture-path SELECT, so it cannot 500 a lead.
			{"events redacted_at", `ALTER TABLE integration_events
				ADD COLUMN IF NOT EXISTS redacted_at TIMESTAMPTZ`},
			// Serves the sweep and empties itself as the backlog drains. Distinct name
			// for the reason above.
			{"events prunable index", `CREATE INDEX IF NOT EXISTS idx_integration_events_prunable
				ON integration_events(created_at) WHERE redacted_at IS NULL`},
			// NON-unique on purpose. The existing contacts unique index is on raw
			// (org_id, email) and is case-SENSITIVE, so case-variant twins are legal
			// today; a UNIQUE index here would fail to build on any org that has them —
			// silently, leaving prod with no index while local tests pass. Takes an
			// ACCESS EXCLUSIVE lock on contacts for its duration (CONCURRENTLY can't run
			// on the boot path); fine at current size, but it is a write lock on the
			// busiest table.
			{"contacts lower(email) index", `CREATE INDEX IF NOT EXISTS idx_contacts_org_lower_email
				ON contacts(org_id, LOWER(email)) WHERE deleted_at IS NULL`},
			// Phone dedupe: a FUNCTIONAL index on digits rather than a stored column,
			// so there is nothing to backfill (E.164 normalization is not a SQL
			// expression) and it covers existing rows the moment it exists. The
			// expression must stay identical to integrations.normalizePhone or matching
			// silently stops using the index and starts disagreeing with itself.
			//
			// NON-unique, permanently: unlike an email, a shared phone is legitimate
			// (spouses, a switchboard, a recycled number), so uniqueness here would be
			// both unbuildable on real data and an assertion that isn't true.
			{"contacts phone-digits index", `CREATE INDEX IF NOT EXISTS idx_contacts_org_phone_digits
				ON contacts(org_id, regexp_replace(phone, '[^0-9]', '', 'g'))
				WHERE phone IS NOT NULL AND phone <> '' AND deleted_at IS NULL`},

			// L2 owner routing. CREATE TABLE IF NOT EXISTS above never adds columns to a
			// table that already exists, so every install that booted an earlier build
			// needs these ALTERs. Metadata-only on PG11+ (non-volatile DEFAULT), so no
			// table rewrite and none of the lock cost the contacts indexes above carry.
			//
			// A failure here is survivable ONLY because none of these columns is mapped
			// onto a GORM struct: this loop logs and boots on, and an unmapped column
			// that doesn't exist fails one raw statement (routing degrades to
			// default_owner_id). Mapped, GORM would name owner_pool in
			// FindSourceByTokenHash's SELECT and every capture request in every org would
			// 500 — a routing column taking down lead capture platform-wide.
			{"lead_sources owner_pool", `ALTER TABLE lead_sources
				ADD COLUMN IF NOT EXISTS owner_pool JSONB NOT NULL DEFAULT '[]'::jsonb`},
			// A monotonic ticket counter, never an index into the array: an index points
			// out of range the moment the pool shrinks, and every implementation then
			// clamps — the clamp is where the fairness skew hides. BIGINT because a
			// wrapped negative ticket makes `ticket % n` negative and panics on the slice
			// index, and a panic on the capture path is silent lead loss.
			{"lead_sources owner_cursor", `ALTER TABLE lead_sources
				ADD COLUMN IF NOT EXISTS owner_cursor BIGINT NOT NULL DEFAULT 0`},
			// lead_sources is now written up to twice per lead (cursor bump, then
			// TouchSourceUsed). HOT updates keep the token_hash UNIQUE index — probed on
			// EVERY capture request — free of new entries only while the heap page has
			// room, and the default fillfactor of 100 leaves none.
			{"lead_sources fillfactor", `ALTER TABLE lead_sources SET (fillfactor = 90)`},
			// L7.2b: "one webhook_inbound source per org" is an ASSUMPTION everything
			// else here rests on — the resolve-or-create, the ON CONFLICT DO NOTHING,
			// and the delete guard that makes a duplicate permanently undeletable. This
			// index is what makes it true. Without it two concurrent first deliveries
			// both insert, the ledger and the owner rotation split across rows that look
			// identical in the UI, and neither can be removed.
			//
			// A DISTINCT NAME, because CREATE INDEX IF NOT EXISTS matches on name only
			// and never compares columns. Partial on the kind, mirroring
			// uix_lead_sources_conn_form.
			{"lead_sources one legacy webhook per org", `
				CREATE UNIQUE INDEX IF NOT EXISTS uix_lead_sources_org_webhook_inbound
				ON lead_sources (org_id) WHERE kind = 'webhook_inbound' AND deleted_at IS NULL`},
			// L7.2b: one webhook_inbound source per org that already has a legacy
			// automation webhook token, so the Integrations page has something to
			// configure on day one rather than only after the org's next delivery.
			//
			// NOT an ON CONFLICT: there is no unique index to conflict on, deliberately —
			// this kind adds no columns and no indexes, so its whole schema footprint is
			// a new VALUE in an existing varchar (kind has no CHECK constraint, by an
			// explicit decision recorded in integrations/models.go). WHERE NOT EXISTS is
			// what makes it idempotent across boots instead.
			//
			// token_hash is left NULL rather than '': the column is UNIQUE, and an empty
			// string would collide across the second such row in the fleet. It must stay
			// NULL anyway — FindSourceByTokenHash has no kind filter, so a token here
			// would open a second capture-API ingress into the org.
			// Wrapped in a to_regclass check because automation_workflow_org_tokens is
			// created by that package's GORM AutoMigrate, which runs AFTER this block —
			// so on a fresh database the table does not exist yet and a bare INSERT
			// would log an error every first boot. Missing it costs nothing: an org
			// without a token has no legacy webhook, and the first delivery through one
			// creates the row anyway.
			{"lead_sources legacy webhook backfill", `
				DO $$
				BEGIN
					IF to_regclass('public.automation_workflow_org_tokens') IS NOT NULL THEN
						INSERT INTO lead_sources (
							id, org_id, kind, name, target_slug, match_fields, field_map,
							update_policy, config, status, daily_cap, created_at, updated_at)
						SELECT uuid_generate_v4(), t.org_id, 'webhook_inbound', 'Workflow webhook (legacy)',
						       'contact', '["email"]'::jsonb, '{}'::jsonb, 'overwrite', '{}'::jsonb,
						       'active', 0, NOW(), NOW()
						FROM automation_workflow_org_tokens t
						WHERE NOT EXISTS (
							SELECT 1 FROM lead_sources s
							WHERE s.org_id = t.org_id AND s.kind = 'webhook_inbound' AND s.deleted_at IS NULL
						);
					END IF;
				END $$`},
			// Binds the rotation ticket to the DELIVERY, not the attempt. Ingest
			// deliberately re-runs the pipeline against a prior `failed` row on an
			// Idempotency-Key retry, so without this a failure-correlated retry pattern
			// takes a second ticket every time and one rep silently receives everything.
			{"events assigned_owner_id", `ALTER TABLE integration_events
				ADD COLUMN IF NOT EXISTS assigned_owner_id UUID`},
			// L2 batch. Positive polarity defaulting FALSE so the absent value is the
			// safe one: a recovery batch does not enrol 100 contacts into every
			// contact_created workflow unless an admin opted in. Unmapped on the struct
			// for the same reason as owner_pool — see the note on LeadSource.
			{"lead_sources batch_enroll_automation", `ALTER TABLE lead_sources
				ADD COLUMN IF NOT EXISTS batch_enroll_automation BOOLEAN NOT NULL DEFAULT FALSE`},
			// L2 consent envelope. Nullable with no default so NULL (never sent), an
			// object (sent) and a tombstone (erased) stay distinguishable — writing '{}'
			// on erasure would make the ledger falsely assert no consent was obtained.
			//
			// Survivable if it fails ONLY because the column is unmapped: this loop logs
			// and boots on, so a missing column costs one advisory UPDATE (the lead still
			// lands, the caller is warned) instead of appearing in three separate
			// INSERT/UPDATE column lists and taking down capture platform-wide.
			{"events consent column", `ALTER TABLE integration_events
				ADD COLUMN IF NOT EXISTS consent JSONB`},
			// Erasure is contact-keyed and result_record_id was unindexed, so redacting a
			// deleted contact's ledger rows would scan the whole table.
			{"events result index", `CREATE INDEX IF NOT EXISTS idx_integration_events_result
				ON integration_events(org_id, result_record_id) WHERE result_record_id IS NOT NULL`},
			// L3 google_ads. Both UNMAPPED on the struct (see LeadSource): a failed
			// guard here breaks the google_ads route only, never the bearer capture
			// path. public_token is the webhook URL's source identifier (not a secret —
			// the key is); google_key_hash is SHA-256 of the key Google posts in the
			// body. Partial-unique: every non-google row is NULL, and uniqueness is
			// what makes a URL resolve to at most one source.
			{"lead_sources public_token", `ALTER TABLE lead_sources
				ADD COLUMN IF NOT EXISTS public_token VARCHAR(64)`},
			{"lead_sources public_token index", `CREATE UNIQUE INDEX IF NOT EXISTS idx_lead_sources_public_token
				ON lead_sources(public_token) WHERE public_token IS NOT NULL`},
			{"lead_sources google_key_hash", `ALTER TABLE lead_sources
				ADD COLUMN IF NOT EXISTS google_key_hash VARCHAR(64)`},
			// L4 form embeds. allowed_origins is the browser origins a form accepts
			// submissions from — UNMAPPED, and deliberately NOT inside the config blob
			// where the form's other settings live: that parser cannot fail by design,
			// and an allowlist needs "unreadable" to be distinguishable from "empty"
			// because the two must have opposite outcomes. No DEFAULT, so a NULL (guard
			// never ran) reads differently from '[]' (admin allowed nothing yet).
			{"lead_sources allowed_origins", `ALTER TABLE lead_sources
				ADD COLUMN IF NOT EXISTS allowed_origins JSONB`},
			// The PRIVATE half of a Turnstile pair. Sent verbatim to Cloudflare's
			// siteverify, so it cannot be hashed; never serialized to any response —
			// the management API reports only whether one is configured.
			{"lead_sources turnstile_secret", `ALTER TABLE lead_sources
				ADD COLUMN IF NOT EXISTS turnstile_secret TEXT`},

			// ── L5.1 provider connector framework ──────────────────────────
			// Mirrors migrations/000049_lead_provider_connections.up.sql; that
			// file is what a fresh install and the Docker harness run, this is
			// what prod runs. Both must agree.
			{"integration_connections", `CREATE TABLE IF NOT EXISTS integration_connections (
				id                       UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				org_id                   UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
				provider                 VARCHAR(32) NOT NULL,
				external_account_id      VARCHAR(255) NOT NULL,
				external_account_label   VARCHAR(255) NOT NULL DEFAULT '',
				encrypted_credentials    TEXT NOT NULL,
				key_version              INT NOT NULL DEFAULT 0,
				webhook_secret_encrypted TEXT,
				status                   VARCHAR(32) NOT NULL DEFAULT 'connected',
				cursor                   JSONB NOT NULL DEFAULT '{}'::jsonb,
				config                   JSONB NOT NULL DEFAULT '{}'::jsonb,
				last_synced_at           TIMESTAMPTZ,
				last_error               TEXT,
				consecutive_failures     INT NOT NULL DEFAULT 0,
				created_by               UUID,
				created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				deleted_at               TIMESTAMPTZ
			)`},
			{"integration_connections org index", `CREATE INDEX IF NOT EXISTS idx_integration_connections_org
				ON integration_connections(org_id) WHERE deleted_at IS NULL`},
			{"integration_connections account unique", `CREATE UNIQUE INDEX IF NOT EXISTS uix_integration_connections_org_account
				ON integration_connections(org_id, provider, external_account_id)
				WHERE deleted_at IS NULL`},
			// The exclusive page->workspace claim. Releasing it on disconnect is
			// what lets a customer move a page between workspaces; see the
			// migration for why deleted_at is in the predicate.
			{"integration_connections claim", `CREATE UNIQUE INDEX IF NOT EXISTS uix_integration_connections_claim
				ON integration_connections(provider, external_account_id)
				WHERE deleted_at IS NULL AND status IN ('connected', 'degraded', 'error')`},

			{"integration_oauth_states", `CREATE TABLE IF NOT EXISTS integration_oauth_states (
				id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				state_hash    VARCHAR(64) NOT NULL UNIQUE,
				org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
				user_id       UUID NOT NULL,
				provider      VARCHAR(32) NOT NULL,
				return_to     TEXT NOT NULL DEFAULT '',
				code_verifier TEXT,
				key_version   INT NOT NULL DEFAULT 0,
				expires_at    TIMESTAMPTZ NOT NULL,
				consumed_at   TIMESTAMPTZ,
				created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`},
			{"integration_oauth_states expiry index", `CREATE INDEX IF NOT EXISTS idx_integration_oauth_states_expiry
				ON integration_oauth_states(expires_at) WHERE consumed_at IS NULL`},

			{"integration_pending_connections", `CREATE TABLE IF NOT EXISTS integration_pending_connections (
				id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				org_id               UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
				user_id              UUID NOT NULL,
				provider             VARCHAR(32) NOT NULL,
				encrypted_token      TEXT NOT NULL,
				key_version          INT NOT NULL DEFAULT 0,
				candidate_accounts   JSONB NOT NULL DEFAULT '[]'::jsonb,
				selection_token_hash VARCHAR(64) NOT NULL UNIQUE,
				expires_at           TIMESTAMPTZ NOT NULL,
				consumed_at          TIMESTAMPTZ,
				created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`},
			{"integration_pending_connections expiry index", `CREATE INDEX IF NOT EXISTS idx_integration_pending_expiry
				ON integration_pending_connections(expires_at) WHERE consumed_at IS NULL`},

			// Which connection a provider-backed source belongs to. UNMAPPED on
			// the LeadSource struct — the owner_pool rule: FindSourceByTokenHash
			// selects lead_sources.*, so a mapped column whose ALTER failed here
			// would 500 every capture request in every org, while unmapped it
			// breaks only the provider route that reads it explicitly.
			{"lead_sources connection_id", `ALTER TABLE lead_sources
				ADD COLUMN IF NOT EXISTS connection_id UUID`},
			{"lead_sources connection index", `CREATE INDEX IF NOT EXISTS idx_lead_sources_connection
				ON lead_sources(connection_id) WHERE connection_id IS NOT NULL AND deleted_at IS NULL`},
			// One facebook_form source per (connection, form id) — the backstop for the
			// enable-form idempotency race (L5.4). Partial on deleted_at so re-enable is
			// an ordinary insert.
			{"lead_sources conn form unique", `CREATE UNIQUE INDEX IF NOT EXISTS uix_lead_sources_conn_form
				ON lead_sources (connection_id, (config->'facebook'->>'form_id'))
				WHERE kind = 'facebook_form' AND deleted_at IS NULL`},
		}
		for _, g := range leadGuards {
			if err := db.Exec(g.sql).Error; err != nil {
				log.Error("lead integrations boot guard failed", zap.String("what", g.what), zap.Error(err))
			}
		}

		// RLS on, matching every other table this app adds (api_tokens, notifications).
		// The app enforces org scoping in SQL, so this is defence in depth — but a table
		// that opts out silently is the one nobody notices is different. The three L5.1
		// tables are the MOST sensitive of the set (they hold envelope-sealed provider
		// credentials, PKCE verifiers and exchanged tokens), so they least of all should
		// be the ones that silently differ.
		db.Exec(`ALTER TABLE lead_sources ENABLE ROW LEVEL SECURITY`)
		db.Exec(`ALTER TABLE integration_events ENABLE ROW LEVEL SECURITY`)
		db.Exec(`ALTER TABLE integration_connections ENABLE ROW LEVEL SECURITY`)
		db.Exec(`ALTER TABLE integration_oauth_states ENABLE ROW LEVEL SECURITY`)
		db.Exec(`ALTER TABLE integration_pending_connections ENABLE ROW LEVEL SECURITY`)

		// ── Industry starter templates ───────────────────────────────────
		// system_templates and org_settings both originate in migration 000002 —
		// which is the exact version golang-migrate went dirty at — so on prod they
		// may not exist at all. CREATE TABLE IF NOT EXISTS here is load-bearing, not
		// defensive. Verified on the local DB: the table exists with the original
		// nine columns and kb_templates (added by 000006) is MISSING, so the ALTERs
		// below are the only reason a GORM read of SystemTemplate does not explode
		// with "column system_templates.kb_templates does not exist".
		//
		// Ordering matters: every one of these must land before SeedSystemTemplates
		// runs and before the repository first reads the table.
		templateGuards := []struct {
			what string
			sql  string
		}{
			{"system_templates", `CREATE TABLE IF NOT EXISTS system_templates (
				id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				slug              VARCHAR(100) NOT NULL UNIQUE,
				name              VARCHAR(255) NOT NULL,
				pipeline_stages   JSONB NOT NULL DEFAULT '[]'::jsonb,
				custom_field_defs JSONB NOT NULL DEFAULT '[]'::jsonb,
				ai_context        TEXT,
				automation_rules  JSONB NOT NULL DEFAULT '[]'::jsonb,
				created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`},
			// From migration 000006, which never reached the local DB and may not have
			// reached prod. NOT NULL DEFAULT '{}' closes the drift between the GORM tag
			// (which claims a default) and the column (which had none).
			{"system_templates.kb_templates", `ALTER TABLE system_templates
				ADD COLUMN IF NOT EXISTS kb_templates JSONB NOT NULL DEFAULT '{}'::jsonb`},
			// New: custom objects are the highest-value thing a template installs and no
			// existing column could express them.
			{"system_templates.object_defs", `ALTER TABLE system_templates
				ADD COLUMN IF NOT EXISTS object_defs JSONB NOT NULL DEFAULT '[]'::jsonb`},
			// Catalog metadata — a 20+ card picker needs grouping, ordering and prose.
			{"system_templates.category", `ALTER TABLE system_templates
				ADD COLUMN IF NOT EXISTS category VARCHAR(50) NOT NULL DEFAULT 'general'`},
			{"system_templates.description", `ALTER TABLE system_templates
				ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT ''`},
			{"system_templates.icon", `ALTER TABLE system_templates
				ADD COLUMN IF NOT EXISTS icon VARCHAR(16) NOT NULL DEFAULT ''`},
			{"system_templates.sort_order", `ALTER TABLE system_templates
				ADD COLUMN IF NOT EXISTS sort_order INT NOT NULL DEFAULT 100`},
			// Retire by flag, never DELETE: org_settings.industry_template_slug carries a
			// foreign key onto slug, so removing a row would fail or orphan an org.
			{"system_templates.is_active", `ALTER TABLE system_templates
				ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE`},
			// Content revision, recorded on the application ledger. Note the seeder does
			// NOT gate its upsert on this: pre-existing rows back-fill to 1 here, and a
			// gate of "only overwrite when newer" then silently preserved the 2022 rows.
			{"system_templates.spec_version", `ALTER TABLE system_templates
				ADD COLUMN IF NOT EXISTS spec_version INT NOT NULL DEFAULT 1`},
			{"system_templates slug unique", `CREATE UNIQUE INDEX IF NOT EXISTS uix_system_templates_slug
				ON system_templates(slug)`},

			{"org_settings", `CREATE TABLE IF NOT EXISTS org_settings (
				org_id                 UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
				industry_template_slug VARCHAR(100),
				ai_context_override    TEXT,
				custom_field_defs      JSONB NOT NULL DEFAULT '[]'::jsonb,
				onboarding_completed   BOOLEAN NOT NULL DEFAULT FALSE,
				created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`},
			{"org_settings.industry_template_slug", `ALTER TABLE org_settings
				ADD COLUMN IF NOT EXISTS industry_template_slug VARCHAR(100)`},
			{"org_settings.ai_context_override", `ALTER TABLE org_settings
				ADD COLUMN IF NOT EXISTS ai_context_override TEXT`},

			// The application ledger. UNIQUE(org_id, template_slug) is what turns a
			// second apply into a reported no-op instead of a duplicate install.
			{"org_template_applications", `CREATE TABLE IF NOT EXISTS org_template_applications (
				id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
				template_slug VARCHAR(100) NOT NULL,
				spec_version  INT NOT NULL DEFAULT 1,
				status        VARCHAR(32) NOT NULL,
				result        JSONB NOT NULL DEFAULT '{}'::jsonb,
				applied_by    UUID,
				created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`},
			{"org_template_applications unique", `CREATE UNIQUE INDEX IF NOT EXISTS uix_org_template_app
				ON org_template_applications(org_id, template_slug)`},
		}
		for _, g := range templateGuards {
			if err := db.Exec(g.sql).Error; err != nil {
				log.Error("system template boot guard failed", zap.String("what", g.what), zap.Error(err))
			}
		}
		// system_templates is deliberately NOT row-level secured: it is a GLOBAL
		// catalog with no org_id, shared by every workspace. The ledger is org-scoped
		// and gets the same treatment as every other table this app adds.
		db.Exec(`ALTER TABLE org_template_applications ENABLE ROW LEVEL SECURITY`)

		// idx_contacts_org_email — the UNIQUE index lead ingestion's race guard is
		// built on. It ships only in migrations/000003, which has NEVER run on prod
		// (golang-migrate is dirty at v2 and start.sh swallows the failure), so prod
		// most likely has no contact-email uniqueness at all. Without it the ingest
		// upsert loop's 23505 recovery can never fire and two concurrent first-time
		// leads BOTH insert — the duplicate-contact bug the loop exists to prevent,
		// live in the only environment that matters.
		//
		// Promotion follows the uq_users_email_lower ritual above: prod data may
		// already violate the constraint, and a bare CREATE UNIQUE INDEX would fail
		// SILENTLY here (guards discard errors), leaving no index while local tests
		// pass. So: probe pg_index.indisunique by name (not just the name, which a
		// non-unique impostor could satisfy), and refuse to build over existing
		// duplicates, loudly, rather than half-applying.
		const uniqueContactEmailIndex = `SELECT EXISTS(SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid WHERE c.relname = 'idx_contacts_org_email' AND i.indisunique)`
		var hasUniqueContactEmail bool
		db.Raw(uniqueContactEmailIndex).Scan(&hasUniqueContactEmail)
		if !hasUniqueContactEmail {
			// Same predicate as migrations/000003 — a narrower or wider one would not
			// be the constraint the ingest loop assumes.
			var contactEmailDups int64
			db.Raw(`SELECT COUNT(*) FROM (
				SELECT org_id, email FROM contacts
				WHERE email IS NOT NULL AND deleted_at IS NULL
				GROUP BY org_id, email HAVING COUNT(*) > 1
			) d`).Scan(&contactEmailDups)
			if contactEmailDups > 0 {
				log.Error("contacts: duplicate (org_id, email) pairs exist — UNIQUE index skipped; lead ingestion's concurrent-duplicate guard is INACTIVE until they are merged",
					zap.Int64("emails", contactEmailDups))
			} else if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_contacts_org_email
				ON contacts(org_id, email) WHERE email IS NOT NULL AND deleted_at IS NULL`).Error; err != nil {
				log.Error("contacts: failed to create idx_contacts_org_email", zap.Error(err))
			} else {
				log.Info("contacts: idx_contacts_org_email created (lead ingestion race guard active)")
			}
		}

		// Contact search indexes. Both are GIN over a to_tsvector expression, and both
		// must stay character-identical to the expressions in contactRepository.List —
		// Postgres matches an expression index structurally, so a stray COALESCE or a
		// different config name here means the index exists, is maintained on every
		// write, and is never used by the query it was built for.
		for _, g := range []struct{ what, sql string }{
			// Backs the company-name arm of contact search. companies.name is NOT NULL,
			// so no COALESCE — in the index or the query.
			{"companies fulltext index", `CREATE INDEX IF NOT EXISTS idx_companies_fulltext
				ON companies USING GIN (to_tsvector('simple', name))`},
			// NOT new, and that is the point: idx_contacts_fulltext ships only in
			// migrations/000003 — the same migration whose OTHER index had to be promoted
			// to a boot guard just above because it never ran on prod. So the index behind
			// the name/email half of contact search has almost certainly never existed in
			// production, and every contact search there has been a sequential scan over
			// contacts. Cheap to assert, so assert it rather than assume.
			{"contacts fulltext index", `CREATE INDEX IF NOT EXISTS idx_contacts_fulltext
				ON contacts USING GIN (to_tsvector('simple', first_name || ' ' || last_name || ' ' || COALESCE(email, '')))`},
		} {
			// Builds take an ACCESS EXCLUSIVE lock for their duration (CONCURRENTLY cannot
			// run on the boot path) — the same trade the contacts indexes above already
			// make, and a no-op on every boot after the first.
			if err := db.Exec(g.sql).Error; err != nil {
				log.Error("contact search index boot guard failed", zap.String("what", g.what), zap.Error(err))
			}
		}

		log.Info("Lead integration tables ready")

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

		// Industry starter templates. Content lives in
		// internal/repository/templates/*.json and is upserted on slug; any row we no
		// longer ship is retired (is_active = false) rather than deleted, since
		// org_settings.industry_template_slug foreign-keys onto it. Logged and
		// survivable: a bad template must not stop the server booting, it just
		// leaves the picker short an option.
		if n, err := repository.SeedSystemTemplates(db); err != nil {
			log.Error("Failed to seed system templates", zap.Error(err))
		} else if n > 0 {
			log.Info("Seeded system templates", zap.Int("templates", n))
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
	// Declared out here so the shutdown block can drain it. A health alert is
	// buffered in memory between the transition that raised it and the fan-out that
	// sends it, and a rolling deploy is itself a likely cause of the failures being
	// reported — so without a drain the alarm is lost exactly when it fires.
	var integrationsHealth *integrations.HealthReporter
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

		// And the org_settings repository, so the assistant adopts the per-org AI
		// persona an industry template installs. Without this the persona is written
		// on every template apply and never read by anything.
		kbBuilder.SetOrgSettingsRepo(repository.NewOrgSettingsRepository(db))

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
		// Record shares (U6.2): grant a record to a user, a role, or a group at
		// view/edit — parity with report sharing. Visibility is the gate: the usecase
		// reuses the scope-aware RecordService.Get, so a row-scoped role can only
		// share what it can already reach. The identity/group/role repos resolve and
		// validate share targets (they are stateless db wrappers; roleRepo and
		// userGroupRepo are constructed again below for the admin usecases).
		shareRepo := repository.NewRecordShareRepository(db)
		shareUC := usecase.NewShareUseCase(
			recordService,
			shareRepo,
			repository.NewShareIdentityRepository(db),
			repository.NewUserGroupRepository(db),
			repository.NewRoleRepository(db),
			authRepo,
			permissionUC,
		)
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
		// userGroupRepo doubles as the member-drawer group reader (U4).
		userGroupRepo := repository.NewUserGroupRepository(db)
		workspaceUseCase := usecase.NewWorkspaceUseCase(authRepo, mailerSvc, appEnv, cfg.FrontendURL, sessionEvictor, permissionUC, roleRepo,
			repository.NewOffboardRepository(db), userGroupRepo)
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
		// (userGroupRepo is constructed above for the member-drawer group reader.)
		userGroupUC := usecase.NewUserGroupUseCase(userGroupRepo, authRepo)
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

		// Industry starter templates. Constructed after kbUseCase because applying a
		// template preloads the knowledge base. The repo factories let the apply
		// engine build tx-scoped repositories for its atomic phase without this
		// package's layering being inverted (usecase does not import repository).
		systemTemplateUC := usecase.NewSystemTemplateUseCase(
			db,
			repository.NewSystemTemplateRepository(db),
			kbUseCase,
			repository.NewOrgSettingsRepository(db),
			permissionUC,
			usecase.TemplateRepoFactories{
				ObjectRegistry: repository.NewObjectRegistryRepository,
				CustomObject:   repository.NewCustomObjectRepository,
			},
		)
		// Applying a template writes the org's AI persona; the assistant's prompt is
		// cached for 30 minutes and the persona is not in its cache key, so the apply
		// engine has to bust it explicitly.
		if setter, ok := systemTemplateUC.(interface {
			SetKBCacheBuster(domain.SchemaCacheBuster)
		}); ok {
			setter.SetKBCacheBuster(kbBuilder)
		} else {
			// Loud on purpose. A silently-skipped assertion here degrades into
			// "personas take 30 minutes to appear", which nobody would trace back
			// to this line.
			log.Error("system template usecase does not accept a KB cache buster; AI personas will lag the prompt cache")
		}
		templateHandler := delivery.NewSystemTemplateHandler(systemTemplateUC)

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
		// U5: preference gating + email channel. authRepo resolves a recipient's
		// email; mailerSvc sends the immediate + digest emails; FrontendURL builds
		// absolute links. All nil-safe in the usecase.
		notificationPrefRepo := repository.NewNotificationPreferenceRepository(db)
		notificationUC := usecase.NewNotificationUseCase(notificationRepo, notificationPrefRepo, authRepo, mailerSvc, redisClient, cfg.FrontendURL)
		notificationHandler := delivery.NewNotificationHandler(notificationUC)

		// Personal API tokens (U6.5). The repo is passed to RegisterRoutes as well:
		// the protected group's middleware authenticates a PAT by hash on every
		// request (that read IS the revocation check, which is why it isn't cached).
		apiTokenRepo := repository.NewAPITokenRepository(db)
		apiTokenHandler := delivery.NewAPITokenHandler(
			usecase.NewAPITokenUseCase(apiTokenRepo, permissionUC, authRepo),
		)
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
		// Daily-digest pass (U5): email members who chose email_digest='daily' a
		// summary of their recent unread notifications, at most ~once a day (the
		// last_digest_sent_at guard makes this hourly tick restart-safe). Best-effort.
		go func() {
			run := func() {
				if n, err := notificationUC.RunDailyDigest(context.Background()); err != nil {
					log.Warn("notifications: digest pass failed", zap.Error(err))
				} else if n > 0 {
					log.Info("notifications: digest sent", zap.Int("emails", n))
				}
			}
			ticker := time.NewTicker(1 * time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				run()
			}
		}()

		voiceNoteRepo := repository.NewVoiceNoteRepository(db)
		voiceNoteUC := usecase.NewVoiceNoteUseCase(voiceNoteRepo, aiJobQueue, cfg, contactRepo)
		voiceHandler := delivery.NewVoiceHandler(voiceNoteUC)

		delivery.RegisterRoutes(router, authHandler, contactHandler, companyHandler, tagHandler, dealHandler, pipelineHandler, activityHandler, taskHandler, userHandler, aiHandler, settingsHandler, customObjHandler, objectRegistryHandler, recordHandler, permissionHandler, searchHandler, kbHandler, commandHandler, eventsHandler, workspaceHandler, chatSessionHandler, voiceHandler, layoutHandler, roleHandler, roleAccessHandler, auditHandler, reportHandler, reportShareHandler, reportCommentHandler, dashboardHandler, userGroupHandler, notificationHandler, apiTokenHandler, templateHandler, cfg, db, redisClient, authRepo, apiTokenRepo, permissionUC)

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
		// L7.3: WEBHOOK_SKIP_SIGNATURE is honoured only in development/test. Passed
		// rather than read inside the handler so the escape hatch is gated by the same
		// config value as every other one, and so a handler that is never told its
		// environment defaults to production.
		autoHandler.SetAppEnv(cfg.AppEnv)
		autoHandler.RegisterRoutes(router,
			delivery.AuthMiddleware(cfg.JWTSecret, authRepo, redisClient),
			func(code string) gin.HandlerFunc { return delivery.RequireCapability(permissionUC, code) },
		)

		// ── Lead integrations (L1.1) ─────────────────────────────────────
		// The protected-group-equivalent stack, built once and handed over whole.
		// It is NOT the plain AuthMiddleware the automation registration above
		// passes: that call is why automation's management routes reject personal
		// access tokens and skip the workspace 2FA policy. Order matters —
		// RequireTwoFactorSatisfied reads a context key the auth middleware sets, so
		// mounted first or alone it silently passes everything.
		integrationsProtected := []gin.HandlerFunc{
			delivery.AuthMiddlewareWithAPITokens(cfg.JWTSecret, authRepo, redisClient, apiTokenRepo),
			delivery.RequireTwoFactorSatisfied(),
		}
		integrationsRepo := integrations.NewRepository(db)
		// Deleting a contact must erase what the lead ledger stored about them — the raw
		// payload, the capture context, and the consent envelope. The ledger deliberately
		// outlives its source, so nothing else would ever remove it, and a consent record
		// that survives the person it describes is the failure a consent feature must not
		// have. Wired here because usecase must not import integrations.
		if setter, ok := contactUseCase.(interface {
			SetLeadLedgerRedactor(usecase.LeadLedgerRedactor)
		}); ok {
			setter.SetLeadLedgerRedactor(integrationsRepo)
		}
		// redisClient may be nil; the limiter then runs entirely in-process. It never
		// degrades to "allow" — see integrations.RateLimiter.
		integrationsLimiter := integrations.NewRateLimiter(redisClient, 0, 0)
		// The batch endpoint charges BOTH buckets the full item count, which is what
		// stops a 100-item request from costing what one lead costs. That makes the IP
		// bucket bite 100x sooner for callers sharing egress — a corporate NAT, or
		// Make/Zapier's own outbound addresses — so its ceiling is raised deliberately
		// rather than bought by undercharging the bucket. The per-KEY limit stays at the
		// default, which is what bounds a single compromised credential.
		integrationsIPLimiter := integrations.NewRateLimiter(redisClient, 1200, 0)
		// Membership reads for owner routing: the single-user check the source-save path
		// already used, plus the batched liveness check every routed lead makes. Wraps
		// authRepo rather than widening domain.AuthRepository, whose many methods are
		// stubbed by hand in several test fakes.
		integrationsMembers := repository.NewOrgMemberReader(db, authRepo)
		// L6.1 health alerting. Reuses the A6/U5 notification pipeline wholesale —
		// preferences, digest batching, the email channel and the per-user SSE channel
		// all come free from NotificationUseCase.Create, so nothing here is new
		// notification infrastructure. What IS new is the recipient query: nothing in
		// the permission layer could enumerate the users in an org holding a capability.
		//
		// Nil-tolerant end to end: NewHealthReporter returns nil when notifications are
		// not configured, and every method no-ops on a nil receiver, so a deployment
		// without it keeps capturing leads exactly as before.
		integrationsHealth = integrations.NewHealthReporter(
			notificationUC,
			repository.NewIntegrationAudienceReader(db),
			integrationsMembers,
			autoLogger,
		)
		go integrationsHealth.Start(context.Background())
		// Extracted to a variable (was inline) so the async webhook processor (L5.3)
		// shares the very same ingest pipeline the sync capture routes use.
		integrationsIngest := integrations.NewLeadIngestService(
			integrationsRepo, recordService, contactRepo, objectRegistryUC, orgSettingsUC,
			integrationsMembers, // owner-pool liveness
			stageRepo,           // re-checks the configured deal stage: deleting one is a SOFT delete, so a stale id keeps satisfying the FK
			autoLogger,          // routing degradations are invisible otherwise: the write still succeeds
		).WithHealthReporter(integrationsHealth)
		// L7.2b: lend the legacy inbound webhook the platform's delivery ledger, owner
		// routing and health signal. Its contact write and its trigger payload are
		// deliberately untouched — see integrations.LegacyCapture for why moving the
		// write would silently rewrite what every existing workflow reads.
		autoHandler.SetLeadCapture(integrations.NewLegacyCapture(integrationsRepo, integrationsIngest, autoLogger))

		integrationsHandler := integrations.NewHandler(
			integrationsRepo,
			integrationsIngest,
			permissionUC,
			integrationsMembers, // membership check for default_owner_id + owner_pool
			objectRegistryUC,
			stageRepo, // save-time validation of the deal option's stage
			integrationsLimiter,
			integrationsIPLimiter,
			autoLogger, // same slog handler the automation engine writes to
		).WithHealthReporter(integrationsHealth)
		// A delivery whose process died mid-write stays at `processing`, and the replay
		// switch then answers 409 to every retry — turning the Idempotency-Key into the
		// thing that makes the lead permanently unrecoverable. The reaper releases them.
		go integrations.StartReaper(context.Background(), integrationsRepo, autoLogger)

		integrationsHandler.RegisterRoutes(router,
			integrationsProtected,
			func(code string) gin.HandlerFunc { return delivery.RequireCapability(permissionUC, code) },
		)

		// ── L5.1 provider connector framework ────────────────────────────
		// The registry holds the provider adapters that have shipped. It stays EMPTY
		// (and every /connect a clean 404) until a provider is BOTH built and
		// configured — which keeps a deployment that has set no provider env vars
		// booting without INTEGRATION_ENC_KEY (buildIntegrationCodec's "no provider
		// configured" branch).
		integrationsRegistry := integrations.NewRegistry()
		// L5.2 Facebook Lead Ads. Registered only when the app credentials are set;
		// an unset FACEBOOK_APP_ID leaves it unregistered (dormant). When it IS set,
		// providerCredentialEnvSet reports it, so buildIntegrationCodec (Phase 1)
		// already refused to boot a production-like env that lacks INTEGRATION_ENC_KEY.
		if cfg.FacebookAppID != "" && cfg.FacebookAppSecret != "" {
			integrationsRegistry.Register(integrations.NewFacebookProvider(
				cfg.FacebookAppID, cfg.FacebookAppSecret, cfg.FacebookLoginConfigID,
				integrations.NewHTTPClient(nil),
			))
			log.Info("Facebook Lead Ads provider registered")
		}
		integrationsConnSvc := integrations.NewConnectionService(
			integrationsRepo,
			integrationCodec, // resolved in Phase 1 (top of main); this is its first consumer
			integrationsRegistry,
			cfg.PublicAPIBaseURL, // provider redirect_uri origin — never c.Request.Host
			cfg.FrontendURL,      // where the browser lands after the OAuth callback
			autoLogger,
		)
		// The startup canary: prove the configured INTEGRATION_ENC_KEY actually opens
		// the provider credentials already at rest. It must run HERE — after the DB and
		// the connection boot guards — because buildIntegrationCodec (Phase 1, before
		// the DB) can only check that the key PARSES, not that it is the key the stored
		// rows were sealed under. A rotated Railway variable or a keyring entry dropped
		// during a paste is caught now, at deploy, rather than on the first admin's
		// Connect click days later. A clean or unconfigured install is a no-op pass.
		if err := integrationsConnSvc.Canary(context.Background()); err != nil {
			log.Fatal("Provider credential encryption canary failed", zap.Error(err))
		}
		// The source handler's backfill action needs a connection's creds+adapter.
		integrationsHandler.WithConnections(integrationsConnSvc)
		integrations.NewConnectionHandler(integrationsRepo, integrationsConnSvc, permissionUC, objectRegistryUC, autoLogger).
			RegisterRoutes(router,
				integrationsProtected,
				func(code string) gin.HandlerFunc { return delivery.RequireCapability(permissionUC, code) },
			)
		// A short sweep of consumed/expired OAuth state and pending-connection rows,
		// so the two custody tables do not grow without bound. Advisory — every
		// consume already re-checks expiry, so a lingering row is harmless.
		go integrations.StartOAuthArtifactSweeper(context.Background(), integrationsRepo, autoLogger)

		// Ledger retention (L6.5b). Contact-keyed erasure covers every delivery that
		// became a record and none of the rest: a failed or quarantined delivery still
		// holds the person's payload verbatim and has no contact to key an erasure off,
		// so bounding its lifetime is the only reachable answer. Redacts, never
		// deletes, and never touches a delivery that produced a record — those are
		// erasable on request and are the only rows that can carry a consent envelope.
		integrationsPurge := integrations.NewPurgeService(integrationsRepo, integrationsConnSvc, autoLogger).
			WithBackfillCanceller(integrationsHandler.CancelBackfillsForOrg)
		go integrations.StartLedgerPrune(context.Background(), integrationsRepo, integrationsPurge, autoLogger)

		// Workspace teardown (L6.4). Deleting a workspace touched no integrations
		// table: the sealed provider credentials stayed at rest, the page CLAIM went on
		// blocking every other workspace from connecting a page the customer could no
		// longer release, and anything already queued kept being written into the
		// deleted org. Injected by interface assertion, the SetLeadLedgerRedactor
		// crossing — usecase must not import integrations.
		if setter, ok := workspaceUseCase.(interface {
			SetIntegrationsPurger(usecase.IntegrationsPurger)
		}); ok {
			setter.SetIntegrationsPurger(integrationsPurge)
		}

		// ── L5.3 Facebook leadgen webhook + async processor ───────────────
		// The PUBLIC webhook endpoint (GET verify handshake + POST signed receipt)
		// and the async worker that claims pending deliveries, fetches each lead from
		// Graph, and runs it through the shared ingest pipeline. Both are harmless when
		// Facebook is unconfigured: the endpoint's provider lookup misses and acks, and
		// the processor finds no pending rows to claim.
		integrations.NewWebhookHandler(integrationsRepo, integrationsConnSvc, cfg.FacebookWebhookVerifyToken, integrationsIPLimiter, autoLogger).
			RegisterRoutes(router)
		go integrations.StartWebhookProcessor(context.Background(), integrationsRepo, integrationsConnSvc, integrationsIngest, autoLogger, integrationsHealth)

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
		// Applying a template installs objects and fields, so it needs the same
		// double invalidation. The OLS half is done inside the apply engine (it must
		// run before the response returns, or the admin 403s on their new objects);
		// this wires the workflow-builder schema cache.
		templateHandler.SetSchemaInvalidator(invalidator)

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
	// Drain buffered health alerts before the process exits (see the declaration).
	integrationsHealth.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown", zap.Error(err))
	}

	log.Info("Server exiting")
}

// sanitizeRequestID validates a client-supplied correlation id. It returns "" for
// anything that is not a short, plain token, so the caller falls back to a
// server-generated uuid. The id is echoed on a response header and written into
// every log line for the request, so accepting arbitrary bytes here would let a
// caller forge log entries (newlines) or attempt header splitting (CR/LF).
func sanitizeRequestID(s string) string {
	const maxLen = 64
	if len(s) == 0 || len(s) > maxLen {
		return ""
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.'
		if !ok {
			return ""
		}
	}
	return s
}

// providerCredentialEnvSet reports whether any third-party provider is
// configured — i.e. whether this deployment is in a position to store an
// encrypted credential at all.
//
// Adding a provider means adding its env var here. That is the coupling: a
// provider configured without INTEGRATION_ENC_KEY refuses to boot, so the
// failure is a startup message naming the missing variable rather than a
// runtime error the first time an admin clicks Connect.
func providerCredentialEnvSet(cfg *config.Config) []string {
	var configured []string
	// Facebook (L5.2): once its app credentials are set, this deployment can store
	// an encrypted page token, so INTEGRATION_ENC_KEY becomes mandatory in a
	// production-like environment. The condition MATCHES the registration condition
	// below (id AND secret) on purpose — reporting it on the id alone would refuse
	// to boot for a provider that will never register (registration needs both), so
	// a half-set config would crash-loop over a key it does not actually need yet.
	if cfg.FacebookAppID != "" && cfg.FacebookAppSecret != "" {
		configured = append(configured, "FACEBOOK_APP_ID")
	}
	return configured
}

// buildIntegrationCodec resolves INTEGRATION_ENC_KEY into the envelope codec
// used to seal provider credentials.
//
// The three outcomes are deliberately not symmetric:
//
//   - MALFORMED is always fatal, in every environment. A key that fails to
//     parse is a typo, and booting past it would mean every credential write
//     fails at runtime instead of at deploy time, where somebody is watching.
//   - ABSENT with a provider configured is fatal in production-like
//     environments, following the RESEND_API_KEY precedent below — including
//     its devLike allowlist rather than an `== "production"` match, because
//     APP_ENV is unset on prod today and a string match on it fails OPEN.
//   - ABSENT with no provider configured returns a nil codec. That is the
//     current state of every existing deployment, and it must keep booting: the
//     connect routes answer 503 with an actionable message, and nothing else in
//     the app touches the codec.
//
// A nil *envelope.Codec is a valid value — every method on it returns
// ErrNotConfigured — so callers do not need a nil check to stay safe.
func buildIntegrationCodec(cfg *config.Config, log *zap.Logger) *envelope.Codec {
	configured := providerCredentialEnvSet(cfg)

	ring, err := envelope.ParseKeyring(cfg.IntegrationEncKey)
	switch {
	case err == nil:
		// Do not log the versions at Info on every boot without saying what
		// they are for; an operator reading this line during an incident needs
		// to know a rotation is half-applied.
		log.Info("Provider credential encryption is configured",
			zap.Ints("key_versions", ring.Versions()),
			zap.Int("sealing_under", ring.Primary()))
		return envelope.NewCodec(ring)

	case errors.Is(err, envelope.ErrNoKeys):
		devLike := cfg.AppEnv == "development" || cfg.AppEnv == "test"
		if len(configured) > 0 && !devLike {
			log.Fatal("INTEGRATION_ENC_KEY is not set, but provider credentials are configured (" +
				strings.Join(configured, ", ") + ") and this is a production-like environment " +
				"(APP_ENV is not development/test): refusing to boot, because provider access tokens would be stored " +
				"unencrypted or not at all. Generate one with `openssl rand -base64 32` and set INTEGRATION_ENC_KEY.")
		}
		log.Warn("INTEGRATION_ENC_KEY is not set: connecting a third-party provider account will be refused. " +
			"Generate one with `openssl rand -base64 32`. This is expected for deployments that do not use provider connections.")
		return nil

	default:
		// Never zap.Error(err) blind here — ParseKeyring's messages are written
		// to be material-free precisely so this line can be logged, but the
		// Fatal is what matters: a malformed key must not reach runtime.
		log.Fatal("INTEGRATION_ENC_KEY is set but could not be parsed: " + err.Error() +
			". Expected a base64-encoded 32-byte key, optionally as a comma-separated `version:key` keyring.")
		return nil
	}
}
