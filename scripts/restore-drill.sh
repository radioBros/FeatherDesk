#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Backup → restore drill (DEP-013). For the chosen engine it:
#   1. starts a hardened app container on a FRESH data volume (and, for
#      postgres, a fresh postgres:18-alpine), seeds demo data (`seed`), creates
#      an admin and uploads attachments through the API;
#   2. takes the documented backup while the app runs:
#        sqlite   `featherdesk backup` (VACUUM INTO + files + manifest)
#        postgres `pg_dump -Fc` + `featherdesk backup` (files + manifest)
#   3. restores into a NEW volume / NEW Postgres on NEW containers:
#        sqlite   `featherdesk restore --from <backup>`
#        postgres `pg_restore` (must exit 0) + `featherdesk restore` (files)
#   4. compares: row count of EVERY table, SQLite `PRAGMA integrity_check`,
#      the SHA-256 of every stored file, and every attachment downloaded
#      through the API of the restored instance (byte-identical), /readyz.
#
# Usage: scripts/restore-drill.sh --engine sqlite|postgres [--image featherdesk:local] [--tickets 40]
#        KEEP=1 leaves the containers and volumes for inspection.
# Needs: docker, curl, sqlite3 (sqlite engine), shasum or sha256sum.
# Exit status: 0 = restore identical, 1 = a comparison failed, 2 = setup error.
set -euo pipefail

IMAGE=${IMAGE:-featherdesk:local}
ENGINE=sqlite
TICKETS=40
PG_IMAGE=postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
while [ $# -gt 0 ]; do
  case $1 in
    --engine) ENGINE=$2; shift 2 ;;
    --image) IMAGE=$2; shift 2 ;;
    --tickets) TICKETS=$2; shift 2 ;;
    -h|--help) sed -n '2,22p' "$0"; exit 0 ;;
    *) echo "unknown flag $1" >&2; exit 2 ;;
  esac
done
case $ENGINE in sqlite|postgres) ;; *) echo "--engine sqlite|postgres" >&2; exit 2 ;; esac

ID=fd-drill-$ENGINE-$$
WORK=$(mktemp -d "${TMPDIR:-/tmp}/fd-drill.XXXXXX")
SECRET=drill-secret-key-0123456789abcdef0123456789abcdef
ADMIN_EMAIL=drill-admin@drill.test
ADMIN_PASSWORD=drill-admin-password-123
PGPW=drill-pg-password
FAIL=0
sha() { if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
log() { printf '[drill %s] %s\n' "$ENGINE" "$*"; }
bad() { printf '[drill %s] FAIL: %s\n' "$ENGINE" "$*" >&2; FAIL=1; }

cleanup() {
  if [ -n "${KEEP:-}" ]; then echo "KEEP set: containers/volumes $ID-* left in place" >&2; return; fi
  docker rm -f "$ID-a" "$ID-b" "$ID-pg1" "$ID-pg2" "$ID-tool" >/dev/null 2>&1 || true
  docker volume rm "$ID-src" "$ID-dst" "$ID-pgsrc" "$ID-pgdst" >/dev/null 2>&1 || true
  docker network rm "$ID-net" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

docker network create "$ID-net" >/dev/null
docker volume create "$ID-src" >/dev/null
docker volume create "$ID-dst" >/dev/null

HARDEN=(--read-only --tmpfs /tmp:size=64m --cap-drop ALL --security-opt no-new-privileges:true --network "$ID-net")
dburl() { echo "postgres://st:$PGPW@$1:5432/st?sslmode=disable"; }

start_pg() { # name volume
  docker volume create "$2" >/dev/null
  docker run -d --name "$1" --network "$ID-net" -e POSTGRES_USER=st -e POSTGRES_PASSWORD=$PGPW -e POSTGRES_DB=st \
    -v "$2":/var/lib/postgresql "$PG_IMAGE" >/dev/null
  # The official image first runs a TEMPORARY server (unix socket only) to create the
  # user/database, stops it and starts the real one. pg_isready over the socket (or a
  # connect to a database that is not created yet) answers during that phase, so use
  # TCP (the temporary server has listen_addresses='') and a real query on "st".
  for _ in $(seq 1 120); do
    docker exec "$1" psql -h 127.0.0.1 -U st -d st -tAc 'SELECT 1' >/dev/null 2>&1 && return 0
    sleep 0.5
  done
  echo "postgres $1 did not start" >&2; exit 2
}

# app_env <data volume> [pg container]
app_env() {
  APP_ENV=(-e "FD_SECRET_KEY=$SECRET" -e FD_PUBLIC_URL=http://127.0.0.1 -e FD_LOG_LEVEL=warn -v "$1":/data)
  if [ -n "${2:-}" ]; then APP_ENV+=(-e FD_DATABASE_URL="$(dburl "$2")"); fi
}

oneoff() { # volume pg args...
  local vol=$1 pg=$2; shift 2
  app_env "$vol" "$pg"
  docker run --rm "${HARDEN[@]}" "${APP_ENV[@]}" -i "$IMAGE" "$@"
}

serve() { # name volume pg → prints host port
  app_env "$2" "$3"
  docker run -d --name "$1" "${HARDEN[@]}" "${APP_ENV[@]}" -p 127.0.0.1::8080 "$IMAGE" serve >/dev/null
  local port
  port=$(docker port "$1" 8080/tcp | head -1 | sed 's/.*://')
  for _ in $(seq 1 120); do
    if curl -sf "http://127.0.0.1:$port/readyz" >/dev/null; then echo "$port"; return 0; fi
    sleep 0.25
  done
  docker logs "$1" >&2; echo "$1 not ready" >&2; exit 2
}

# ---- 1. source instance with data -----------------------------------------
PG_SRC=""; PG_DST=""
if [ "$ENGINE" = postgres ]; then
  PG_SRC=$ID-pg1; PG_DST=$ID-pg2
  start_pg "$PG_SRC" "$ID-pgsrc"
fi
log "seeding $TICKETS demo tickets"
oneoff "$ID-src" "$PG_SRC" seed --tickets "$TICKETS" >/dev/null
printf '%s\n' "$ADMIN_PASSWORD" | oneoff "$ID-src" "$PG_SRC" create-admin --email "$ADMIN_EMAIL" --password-stdin >/dev/null
PORT_A=$(serve "$ID-a" "$ID-src" "$PG_SRC")
A=http://127.0.0.1:$PORT_A
log "source up on $A"

# Admin session → personal token → attachments on a new ticket.
JAR=$WORK/jar
curl -sf -c "$JAR" -H 'Content-Type: application/json' -d "{\"audience\":\"staff\",\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" "$A/api/v1/auth/login" >/dev/null
CSRF=$(curl -sf -b "$JAR" "$A/api/v1/me" | sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p')
TOKEN=$(curl -sf -b "$JAR" -H "X-CSRF-Token: $CSRF" -H "Origin: http://127.0.0.1" -H 'Content-Type: application/json' \
  -d '{"name":"drill","scopes":["tickets:read","tickets:write","admin"]}' "$A/api/v1/me/tokens" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
[ -n "$TOKEN" ] || { echo "could not create a token" >&2; exit 2; }
AUTH=(-H "Authorization: Bearer $TOKEN")
IDS=()
for i in 1 2 3; do
  head -c $((20000 * i)) /dev/urandom > "$WORK/file$i.bin"
  att=$(curl -sf "${AUTH[@]}" -F "file=@$WORK/file$i.bin;type=application/octet-stream" "$A/api/v1/agent/attachments" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p' | head -1)
  [ -n "$att" ] || { echo "upload $i failed" >&2; exit 2; }
  IDS+=("$att")
done
ids_json=$(printf '"%s",' "${IDS[@]}"); ids_json="[${ids_json%,}]"
curl -sf "${AUTH[@]}" -H 'Content-Type: application/json' -H "Idempotency-Key: drill-$$" \
  -d "{\"subject\":\"Restore drill\",\"body_md\":\"with attachments\",\"requester_email\":\"drill-customer@drill.test\",\"attachment_ids\":$ids_json}" \
  "$A/api/v1/agent/tickets" >/dev/null
for i in 1 2 3; do curl -sf "${AUTH[@]}" "$A/api/v1/agent/attachments/${IDS[$((i-1))]}" -o "$WORK/before$i.bin"; done

# Let the in-memory queue drain, so stopping the source after the backup spills
# nothing into deferred_jobs (that would differ from the backup).
for _ in $(seq 1 120); do
  q=$(curl -sf "${AUTH[@]}" "$A/api/v1/admin/queue" || true)
  if printf '%s' "$q" | grep -q '"lag_events":0' && ! printf '%s' "$q" | grep -Eq '"depth":[1-9]'; then break; fi
  sleep 0.5
done

# ---- 2. backup (online) -----------------------------------------------------
out=$(docker exec "$ID-a" /featherdesk backup --out /data/backups)
BK=$(printf '%s\n' "$out" | sed -n 's/^backup written to //p')
log "backup: $BK"
if [ "$ENGINE" = postgres ]; then
  docker exec "$PG_SRC" pg_dump -U st -Fc st > "$WORK/st.pgdump"
  log "pg_dump: $(wc -c < "$WORK/st.pgdump" | tr -d ' ') bytes"
fi
docker cp "$ID-a:$BK" "$WORK/backup" >/dev/null
docker stop "$ID-a" >/dev/null # no writes after the backup point

# ---- 3. restore into new volume / database / containers ----------------------
if [ "$ENGINE" = postgres ]; then
  start_pg "$PG_DST" "$ID-pgdst"
  if docker exec -i "$PG_DST" pg_restore -U st -d st --no-owner --exit-on-error < "$WORK/st.pgdump"; then
    log "pg_restore: exit 0"
  else
    bad "pg_restore exited non-zero"
  fi
fi
app_env "$ID-dst" "$PG_DST"
docker create --name "$ID-tool" "${HARDEN[@]}" "${APP_ENV[@]}" "$IMAGE" restore --from /data/restore-in >/dev/null
chmod -R a+rX "$WORK/backup" # docker cp hands the files to root; the app runs as 65532
docker cp "$WORK/backup" "$ID-tool:/data/restore-in" >/dev/null
docker start -a "$ID-tool"

# ---- 4. compare ---------------------------------------------------------------
counts_sqlite() { # container → "table count" lines
  local d=$WORK/db-$1; mkdir -p "$d"
  docker cp "$1:/data/featherdesk.db" "$d/" >/dev/null
  docker cp "$1:/data/featherdesk.db-wal" "$d/" >/dev/null 2>&1 || true
  # FTS5 shadow tables (*_fts_data, *_fts_idx, …) are the index's internal pages and
  # are not comparable row by row; search is checked through the API instead.
  for t in $(sqlite3 "$d/featherdesk.db" "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '%_fts_%' ORDER BY name"); do
    printf '%s %s\n' "$t" "$(sqlite3 "$d/featherdesk.db" "SELECT COUNT(*) FROM \"$t\"")"
  done
}
counts_pg() { # pg container
  for t in $(docker exec "$1" psql -U st -d st -tAc "SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename"); do
    printf '%s %s\n' "$t" "$(docker exec "$1" psql -U st -d st -tAc "SELECT COUNT(*) FROM \"$t\"")"
  done
}
files_sha() { # volume
  docker run --rm -v "$1":/data:ro --entrypoint sh "$PG_IMAGE" -c \
    'cd /data && find attachments branding kb inbound -type f 2>/dev/null | sort | xargs -r sha256sum'
}

# Counted before any app runs on the restored data: a restored instance replays
# events after the restored cursor (documented), which writes queue state.
if [ "$ENGINE" = sqlite ]; then
  counts_sqlite "$ID-a" > "$WORK/counts-before"
  counts_sqlite "$ID-tool" > "$WORK/counts-after"
  ic=$(sqlite3 "$WORK/db-$ID-tool/featherdesk.db" 'PRAGMA integrity_check')
  [ "$ic" = ok ] && log "integrity_check: ok" || bad "integrity_check: $ic"
else
  counts_pg "$PG_SRC" > "$WORK/counts-before"
  counts_pg "$PG_DST" > "$WORK/counts-after"
fi
if diff "$WORK/counts-before" "$WORK/counts-after" >/dev/null; then
  log "row counts equal in all $(wc -l < "$WORK/counts-before" | tr -d ' ') tables (tickets $(awk '$1=="tickets"{print $2}' "$WORK/counts-before"), messages $(awk '$1=="ticket_messages"{print $2}' "$WORK/counts-before"), attachments $(awk '$1=="attachments"{print $2}' "$WORK/counts-before"))"
else
  bad "row counts differ:"; diff "$WORK/counts-before" "$WORK/counts-after" >&2 || true
fi
files_sha "$ID-src" > "$WORK/files-before"
files_sha "$ID-dst" > "$WORK/files-after"
if [ -s "$WORK/files-before" ] && diff "$WORK/files-before" "$WORK/files-after" >/dev/null; then
  log "stored files identical ($(wc -l < "$WORK/files-before" | tr -d ' ') files, SHA-256)"
else
  bad "stored files differ"; diff "$WORK/files-before" "$WORK/files-after" >&2 || true
fi

docker rm "$ID-tool" >/dev/null

# A new container on the restored data serves the same bytes (the token lives in the restored DB).
PORT_B=$(serve "$ID-b" "$ID-dst" "$PG_DST")
B=http://127.0.0.1:$PORT_B
log "restored instance up on $B (/readyz 200)"
for i in 1 2 3; do
  curl -sf "${AUTH[@]}" "$B/api/v1/agent/attachments/${IDS[$((i-1))]}" -o "$WORK/after$i.bin" || bad "download $i from the restored instance"
  if cmp -s "$WORK/before$i.bin" "$WORK/after$i.bin" && cmp -s "$WORK/file$i.bin" "$WORK/after$i.bin"; then :; else bad "attachment $i differs"; fi
done
[ $FAIL = 0 ] && log "attachments downloaded from the restored instance are byte-identical (3/3)"
# Full-text search answers from the restored index.
found=$(curl -s "${AUTH[@]}" "$B/api/v1/agent/tickets?q=Restore%20drill")
if printf '%s' "$found" | grep -q '"subject":"Restore drill"'; then
  log "search finds the drill ticket on the restored instance"
else
  bad "search on the restored instance: $(printf '%s' "$found" | head -c 300)"
fi

if [ $FAIL = 0 ]; then log "RESULT: PASS"; else log "RESULT: FAIL"; fi
exit $FAIL
