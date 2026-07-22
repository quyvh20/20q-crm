#!/bin/sh
set -e

# Railway's healthcheck clock starts when the CONTAINER starts, not when
# ./bin/server does. Everything below runs with nothing listening on any port, so
# every second spent here is deducted from healthcheckTimeout (100s, railway.toml)
# — and a hang here presents identically to a crashed server: connection refused
# until the deploy times out, with no Go log line to explain it, because main()
# was never reached.
#
# `|| true` only tolerates a non-zero EXIT. It cannot rescue a process that never
# exits, and golang-migrate has two ways to do exactly that:
#
#   * Its Postgres driver takes SELECT pg_advisory_lock() before anything else,
#     including before the dirty-version check. That call has no timeout and waits
#     forever, so a lock held by a previous or concurrent deploy hangs this line
#     permanently — and each retried deploy queues another waiter.
#   * The connection string carries no connect_timeout, so a blackholed database
#     host stalls on the TCP dial for the kernel's syn-retry budget (~127s),
#     which alone exceeds the healthcheck window.
#
# So the step is bounded. If it is killed the server still starts: migrations are
# NOT the schema path in production anyway (golang-migrate is dirty at v2 there and
# these files do not run — the boot guards in cmd/server/main.go are what actually
# create tables and columns). A fresh install is small, fast DDL against empty
# tables and finishes well inside the bound.
echo "[start.sh] migrate: begin"
if command -v timeout >/dev/null 2>&1; then
	# 124 = killed by timeout, and `|| true` absorbs it like any other failure.
	timeout 30 ./bin/migrate -path ./migrations -database "$DATABASE_URL" up || true
else
	# No coreutils/busybox timeout: fall back rather than fail the deploy outright.
	./bin/migrate -path ./migrations -database "$DATABASE_URL" up || true
fi
echo "[start.sh] migrate: done — starting server"

# The two markers above are the triage tree for a failed deploy:
#   neither marker                        -> container never got this far
#   "begin" but no "done"                 -> migrate hung; the server was never reached
#   "done" but no "Starting CRM backend"  -> the binary failed to exec
#   "Starting CRM backend" but no
#   "Server listening (health endpoint ready)"
#                                         -> a pre-bind fatal inside main(), message right there
exec ./bin/server
