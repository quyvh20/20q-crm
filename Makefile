.PHONY: dev build test clean migrate-up migrate-down lint docker-up docker-down help

# --- Variables ---
BINARY_NAME = server
DB_URL ?= "postgres://crm_user:crm_password@localhost:5432/crm_db?sslmode=disable"
MIGRATIONS_PATH = crm-backend/migrations

# --- Main Targets ---
help: ## Show this help message
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

dev: ## Run development server with auto-reload (requires air if set)
	cd crm-backend && go run cmd/server/main.go

build: ## Build the Go binary
	cd crm-backend && go build -o bin/$(BINARY_NAME) cmd/server/main.go

test: ## Run go tests
	cd crm-backend && go test -v ./...

clean: ## Clean built binaries
	rm -rf crm-backend/bin/

# --- Database & Migrations ---
# NOTE: Requires 'migrate' CLI installed. Install via: go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
migrate-up: ## Apply database migrations
	migrate -path $(MIGRATIONS_PATH) -database $(DB_URL) up

migrate-down: ## Rollback migrations
	migrate -path $(MIGRATIONS_PATH) -database $(DB_URL) down

docker-up: ## Start local database (Docker Compose)
	docker-compose up -d

docker-down: ## Stop local database
	docker-compose down

# --- Quality ---
lint: ## Run linter (requires golangci-lint)
	golangci-lint run ./crm-backend/...
