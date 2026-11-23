#!/usr/bin/env bash
# Mirror of .github/workflows/ci.yml for local runs: ./scripts/ci-local.sh
set -euo pipefail
cd "$(dirname "$0")/.."

echo "==> fmt check"
test -z "$(gofmt -l .)" || { echo "gofmt found diffs:"; gofmt -l .; exit 1; }

echo "==> go vet"
go vet ./...

echo "==> go test -race"
go test -race -count=1 ./...

# Integration suite: disposable Postgres from the dev compose stack, its own
# spa_test database, one package at a time (-p 1). If the Docker engine is down
# and you only want the Go steps, stop before this block.
echo "==> postgres up"
if ! docker info >/dev/null 2>&1; then
  echo "Docker engine is not running - cannot run the integration suite." >&2
  echo "Go-only checks: gofmt -l . && go vet ./... && go test -race -count=1 ./..." >&2
  exit 1
fi
if [ -z "$(docker compose ps --status running --quiet postgres)" ]; then
  docker compose up -d postgres
fi
ready=0
for _ in $(seq 1 60); do
  if docker compose exec -T postgres pg_isready -U spa -d postgres >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
if [ "$ready" -ne 1 ]; then
  echo "postgres did not become ready:"
  docker compose logs --tail 50 postgres
  exit 1
fi

echo "==> ensure spa_test"
if ! docker compose exec -T postgres psql -U spa -d postgres -tAc \
  "SELECT 1 FROM pg_database WHERE datname = 'spa_test'" | grep -q 1; then
  docker compose exec -T postgres createdb -U spa spa_test
fi

echo "==> go vet -tags=integration"
go vet -tags=integration ./...

echo "==> go test -tags=integration -race -p 1"
TEST_DATABASE_URL="postgres://spa:spa@localhost:5432/spa_test" \
  go test -tags=integration -race -count=1 -p 1 ./...

echo "==> go build"
go build ./...

echo "==> docker build"
docker build -t spa-booking:local .

echo "==> compose config"
docker compose config --quiet

echo "LOCAL CI OK"
