#!/usr/bin/env bash
# Nightly backups on the VPS (runs from that machine's crontab):
#   pg_dump of both databases + tar of uploads/, kept for 14 days.
# Requires no sudo — the running user owns the compose project dir.
# Crontab entry (installed automatically by the migration):
#   15 3 * * * $HOME/ws/scripts/backup-vps.sh >> $HOME/backups/backup.log 2>&1
set -euo pipefail

DEST="${HOME}/backups"
KEEP=14
COMPOSE=(docker compose -f "${HOME}/ws/docker-compose.vps.yml")

mkdir -p "$DEST"
STAMP="$(date +%Y%m%d-%H%M)"

"${COMPOSE[@]}" exec -T postgres pg_dump -U spa spa | gzip > "$DEST/spa-$STAMP.sql.gz"
"${COMPOSE[@]}" exec -T postgres pg_dump -U spa umami | gzip > "$DEST/umami-$STAMP.sql.gz"
tar -czf "$DEST/uploads-$STAMP.tar.gz" -C "$HOME/ws" uploads

# Evolution API (WhatsApp): its own database plus the Baileys auth volume.
# Guarded so backups still work on a box that has not enabled WhatsApp yet.
if "${COMPOSE[@]}" exec -T postgres psql -U spa -d spa -tAc "SELECT 1 FROM pg_database WHERE datname='evolution'" | grep -qx 1; then
  "${COMPOSE[@]}" exec -T postgres pg_dump -U spa evolution | gzip > "$DEST/evolution-$STAMP.sql.gz"
fi
VOL="$(basename "${HOME}/ws")_evolution_instances"
if docker volume inspect "$VOL" >/dev/null 2>&1; then
  docker run --rm -v "$VOL":/data:ro -v "$DEST":/backup alpine \
    tar -czf "/backup/evolution-instances-$STAMP.tar.gz" -C /data .
fi

# Keep the newest KEEP of each prefix; drop older ones.
for pre in spa umami evolution evolution-instances uploads; do
  ls -1t "$DEST/${pre}-"* 2>/dev/null | tail -n +$((KEEP + 1)) | xargs -r rm -f --
done
printf 'backup %s ok (%s)\n' "$STAMP" "$(du -sh "$DEST" | cut -f1)"
