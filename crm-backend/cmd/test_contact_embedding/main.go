package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"crm-backend/internal/ai"
	"crm-backend/internal/domain"
	"crm-backend/internal/repository"
	"crm-backend/internal/usecase"
	"crm-backend/internal/worker"
	"crm-backend/pkg/config"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Println("Warning: could not load .env:", err)
	}

	dsn := cfg.DatabaseURL
	if dsn == "" {
		// fallback to os.Getenv just in case
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		fmt.Println("DATABASE_URL not set")
		os.Exit(1)
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		fmt.Println("Failed to connect to DB:", err)
		os.Exit(1)
	}

	log, _ := zap.NewDevelopment()

	accountID := cfg.CFAccountID
	token := cfg.CFAIToken
	gatewayID := cfg.CFAIGatewayID
	gatewayTok := cfg.CFAIGatewayToken

	embedSvc := ai.NewEmbeddingService(accountID, gatewayID, token, gatewayTok)
	embedWorker := worker.NewEmbeddingWorker(embedSvc, db, log, 10)
	go embedWorker.Start(context.Background(), 1)

	contactRepo := repository.NewContactRepository(db)
	contactUseCase := usecase.NewContactUseCase(contactRepo, embedWorker)

	orgID := uuid.New()
	// Optionally create a dummy org record if foreign keys require it
	db.Exec("INSERT INTO organizations (id, name, subscription_tier) VALUES (?, 'Test Org', 'free') ON CONFLICT DO NOTHING", orgID)

	input := domain.CreateContactInput{
		FirstName: "Embedding",
		LastName:  "Test",
	}
	email := fmt.Sprintf("embed%d@test.com", time.Now().UnixNano())
	input.Email = &email

	fmt.Println("1. Creating contact via UseCase...")
	contact, err := contactUseCase.Create(context.Background(), orgID, input)
	if err != nil {
		fmt.Println("Error creating contact:", err)
		os.Exit(1)
	}
	fmt.Printf("   Contact ID: %s\n", contact.ID)

	fmt.Println("2. Waiting for worker to process (5 seconds)...")
	time.Sleep(5 * time.Second)

	fmt.Println("3. Verifying DB...")
	var raw struct {
		ID        string
		FirstName string
		Embedding string
	}
	// Use raw SQL to get embedding to see if it's not null
	err = db.Raw("SELECT id, first_name, embedding::text FROM contacts WHERE id = ?", contact.ID).Scan(&raw).Error
	if err != nil {
		fmt.Println("Error querying DB:", err)
		os.Exit(1)
	}

	if raw.Embedding == "" || raw.Embedding == "null" {
		fmt.Println("💥 FAIL: missing embedding")
		os.Exit(1)
	}

	fmt.Printf("   ✅ PASS: Embedding populated successfully for %s! Length of serialized vector: %d\n", raw.FirstName, len(raw.Embedding))
}
