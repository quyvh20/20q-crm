package config

import (
	"log"

	"github.com/spf13/viper"
)

type Config struct {
	// App
	Port string `mapstructure:"PORT"`

	// Database & Cache
	DatabaseURL string `mapstructure:"DATABASE_URL"`
	RedisURL    string `mapstructure:"REDIS_URL"`

	// Auth
	JWTSecret          string `mapstructure:"JWT_SECRET"`
	GoogleClientID     string `mapstructure:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `mapstructure:"GOOGLE_CLIENT_SECRET"`
	GoogleRedirectURL  string `mapstructure:"GOOGLE_REDIRECT_URL"`
	FrontendURL        string `mapstructure:"FRONTEND_URL"`

	// Monitoring
	SentryDSN string `mapstructure:"SENTRY_DSN"`

	// AI (Cloudflare)
	CFAccountID string `mapstructure:"CF_ACCOUNT_ID"`
	CFAIToken   string `mapstructure:"CF_AI_TOKEN"`

	// Payments
	PaddleWebhookSecret string `mapstructure:"PADDLE_WEBHOOK_SECRET"`

	// Email
	ResendAPIKey string `mapstructure:"RESEND_API_KEY"`
}

func LoadConfig() (*Config, error) {
	viper.SetConfigFile(".env")
	viper.AutomaticEnv()

	// Default values
	viper.SetDefault("PORT", "8080")
	viper.SetDefault("JWT_SECRET", "dev-secret-change-me-in-production-32chars!")
	viper.SetDefault("FRONTEND_URL", "http://localhost:5173")
	viper.SetDefault("GOOGLE_REDIRECT_URL", "http://localhost:8080/api/auth/google/callback")

	if err := viper.ReadInConfig(); err != nil {
		log.Printf("No .env file found or error reading it, relying on environment variables: %v", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, err
	}

	return &config, nil
}
