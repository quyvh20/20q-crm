package main

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	dsn := "postgres://crm_user:crm_password@localhost:5432/crm_db?sslmode=disable"
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		log.Fatal("Failed to connect to db:", err)
	}

	// We use a transaction so we can rollback and not dirty the db
	tx := db.Begin()
	defer tx.Rollback()

	// 1. Get an existing org_id to own the contacts
	var orgID uuid.UUID
	if err := tx.Model(&domain.Organization{}).Select("id").Limit(1).Pluck("id", &orgID).Error; err != nil || orgID == uuid.Nil {
		// Just generate a fake one if none exists
		orgID = uuid.New()
		err = tx.Create(&domain.Organization{
			ID:   orgID,
			Name: "Test Org",
		}).Error
		if err != nil {
			log.Fatal("Failed to create dummy org:", err)
		}
	}

	count := 10_000
	fmt.Printf("Generating %d dummy contacts with 768d random embeddings...\n", count)
	
	start := time.Now()
	var contacts []*domain.Contact
	
	for i := 0; i < count; i++ {
		vec := make([]float32, 768)
		for j := 0; j < 768; j++ {
			vec[j] = rand.Float32()*2 - 1.0 // random between -1 and 1
		}
		pgVec := pgvector.NewVector(vec)
		
		email := fmt.Sprintf("dummy%d@test.com", i)
		contacts = append(contacts, &domain.Contact{
			ID:        uuid.New(),
			OrgID:     orgID,
			FirstName: fmt.Sprintf("Dummy%d", i),
			LastName:  "Test",
			Email:     &email,
			Embedding: &pgVec,
		})
	}

	fmt.Printf("Generated in %v. Inserting into DB in batches of 1000...\n", time.Since(start))
	
	start = time.Now()
	if err := tx.CreateInBatches(contacts, 1000).Error; err != nil {
		log.Fatal("Failed to insert contacts:", err)
	}
	fmt.Printf("Inserted 10k contacts in %v.\n", time.Since(start))

	// 2. Perform PgVector cosine similarity search
	queryVec := make([]float32, 768)
	for j := 0; j < 768; j++ {
		queryVec[j] = rand.Float32()*2 - 1.0
	}
	pqQueryVec := pgvector.NewVector(queryVec)

	fmt.Println("Running cosine similarity query (LIMIT 10)...")
	var results []domain.Contact
	
	searchStart := time.Now()
	
	err = tx.Where("org_id = ? AND embedding IS NOT NULL", orgID).
		Order(gorm.Expr("embedding <=> ?", pqQueryVec)).
		Limit(10).
		Find(&results).Error
		
	searchDuration := time.Since(searchStart)

	if err != nil {
		log.Fatal("Search failed:", err)
	}

	fmt.Printf("PgVector similarity search on 10k+ rows executed in: %v\n", searchDuration)
	if searchDuration < 100*time.Millisecond {
		fmt.Println("✅ SUCCESS: Query executes in < 100ms")
	} else {
		fmt.Println("❌ FAILED: Query is too slow")
	}
	
	fmt.Printf("Found %d results\n", len(results))
	fmt.Println("Rolling back transaction to clean up...")
}
