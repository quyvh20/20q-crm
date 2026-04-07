package database

import (
	"log"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func NewConnection(dbURL string) (*gorm.DB, error) {
	if dbURL == "" {
		log.Println("Database URL is not provided")
		return nil, nil // Return nil or handle according to requirement if db is optional
	}

	db, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return nil, err
	}

	log.Println("Successfully connected to Postgres database")
	return db, nil
}
