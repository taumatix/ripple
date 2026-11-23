#!/usr/bin/env bash
# Starts Postgres and DynamoDB Local, runs the whole test suite against them with
# RIPPLE_REQUIRE_SERVICES=1 (a skipped service test fails), and removes both containers.
# Container runtime: Docker when its daemon answers, else Apple `container` (agent host).
# Override with RIPPLE_CONTAINER_RUNTIME=docker|container.
set -euo pipefail
cd "$(dirname "$0")/.."

PG_PORT=${RIPPLE_TEST_PG_PORT:-55432}
DDB_PORT=${RIPPLE_TEST_DDB_PORT:-58000}
PG=ripple-pg-$$
DDB=ripple-ddb-$$

rt=${RIPPLE_CONTAINER_RUNTIME:-}
if [ -z "$rt" ]; then
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    rt=docker
  elif command -v container >/dev/null 2>&1 && container system status >/dev/null 2>&1; then
    rt=container
  else
    echo "test-services: no container runtime: Docker has no daemon and Apple 'container' is not running (try: container system start)" >&2
    exit 2
  fi
fi
echo "test-services: using $rt"

cleanup() {
  "$rt" stop "$PG" "$DDB" >/dev/null 2>&1 || true
  "$rt" rm -f "$PG" "$DDB" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# Both runtimes accept: run -d --name N -e K=V -p host:container image
"$rt" run -d --name "$PG" -e POSTGRES_PASSWORD=ripple -p "127.0.0.1:$PG_PORT:5432" postgres:17 >/dev/null
"$rt" run -d --name "$DDB" -p "127.0.0.1:$DDB_PORT:8000" amazon/dynamodb-local >/dev/null

echo "test-services: waiting for Postgres on :$PG_PORT and DynamoDB Local on :$DDB_PORT"
ready=
for _ in $(seq 1 90); do
  # pg_isready inside the container: the port opens before Postgres accepts connections.
  if "$rt" exec "$PG" pg_isready -U postgres >/dev/null 2>&1 \
     && curl -s -o /dev/null "http://127.0.0.1:$DDB_PORT/"; then ready=1; break; fi
  sleep 1
done
if [ -z "$ready" ]; then
  echo "test-services: services did not become ready in 90s" >&2
  "$rt" logs "$PG" 2>&1 | tail -20 >&2 || true
  "$rt" logs "$DDB" 2>&1 | tail -20 >&2 || true
  exit 1
fi

RIPPLE_REQUIRE_SERVICES=1 \
RIPPLE_TEST_POSTGRES_DSN="postgres://postgres:ripple@127.0.0.1:$PG_PORT/postgres?sslmode=disable" \
RIPPLE_TEST_DYNAMODB_ENDPOINT="http://127.0.0.1:$DDB_PORT" \
AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local AWS_REGION=us-east-1 \
  go test -race -count=1 ./...
