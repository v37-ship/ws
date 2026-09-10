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

echo "==> go build"
go build ./...

echo "==> docker build"
docker build -t spa-booking:local .

echo "==> compose config"
docker compose config --quiet

echo "LOCAL CI OK"
