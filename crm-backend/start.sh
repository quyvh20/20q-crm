#!/bin/sh
set -e

# Run migrations (allow failure for idempotent reruns)
./bin/migrate -path ./migrations -database "$DATABASE_URL" up || true

# Start the server
exec ./bin/server
