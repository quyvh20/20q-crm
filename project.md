# Project Summary: AI-First SaaS CRM

This project is built using **Clean Architecture** to ensure testability, decoupled structure, and ease of maintenance. Below is a guide to the system structure, data flow, and infrastructure setup.

## 1. Directory Structure (Backend Layer)

### `cmd/server/main.go`
This is the entry point of the project. Everything is initialized and injected (Dependency Injection) from here:
1. Load configuration (`pkg/config.go`)
2. Connect to Database and Redis (`pkg/database` & `pkg/cache`)
3. Initialize Logger (Zap) and Sentry
4. Instantiate Repositories, UseCases, and Handlers, then register them with the router (Gin).

### `internal/domain/`
The core of the system (Enterprise Business Rules), independent of any external libraries or frameworks:
- Defines data models (Structs like `User`, `Customer`).
- Declares **Interfaces** for `UseCase` and `Repository` implementations (e.g., `CustomerRepository`, `CustomerUseCase`).

### `internal/repository/`
Direct implementations of the Database interfaces defined in `domain`:
- Uses GORM to interact with PostgreSQL.
- Executes complex queries using `pgvector` here.
- Returns results as `domain` entities.

### `internal/usecase/`
Contains application-specific business logic (Application Business Rules):
- Implements the UseCase interfaces from `domain`.
- Orchestrates logic by calling Repository interfaces (injected from `main.go`).
- Example: `CreateCustomerUseCase` validates an email before calling `CustomerRepository.Save()`.

### `internal/delivery/http/`
The communication layer that interface with the outside world (Controllers):
- Receives HTTP Requests from the Client (React Frontend) and validates input JSON.
- Calls the appropriate `UseCase` and receives results.
- Returns JSON Responses to the Client.

### `internal/ai/`
Contains clients for Cloudflare Workers AI using HTTP or SDKs. Functions as a specialized repository for AI model interactions.

---

## 2. Data Flow

A typical processing flow (e.g., creating a new Customer via API):

1. **Client Request (Frontend)**
   → `POST /api/v1/customers` (Frontend sends HTTP JSON request).

2. **Communication (Delivery Layer - `delivery/http/customer_handler.go`)**
   → Handler receives the request and unmarshals JSON into an input struct.
   → Passes validated data to the UseCase layer: `h.customerUseCase.CreateCustomer(ctx, input)`.

3. **Business Logic (UseCase Layer - `usecase/customer_usecase.go`)**
   → UseCase receives the data. It may call `internal/ai/` for tagging or classification.
   → UseCase then calls the Repository Interface to persist data: `uc.customerRepo.Insert(ctx, domainEntity)`.

4. **Persistence (Repository Layer - `repository/customer_postgres.go`)**
   → The actual implementation of `Insert` uses GORM to query the Postgres DB and returns the entity or an error.

5. **Client Response**
   → Data flows back: `Repository` → `UseCase` → `Delivery/Handler`.
   → The Handler formats the data into standard REST JSON and sends the `200 OK` response.

---

## 3. Frontend Architecture (crm-frontend)

The frontend application uses React + Vite + Tailwind CSS v4 + Shadcn UI.
- **`src/App.tsx`**: React entry point and route definitions.
- **`src/AppLayout.tsx`**: Main layout frame providing Sidebar Navigation, Header, and dynamic Content area.
- **`src/components/ui/`**: Shadcn UI base components (Button, Input, Form) with pre-configured styling.
- **`src/pages/`**: Recommended folder for individual route designs (e.g., Dashboard, Customers).
- **`src/lib/api/`**: Organization for API calls using Fetch or Axios, separating View logic from Data fetching.

---

## 4. Infrastructure & Deployment (Active)

The project is fully automated and deployed to production:

### Persistence & Storage
- **Production DB**: [Supabase](https://supabase.com) (PostgreSQL 16)
  - **Extensions Enabled**: `pgvector` (AI Vector Search), `uuid-ossp` (UUID generation).
  - **Connection**: Managed via Transaction Pooler (Port 6543).
- **Local DB**: Docker container (`crm-postgres`) using the official `pgvector/pgvector:pg16` image.

### Cloud Deployment
- **Backend**: [Railway](https://railway.app)
  - Auto-builds from `main` branch.
  - Health Endpoint: `https://20q-crm-production.up.railway.app/health`
- **Frontend**: [Vercel](https://vercel.app)
  - Live URL: [https://20q-crm.vercel.app](https://20q-crm.vercel.app)

---

## 5. Development Setup

### Environment Variables
Copy `.env.example` to `.env` to configure your local environment:
- `DATABASE_URL`: Connection string for Postgres.
- `REDIS_URL`: Connection string for Redis.
- `CF_AI_TOKEN`: Cloudflare Workers AI access.

### Makefile Commands
- `make dev`: Start backend server.
- `make build`: Compile Go binary.
- `make migrate-up`: Apply database migrations (requires `migrate` CLI).
- `make docker-up`: Start local Postgres and Redis containers.
