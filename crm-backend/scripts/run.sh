#!/bin/bash
set -e

echo "Running migrations..."
# Download and install golang-migrate
if [ ! -f "migrate" ]; then
    echo "Downloading golang-migrate..."
    curl -L https://github.com/golang-migrate/migrate/releases/download/v4.17.0/migrate.linux-amd64.tar.gz | tar xvz
fi

# Run the migrations unconditionally
echo "Applying migrations to DATABASE_URL..."
./migrate -path ./migrations -database "$DATABASE_URL" up

echo "Starting server..."
exec ./bin/server
