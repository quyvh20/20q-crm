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

# This is the FULL suite, not a quick one: internal/repository,
# internal/integrations and internal/automation self-provision postgres:16-alpine
# and pgvector/pgvector:pg16 via testcontainers, so Docker must be running or
# those packages fail. Expect ~40 min serially. CI shards it four ways
# (.github/workflows/deploy.yml, job backend-integration); for the fast local
# loop use `cd crm-backend && go test -short ./...` instead.
test: ## Run go tests (FULL suite — needs Docker, ~40 min; use `go test -short` for the fast loop)
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
# MUST cd into crm-backend first. The old form, `golangci-lint run
# ./crm-backend/...` from the repo root, fails under golangci-lint v2 with
# "directory prefix crm-backend does not contain main module or its selected
# dependencies" — and does it insidiously: it still prints "0 issues." before
# exiting 7, so it reads as a clean lint run unless you check $?.
# The config it picks up is crm-backend/.golangci.yml, the same one CI uses.
lint: ## Run linter (requires golangci-lint v2)
	cd crm-backend && golangci-lint run ./...
