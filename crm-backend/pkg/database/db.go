package database

import (
	"log"

	"github.com/jackc/pgx/v5/stdlib"
	pgxgorm "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	pgxlib "github.com/jackc/pgx/v5"
)

func NewConnection(dbURL string) (*gorm.DB, error) {
	if dbURL == "" {
		log.Println("Database URL is not provided")
		return nil, nil
	}

	// Parse the pgx config from the DSN
	config, err := pgxlib.ParseConfig(dbURL)
	if err != nil {
		return nil, err
	}

	// PreferSimpleProtocol disables the prepared statement cache,
	// which is required when using Supabase's PgBouncer connection pooler
	// (it throws SQLSTATE 42P05 if prepared statements are reused across connections).
	config.DefaultQueryExecMode = pgxlib.QueryExecModeSimpleProtocol

	// Register the configured pgx connection with stdlib so GORM can use it
	sqlDB := stdlib.OpenDB(*config)

	db, err := gorm.Open(pgxgorm.New(pgxgorm.Config{
		Conn: sqlDB,
	}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return nil, err
	}

	log.Println("Successfully connected to Postgres database")
	return db, nil
}
