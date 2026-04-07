.PHONY: dev migrate-up migrate-down test build

DB_URL ?= "postgres://crm_user:crm_password@localhost:5432/crm_db?sslmode=disable"
MIGRATIONS_DIR = crm-backend/migrations

dev:
	cd crm-backend && go run cmd/server/main.go

migrate-up:
	migrate -path $(MIGRATIONS_DIR) -database $(DB_URL) up

migrate-down:
	migrate -path $(MIGRATIONS_DIR) -database $(DB_URL) down

test:
	cd crm-backend && go test -v ./...

build:
	cd crm-backend && go build -o bin/server cmd/server/main.go
