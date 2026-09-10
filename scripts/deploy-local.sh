#!/usr/bin/env bash
# Local deploy: rebuild + start stack, wait for health, smoke-test endpoints.
# Usage: ./scripts/deploy-local.sh
set -euo pipefail
cd "$(dirname "$0")/.."

APP_URL="${APP_URL:-http://localhost:8088}"

echo "==> docker compose up --build -d"
docker compose up --build -d

echo "==> waiting for app health (up to ~3 min)"
for i in $(seq 1 36); do
  if docker inspect --format='{{.State.Health.Status}}' ws-app-1 2>/dev/null | grep -q healthy; then
    echo "app container healthy"
    break
  fi
  if curl -fsS -m 3 "$APP_URL/healthz" >/dev/null 2>&1; then
    echo "app /healthz reachable"
    break
  fi
  sleep 5
  if [ "$i" -eq 36 ]; then
    echo "app did not become healthy in time"
    docker compose ps
    docker compose logs --tail=50 app
    exit 1
  fi
done

echo "==> smoke test"
curl -fsS "$APP_URL/healthz"; echo " healthz ok"
curl -fsS "$APP_URL/readyz"; echo " readyz ok"
curl -fsS "$APP_URL/" | head -c 200; echo; echo " home ok"

echo "==> status"
docker compose ps

echo "LOCAL DEPLOY OK -> $APP_URL (proxy: http://localhost:8089, mail: http://localhost:8025)"
