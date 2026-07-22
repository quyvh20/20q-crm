package config

import (
	"fmt"
	"log"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	// App
	Port string `mapstructure:"PORT"`
	// AppEnv gates dev-only escape hatches (debug tokens) and the fail-closed
	// mail check. Read through viper so a local crm-backend/.env works. Anything
	// other than the explicit "development"/"test" allowlist values is treated
	// as production — unset/typo'd fails CLOSED (P10 P1).
	AppEnv string `mapstructure:"APP_ENV"`

	// Database & Cache
	DatabaseURL string `mapstructure:"DATABASE_URL"`
	RedisURL    string `mapstructure:"REDIS_URL"`

	// Auth
	JWTSecret string `mapstructure:"JWT_SECRET"`
	// TOTPEncKey encrypts the stored TOTP secrets at rest (U6.4). Optional: when
	// unset, the key is derived from JWT_SECRET, so turning 2FA on doesn't force a
	// self-hosted deployment to manage a second secret. Set it explicitly to rotate
	// the two independently — and note that rotating JWT_SECRET without setting this
	// first makes existing TOTP secrets undecryptable (users recover with a backup
	// code, then re-enroll).
	TOTPEncKey         string `mapstructure:"TOTP_ENC_KEY"`
	GoogleClientID     string `mapstructure:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `mapstructure:"GOOGLE_CLIENT_SECRET"`
	GoogleRedirectURL  string `mapstructure:"GOOGLE_REDIRECT_URL"`
	FrontendURL        string `mapstructure:"FRONTEND_URL"`
	// PublicAPIBaseURL is the origin third parties and providers reach us on — the
	// base for capture URLs shown in the UI and, later, OAuth redirect URIs and
	// provider webhook callbacks.
	//
	// Config-derived, never c.Request.Host: prod fronts /api through a Cloudflare
	// Pages Function that strips Host and sends no X-Forwarded-Host, so a
	// host-derived URL renders the Railway origin — and a later move to a custom
	// domain would silently break every callback already registered in a provider's
	// console. Server-to-server callers need no cookies, so pointing them straight
	// at the API origin is correct.
	PublicAPIBaseURL string `mapstructure:"PUBLIC_API_BASE_URL"`
	// IntegrationEncKey is the key-encryption keyring that seals third-party
	// provider credentials at rest (L5). Format is a comma-separated list of
	// `version:base64key` entries; a bare 32-byte base64 key is read as version
	// 1. See internal/integrations/envelope.
	//
	// It has NO default, and that is the point. A default would make the key
	// resolve to a known value and mask its own absence, which is how
	// TOTP_ENC_KEY ended up deriving every stored 2FA secret from JWT_SECRET in
	// production. There is deliberately no fallback to any other secret either:
	// rotating an unrelated signing key would then silently orphan every stored
	// provider credential, with no version bump to detect it and no recovery.
	IntegrationEncKey string `mapstructure:"INTEGRATION_ENC_KEY"`
	// Facebook Lead Ads provider (L5.2). All optional: when FacebookAppID is empty
	// the provider is not registered and every /connect for it 404s — which is the
	// state of every deployment until the Meta app exists. No defaults (secrets),
	// and each needs its own BindEnv below or it silently reads "" in production
	// (the PADDLE_WEBHOOK_SECRET / TOTP_ENC_KEY failure — the config test enforces
	// this).
	FacebookAppID     string `mapstructure:"FACEBOOK_APP_ID"`
	FacebookAppSecret string `mapstructure:"FACEBOOK_APP_SECRET"`
	// FacebookWebhookVerifyToken authenticates the GET hub.challenge handshake on
	// the app-level leadgen webhook (L5.3).
	FacebookWebhookVerifyToken string `mapstructure:"FACEBOOK_WEBHOOK_VERIFY_TOKEN"`
	// FacebookLoginConfigID selects Facebook Login for Business (Business
	// Integration System User tokens that survive employee departure). Empty falls
	// back to the classic scope flow.
	FacebookLoginConfigID string `mapstructure:"FACEBOOK_LOGIN_CONFIG_ID"`

	// TikTok Lead Generation provider (L7.5). All optional: when TikTokAppID is empty
	// the provider is not registered and every /connect for it 404s. TikTokAuthURL is
	// the advertiser authorization URL copied from the app's page in TikTok's
	// developer portal — it is configuration rather than a construction because
	// TikTok does not document the parameters. Each needs its own BindEnv below.
	TikTokAppID     string `mapstructure:"TIKTOK_APP_ID"`
	TikTokAppSecret string `mapstructure:"TIKTOK_APP_SECRET"`
	TikTokAuthURL   string `mapstructure:"TIKTOK_AUTH_URL"`
	// TrustedProxies is a comma-separated CIDR list of edge proxies whose
	// X-Forwarded-For may be believed. EMPTY (the default) trusts none, so
	// c.ClientIP() returns the unforgeable peer address. Gin's own default is the
	// opposite -- trust everything -- which makes ClientIP() attacker-controlled and
	// every rate limit keyed on it decorative.
	TrustedProxies string `mapstructure:"TRUSTED_PROXIES"`

	// Refresh-token cookie policy (P2). The refresh token moves out of
	// localStorage into an httpOnly cookie. In production the frontend and API
	// are cross-site (Cloudflare Pages + separate API host), so the cookie needs
	// SameSite=None; Secure. Local dev is same-site over http, so it defaults to
	// SameSite=Lax; Secure=false. CookieDomain is empty (host-only) unless a
	// shared parent domain is configured.
	CookieSecure   bool   `mapstructure:"COOKIE_SECURE"`
	CookieSameSite string `mapstructure:"COOKIE_SAMESITE"` // strict | lax | none
	CookieDomain   string `mapstructure:"COOKIE_DOMAIN"`

	// Monitoring
	SentryDSN string `mapstructure:"SENTRY_DSN"`

	// AI (Cloudflare AI Gateway)
	CFAccountID      string `mapstructure:"CF_ACCOUNT_ID"`
	CFAIToken        string `mapstructure:"CF_AI_TOKEN"`
	CFAIGatewayID    string `mapstructure:"CF_AI_GATEWAY_ID"`
	CFAIGatewayToken string `mapstructure:"CF_AI_GATEWAY_TOKEN"`

	// Payments
	PaddleWebhookSecret string `mapstructure:"PADDLE_WEBHOOK_SECRET"`

	// Email
	ResendAPIKey string `mapstructure:"RESEND_API_KEY"`
	// MailFrom is the From address for all outgoing mail; the domain must be
	// verified in Resend or every send silently fails at the provider.
	MailFrom string `mapstructure:"MAIL_FROM"`
	// MailDisabled is the explicit escape hatch for prod-like deployments that
	// genuinely run without email: without it, production boot REFUSES to start
	// when RESEND_API_KEY is missing rather than silently dropping recovery
	// emails (P10 P1).
	MailDisabled bool `mapstructure:"MAIL_DISABLED"`

	// Storage (Cloudflare R2 — optional, falls back to base64-in-redis for dev)
	R2AccountID       string `mapstructure:"R2_ACCOUNT_ID"`
	R2AccessKeyID     string `mapstructure:"R2_ACCESS_KEY_ID"`
	R2SecretAccessKey string `mapstructure:"R2_SECRET_ACCESS_KEY"`
	R2BucketName      string `mapstructure:"R2_BUCKET_NAME"`
	R2PublicURL       string `mapstructure:"R2_PUBLIC_URL"`
}

func LoadConfig() (*Config, error) {
	viper.SetConfigFile(".env")
	viper.AutomaticEnv()

	// Explicitly bind env vars so Unmarshal picks them up even without a .env file
	viper.BindEnv("DATABASE_URL")
	viper.BindEnv("REDIS_URL")
	viper.BindEnv("JWT_SECRET")
	viper.BindEnv("GOOGLE_CLIENT_ID")
	viper.BindEnv("GOOGLE_CLIENT_SECRET")
	viper.BindEnv("GOOGLE_REDIRECT_URL")
	viper.BindEnv("FRONTEND_URL")
	// Required, not optional: viper.AutomaticEnv() does NOT feed Unmarshal into the
	// struct — only explicitly bound keys land. Without this line the field reads ""
	// in prod (no .env file) and every capture URL the UI renders is malformed.
	// PADDLE_WEBHOOK_SECRET and TOTP_ENC_KEY are both live victims of exactly this.
	viper.BindEnv("PUBLIC_API_BASE_URL")
	viper.BindEnv("INTEGRATION_ENC_KEY")
	viper.BindEnv("FACEBOOK_APP_ID")
	viper.BindEnv("FACEBOOK_APP_SECRET")
	viper.BindEnv("FACEBOOK_WEBHOOK_VERIFY_TOKEN")
	viper.BindEnv("FACEBOOK_LOGIN_CONFIG_ID")
	viper.BindEnv("TIKTOK_APP_ID")
	viper.BindEnv("TIKTOK_APP_SECRET")
	viper.BindEnv("TIKTOK_AUTH_URL")
	viper.BindEnv("TRUSTED_PROXIES")
	viper.BindEnv("COOKIE_SECURE")
	viper.BindEnv("COOKIE_SAMESITE")
	viper.BindEnv("COOKIE_DOMAIN")
	viper.BindEnv("SENTRY_DSN")
	viper.BindEnv("CF_ACCOUNT_ID")
	viper.BindEnv("CF_AI_TOKEN")
	viper.BindEnv("CF_AI_GATEWAY_ID")
	viper.BindEnv("CF_AI_GATEWAY_TOKEN")

	viper.BindEnv("APP_ENV")
	viper.BindEnv("RESEND_API_KEY")
	viper.BindEnv("MAIL_FROM")
	viper.BindEnv("MAIL_DISABLED")
	viper.BindEnv("R2_ACCOUNT_ID")
	viper.BindEnv("R2_ACCESS_KEY_ID")
	viper.BindEnv("R2_SECRET_ACCESS_KEY")
	viper.BindEnv("R2_BUCKET_NAME")
	viper.BindEnv("R2_PUBLIC_URL")

	// Default values
	viper.SetDefault("PORT", "8080")
	viper.SetDefault("CF_AI_GATEWAY_ID", "crm-ai-gateway")
	viper.SetDefault("JWT_SECRET", "dev-secret-change-me-in-production-32chars!")
	viper.SetDefault("FRONTEND_URL", "http://localhost:5173")
	viper.SetDefault("GOOGLE_REDIRECT_URL", "http://localhost:8080/api/auth/google/callback")
	// Dev defaults: same-site over http. Production sets COOKIE_SAMESITE=none +
	// COOKIE_SECURE=true (cross-site cookie delivery).
	viper.SetDefault("COOKIE_SAMESITE", "lax")
	viper.SetDefault("COOKIE_SECURE", false)
	viper.SetDefault("MAIL_FROM", "noreply@twentyq.io")
	// The Railway origin third parties reach us on. A default matters here in a
	// way it does not for most config: this value is pasted into Google Ads,
	// Meta and every customer's website, and it has to byte-match what is
	// registered in a provider console. An empty base yields the RELATIVE string
	// "/api/integrations/facebook/callback", which providers reject and which
	// passes unit tests silently.
	//
	// Four places now hardcode this deployment's topology and must move
	// together: here, crm-frontend/functions/api/[[path]].ts (DEFAULT_BACKEND),
	// the pages.dev origin in delivery/http/middleware.go, and GOOGLE_REDIRECT_URL
	// below — plus the Google Ads and Meta consoles, which are outside the repo.
	viper.SetDefault("PUBLIC_API_BASE_URL", "https://20q-crm-production.up.railway.app")
	// No default for APP_ENV: absence must mean "production" (fail closed).
	// No default for INTEGRATION_ENC_KEY: see the field comment — a default
	// would mask the key's own absence, which is the failure it exists to
	// prevent.

	if err := viper.ReadInConfig(); err != nil {
		log.Printf("No .env file found or error reading it, relying on environment variables: %v", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, err
	}

	if err := validateFrontendURL(config.FrontendURL); err != nil {
		return nil, err
	}

	return &config, nil
}

// validateFrontendURL rejects a FRONTEND_URL that the CORS layer cannot accept.
//
// This exists because the failure it prevents is close to undiagnosable in
// production. FRONTEND_URL feeds delivery.AllowedOrigins, which feeds
// cors.New(...) in main.go — and gin-contrib/cors PANICS on a bad origin rather
// than returning an error (cors@v1.7.7/config.go:45). That call sits BEFORE
// srv.ListenAndServe(), so the process dies without ever binding the port: no
// /health, no route table, nothing to probe. The platform sees only a connection
// refused for the whole healthcheck window and reports a generic healthcheck
// failure, while the last-good container keeps serving and hides it. Grepping the
// codebase for log.Fatal finds nothing, because the fatal is inside a dependency.
//
// Failing here instead turns that into one line naming the variable and the fix,
// emitted by the existing "Failed to load config" fatal in main.go. It stays FATAL
// rather than falling back to a default: a wrong CORS origin means the frontend
// cannot reach the API at all, so booting anyway would ship a broken deployment
// where refusing to boot keeps the previous one alive.
//
// The scheme rule mirrors the library exactly — an origin must contain "*" or
// start with one of cors.DefaultSchemas ("http://", "https://"). Keep it in sync
// if the CORS config ever enables AllowWebSockets/AllowFiles/CustomSchemas, which
// widen the accepted set.
func validateFrontendURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("FRONTEND_URL is empty: set it to the browser origin of the frontend, " +
			"scheme included (e.g. https://app.example.com)")
	}
	// Checked before the scheme test, which HasPrefix would otherwise pass. A padded
	// value never panics — it silently fails to match any browser Origin header, so
	// every cross-origin request is rejected and the frontend looks broken for a
	// reason nothing logs.
	if raw != strings.TrimSpace(raw) {
		return fmt.Errorf("FRONTEND_URL %q has leading or trailing whitespace: an origin is "+
			"compared byte-for-byte against the browser's Origin header, so the padding would "+
			"silently reject every cross-origin request", raw)
	}
	if strings.Contains(raw, "*") {
		return nil
	}
	for _, scheme := range []string{"http://", "https://"} {
		if strings.HasPrefix(raw, scheme) {
			return nil
		}
	}
	return fmt.Errorf("FRONTEND_URL %q has no scheme: it must start with http:// or https:// "+
		"(or contain a * wildcard). Without one the CORS layer panics before the server binds "+
		"its port, which surfaces only as a healthcheck timeout", raw)
}
