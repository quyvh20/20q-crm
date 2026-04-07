package config

import (
	"log"

	"github.com/spf13/viper"
)

type Config struct {
	DatabaseURL         string `mapstructure:"DATABASE_URL"`
	RedisURL            string `mapstructure:"REDIS_URL"`
	Port                string `mapstructure:"PORT"`
	SentryDSN           string `mapstructure:"SENTRY_DSN"`
	CFAccountID         string `mapstructure:"CF_ACCOUNT_ID"`
	CFAIToken           string `mapstructure:"CF_AI_TOKEN"`
	PaddleWebhookSecret string `mapstructure:"PADDLE_WEBHOOK_SECRET"`
	ResendAPIKey        string `mapstructure:"RESEND_API_KEY"`
}

func LoadConfig() (*Config, error) {
	viper.SetConfigFile(".env")
	viper.AutomaticEnv() // Default to reading from environment variables

	// Set Default Values
	viper.SetDefault("PORT", "8080")

	if err := viper.ReadInConfig(); err != nil {
		log.Printf("No .env file found or error reading it, relying on environment variables: %v", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, err
	}

	return &config, nil
}
