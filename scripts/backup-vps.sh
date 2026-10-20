#!/usr/bin/env bash
# Nightly backups on the VPS (runs from that machine's crontab):
#   pg_dump of both databases + tar of uploads/, kept for 14 days.
# Requires no sudo — the running user owns the compose project dir.
# Crontab entry (installed by `crontab -` with this line):
#   15 3 * * * $HOME/ws/scripts/backup-vps.sh >> $HOME/backups/backup.log 2>&1
set -euo pipefail

DEST="${HOME}/backups"
KEEP=14
COMPOSE="docker compose -f ${HOME}/ws/docker-compose.vps.yml"

mkdir -p "$DEST"
STAMP="$(date +%Y%m%d-%H%M)"

"$COMPOSE" exec -T postgres pg_dump -U spa spa | gzip > "$DEST/spa-$STAMP.sql.gz"
"$COMPOSE" exec -T postgres pg_dump -U spa umami | gzip > "$DEST/umami-$STAMP.sql.gz"
tar -czf "$DEST/uploads-$STAMP.tar.gz" -C "$HOME/ws" uploads

# Immunity to parameter-expansion surprises; glob covers the three prefixes.
ls -1t "$DEST" | tail -n +$((KEEP * 3 + 1)) | while read -r old; do
  rm -f "$DEST/$old"
done
printf 'backup %s ok (%s)\n' "$STAMP" "$(du -sh "$DEST" | cut -f1)"
