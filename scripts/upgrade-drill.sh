#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Upgrade + rollback drill (DEP-014), per engine, on fresh volumes:
#   1. the PREVIOUS image (--from) creates the schema and data: `seed`, an admin,
#      a ticket with an attachment through the API; the queue drains; it stops;
#   2. the documented pre-upgrade backup (`simpletickets backup`, + `pg_dump -Fc`
#      on Postgres);
#   3. the NEW image (--to) starts on the same data: pending migrations run on
#      boot (ST_MIGRATE_ON_BOOT default), /readyz turns 200, every pre-existing
#      table keeps its row count, the admin signs in and the attachment
#      downloads byte-identical;
#   4. the upgraded schema equals the schema of a fresh install of the new image
#      (catches migrations edited in place);
#   5. the previous image refuses the upgraded database (exit ≠ 0, "newer than
#      this binary") — a mistaken rollback cannot damage data;
#   6. rollback = previous image + pre-upgrade backup: it serves again with the
#      same row counts.
#
# --next-migration builds the NEW image from this source tree plus one extra
# drill migration (version 0099, both engines), so step 3 really applies a new
# migration even when --from and the tree have the same schema head.
#
# Usage: scripts/upgrade-drill.sh --engine sqlite|postgres --from IMAGE [--to IMAGE | --next-migration]
# Needs: docker, curl, sqlite3 (sqlite engine), rsync (--next-migration).
# Exit status: 0 = all checks passed, 1 = a check failed, 2 = setup error.
set -euo pipefail

ENGINE=sqlite
FROM=""
TO=""
NEXT=0
PG_IMAGE=postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
while [ $# -gt 0 ]; do
  case $1 in
    --engine) ENGINE=$2; shift 2 ;;
    --from) FROM=$2; shift 2 ;;
    --to) TO=$2; shift 2 ;;
    --next-migration) NEXT=1; shift ;;
    -h|--help) sed -n '2,28p' "$0"; exit 0 ;;
    *) echo "unknown flag $1" >&2; exit 2 ;;
  esac
done
case $ENGINE in sqlite|postgres) ;; *) echo "--engine sqlite|postgres" >&2; exit 2 ;; esac
[ -n "$FROM" ] || { echo "--from IMAGE is required (the previous release)" >&2; exit 2; }
[ -n "$TO" ] || [ $NEXT = 1 ] || { echo "--to IMAGE or --next-migration" >&2; exit 2; }

ROOT=$(cd "$(dirname "$0")/.." && pwd)
ID=st-upgrade-$ENGINE-$$
WORK=$(mktemp -d "${TMPDIR:-/tmp}/st-upgrade.XXXXXX")
SECRET=drill-secret-key-0123456789abcdef0123456789abcdef
ADMIN_EMAIL=drill-admin@drill.test
ADMIN_PASSWORD=drill-admin-password-123
PGPW=drill-pg-password
FAIL=0
log() { printf '[upgrade %s] %s\n' "$ENGINE" "$*"; }
bad() { printf '[upgrade %s] FAIL: %s\n' "$ENGINE" "$*" >&2; FAIL=1; }

cleanup() {
  if [ -n "${KEEP:-}" ]; then echo "KEEP set: $ID-* left in place" >&2; return; fi
  docker rm -f "$ID-app" "$ID-pg" "$ID-pgfresh" >/dev/null 2>&1 || true
  docker volume rm "$ID-data" "$ID-fresh" "$ID-pgdata" "$ID-pgfreshdata" >/dev/null 2>&1 || true
  docker network rm "$ID-net" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

if [ $NEXT = 1 ]; then
  TO=simpletickets:upgrade-drill
  log "building $TO = this tree + drill migration 0099"
  rsync -a --exclude .git --exclude node_modules --exclude web/dist --exclude bin --exclude data "$ROOT/" "$WORK/src/"
  for eng in sqlite postgres; do
    printf -- '-- Upgrade drill only (scripts/upgrade-drill.sh): never committed.\nCREATE TABLE upgrade_drill (id TEXT PRIMARY KEY, note TEXT NOT NULL);\n' \
      > "$WORK/src/internal/store/migrations/$eng/0099_upgrade_drill.sql"
  done
  docker build -q --build-arg VERSION=upgrade-drill --build-arg COMMIT=drill -t "$TO" "$WORK/src" >/dev/null
fi
log "from $FROM ($(docker run --rm "$FROM" version)) to $TO ($(docker run --rm "$TO" version))"

docker network create "$ID-net" >/dev/null
docker volume create "$ID-data" >/dev/null
HARDEN=(--read-only --tmpfs /tmp:size=64m --cap-drop ALL --security-opt no-new-privileges:true --network "$ID-net")
PG=""
start_pg() { # name volume
  docker volume create "$2" >/dev/null
  docker run -d --name "$1" --network "$ID-net" -e POSTGRES_USER=st -e POSTGRES_PASSWORD=$PGPW -e POSTGRES_DB=st \
    -v "$2":/var/lib/postgresql "$PG_IMAGE" >/dev/null
  for _ in $(seq 1 60); do docker exec "$1" pg_isready -U st -d st >/dev/null 2>&1 && return 0; sleep 0.5; done
  echo "postgres $1 did not start" >&2; exit 2
}
if [ "$ENGINE" = postgres ]; then PG=$ID-pg; start_pg "$PG" "$ID-pgdata"; fi
envs() { # volume pg
  E=(-e "ST_SECRET_KEY=$SECRET" -e ST_PUBLIC_URL=http://127.0.0.1 -e ST_LOG_LEVEL=warn -v "$1":/data)
  [ -n "${2:-}" ] && E+=(-e ST_DATABASE_URL="postgres://st:$PGPW@$2:5432/st?sslmode=disable")
  return 0
}
oneoff() { local img=$1 vol=$2 pg=$3; shift 3; envs "$vol" "$pg"; docker run --rm "${HARDEN[@]}" "${E[@]}" -i "$img" "$@"; }
serve() { # image volume pg → base URL
  envs "$2" "$3"
  docker run -d --name "$ID-app" "${HARDEN[@]}" "${E[@]}" -p 127.0.0.1::8080 "$1" serve >/dev/null
  local url
  url=http://127.0.0.1:$(docker port "$ID-app" 8080/tcp | head -1 | sed 's/.*://')
  for _ in $(seq 1 120); do curl -sf "$url/readyz" >/dev/null && { echo "$url"; return 0; }; sleep 0.25; done
  docker logs "$ID-app" >&2; echo "not ready" >&2; exit 2
}
stop_app() { docker stop "$ID-app" >/dev/null; docker rm "$ID-app" >/dev/null; }
tables() { # → "table count"; schema_migrations and FTS5 shadow tables excluded
  if [ "$ENGINE" = sqlite ]; then
    docker create --name "$ID-peek" -v "$ID-data":/data "$FROM" >/dev/null
    rm -rf "$WORK/peek"; mkdir -p "$WORK/peek"
    docker cp "$ID-peek:/data/simpletickets.db" "$WORK/peek/" >/dev/null
    docker cp "$ID-peek:/data/simpletickets.db-wal" "$WORK/peek/" >/dev/null 2>&1 || true
    docker rm "$ID-peek" >/dev/null
    for t in $(sqlite3 "$WORK/peek/simpletickets.db" "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '%_fts_%' AND name <> 'schema_migrations' ORDER BY name"); do
      printf '%s %s\n' "$t" "$(sqlite3 "$WORK/peek/simpletickets.db" "SELECT COUNT(*) FROM \"$t\"")"
    done
  else
    for t in $(docker exec "$PG" psql -U st -d st -tAc "SELECT tablename FROM pg_tables WHERE schemaname='public' AND tablename <> 'schema_migrations' ORDER BY tablename"); do
      printf '%s %s\n' "$t" "$(docker exec "$PG" psql -U st -d st -tAc "SELECT COUNT(*) FROM \"$t\"")"
    done
  fi
}
schema() { # volume pg-container → normalized schema
  if [ "$ENGINE" = sqlite ]; then
    docker create --name "$ID-peek" -v "$1":/data "$FROM" >/dev/null
    rm -rf "$WORK/sch"; mkdir -p "$WORK/sch"
    docker cp "$ID-peek:/data/simpletickets.db" "$WORK/sch/" >/dev/null
    docker cp "$ID-peek:/data/simpletickets.db-wal" "$WORK/sch/" >/dev/null 2>&1 || true
    docker rm "$ID-peek" >/dev/null
    sqlite3 "$WORK/sch/simpletickets.db" "SELECT type||' '||name||' '||COALESCE(sql,'') FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name"
  else
    docker exec "$2" pg_dump -U st -d st --schema-only --no-owner | grep -vE '^(--|SET |SELECT pg_catalog|\\(un)?restrict )' | sed '/^$/d'
  fi
}

# ---- 1. previous image writes data -------------------------------------------
oneoff "$FROM" "$ID-data" "$PG" seed --tickets 20 >/dev/null
printf '%s\n' "$ADMIN_PASSWORD" | oneoff "$FROM" "$ID-data" "$PG" create-admin --email "$ADMIN_EMAIL" --password-stdin >/dev/null
U=$(serve "$FROM" "$ID-data" "$PG")
JAR=$WORK/jar
curl -sf -c "$JAR" -H 'Content-Type: application/json' -d "{\"audience\":\"staff\",\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" "$U/api/v1/auth/login" >/dev/null
CSRF=$(curl -sf -b "$JAR" "$U/api/v1/me" | sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p')
TOKEN=$(curl -sf -b "$JAR" -H "X-CSRF-Token: $CSRF" -H "Origin: http://127.0.0.1" -H 'Content-Type: application/json' \
  -d '{"name":"upgrade","scopes":["tickets:read","tickets:write","admin"]}' "$U/api/v1/me/tokens" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
AUTH=(-H "Authorization: Bearer $TOKEN")
head -c 50000 /dev/urandom > "$WORK/file.bin"
ATT=$(curl -sf "${AUTH[@]}" -F "file=@$WORK/file.bin;type=application/octet-stream" "$U/api/v1/agent/attachments" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p' | head -1)
NUM=$(curl -sf "${AUTH[@]}" -H 'Content-Type: application/json' -H "Idempotency-Key: upgrade-$$" \
  -d "{\"subject\":\"Upgrade drill\",\"body_md\":\"written by the previous release\",\"requester_email\":\"upgrade-customer@drill.test\",\"attachment_ids\":[\"$ATT\"]}" \
  "$U/api/v1/agent/tickets" | sed -n 's/.*"number":\([0-9]*\).*/\1/p' | head -1)
[ -n "$ATT" ] && [ -n "$NUM" ] || { echo "seeding through the API failed" >&2; exit 2; }
for _ in $(seq 1 120); do
  q=$(curl -sf "${AUTH[@]}" "$U/api/v1/admin/queue" || true)
  if printf '%s' "$q" | grep -q '"lag_events":0' && ! printf '%s' "$q" | grep -Eq '"depth":[1-9]'; then break; fi
  sleep 0.5
done
stop_app
OLD_STATUS=$(oneoff "$FROM" "$ID-data" "$PG" migrate status | tail -1)
log "previous release wrote: ticket #$NUM with an attachment; $OLD_STATUS"
tables > "$WORK/before"

# ---- 2. pre-upgrade backup -----------------------------------------------------
BK=$(oneoff "$FROM" "$ID-data" "$PG" backup --out /data/backups | sed -n 's/^backup written to //p')
[ "$ENGINE" = postgres ] && docker exec "$PG" pg_dump -U st -Fc st > "$WORK/pre-upgrade.pgdump"
log "pre-upgrade backup: $BK$([ "$ENGINE" = postgres ] && echo " + pg_dump ($(wc -c < "$WORK/pre-upgrade.pgdump" | tr -d ' ') bytes)")"

# ---- 3. upgrade: new image on the same data ------------------------------------
U=$(serve "$TO" "$ID-data" "$PG")
log "new release serving after migrate-on-boot (/readyz 200)"
stop_app
NEW_STATUS=$(oneoff "$TO" "$ID-data" "$PG" migrate status | tail -1)
log "after upgrade: $NEW_STATUS"
case $NEW_STATUS in *"pending=false"*) ;; *) bad "migrations still pending: $NEW_STATUS" ;; esac
tables > "$WORK/after"
# Every table that existed keeps its rows; new tables are listed.
missing=$(comm -23 <(sort "$WORK/before") <(sort "$WORK/after") | tr "\n" " ")
if [ -z "$missing" ]; then
  log "row counts unchanged in all $(wc -l < "$WORK/before" | tr -d ' ') pre-existing tables"
else
  bad "rows changed or tables lost: $missing"
fi
added=$(comm -13 <(cut -d' ' -f1 "$WORK/before" | sort) <(cut -d' ' -f1 "$WORK/after" | sort) | tr '\n' ' ')
[ -n "$added" ] && log "tables added by the upgrade: $added"
U=$(serve "$TO" "$ID-data" "$PG")
curl -sf "${AUTH[@]}" "$U/api/v1/agent/attachments/$ATT" -o "$WORK/after.bin" && cmp -s "$WORK/file.bin" "$WORK/after.bin" \
  && log "token, ticket #$NUM and its attachment survive the upgrade (byte-identical)" || bad "attachment after upgrade"
curl -sf "${AUTH[@]}" "$U/api/v1/agent/tickets/by-number/$NUM" | grep -q '"subject":"Upgrade drill"' || bad "ticket #$NUM after upgrade"
stop_app

# ---- 4. upgraded schema == fresh install of the new image -----------------------
docker volume create "$ID-fresh" >/dev/null
FRESHPG=""
if [ "$ENGINE" = postgres ]; then FRESHPG=$ID-pgfresh; start_pg "$FRESHPG" "$ID-pgfreshdata"; fi
oneoff "$TO" "$ID-fresh" "$FRESHPG" migrate >/dev/null
schema "$ID-data" "$PG" > "$WORK/schema-upgraded"
schema "$ID-fresh" "$FRESHPG" > "$WORK/schema-fresh"
if diff "$WORK/schema-upgraded" "$WORK/schema-fresh" > "$WORK/schema.diff"; then
  log "upgraded schema identical to a fresh install ($(wc -l < "$WORK/schema-fresh" | tr -d ' ') objects/lines)"
else
  bad "upgraded schema differs from a fresh install:"; head -40 "$WORK/schema.diff" >&2
fi

# ---- 5. the previous image refuses the newer schema ------------------------------
envs "$ID-data" "$PG"
set +e
out=$(docker run --rm "${HARDEN[@]}" "${E[@]}" "$FROM" serve 2>&1)
rc=$?
set -e
if [ $rc -ne 0 ] && printf '%s' "$out" | grep -q 'newer than this binary'; then
  log "previous image refuses the upgraded DB: exit $rc, \"$(printf '%s' "$out" | grep -o 'database schema version.*refusing to start' | head -1)\""
else
  bad "previous image against the upgraded DB: exit $rc: $(printf '%s' "$out" | tail -3)"
fi

# ---- 6. rollback = previous image + pre-upgrade backup ------------------------------
if [ "$ENGINE" = sqlite ]; then
  oneoff "$FROM" "$ID-data" "" restore --from "$BK" --force >/dev/null
else
  docker exec "$PG" psql -U st -d postgres -qc 'DROP DATABASE st WITH (FORCE)' -c 'CREATE DATABASE st' >/dev/null
  docker exec -i "$PG" pg_restore -U st -d st --no-owner --exit-on-error < "$WORK/pre-upgrade.pgdump" || bad "pg_restore of the pre-upgrade dump"
fi
U=$(serve "$FROM" "$ID-data" "$PG")
stop_app
tables > "$WORK/rolledback"
if diff "$WORK/before" "$WORK/rolledback" >/dev/null; then
  log "rollback: previous image serves the pre-upgrade backup (/readyz 200), row counts equal"
else
  bad "rollback differs:"; diff "$WORK/before" "$WORK/rolledback" >&2 || true
fi

if [ $FAIL = 0 ]; then log "RESULT: PASS"; else log "RESULT: FAIL"; fi
exit $FAIL
