#!/usr/bin/env bash
# Ship to the VPS: pull latest main there, rebuild, and verify health.
# Usage: ./scripts/deploy-vps.sh
set -euo pipefail

HOST="${APP_URL:-vps}"   # ssh alias for the VPS (see ~/.ssh/config)
DIR="~/ws"

echo "==> pulling latest main on the VPS"
ssh "$HOST" "cd $DIR && git pull --ff-only"

echo "==> building + restarting app"
ssh "$HOST" "cd $DIR && docker compose -f docker-compose.vps.yml up -d --build app"

echo "==> waiting for /healthz"
for i in $(seq 1 60); do
  if ssh "$HOST" 'curl -fsS http://127.0.0.1:8088/healthz >/dev/null 2>&1'; then
    echo "==> healthy after ${i}0s-ish (attempt $i)"
    ssh "$HOST" 'curl -fsS http://127.0.0.1:8088/readyz >/dev/null && echo readyz OK'
    echo "DEPLOY OK -> https://wangga.biz.id"
    exit 0
  fi
  sleep 3
done
echo "deploy-vps: health check did not pass in ~3 min" >&2
ssh "$HOST" "cd $DIR && docker compose -f docker-compose.vps.yml logs --tail 30 app" >&2
exit 1
