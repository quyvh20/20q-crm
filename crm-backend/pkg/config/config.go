package config

import (
	"log"

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
	JWTSecret          string `mapstructure:"JWT_SECRET"`
	GoogleClientID     string `mapstructure:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `mapstructure:"GOOGLE_CLIENT_SECRET"`
	GoogleRedirectURL  string `mapstructure:"GOOGLE_REDIRECT_URL"`
	FrontendURL        string `mapstructure:"FRONTEND_URL"`

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
	// No default for APP_ENV: absence must mean "production" (fail closed).

	if err := viper.ReadInConfig(); err != nil {
		log.Printf("No .env file found or error reading it, relying on environment variables: %v", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, err
	}

	return &config, nil
}
